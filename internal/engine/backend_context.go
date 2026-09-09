package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cloudfluent/terragraph/internal/exec"
)

// backendContext binds native initialization to the declaration that selected it; it is a cache guard, never infrastructure state.
type backendContext struct {
	Declaration string `json:"declaration"`
	Native      string `json:"native"`
}

func (e *Engine) backendDeclaration(name string) (string, error) {
	node := e.Graph.Nodes[name]
	target, err := e.executionTarget(name)
	if err != nil {
		return "", err
	}
	return executionDigest(struct {
		Dir, Target      string
		Schema, Override map[string]string
	}{node.Dir, target, node.Schema.BackendConfig, node.BackendConfig})
}

func (e *Engine) rememberBackendContext(name string, r *exec.Runner) error {
	declaration, err := e.backendDeclaration(name)
	if err != nil {
		return err
	}
	native, err := r.BackendContext()
	if err != nil {
		return err
	}
	prepare, err := prepareSavedPlan(filepath.Join(filepath.Dir(e.dataDir(name)), ".prepare-"+newExecutionID("context")))
	if err != nil {
		return err
	}
	prepare()
	path := filepath.Join(e.dataDir(name), ".terragraph-context.json")
	cleanup, err := prepareSavedPlan(path)
	if err != nil {
		return err
	}
	cleanup()
	data, err := json.Marshal(backendContext{Declaration: declaration, Native: native})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func (e *Engine) verifyBackendContext(name string, r *exec.Runner) error {
	data, err := os.ReadFile(filepath.Join(e.dataDir(name), ".terragraph-context.json"))
	if err != nil {
		return fmt.Errorf("node.%s: initialized node context is unavailable; run terragraph run --node %s -- init first", name, name)
	}
	var stored backendContext
	if err := json.Unmarshal(data, &stored); err != nil {
		return fmt.Errorf("node.%s: initialized context is invalid; run the node's explicit init command", name)
	}
	declaration, err := e.backendDeclaration(name)
	if err != nil {
		return err
	}
	native, err := r.BackendContext()
	if err != nil {
		return err
	}
	if stored.Declaration != declaration || stored.Native != native {
		return fmt.Errorf("node.%s: backend configuration or native cache changed; run the node's explicit init command before this operation", name)
	}
	return nil
}

// initNode shares native setup across planning paths and records its binding for later operations that deliberately do not auto-init.
func (e *Engine) initNode(name string, r *exec.Runner) error {
	if err := r.Init(e.Graph.Nodes[name].BackendConfig); err != nil {
		return err
	}
	return e.rememberBackendContext(name, r)
}
