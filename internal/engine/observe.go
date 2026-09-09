package engine

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cloudfluent/terragraph/internal/exec"
	"github.com/cloudfluent/terragraph/internal/graph"
)

// Diagnostic supplies stable branching fields without reflecting runtime output that may contain secrets.
type Diagnostic struct {
	Code, Phase, Subject, Message, Remedy  string
	Category, Severity, RelatedExecutionID string
}

// ObservationSession owns the local source lock and private caches until all reads finish.
type ObservationSession struct {
	engine *Engine
	dir    string
	unlock func()
}

// OpenObservation holds the existing exclusive local lock across source inspection and runtime reads; no remote graph lock is acquired.
func OpenObservation(ctx context.Context, path string, binary exec.Binary, stderr io.Writer) (*ObservationSession, error) {
	e, lock, err := load(ctx, path, binary, io.Discard, stderr, true, true)
	if err != nil {
		return nil, err
	}
	s := &ObservationSession{engine: e, unlock: func() { _ = lock.Close() }}
	parent := filepath.Join(e.BaseDir, ".terragraph", "tfdata-read")
	// Reuse saved-plan directory protection before creating a process-unique subtree.
	cleanup, err := prepareSavedPlan(filepath.Join(parent, "prepare"))
	if err != nil {
		s.Close()
		return nil, err
	}
	cleanup()
	s.dir, err = os.MkdirTemp(parent, "read-")
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("creating observation directory: %w", err)
	}
	return s, nil
}

// Close removes backend credentials cached by init before releasing source coordination.
func (s *ObservationSession) Close() {
	if s.dir != "" {
		_ = os.RemoveAll(s.dir)
	}
	if s.unlock != nil {
		s.unlock()
		s.unlock = nil
	}
}

// Observation keeps unavailable evidence distinct from successful empty collections.
type Observation struct {
	Node, Runtime, Backend, Identity, State string
	Outputs                                 exec.Outputs
	Resources, OutputCount                  int
	Diagnostic                              *Diagnostic
}

func (s *ObservationSession) Names(target string) ([]string, error) {
	names := make([]string, 0, len(s.engine.Graph.Nodes))
	for name := range s.engine.Graph.Nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	if target == "" {
		return names, nil
	}
	if _, ok := s.engine.Graph.Nodes[target]; ok {
		return []string{target}, nil
	}
	candidates := []string{}
	for _, name := range names {
		if strings.HasPrefix(name, target+".") {
			candidates = append(candidates, name)
		}
	}
	if len(candidates) == 0 {
		candidates = names
	}
	return nil, fmt.Errorf("unknown node %q; select an expanded leaf with --node: %s", target, strings.Join(candidates, ", "))
}

func observationFailure(result Observation, code, phase, message, remedy string) Observation {
	result.State = "unavailable"
	result.Diagnostic = &Diagnostic{Code: code, Phase: phase, Subject: "node." + result.Node, Message: message, Remedy: remedy}
	return result
}

