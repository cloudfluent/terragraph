package plugins

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/privatefs"
	sdk "github.com/cloudfluent/terragraph/plugin"
)

type featureInstance struct {
	configDigest string
	config       blueprint.PluginConfig
	pkg          Package
	feature      sdk.Feature
	mode         string
	timeout      time.Duration
}

// Lifecycle owns feature dispatch, process quarantine and lease lifetime; only the engine may turn its decisions into infrastructure work.
type Lifecycle struct {
	sessionGate                                   chan struct{}
	dir, operation, executionID, invocation, work string
	features                                      []featureInstance
	sessions                                      map[string]*Session
	failed                                        map[string]error
	mu                                            sync.Mutex
	seq                                           atomic.Uint64
	logger                                        *slog.Logger
	record                                        func(sdk.CallRecord) error
	nodesMu                                       sync.Mutex
	nodes                                         map[string]*credentialScope
	closed                                        bool
	terminalMu                                    sync.Mutex
	terminal                                      map[string]bool
	completionMu                                  sync.Mutex
	completionErr                                 error
	localCalls                                    []sdk.CallRecord
	localRecord                                   bool
	closeMu                                       sync.Mutex
	closeDone                                     bool
	closeErr                                      error
}

// NewLifecycle verifies all packages before any process starts; static operations exclude external effects and credential inheritance.
func NewLifecycle(ctx context.Context, dir string, configs []blueprint.PluginConfig, operation, executionID string, record func(sdk.CallRecord) error) (*Lifecycle, error) {
	m := &Lifecycle{sessionGate: make(chan struct{}, 1), dir: dir, operation: operation, executionID: executionID, invocation: rand.Text(), sessions: map[string]*Session{}, failed: map[string]error{}, logger: settingsFor(ctx).logger, record: record, nodes: map[string]*credentialScope{}}
	if m.logger == nil {
		m.logger = slog.New(slog.DiscardHandler)
	}
	for _, c := range configs {
		p, err := Resolve(dir, c)
		if err != nil {
			return nil, err
		}
		seen := map[string]bool{}
		for _, f := range p.Descriptor.Features {
			seen[f.Name] = true
			options := c.Features[f.Name]
			mode := options.Mode
			if mode == "" {
				mode = "enforce"
				if f.Kind == "observer" {
					mode = "best_effort"
				}
			}
			if mode == "best_effort" && f.Kind != "observer" {
				return nil, fmt.Errorf("plugin.%s.feature.%s: best_effort is only valid for observers", c.Name, f.Name)
			}
			if mode == "advisory" && f.Kind != "gate" && f.Kind != "validator" {
				return nil, fmt.Errorf("plugin.%s.feature.%s: advisory is only valid for gates and validators", c.Name, f.Name)
			}
			if !isStaticOperation(operation) && len(f.Operations) > 0 && !slices.Contains(f.Operations, operation) {
				continue
			}
			timeout := options.Timeout
			if timeout == 0 {
				timeout = 30 * time.Second
			}
			if f.Kind == "gate" {
				if options.Timeout == 0 {
					timeout = time.Minute
				}
			}
			if !isStaticOperation(operation) && executionID == "" && f.Kind == "observer" && mode == "enforce" {
				return nil, fmt.Errorf("plugin.%s.feature.%s: required observers need an execution record; use best_effort for read and vendor commands", c.Name, f.Name)
			}
			digest, err := featureConfigDigest(c)
			if err != nil {
				return nil, fmt.Errorf("plugin.%s.config: %w", c.Name, err)
			}
			m.features = append(m.features, featureInstance{digest, c, p, f, mode, timeout})
		}
		for name := range c.Features {
			if !seen[name] {
				return nil, fmt.Errorf("plugin.%s.feature.%s: feature is not exported by the locked package", c.Name, name)
			}
		}
	}
	sort.Slice(m.features, func(i, j int) bool {
		a, b := m.features[i], m.features[j]
		return a.config.Name+"."+a.feature.Name < b.config.Name+"."+b.feature.Name
	})
	if m.record == nil && !isStaticOperation(operation) {
		m.record = m.recordLocal
		m.localRecord = true
	}
	return m, nil
}

