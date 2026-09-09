package engine

import (
	"context"
	"errors"
	"fmt"
	"github.com/cloudfluent/terragraph/internal/exec"
	"github.com/cloudfluent/terragraph/internal/plugins"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// recoverNodeOutputs isolates backend caches; backends without verified read-only initialization require explicit, journaled preparation.
func (e *Engine) recoverNodeOutputs(session *executionSession, name string, allowInit bool) (resultErr error) {
	node := e.Graph.Nodes[name]
	if node.Schema == nil || !node.Schema.BackendConfigKnown {
		return fmt.Errorf("node.%s: backend configuration cannot be verified; restore literal backend configuration", name)
	}
	r := e.runner(name)
	if len(node.Credentials) > 0 {
		m, err := plugins.NewCredentialRecovery(plugins.WithLogger(e.context(), e.logger()), e.BaseDir, e.Blueprint.Plugins, session.record.ID, session.recordPlugin)
		if err != nil {
			return err
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(e.context()), 30*time.Second)
			defer cancel()
			resultErr = errors.Join(resultErr, m.Close(ctx))
		}()
		r.Hook = func(ctx context.Context, _ string) (context.Context, map[string]string, func(error) error, error) {
			credentialCtx, env, err := m.Credentials(ctx, name, node.Credentials)
			return exec.WithCredentialLifetime(credentialCtx), env, nil, err
		}
	}

	env := maps.Clone(r.Env)
	if env == nil {
		env = map[string]string{}
	}
	workspace, err := e.executionWorkspace(name)
	if err != nil {
		return err
	}
	env["TF_WORKSPACE"] = workspace
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(key), "TF_CLI_ARGS") || strings.HasPrefix(strings.ToUpper(key), "TF_LOG") {
			env[key] = ""
		}
	}
	for key := range env {
		if strings.HasPrefix(strings.ToUpper(key), "TF_CLI_ARGS") || strings.HasPrefix(strings.ToUpper(key), "TF_LOG") {
			env[key] = ""
		}
	}
	r.Env = env
	backend := node.Schema.Backend
	readOnly := (backend == "" || backend == "local" || backend == "http") && (workspace == "" || workspace == "default")
	if !readOnly && !allowInit {
		return fmt.Errorf("node.%s: backend has no verified read-only preparation; pass --initialize-backend to explicitly initialize an isolated backend cache before reading outputs", name)
	}
	if !readOnly && session.record.Preparation != "" {
		return fmt.Errorf("backend preparation has an unresolved outcome; inspect state before --state-reviewed --replan")
	}
	parent := filepath.Join(e.BaseDir, ".terragraph", "tfdata-read")
	cleanup, err := prepareSavedPlan(filepath.Join(parent, "prepare"))
	if err != nil {
		return err
	}
	cleanup()
	dir, err := os.MkdirTemp(parent, "recovery-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	r.DataDir = dir
	if !readOnly {
		next := session.record
		next.Preparation = "initializing:" + name
		if err := session.publish(next); err != nil {
			return err
		}
	}
	if err := r.InitRead(node.BackendConfig); err != nil {
		return fmt.Errorf("node.%s: preparing output recovery: %w", name, err)
	}
	if !readOnly {
		next := session.record
		next.Preparation = ""
		if err := session.publish(next); err != nil {
			return err
		}
	}
	outputs, err := r.Outputs()
	if err != nil {
		return fmt.Errorf("node.%s: reading outputs without reapplying: %w", name, err)
	}
	if err := e.validateOutputContracts(name, outputs); err != nil {
		return err
	}
	return e.writeSnapshot(name, outputs)
}
