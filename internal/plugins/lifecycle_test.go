package plugins_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/engine"
	"github.com/cloudfluent/terragraph/internal/exec"
	"github.com/cloudfluent/terragraph/internal/plugins"
	sdk "github.com/cloudfluent/terragraph/plugin"
	"github.com/zclconf/go-cty/cty"
)

func lifecycleDescriptor() sdk.Descriptor {
	name := "lifecycle-plugin"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return sdk.Descriptor{Name: "lifecycle", Version: "1.0.0", Protocol: sdk.ProtocolVersion, Executable: name, Features: []sdk.Feature{
		{Name: "trace", Kind: "observer", Effect: "pure", Events: sdk.LifecyclePhases},
		{Name: "guard", Kind: "gate", Effect: "read_only", Events: []string{"node.plan.ready", "node.mutation.admit"}},
		{Name: "secret", Kind: "input_resolver", Effect: "read_only"},
		{Name: "auth", Kind: "credential_provider", Effect: "read_only"},
		{Name: "delivery", Kind: "observer", Effect: "idempotent_external", Events: []string{"node.mutation.finished", "run.finished", "session.close"}},
		{Name: "expand_nodes", Kind: "expansion", Effect: "pure"},
	}}
}

func serveLifecycleFixture() {
	config := map[string]any{}
	sdk.Serve(lifecycleDescriptor(), func(ctx context.Context, r sdk.Request) (sdk.Response, error) {
		switch r.Action {
		case "configure":
			config = r.Config
			return sdk.Response{}, nil
		case "expand":
			expansion := &sdk.Expansion{}
			if config["expand"] == true {
				expansion.Nodes = []sdk.ExpandedNode{{Name: "generated", Source: "./module"}}
			}
			return sdk.Response{Expansion: expansion}, nil
		case "observer":
			if r.Feature == "trace" {
				sdk.Logger(ctx).Info("lifecycle event", "phase", r.Event.Phase, "node", r.Event.Node, "status", r.Event.Status)
				if config["panic_observer"] == true && r.Event.Phase == "node.mutation.finished" {
					panic("private plugin error")
				}
			}
			if r.Feature == "delivery" && ((config["fail_delivery"] == true && r.Event.Phase == "node.mutation.finished") || (config["fail_terminal"] == r.Event.Phase && config["fail_operation"] == r.Event.Operation)) {
				return sdk.Response{}, errors.New("private delivery error")
			}
			return sdk.Response{}, nil
		case "gate":
			if config["deny_phase"] == r.Event.Phase {
				return sdk.Response{Decision: "deny"}, nil
			}
			if config["unknown"] == true {
				return sdk.Response{Decision: "unknown"}, nil
			}
			if config["need_large_plan"] == true && len(r.Event.Plan) < 5<<20 {
				return sdk.Response{Decision: "deny"}, nil
			}
			return sdk.Response{Decision: "allow"}, nil
		case "resolve":
			v, _ := sdk.EncodeValue(cty.StringVal("secret-value"), true)
			response := sdk.Response{Value: &v, Version: "revision-1"}
			if config["short_input"] == true {
				response.ExpiresAt = time.Now().Add(500 * time.Millisecond)
			}
			return response, nil
		case "acquire":
			response := sdk.Response{Identity: "test-account", Credentials: map[string]string{"PLUGIN_TOKEN": "lease-token"}}
			if config["lease"] == true {
				response.Lease = &sdk.Lease{ID: "lease-1", ExpiresAt: time.Now().Add(time.Minute), RenewAt: time.Now().Add(30 * time.Millisecond)}
			}
			if config["renew_due"] == true {
				response.Lease.RenewAt = time.Now().Add(-time.Second)
			}
			if config["missing_identity"] == true {
				response.Identity = ""
			}
			if config["bad_env"] == true {
				response.Credentials = map[string]string{"TF_DATA_DIR": "override"}
			}
			return response, nil
		case "renew":
			if config["renew_fail"] == true {
				return sdk.Response{}, errors.New("private renewal error")
			}
			return sdk.Response{Lease: &sdk.Lease{ID: r.Lease.ID, ExpiresAt: time.Now().Add(time.Second), RenewAt: time.Now().Add(100 * time.Millisecond)}}, nil
		case "release":
			sdk.Logger(ctx).Info("lease released")
			return sdk.Response{}, nil
		}
		return sdk.Response{}, nil
	})
}