func isStaticOperation(operation string) bool {
	return operation == "static" || operation == "validate" || operation == "graph"
}

func (m *Lifecycle) session(ctx context.Context, f featureInstance) (*Session, error) {
	select {
	case m.sessionGate <- struct{}{}:
		defer func() { <-m.sessionGate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, fmt.Errorf("plugin session closed")
	}
	alias := f.config.Name
	if err := m.failed[alias]; err != nil {
		return nil, err
	}
	if s := m.sessions[alias]; s != nil {
		return s, nil
	}
	if err := m.prepareWork(); err != nil {
		return nil, err
	}
	work := filepath.Join(m.work, alias)
	if err := privatefs.Directory(work); err != nil {
		return nil, err
	}

	env := []string{}
	if !isStaticOperation(m.operation) {
		for _, key := range f.config.Environment {
			if strings.HasPrefix(strings.ToUpper(key), "TERRAGRAPH_") || strings.EqualFold(key, "PLUGIN_PROTOCOL_VERSIONS") {
				return nil, fmt.Errorf("plugin.%s.access.environment: reserved environment name", alias)
			}
			if value, ok := os.LookupEnv(key); ok {
				env = append(env, key+"="+value)
			}
		}
	}
	settings := settingsFor(ctx)
	settings.alias = alias
	s, err := Open(context.WithValue(ctx, loggingKey{}, settings), f.pkg, work, env, context.WithoutCancel(ctx))
	if err == nil {
		_, err = s.Call(ctx, sdk.Request{Action: "configure", Config: f.config.Config, Event: sdk.Event{Operation: m.operation, Phase: "configure"}}, 10*time.Second)
	}
	if err != nil {
		if s != nil {
			s.Close()
		}
		m.failed[alias] = err
		return nil, err
	}
	m.sessions[alias] = s
	return s, nil
}

func (m *Lifecycle) event(e sdk.Event) sdk.Event {
	if e.ID == "" {
		e.ID = fmt.Sprintf("%s-%d", m.invocation, m.seq.Add(1))
	}
	e.Operation = m.operation
	e.ExecutionID = m.executionID
	return e
}

func (m *Lifecycle) invoke(ctx context.Context, f featureInstance, r sdk.Request) (sdk.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()
	r.Event = m.event(r.Event)
	r.Feature = f.feature.Name
	if r.ID == "" {
		r.ID = rand.Text()
	}
	key := sha256.Sum256([]byte(r.Event.ID + "\x00" + f.config.Name + "\x00" + f.feature.Name + "\x00" + r.Action))
	r.IdempotencyKey = hex.EncodeToString(key[:])
	record := sdk.CallRecord{ID: r.IdempotencyKey, Alias: f.config.Name, Feature: f.feature.Name, Kind: f.feature.Kind, Effect: f.feature.Effect, Digest: f.pkg.Digest, Mode: f.mode, Event: r.Event, Status: "pending"}
	record.ConfigDigest = f.configDigest
	record.Event.Plan = nil
	if r.Action == "acquire" || r.Action == "renew" || r.Action == "release" {
		record.Cleanup = &sdk.CredentialCleanup{Reference: r.Reference, Lease: r.Lease}
	}
	record.Event.Graph = nil
	external := f.feature.Effect == "idempotent_external" || f.feature.Effect == "non_idempotent_external"
	if external && m.record == nil && f.mode == "enforce" {
		return sdk.Response{}, fmt.Errorf("plugin.%s.%s: external effect requires a durable execution record", f.config.Name, f.feature.Name)
	}
	if m.record != nil {
		if err := m.record(record); err != nil {
			return sdk.Response{}, err
		}
	}
	s, err := m.session(ctx, f)
	var response sdk.Response
	if err == nil {
		for attempt := 0; attempt < 3; attempt++ {
			response, err = s.Call(ctx, r, f.timeout)
			var call *CallError
			if !errors.As(err, &call) || !call.Retryable || call.Fatal || (f.feature.Effect != "pure" && f.feature.Effect != "read_only") || attempt == 2 {
				break
			}
			timer := time.NewTimer(time.Duration(attempt+1) * 30 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				err = ctx.Err()
			case <-timer.C:
			}
			if ctx.Err() != nil {
				break
			}
		}
	}
	record.Status = "completed"
	record.Report = response.Report
	if response.Lease != nil {
		record.Cleanup = &sdk.CredentialCleanup{Reference: r.Reference, Lease: response.Lease}
	}
	record.Decision = response.Decision
	record.Version = response.Version
	if err != nil {
		record.Status = "failed"
		record.Code = "plugin_failed"
		var call *CallError
		if errors.As(err, &call) {
			record.Code = call.Code
			if call.OutcomeUnknown && (external || f.feature.Kind == "credential_provider") {
				record.Status = "unknown"
			}
		}
	}
	if m.record != nil {
		err = errors.Join(err, m.record(record))
	}
	if err != nil {
		return response, fmt.Errorf("plugin.%s.%s at %s: %w; inspect plugin diagnostics before retrying", f.config.Name, f.feature.Name, r.Event.Phase, err)
	}
	if r.Action == "resolve" || r.Action == "acquire" || r.Action == "renew" || r.Action == "release" {
		event := r.Event
		event.Status = "completed"
		event.Plan = nil
		if emitErr := m.Emit(ctx, event); emitErr != nil {
			m.AddCompletionError(emitErr)
		}
	}
	return response, nil
}

// Emit keeps observers from changing completed infrastructure results and makes gates fail closed on missing or invalid decisions.
func (m *Lifecycle) Emit(ctx context.Context, event sdk.Event) error {
	if m == nil {
		return nil
	}
	if event.Phase == "node.finished" {
		m.terminalMu.Lock()
		if m.terminal == nil {
			m.terminal = map[string]bool{}
		}
		if m.terminal[event.Node] {
			m.terminalMu.Unlock()
			return nil
		}
		m.terminal[event.Node] = true
		m.terminalMu.Unlock()
	}
	event = m.event(event)
	var required error
	for _, f := range m.features {
		if !slices.Contains(f.feature.Events, event.Phase) {
			continue
		}
		kind := f.feature.Kind
		if kind != "observer" && kind != "gate" && kind != "validator" {
			continue
		}
		if isStaticOperation(m.operation) && f.feature.Effect != "pure" {
			continue
		}
		e := event
		if !f.config.PlanAccess || kind == "observer" {
			e.Plan = nil
		}
		response, err := m.invoke(ctx, f, sdk.Request{Action: kind, Event: e})
		if err == nil && (kind == "gate" || kind == "validator") {
			switch response.Decision {
			case "allow":
				if !response.ExpiresAt.IsZero() && !response.ExpiresAt.After(time.Now()) {
					err = fmt.Errorf("plugin.%s.%s: decision expired; repeat the check", f.config.Name, f.feature.Name)
				}
			case "deny":
				err = fmt.Errorf("plugin.%s.%s: policy denied %s; satisfy the policy before retrying", f.config.Name, f.feature.Name, event.Phase)
			default:
				err = fmt.Errorf("plugin.%s.%s: policy decision unknown; restore the checker before retrying", f.config.Name, f.feature.Name)
			}
		}
		if err != nil {
			if f.mode == "enforce" {
				required = errors.Join(required, err)
			} else {
				m.logger.Warn("plugin feature failed", "plugin", f.config.Name, "feature", f.feature.Name, "phase", event.Phase, "node", event.Node, "error", err)
			}
		}
	}
	return required
}

func (m *Lifecycle) Has(phase string) bool {
	if m == nil {
		return false
	}
	for _, f := range m.features {
		if slices.Contains(f.feature.Events, phase) && f.feature.Kind != "observer" {
			return true
		}
	}
	return false
}

func (m *Lifecycle) lookup(alias, feature, kind string) (featureInstance, error) {
	for _, f := range m.features {
		if f.config.Name == alias && f.feature.Name == feature && f.feature.Kind == kind {
			return f, nil
		}
	}
	return featureInstance{}, fmt.Errorf("plugin.%s.%s: no %s feature for this operation; correct the binding or package", alias, feature, kind)
}

// Resolve deliberately has no static equivalent: secrets are obtained only for inputs the engine actually needs.
func (m *Lifecycle) Resolve(ctx context.Context, node string, b blueprint.PluginBinding) (sdk.Response, error) {
	if m == nil {
		return sdk.Response{}, fmt.Errorf("node.%s: plugin input requires an active lifecycle", node)
	}
	f, err := m.lookup(b.Alias, b.Feature, "input_resolver")
	if err != nil {
		return sdk.Response{}, err
	}
	response, err := m.invoke(ctx, f, sdk.Request{Action: "resolve", Reference: b.Reference, Event: sdk.Event{Phase: "input.resolve", Node: node}})
	if err != nil {
		return response, err
	}
	if response.Value == nil {
		return response, fmt.Errorf("plugin.%s.%s: resolver returned no value", b.Alias, b.Feature)
	}
	response.Value.Sensitive = true
	if _, err := response.Value.Cty(); err != nil {
		return response, fmt.Errorf("plugin.%s.%s: resolver returned an invalid typed value", b.Alias, b.Feature)
	}
	if !response.ExpiresAt.IsZero() && !response.ExpiresAt.After(time.Now()) {
		return response, fmt.Errorf("plugin.%s.%s: resolved input already expired", b.Alias, b.Feature)
	}
	if !response.ExpiresAt.IsZero() {
		scope := m.scope(ctx, node)
		scope.mu.Lock()
		scope.expires = append(scope.expires, response.ExpiresAt)
		scope.mu.Unlock()
	}
	return response, nil
}

// Close waits for lease workers before bounded release and process teardown; callers retain infrastructure locks until it returns.
func (m *Lifecycle) Close(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.closeMu.Lock()
	defer m.closeMu.Unlock()
	if m.closeDone {
		return m.closeErr
	}
	m.closeDone = true
	var result error
	m.completionMu.Lock()
	result = m.completionErr
	m.completionMu.Unlock()
	result = errors.Join(result, m.closeCredentials(ctx))
	result = errors.Join(result, m.Emit(ctx, sdk.Event{Phase: "session.close", Status: "closing"}))
	m.closeErr = errors.Join(result, m.CloseProcesses(ctx))
	return m.closeErr
}

// CloseProcesses tears down a recovery session without replaying ordinary lifecycle observers.
func (m *Lifecycle) CloseProcesses(_ context.Context) error {
	var result error
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return result
	}
	m.closed = true
	for _, s := range m.sessions {
		s.Close()
	}
	if m.work != "" && m.localRecord && NeedsRecovery(m.localCalls) {
		return errors.Join(result, fmt.Errorf("plugin cleanup remains unresolved at %s; confirm the invocation stopped and review its receipts before removing it", m.work))
	}
	if m.work != "" {
		if err := os.RemoveAll(m.work); err != nil {
			result = errors.Join(result, fmt.Errorf("plugin cleanup %s: %w; remove after verifying the invocation stopped", m.work, err))
		}
	}
	return result
}

