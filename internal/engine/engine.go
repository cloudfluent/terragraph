// Package engine orchestrates blueprint parsing, graph construction, and terraform/tofu execution into the plan/apply/destroy/validate operations exposed by the CLI.
package engine

import (
	"fmt"
	"io"
	"log/slog"
	"path/filepath"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/exec"
	"github.com/cloudfluent/terragraph/internal/graph"
)

// Engine holds a loaded blueprint's graph and the I/O streams terraform/tofu subprocess output is forwarded to.
type Engine struct {
	Binary    exec.Binary
	BaseDir   string
	Blueprint *blueprint.Blueprint
	Graph     *graph.Graph
	Stdout    io.Writer
	Stderr    io.Writer
	// Logger receives internal-machinery diagnostics (node dispatch, cache hits, load steps); it never carries a command's actual result, which always goes through Stdout/Stderr directly. Nil is valid and discards everything, so callers that don't care about logging (including every existing test that builds an Engine by hand) need no changes.
	Logger *slog.Logger
}

func (e *Engine) logger() *slog.Logger {
	if e.Logger != nil {
		return e.Logger
	}
	return slog.New(slog.DiscardHandler)
}

// Load parses the blueprint at blueprintPath and builds its graph. Node sources are resolved relative to the blueprint file's directory.
func Load(blueprintPath string, binary exec.Binary, stdout, stderr io.Writer) (*Engine, error) {
	bp, err := blueprint.ParseFile(blueprintPath)
	if err != nil {
		return nil, err
	}

	// Absolute, so paths derived from it (DataDir in particular) are unambiguous no matter what working directory a terraform/tofu subprocess runs with. A relative TF_DATA_DIR would otherwise be resolved relative to the subprocess's own cwd (the node's source dir), not this process's, producing a nested, wrong path.
	baseDir, err := filepath.Abs(filepath.Dir(blueprintPath))
	if err != nil {
		return nil, fmt.Errorf("resolving blueprint directory: %w", err)
	}
	g, err := graph.Build(bp, baseDir)
	if err != nil {
		return nil, err
	}

	return &Engine{
		Binary:    binary,
		BaseDir:   baseDir,
		Blueprint: bp,
		Graph:     g,
		Stdout:    stdout,
		Stderr:    stderr,
	}, nil
}

// Validate returns every structural problem found in the graph (missing ports, cycles, unresolved required variables). Check each Problem's IsError(): only errors should block graph/plan/apply/destroy, warnings are advisory.
func (e *Engine) Validate() []graph.Problem {
	return graph.Validate(e.Graph)
}

// TopoOrder returns node names in valid execution order.
func (e *Engine) TopoOrder() ([]string, error) {
	return graph.TopoSort(e.Graph)
}

// Levels returns node names grouped into execution layers: nodes within a layer have no edge between them and are safe to run concurrently.
func (e *Engine) Levels() ([][]string, error) {
	return graph.Levels(e.Graph)
}

func (e *Engine) nodeDir(name string) string {
	return e.Graph.Nodes[name].Dir
}

func (e *Engine) cachePath() string {
	return filepath.Join(e.BaseDir, ".terragraph", "cache.json")
}

// dataDir returns the node's isolated TF_DATA_DIR (see Runner.DataDir). Every node gets one, always (not just nodes with a shared Source), keeping the module's own directory untouched by tool-managed metadata and every node's .terraform/ state independent of the others'.
func (e *Engine) dataDir(name string) string {
	return filepath.Join(e.BaseDir, ".terragraph", "tfdata", name)
}

// runner builds a Runner for internal, non-buffered use (reading an upstream node's already-applied outputs). The per-node runners used for the actual plan/apply/destroy commands (see plan.go/apply.go/destroy.go) are built separately, against that node's own buffered output writer.
func (e *Engine) runner(name string) *exec.Runner {
	return &exec.Runner{Binary: e.Binary, Dir: e.nodeDir(name), DataDir: e.dataDir(name), Stdout: e.Stdout, Stderr: e.Stderr}
}
