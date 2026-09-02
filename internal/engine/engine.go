// Package engine orchestrates blueprint parsing, graph construction, and terraform/tofu execution into the plan/apply/destroy/validate operations exposed by the CLI.
package engine

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

// Load parses the blueprint at blueprintPath and builds its graph. blueprintPath may name a single file or a directory (every .hcl file directly inside it is merged, see blueprint.LoadPath); node sources are resolved relative to the resulting base directory.
func Load(blueprintPath string, binary exec.Binary, stdout, stderr io.Writer) (*Engine, error) {
	bp, dir, err := blueprint.LoadPath(blueprintPath)
	if err != nil {
		return nil, err
	}

	// Absolute, so paths derived from it (DataDir in particular) are unambiguous no matter what working directory a terraform/tofu subprocess runs with. A relative TF_DATA_DIR would otherwise be resolved relative to the subprocess's own cwd (the node's source dir), not this process's, producing a nested, wrong path.
	baseDir, err := filepath.Abs(dir)
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

// Validate returns every structural problem found in the graph (missing ports, cycles, unresolved required variables) plus any tfvars orphan warnings (see tfVarsOrphans). Check each Problem's IsError(): only errors should block graph/plan/apply/destroy, warnings are advisory.
func (e *Engine) Validate() []graph.Problem {
	problems := graph.Validate(e.Graph)
	problems = append(problems, e.tfVarsOrphans()...)
	return problems
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

// tfVarsFileName is the base filename for a node's ephemeral tfvars file, shared by both TFVarsLocation modes: workdir puts it under its own per-node subdirectory, so this alone is unambiguous there, while module puts every node's file directly in the (possibly shared) module directory, where the leading "." plus this name is what keeps one node's file from colliding with another's (see tfVarsPath).
func tfVarsFileName(name string) string {
	return fmt.Sprintf(".terragraph.%s.tfvars.json", name)
}

// tfVarsPath returns where the engine writes name's resolved input values before every plan/apply/destroy, per the blueprint's configured blueprint.TFVarsLocation.
func (e *Engine) tfVarsPath(name string) string {
	if e.Blueprint.TFVarsLocation() == blueprint.TFVarsLocationModule {
		return filepath.Join(e.nodeDir(name), tfVarsFileName(name))
	}
	return filepath.Join(e.BaseDir, ".terragraph", "vars", name+".tfvars.json")
}

// tfVarsOrphans warns about a stale module-location tfvars file left behind in a node's directory by a node that no longer exists under that name (renamed or removed from the blueprint), so it doesn't sit there indefinitely looking like current state. Only relevant for TFVarsLocationModule: the workdir location namespaces every node into its own subdirectory, so nothing there can ever go stale from another node's rename. Never deletes anything; a module directory (especially a vendored or otherwise not-yours-to-write one) is not terragraph's to clean up unasked.
func (e *Engine) tfVarsOrphans() []graph.Problem {
	if e.Blueprint == nil || e.Blueprint.TFVarsLocation() != blueprint.TFVarsLocationModule {
		return nil
	}

	currentByDir := map[string]map[string]bool{}
	for name, n := range e.Graph.Nodes {
		if currentByDir[n.Dir] == nil {
			currentByDir[n.Dir] = map[string]bool{}
		}
		currentByDir[n.Dir][name] = true
	}

	var problems []graph.Problem
	dirs := make([]string, 0, len(currentByDir))
	for dir := range currentByDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // Build already stat'd this directory successfully; a failure here isn't worth failing validate over.
		}
		var stale []string
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			fname := entry.Name()
			if !strings.HasPrefix(fname, ".terragraph.") || !strings.HasSuffix(fname, ".tfvars.json") {
				continue
			}
			owner := strings.TrimSuffix(strings.TrimPrefix(fname, ".terragraph."), ".tfvars.json")
			if !currentByDir[dir][owner] {
				stale = append(stale, fname)
			}
		}
		sort.Strings(stale)
		for _, fname := range stale {
			problems = append(problems, graph.Problem{
				Severity: graph.SeverityWarning,
				Message:  fmt.Sprintf("%s: belongs to no node in this blueprint; remove it if the node was renamed or deleted", filepath.Join(dir, fname)),
			})
		}
	}
	return problems
}