func runLifecycleTerraform() {
	args := os.Args[1:]
	if len(args) == 0 {
		os.Exit(1)
	}
	if path := os.Getenv("TG_LIFECYCLE_RUNTIME_LOG"); path != "" {
		f, _ := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if f != nil {
			_, _ = fmt.Fprintln(f, args[0])
			_ = f.Close()
		}
	}
	switch args[0] {
	case "init":
		os.Exit(0)
	case "version":
		fmt.Print(`{"terraform_version":"1.5.7","platform":"test","provider_selections":{}}`)
	case "plan":
		for _, arg := range args {
			if strings.HasPrefix(arg, "-out=") {
				if err := os.WriteFile(strings.TrimPrefix(arg, "-out="), []byte("immutable-native-plan"), 0600); err != nil {
					os.Exit(1)
				}
			}
		}
		os.Exit(2)
	case "show":
		fmt.Print(`{"format_version":"1.2","terraform_version":"1.5.7","resource_changes":[{"address":"test.a","change":{"actions":["create"]}}],"output_changes":{}}`)
	case "apply", "destroy":
		if os.Getenv("PLUGIN_TOKEN") == "lease-token" {
			if delay := os.Getenv("TG_LIFECYCLE_DELAY"); delay != "" {
				d, _ := time.ParseDuration(delay)
				time.Sleep(d)
			}
		}
	case "output":
		fmt.Print(`{}`)
	}
	os.Exit(0)
}

func copyTestExecutable(t *testing.T, path string) {
	t.Helper()
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("copy: %v, %v", err, closeErr)
	}
}

