package plugins

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	sdk "github.com/cloudfluent/terragraph/plugin"
)

type credentialScope struct {
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	env        map[string]string
	leases     []*credentialLease
	identities map[string]string
	ready      bool
	err        error
	expires    []time.Time
}

type credentialLease struct {
	identity  string
	feature   featureInstance
	reference map[string]any
	lease     sdk.Lease
	env       map[string]string
	stop      context.CancelFunc
	done      chan struct{}
}

func (m *Lifecycle) scope(ctx context.Context, node string) *credentialScope {
	m.nodesMu.Lock()
	defer m.nodesMu.Unlock()
	scope := m.nodes[node]
	if scope == nil {
		c, cancel := context.WithCancel(ctx)
		scope = &credentialScope{ctx: c, cancel: cancel, env: map[string]string{}}
		m.nodes[node] = scope
	}
	return scope
}

// Credentials isolates environment values by node and keeps renewable leases alive until all subprocesses have returned.
func (m *Lifecycle) Credentials(ctx context.Context, node string, bindings map[string]blueprint.PluginBinding) (context.Context, map[string]string, error) {
	if m == nil {
		if len(bindings) > 0 {
			return ctx, nil, fmt.Errorf("node.%s: credentials require an active plugin lifecycle", node)
		}
		return ctx, nil, nil
	}
	scope := m.scope(ctx, node)
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.err != nil {
		return scope.ctx, nil, scope.err
	}
	if scope.ready {
		return scope.ctx, maps.Clone(scope.env), scope.ctx.Err()
	}
	keys := make([]string, 0, len(bindings))
	for key := range bindings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		b := bindings[key]
		f, err := m.lookup(b.Alias, b.Feature, "credential_provider")
		if err != nil {
			return ctx, nil, err
		}
		response, err := m.invoke(ctx, f, sdk.Request{Action: "acquire", Reference: b.Reference, Event: sdk.Event{Node: node, Phase: "credential.acquire"}})
		if err != nil {
			if response.Lease != nil {
				lease := &credentialLease{identity: response.Identity, feature: f, reference: b.Reference, lease: *response.Lease, done: make(chan struct{})}
				close(lease.done)
				scope.leases = append(scope.leases, lease)
			}
			scope.err = err
			return ctx, nil, err
		}
		if response.Identity == "" {
			if response.Lease != nil {
				lease := &credentialLease{feature: f, reference: b.Reference, lease: *response.Lease, done: make(chan struct{})}
				close(lease.done)
				scope.leases = append(scope.leases, lease)
			}
			scope.err = fmt.Errorf("plugin.%s.%s: credentials require a stable non-secret target identity", b.Alias, b.Feature)
			return ctx, nil, scope.err
		}
		if scope.identities == nil {
			scope.identities = map[string]string{}
		}
		scope.identities[key] = response.Identity
		if response.Lease != nil {
			lease := &credentialLease{identity: response.Identity, feature: f, reference: b.Reference, lease: *response.Lease, env: maps.Clone(response.Credentials), done: make(chan struct{})}
			scope.leases = append(scope.leases, lease)
			if response.Lease.ID == "" || !response.Lease.ExpiresAt.After(time.Now()) || (!response.Lease.RenewAt.IsZero() && (!response.Lease.RenewAt.After(time.Now()) || !response.Lease.RenewAt.Before(response.Lease.ExpiresAt))) {
				close(lease.done)
				scope.err = fmt.Errorf("plugin.%s.%s: invalid credential lease", b.Alias, b.Feature)
				return ctx, nil, scope.err
			}
			worker, stop := context.WithCancel(context.WithoutCancel(ctx))
			lease.stop = stop
			go m.renew(worker, node, scope, lease)
		}
		if len(response.Credentials) == 0 {
			scope.err = fmt.Errorf("plugin.%s.%s: credential provider returned no credentials", b.Alias, b.Feature)
			return ctx, nil, scope.err
		}
		for k, v := range response.Credentials {
			if !slices.Contains(b.Environment, k) || !blueprint.CredentialEnvironmentAllowed(k) {
				scope.err = fmt.Errorf("plugin.%s.%s: returned an environment name outside credential allowlist", b.Alias, b.Feature)
				return ctx, nil, scope.err
			}
			if _, exists := scope.env[k]; exists {
				scope.err = fmt.Errorf("node.%s.credential.%s: duplicate environment supplier; remove one binding", node, key)
				return ctx, nil, scope.err
			}
			scope.env[k] = v
		}
	}
	scope.ready = true
	return scope.ctx, maps.Clone(scope.env), nil
}