// Binding includes reviewed package bytes and declaration policy so removing a gate cannot authorize an older saved plan.
func Binding(dir string, configs []blueprint.PluginConfig) (string, error) {
	if len(configs) == 0 {
		return "", nil
	}
	bindings := map[string]any{}
	for _, c := range configs {
		p, err := Resolve(dir, c)
		if err != nil {
			return "", err
		}
		bindings[c.Name] = struct {
			Config blueprint.PluginConfig
			Digest string
		}{c, p.Digest}
	}
	data, err := json.Marshal(bindings)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// AddCompletionError preserves postconditions without turning a completed mutation into an uncertain one.
func (m *Lifecycle) AddCompletionError(err error) {
	m.completionMu.Lock()
	defer m.completionMu.Unlock()
	m.completionErr = errors.Join(m.completionErr, err)
}
func (m *Lifecycle) NeedsPlanDocument() bool {
	for _, f := range m.features {
		if f.config.PlanAccess && f.feature.Kind == "gate" {
			return true
		}
	}
	return false
}

// RecheckPlan reevaluates plan gates after approval against the same bytes; an expired or revoked decision never becomes admission evidence.
func (m *Lifecycle) RecheckPlan(ctx context.Context, event sdk.Event) error {
	for _, f := range m.features {
		if f.feature.Kind != "gate" || !slices.Contains(f.feature.Events, "node.plan.ready") {
			continue
		}
		e := event
		if !f.config.PlanAccess {
			e.Plan = nil
		}
		e.Metadata = map[string]string{"recheck": "true"}
		response, err := m.invoke(ctx, f, sdk.Request{Action: "gate", Event: e})
		if err == nil && (response.Decision != "allow" || (!response.ExpiresAt.IsZero() && !response.ExpiresAt.After(time.Now()))) {
			err = fmt.Errorf("plugin.%s.%s: plan gate did not authorize admission; repeat plan assessment", f.config.Name, f.feature.Name)
		}
		if err != nil {
			if f.mode == "enforce" {
				return err
			}
			m.logger.Warn("advisory gate recheck failed", "error", err)
		}
	}
	return nil
}

func (m *Lifecycle) ValidateBindings(node string, inputs, credentials map[string]blueprint.PluginBinding, env map[string]string) error {
	for _, b := range inputs {
		if _, err := m.lookup(b.Alias, b.Feature, "input_resolver"); err != nil {
			return fmt.Errorf("node.%s: %w", node, err)
		}
	}
	seen := map[string]bool{}
	for _, b := range credentials {
		if _, err := m.lookup(b.Alias, b.Feature, "credential_provider"); err != nil {
			return fmt.Errorf("node.%s: %w", node, err)
		}
		for _, key := range b.Environment {
			upper := strings.ToUpper(key)
			if seen[upper] {
				return fmt.Errorf("node.%s.credential: duplicate environment supplier", node)
			}
			seen[upper] = true
			for existing := range env {
				if strings.EqualFold(existing, key) {
					return fmt.Errorf("node.%s.credential: environment conflicts with node env; remove one supplier", node)
				}
			}
		}
	}
	return nil
}

// prepareWork runs under mu and uses the same ACL implementation as native saved plans.
func (m *Lifecycle) prepareWork() error {
	if m.work != "" {
		return nil
	}
	path := m.dir
	for _, part := range []string{".terragraph", "plugins", "work"} {
		path = filepath.Join(path, part)
		if err := privatefs.Directory(path); err != nil {
			return err
		}
	}
	path = filepath.Join(path, "runtime-"+m.invocation)
	if err := privatefs.Directory(path); err != nil {
		return err
	}
	m.work = path
	return nil
}

// recordLocal preserves credential cleanup evidence for read-only commands without creating an infrastructure execution.
func (m *Lifecycle) recordLocal(call sdk.CallRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.prepareWork(); err != nil {
		return err
	}
	next := append([]sdk.CallRecord(nil), m.localCalls...)
	found := false
	for i, old := range next {
		if old.ID == call.ID {
			next[i] = call
			found = true
			break
		}
	}
	if !found {
		if len(next) >= 10000 {
			return fmt.Errorf("plugin call limit reached")
		}
		next = append(next, call)
	}
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	scratch := filepath.Join(m.work, ".receipt-"+rand.Text())
	cleanup, err := privatefs.Prepare(scratch)
	if err != nil {
		return err
	}
	defer cleanup()
	f, err := os.OpenFile(scratch, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := os.Rename(scratch, filepath.Join(m.work, "receipts.json")); err != nil {
		return err
	}
	m.localCalls = next
	if err := privatefs.SyncDirectory(m.work); err != nil {
		return err
	}
	m.localCalls = next
	return nil
}

// NeedsRecovery distinguishes unresolved external effects and unreleased execution credentials from diagnostic-only failures.
func NeedsRecovery(calls []sdk.CallRecord) bool {
	released := map[string]bool{}
	for _, c := range calls {
		if c.Cleanup != nil && c.Cleanup.Lease != nil && c.Event.Phase == "credential.release" && (c.Status == "completed" || c.Status == "acknowledged") {
			released[c.Alias+"/"+c.Event.Node+"/"+c.Cleanup.Lease.ID] = true
		}
	}
	for _, c := range calls {
		if c.Status == "acknowledged" {
			continue
		}
		if c.Kind == "credential_provider" && c.Cleanup != nil && c.Cleanup.Lease != nil && released[c.Alias+"/"+c.Event.Node+"/"+c.Cleanup.Lease.ID] {
			continue
		}
		if c.Cleanup != nil && c.Cleanup.Lease != nil && c.Event.Phase == "credential.acquire" {
			return true
		}
		if c.Kind == "credential_provider" && (c.Status == "pending" || c.Status == "unknown") {
			return true
		}
		if c.Mode != "enforce" {
			continue
		}
		if strings.HasSuffix(c.Effect, "external") && (c.Status == "pending" || c.Status == "unknown" || (c.Status == "failed" && (c.Kind == "observer" || c.Kind == "credential_provider"))) {
			return true
		}
	}
	return false
}

// Replay never calls Terraform and never repeats a non-idempotent request with an unknown outcome.
func (m *Lifecycle) Replay(ctx context.Context, call sdk.CallRecord) error {
	f, err := m.lookup(call.Alias, call.Feature, call.Kind)
	if err != nil {
		return err
	}
	if f.pkg.Digest != call.Digest || f.configDigest != call.ConfigDigest {
		return fmt.Errorf("plugin.%s: recovery requires the original locked package digest", call.Alias)
	}
	if call.Effect != "idempotent_external" || (call.Kind != "observer" && call.Event.Phase != "credential.release" && (call.Event.Phase != "credential.acquire" || call.Cleanup == nil || call.Cleanup.Lease == nil)) {
		return fmt.Errorf("plugin call is not replayable; inspect the external outcome and explicitly acknowledge it")
	}
	action := call.Kind
	r := sdk.Request{Action: action, Event: call.Event}
	if call.Kind == "credential_provider" {
		if call.Cleanup == nil || call.Cleanup.Lease == nil {
			return fmt.Errorf("plugin release receipt is incomplete")
		}
		r.Action = "release"
		r.Event.Phase = "credential.release"
		r.Reference = call.Cleanup.Reference
		r.Lease = call.Cleanup.Lease
	}
	_, err = m.invoke(ctx, f, r)
	return err
}

func featureConfigDigest(config blueprint.PluginConfig) (string, error) {
	data, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (m *Lifecycle) CompletionError() error {
	if m == nil {
		return nil
	}
	m.completionMu.Lock()
	defer m.completionMu.Unlock()
	return m.completionErr
}

// NewCredentialRecovery restores authentication for output collection without replaying the original run's gates or external observers.
func NewCredentialRecovery(ctx context.Context, dir string, configs []blueprint.PluginConfig, executionID string, record func(sdk.CallRecord) error) (*Lifecycle, error) {
	m, err := NewLifecycle(ctx, dir, configs, "recovery", executionID, record)
	if err != nil {
		return nil, err
	}
	m.features = slices.DeleteFunc(m.features, func(f featureInstance) bool { return f.feature.Kind != "credential_provider" })
	return m, nil
}