func lifecycleFixture(t *testing.T, config, body string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	pkg := t.TempDir()
	d := lifecycleDescriptor()
	copyTestExecutable(t, filepath.Join(pkg, d.Executable))
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	root := `plugin "test" {
 source = "test/lifecycle"
 version = "1.0.0"
 ` + config + "\n}\n" + body
	if err := os.WriteFile(filepath.Join(dir, "blueprint.hcl"), []byte(root), 0600); err != nil {
		t.Fatal(err)
	}
	configs, _, err := blueprint.LoadPlugins(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := plugins.Install(dir, configs[0], pkg, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "module"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "module", "main.tf"), []byte("variable \"token\" {\n type=string\n sensitive=true\n default=null\n}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "terraform-lifecycle")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	copyTestExecutable(t, binary)
	return dir, binary
}

func loadLifecycleEngine(t *testing.T, dir, binary string) (*engine.Engine, *logCapture) {
	t.Helper()
	log := logCapture{changed: make(chan struct{}, 256)}
	ctx := plugins.WithLogger(context.Background(), slog.New(slog.NewTextHandler(&log, &slog.HandlerOptions{Level: slog.LevelInfo})))
	e, unlock, err := engine.LoadLockedContext(ctx, dir, exec.Binary(binary), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(unlock)
	e.Logger = slog.New(slog.NewTextHandler(&log, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return e, &log
}

func TestLifecycle_GateDenialNeverApplies(t *testing.T) {
	dir, binary := lifecycleFixture(t, `config = { deny_phase = "node.plan.ready" }`, `node "a" { source = "./module" }`)
	logPath := filepath.Join(dir, "runtime.log")
	t.Setenv("TG_LIFECYCLE_RUNTIME_LOG", logPath)
	e, _ := loadLifecycleEngine(t, dir, binary)
	result, err := e.Apply(engine.Options{AutoApprove: true})
	if err == nil || !strings.Contains(err.Error(), "policy denied") {
		t.Fatalf("got = %v, want gate rejection", err)
	}
	commands, _ := os.ReadFile(logPath)
	if strings.Contains(string(commands), "apply") {
		t.Fatalf("mutation bypassed gate: %s", commands)
	}
	record, err := e.GetExecution(result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status == "needs_recovery" {
		t.Fatalf("got = %s, want known non-mutation failure", record.Status)
	}
}

func TestLifecycle_ApplyEventsAndInputIsolation(t *testing.T) {
	dir, binary := lifecycleFixture(t, "", `node "a" {
 source = "./module"
 input "token" {
 from = plugin.test.secret
 ref = { path = "test" }
 }
 credential "provider" {
 from = plugin.test.auth
 ref = {}
 environment = ["PLUGIN_TOKEN"]
 }
 }`)
	e, log := loadLifecycleEngine(t, dir, binary)
	result, err := e.Apply(engine.Options{AutoApprove: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) != 1 || result.Nodes[0].Status != engine.StatusApplied {
		t.Fatalf("got = %+v, want applied", result)
	}
	for _, phase := range []string{"config.evaluate", "graph.validate", "selection.ready", "run.prepare", "node.prepare", "node.init.before", "node.plan.ready", "node.mutation.admit", "node.mutation.finished", "node.outputs.ready", "node.finished", "run.finished", "session.close"} {
		if !strings.Contains(log.String(), "fields.phase="+phase) {
			t.Errorf("missing %s in %s", phase, log.String())
		}
	}
	if strings.Contains(log.String(), "secret-value") || strings.Contains(log.String(), "lease-token") {
		t.Fatal("plugin secrets leaked to logs")
	}
	if value := os.Getenv("PLUGIN_TOKEN"); value != "" {
		t.Fatalf("got = %q, want no process-global credential", value)
	}
}

func TestLifecycle_RequiredDeliveryFailurePreservesApplied(t *testing.T) {
	dir, binary := lifecycleFixture(t, `config = { fail_delivery = true }
 feature "delivery" { mode = "enforce" }`, `node "a" { source = "./module" }`)
	e, _ := loadLifecycleEngine(t, dir, binary)
	result, err := e.Apply(engine.Options{AutoApprove: true})
	if err == nil {
		t.Fatal("required delivery failure accepted")
	}
	if result.Nodes[0].Status != engine.StatusApplied {
		t.Fatalf("got = %s, want applied infrastructure", result.Nodes[0].Status)
	}
	record, err := e.GetExecution(result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Nodes[0].Phase != "completed" || record.Status != "needs_recovery" {
		t.Fatalf("got = %+v, want completed infrastructure and unresolved delivery", record)
	}
}

func TestLifecycle_SavedPlanRejectsRemovedPlugin(t *testing.T) {
	dir, binary := lifecycleFixture(t, "", `node "a" { source = "./module" }`)
	e, _ := loadLifecycleEngine(t, dir, binary)
	record, err := e.SavePlans(engine.Options{}, "")
	if err != nil {
		t.Fatal(err)
	}
	e.Blueprint.Plugins = nil
	_, err = e.ApplySavedPlans(record.ID, engine.Options{AutoApprove: true})
	if err == nil {
		t.Fatal("removed plugin bypassed saved-plan binding")
	}
}

func TestLifecycle_LeaseRenewalFailureCancelsDependentWork(t *testing.T) {
	dir, _ := lifecycleFixture(t, `config = { lease = true, renew_due = true, renew_fail = true }`, "")
	configs, _, _ := blueprint.LoadPlugins(dir)
	m, err := plugins.NewLifecycle(context.Background(), dir, configs, "apply", "test", func(sdk.CallRecord) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	ctx, _, err := m.Credentials(context.Background(), "a", map[string]blueprint.PluginBinding{"provider": {Alias: "test", Feature: "auth", Environment: []string{"PLUGIN_TOKEN"}}})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("lease failure did not cancel dependent context")
	}
	if err := m.Admit("a"); err == nil {
		t.Fatal("expired credential admitted new work")
	}
	if err := m.Close(context.Background()); err == nil {
		t.Fatal("lease failure disappeared during cleanup")
	}
}

func TestLifecycle_LargePlanUsesBoundedStream(t *testing.T) {
	dir, _ := lifecycleFixture(t, `config = { need_large_plan = true }
 access { plan = true }`, "")
	configs, _, _ := blueprint.LoadPlugins(dir)
	m, err := plugins.NewLifecycle(context.Background(), dir, configs, "apply", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close(context.Background()) }()
	plan := json.RawMessage(`{"padding":"` + strings.Repeat("x", 5<<20) + `"}`)
	if err := m.Emit(context.Background(), sdk.Event{Phase: "node.plan.ready", Plan: plan, PlanID: "same-plan"}); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycle_ExpiredInputBlocksAdmission(t *testing.T) {
	dir, _ := lifecycleFixture(t, `config = { short_input = true }`, "")
	configs, _, _ := blueprint.LoadPlugins(dir)
	m, err := plugins.NewLifecycle(context.Background(), dir, configs, "apply", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close(context.Background()) }()
	_, err = m.Resolve(context.Background(), "a", blueprint.PluginBinding{Alias: "test", Feature: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second)
	if err := m.Admit("a"); err == nil {
		t.Fatal("expired input accepted at admission")
	}
}

func TestLifecycle_ExpansionUsesOrdinaryGraphValidation(t *testing.T) {
	dir, binary := lifecycleFixture(t, `config = { expand = true }`, "")
	e, _ := loadLifecycleEngine(t, dir, binary)
	if e.Graph.Nodes["generated"] == nil {
		t.Fatal("expanded node missing")
	}
}

func TestLifecycle_AdmissionDenialCannotBeAutoApproved(t *testing.T) {
	dir, binary := lifecycleFixture(t, `config = { deny_phase = "node.mutation.admit" }`, `node "a" { source = "./module" }`)
	path := filepath.Join(dir, "runtime.log")
	t.Setenv("TG_LIFECYCLE_RUNTIME_LOG", path)
	e, _ := loadLifecycleEngine(t, dir, binary)
	_, err := e.Apply(engine.Options{AutoApprove: true})
	if err == nil {
		t.Fatal("admission denial bypassed")
	}
	commands, _ := os.ReadFile(path)
	if strings.Contains(string(commands), "apply") {
		t.Fatalf("got = %s, want no apply", commands)
	}
}

func TestLifecycle_OptionalObserverPanicPreservesSuccess(t *testing.T) {
	dir, binary := lifecycleFixture(t, `config = { panic_observer = true }`, `node "a" { source = "./module" }`)
	e, _ := loadLifecycleEngine(t, dir, binary)
	result, err := e.Apply(engine.Options{AutoApprove: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Nodes[0].Status != engine.StatusApplied {
		t.Fatalf("got = %s, want applied", result.Nodes[0].Status)
	}
}

func TestLifecycle_CredentialCannotReplaceManagedEnvironment(t *testing.T) {
	dir, _ := lifecycleFixture(t, `config = { bad_env = true }`, "")
	configs, _, _ := blueprint.LoadPlugins(dir)
	m, err := plugins.NewLifecycle(context.Background(), dir, configs, "apply", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = m.Credentials(context.Background(), "a", map[string]blueprint.PluginBinding{"provider": {Alias: "test", Feature: "auth", Environment: []string{"PLUGIN_TOKEN"}}})
	if err == nil {
		t.Fatal("managed environment accepted")
	}
	_ = m.Close(context.Background())
}

func TestLifecycle_ExpansionCollisionFailsBeforeRuntime(t *testing.T) {
	dir, binary := lifecycleFixture(t, `config = { expand = true }`, `node "generated" { source = "./module" }`)
	_, _, err := engine.LoadLockedContext(context.Background(), dir, exec.Binary(binary), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "conflicting expanded node") {
		t.Fatalf("got = %v, want collision rejection", err)
	}
}

func TestLifecycle_PluginInputConflictsWithVars(t *testing.T) {
	dir, binary := lifecycleFixture(t, "", `node "a" {
 source = "./module"
 vars = { token = "literal" }
 input "token" {
 from = plugin.test.secret
 ref = {}
 }
 }`)
	e, _ := loadLifecycleEngine(t, dir, binary)
	found := false
	for _, p := range e.Validate() {
		if p.Code == "input_conflict" {
			found = true
		}
	}
	if !found {
		t.Fatal("plugin and literal suppliers were not rejected")
	}
}

func TestLifecycle_PlanAccessRequiresExplicitGrant(t *testing.T) {
	dir, _ := lifecycleFixture(t, `config = { need_large_plan = true }`, "")
	configs, _, _ := blueprint.LoadPlugins(dir)
	m, err := plugins.NewLifecycle(context.Background(), dir, configs, "apply", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close(context.Background()) }()
	err = m.Emit(context.Background(), sdk.Event{Phase: "node.plan.ready", Plan: json.RawMessage(`{"padding":"` + strings.Repeat("x", 5<<20) + `"}`)})
	if err == nil {
		t.Fatal("plan was disclosed without access.plan")
	}
}

func TestLifecycle_SavedPlanAppliesWithSamePolicy(t *testing.T) {
	dir, binary := lifecycleFixture(t, "", `node "a" { source = "./module" }`)
	e, _ := loadLifecycleEngine(t, dir, binary)
	record, err := e.SavePlans(engine.Options{}, "")
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.ApplySavedPlans(record.ID, engine.Options{AutoApprove: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Nodes[0].Status != engine.StatusApplied {
		t.Fatalf("got = %s, want applied", result.Nodes[0].Status)
	}
}

func TestLifecycle_AcknowledgementPreservesInfrastructureHistory(t *testing.T) {
	dir, binary := lifecycleFixture(t, `config = { fail_delivery = true }
 feature "delivery" { mode = "enforce" }`, `node "a" { source = "./module" }`)
	e, _ := loadLifecycleEngine(t, dir, binary)
	result, err := e.Apply(engine.Options{AutoApprove: true})
	if err == nil {
		t.Fatal("delivery failure missing")
	}
	record, err := e.GetExecution(result.ExecutionID)
	if err != nil {
		t.Fatal(err)
	}
	callID := ""
	for _, call := range record.PluginCalls {
		if call.Feature == "delivery" && call.Status == "failed" {
			callID = call.ID
		}
	}
	if callID == "" {
		t.Fatal("delivery failure not recorded")
	}
	record, err = e.RecoverPluginCall(record.ID, callID, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if record.Nodes[0].Phase != "completed" || record.Status == "needs_recovery" {
		t.Fatalf("got = %+v, want preserved completed infrastructure and resolved extension", record)
	}
}

func TestLifecycle_ReleasedLeaseClearsUnknownRenewal(t *testing.T) {
	lease := &sdk.CredentialCleanup{Lease: &sdk.Lease{ID: "lease"}}
	calls := []sdk.CallRecord{
		{Alias: "auth", Kind: "credential_provider", Status: "completed", Event: sdk.Event{Phase: "credential.acquire", Node: "a"}, Cleanup: lease},
		{Alias: "auth", Kind: "credential_provider", Status: "unknown", Event: sdk.Event{Phase: "credential.renew", Node: "a"}, Cleanup: lease},
	}
	if !plugins.NeedsRecovery(calls) {
		t.Fatal("unreleased lease lost recovery barrier")
	}
	calls = append(calls, sdk.CallRecord{Alias: "auth", Kind: "credential_provider", Status: "completed", Event: sdk.Event{Phase: "credential.release", Node: "a"}, Cleanup: lease})
	if plugins.NeedsRecovery(calls) {
		t.Fatal("released lease still blocks recovery")
	}
}

func TestLifecycle_RequiredDeliveryBlocksDownstream(t *testing.T) {
	dir, binary := lifecycleFixture(t, `config = { fail_delivery = true }
 feature "delivery" { mode = "enforce" }`, `node "a" { source = "./module" }
 node "b" { source = "./module" }
 edge {
 from = node.a
 to = node.b
 }`)
	e, _ := loadLifecycleEngine(t, dir, binary)
	result, err := e.Apply(engine.Options{AutoApprove: true})
	if err == nil {
		t.Fatal("required delivery failure accepted")
	}
	for _, n := range result.Nodes {
		if n.Node == "a" && n.Status != engine.StatusApplied {
			t.Fatalf("upstream = %s, want applied", n.Status)
		}
		if n.Node == "b" && n.Status != engine.StatusNotRun {
			t.Fatalf("downstream = %s, want not run", n.Status)
		}
	}
}

func TestLifecycle_InvalidCredentialIdentityStillReleasesLease(t *testing.T) {
	dir, _ := lifecycleFixture(t, `config = { lease = true, missing_identity = true }`, "")
	configs, _, err := blueprint.LoadPlugins(dir)
	if err != nil {
		t.Fatal(err)
	}
	var calls []sdk.CallRecord
	m, err := plugins.NewCredentialRecovery(context.Background(), dir, configs, "test", func(c sdk.CallRecord) error { calls = append(calls, c); return nil })
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = m.Credentials(context.Background(), "a", map[string]blueprint.PluginBinding{"provider": {Alias: "test", Feature: "auth", Environment: []string{"PLUGIN_TOKEN"}}})
	if err == nil {
		t.Fatal("missing identity accepted")
	}
	if err := m.Close(context.Background()); err == nil {
		t.Fatal("credential validation failure lost")
	}
	released := 0
	for _, c := range calls {
		if c.Kind != "credential_provider" {
			t.Fatalf("recovery replayed feature: %+v", c)
		}
		if c.Event.Phase == "credential.release" && c.Status == "completed" {
			released++
		}
	}
	if released != 1 {
		t.Fatalf("release count = %d, want 1", released)
	}
	_ = m.Close(context.Background())
	again := 0
	for _, c := range calls {
		if c.Event.Phase == "credential.release" && c.Status == "completed" {
			again++
		}
	}
	if again != released {
		t.Fatal("repeated close released lease twice")
	}
}

func TestLifecycle_SavePlanTerminalFailureRetainsBarrier(t *testing.T) {
	dir, binary := lifecycleFixture(t, `config = { fail_terminal = "session.close", fail_operation = "plan" }
 feature "delivery" { mode = "enforce" }`, `node "a" { source = "./module" }`)
	e, _ := loadLifecycleEngine(t, dir, binary)
	record, err := e.SavePlans(engine.Options{}, "")
	if err == nil {
		t.Fatal("required teardown failure accepted")
	}
	stored, err := e.GetExecution(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "needs_recovery" || stored.Status != "needs_recovery" || stored.Nodes[0].Phase != "planned" {
		t.Fatalf("got = %s / %+v, want planned node with recovery barrier", record.Status, stored)
	}
	if _, err := e.ApplySavedPlans(record.ID, engine.Options{AutoApprove: true}); err == nil {
		t.Fatal("unresolved teardown allowed saved apply")
	}
}

func TestLifecycle_SavedApplyTerminalFailureRetainsPlanEvidence(t *testing.T) {
	dir, binary := lifecycleFixture(t, `config = { fail_terminal = "run.finished", fail_operation = "apply" }
 feature "delivery" { mode = "enforce" }`, `node "a" { source = "./module" }`)
	e, _ := loadLifecycleEngine(t, dir, binary)
	record, err := e.SavePlans(engine.Options{}, "")
	if err != nil {
		t.Fatal(err)
	}
	planPath := filepath.Join(dir, ".terragraph", "executions", record.Nodes[0].PlanID+".bin")
	if _, err := os.Stat(planPath); err != nil {
		t.Fatal(err)
	}
	result, err := e.ApplySavedPlans(record.ID, engine.Options{AutoApprove: true})
	if err == nil {
		t.Fatal("required teardown failure accepted")
	}
	stored, err := e.GetExecution(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "needs_recovery" || stored.Nodes[0].Phase != "completed" || result.Nodes[0].Status != engine.StatusApplied {
		t.Fatalf("got = %+v / %+v, want completed infrastructure with recovery barrier", stored, result)
	}
	if _, err := os.Stat(planPath); err != nil {
		t.Fatalf("recovery evidence removed before teardown: %v", err)
	}
}