// Read observes one independently resolved leaf and never substitutes execution snapshots.
func (s *ObservationSession) Read(name string, status bool) Observation {
	e := s.engine
	n := e.Graph.Nodes[name]
	result := Observation{Node: name, Runtime: string(e.runtimeFor(name)), State: "indeterminate"}
	fail := func(code, phase, message, remedy string) Observation {
		if e.context().Err() != nil {
			code, message, remedy = "cancelled", "observation cancelled", "retry the observation"
		}
		return observationFailure(result, code, phase, message, remedy)
	}
	if err := e.context().Err(); err != nil {
		return fail("cancelled", "read", "observation cancelled", "retry the observation")
	}
	if n.ObservationError != nil || n.Schema == nil {
		return fail("module_unavailable", "load", "module cannot be inspected", "restore or vendor this node's source and correct its module syntax")
	}
	result.Backend = n.Schema.Backend
	if result.Backend == "" {
		result.Backend = "local"
	}
	if !n.Schema.BackendConfigKnown {
		return fail("backend_indeterminate", "prepare", "backend configuration is not statically known", "use literal backend configuration for observation")
	}
	env := maps.Clone(n.Env)
	if env == nil {
		env = map[string]string{}
	}
	workspace, _, envErr := e.runner(name).EnvironmentValue("TF_WORKSPACE")
	if envErr != nil {
		return fail("invalid_environment", "prepare", "environment conflicts with the managed data directory", "remove TF_DATA_DIR from the node environment")
	}
	if workspace != "" && workspace != "default" {
		return fail("unsupported_workspace", "prepare", "observation supports only the default workspace", "select an independently configured default-workspace node")
	}
	if result.Backend != "local" && result.Backend != "http" {
		return fail("unsupported_backend", "prepare", "backend has no verified read-only preparation capability", "use the backend's native inspection tools")
	}
	// User-supplied runtime arguments and logging could migrate state or write cleartext outside this session.
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
	env["TF_WORKSPACE"] = "default"
	cfg := maps.Clone(n.Schema.BackendConfig)
	if cfg == nil {
		cfg = map[string]string{}
	}
	maps.Copy(cfg, n.BackendConfig)
	identity := cfg["address"]
	if identity == "" {
		identity = env["TF_HTTP_ADDRESS"]
		if identity == "" {
			identity = os.Getenv("TF_HTTP_ADDRESS")
		}
	}
	if result.Backend == "local" {
		var known bool
		identity, known = graph.LocalStatePath(n)
		if !known {
			return fail("backend_indeterminate", "prepare", "local state identity is unknown", "use a literal local state path")
		}
		info, err := os.Stat(identity)
		if err != nil || !info.Mode().IsRegular() {
			return fail("local_state_unavailable", "prepare", "local state is unavailable in this checkout", "recover the existing state file on this machine; do not create replacement state")
		}
	}
	result.Identity = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(result.Backend+"\x00"+identity)))
	if info, err := os.Stat(filepath.Join(n.Dir, ".terraform.lock.hcl")); err != nil || !info.Mode().IsRegular() {
		return fail("lockfile_required", "prepare", "dependency lockfile is missing", "restore the module's committed .terraform.lock.hcl")
	}
	r := e.runner(name)
	r.DataDir = filepath.Join(s.dir, name)
	r.Env = env
	// Runtime diagnostics can echo credentials or unknown-sensitive values before they can be classified.
	r.Stdout, r.Stderr = io.Discard, io.Discard
	if err := r.ValidateEnv(); err != nil {
		return fail("invalid_environment", "prepare", "environment conflicts with the managed data directory", "remove TF_DATA_DIR from the node environment")
	}
	if n.Schema.RequiresTofuFiles {
		if err := r.RequireTofuFiles(); err != nil {
			return fail("unsupported_runtime", "prepare", "runtime cannot read the selected module files", "install a compatible OpenTofu runtime")
		}
	}
	if err := r.InitRead(n.BackendConfig); err != nil {
		return fail("initialization_failed", "prepare", "read-only backend preparation failed", "check runtime, credentials, existing backend, and committed provider lockfile")
	}
	if status {
		state, err := r.ObserveState()
		if err != nil {
			return fail("state_read_failed", "read", "state could not be observed", "check backend access and runtime compatibility")
		}
		result.State = "absent"
		if state.Present {
			result.State = "present"
			if state.Empty {
				result.State = "empty"
				if !state.Determinate {
					result.State = "indeterminate"
				}
			}
		}
		result.Resources, result.OutputCount = state.Resources, state.Outputs
		return result
	}
	outputs, err := r.Outputs()
	if err != nil {
		return fail("output_read_failed", "read", "outputs could not be observed", "check backend access and runtime compatibility")
	}
	for key, output := range outputs {
		if n.Schema.OutputDetails[key].Sensitive {
			sensitive := true
			output.Sensitive = &sensitive
			outputs[key] = output
		}
	}
	result.Outputs, result.State = outputs, "observed"
	return result
}