func (m *Lifecycle) renew(ctx context.Context, node string, scope *credentialScope, lease *credentialLease) {
	defer close(lease.done)
	for {
		next := lease.lease.RenewAt
		if next.IsZero() {
			next = lease.lease.ExpiresAt
		}
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		var err error
		if lease.lease.RenewAt.IsZero() {
			err = fmt.Errorf("node.%s: plugin credentials expired; obtain a new lease before retrying", node)
		} else {
			deadline, cancel := context.WithDeadline(ctx, lease.lease.ExpiresAt)
			response, callErr := m.invoke(deadline, lease.feature, sdk.Request{Action: "renew", Reference: lease.reference, Lease: &lease.lease, Event: sdk.Event{Node: node, Phase: "credential.renew"}})
			cancel()
			err = callErr
			if err == nil {
				if (response.Identity != "" && response.Identity != lease.identity) || response.Lease == nil || response.Lease.ID != lease.lease.ID || !response.Lease.ExpiresAt.After(time.Now()) || (!response.Lease.RenewAt.IsZero() && (!response.Lease.RenewAt.After(time.Now()) || !response.Lease.RenewAt.Before(response.Lease.ExpiresAt))) {
					err = fmt.Errorf("node.%s: plugin returned an invalid renewed lease", node)
				} else if len(response.Credentials) > 0 && !maps.Equal(response.Credentials, lease.env) {
					err = fmt.Errorf("node.%s: credential rotation cannot update an already running subprocess; retry with stable renewable credentials", node)
				} else {
					lease.lease = *response.Lease
				}
			}
		}
		if err != nil {
			scope.mu.Lock()
			scope.err = err
			scope.cancel()
			scope.mu.Unlock()
			return
		}
	}
}

// Admit checks ephemeral inputs again after approval waits without replacing values baked into the native plan.
func (m *Lifecycle) Admit(node string) error {
	if m == nil {
		return nil
	}
	m.nodesMu.Lock()
	scope := m.nodes[node]
	m.nodesMu.Unlock()
	if scope == nil {
		return nil
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.err != nil {
		return scope.err
	}
	for _, expiry := range scope.expires {
		if !expiry.After(time.Now()) {
			return fmt.Errorf("node.%s: plugin input expired; create a fresh plan", node)
		}
	}
	return scope.ctx.Err()
}

func (m *Lifecycle) closeCredentials(ctx context.Context) error {
	var result error
	m.nodesMu.Lock()
	scopes := maps.Clone(m.nodes)
	m.nodesMu.Unlock()
	for node, scope := range scopes {
		for _, lease := range scope.leases {
			if lease.stop != nil {
				lease.stop()
			}
			<-lease.done
		}
		scope.mu.Lock()
		result = errors.Join(result, scope.err)
		scope.mu.Unlock()
		for i := len(scope.leases) - 1; i >= 0; i-- {
			lease := scope.leases[i]
			_, err := m.invoke(ctx, lease.feature, sdk.Request{Action: "release", Reference: lease.reference, Lease: &lease.lease, Event: sdk.Event{Node: node, Phase: "credential.release"}})
			result = errors.Join(result, err)
		}
		scope.cancel()
	}
	return result
}

// CredentialIdentities excludes token bytes while binding saved plans to the authenticated targets used during planning.
func (m *Lifecycle) CredentialIdentities(node string) map[string]string {
	if m == nil {
		return nil
	}
	m.nodesMu.Lock()
	scope := m.nodes[node]
	m.nodesMu.Unlock()
	if scope == nil {
		return nil
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	return maps.Clone(scope.identities)
}
