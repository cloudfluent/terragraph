// Package engine orchestrates blueprint parsing, graph construction, and terraform/tofu execution into the plan/apply/destroy/validate operations exposed by the CLI.
package engine

import (
	"bufio"
	"context"
	"errors"
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
	"github.com/cloudfluent/terragraph/internal/graphlock"
	"github.com/cloudfluent/terragraph/internal/runlock"
)

// Engine holds a loaded blueprint's graph and the I/O streams terraform/tofu subprocess output is forwarded to.
type Engine struct {
	Binary    exec.Binary
	BaseDir   string
	Blueprint *blueprint.Blueprint
	Graph     *graph.Graph
	Stdout    io.Writer
	Stderr    io.Writer
	// Stdin is where an interactive approval is read from. Nil means no answer can be obtained, which is not an error in itself: a run where nothing has changed never asks. Apply only fails on a missing Stdin at the moment a node actually needs approving.
	Stdin io.Reader
	// Logger receives internal-machinery diagnostics (node dispatch, plan verdicts, load steps); it never carries a command's actual result, which always goes through Stdout/Stderr directly. Nil is valid and discards everything, so callers that don't care about logging (including every existing test that builds an Engine by hand) need no changes.
	Logger *slog.Logger

	stdin *bufio.Reader
	// runLock is the lock LoadLocked already holds. lockRun must not Close it; the LoadLocked caller owns the lifetime.
	runLock *runlock.Lock
}

// approvals is the single reader every prompt shares. A fresh bufio.Reader per question would buffer past the newline it needed and swallow the next question's answer.
func (e *Engine) approvals() *bufio.Reader {
	if e.stdin == nil {
		e.stdin = bufio.NewReader(e.Stdin)
	}
	return e.stdin
}

// errNoApproval reports that a node needs approving and there is no answer to be had — stdin was never wired up, or it is closed or empty (a CI runner with no terminal, `</dev/null`). Distinct from a declined approval, which is a decision someone actually made.
var errNoApproval = errors.New("no approval could be read")

func noApprovalError(name string) error {
	return fmt.Errorf("node %s has changes but %w; pass --auto-approve to apply without asking", name, errNoApproval)
}

// approve asks whether name's planned changes should be applied, having just streamed that node's plan to out. Only "y" or "yes" approves.
//
// An immediate EOF is not a "no": there is a difference between someone declining and nobody being there, and silently treating an unattended run as a decline would report a refusal nobody made. Input that ends without a trailing newline still counts, so `echo yes | terragraph apply` works — which is why this reads for content rather than testing whether stdin is a terminal.
func (e *Engine) approve(name string, out io.Writer) (bool, error) {
	if e.Stdin == nil {
		return false, noApprovalError(name)
	}
	_, _ = fmt.Fprintf(out, "\nApply these changes to node %s? [y/N]: ", name)
	line, err := e.approvals().ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer == "" && err != nil {
		_, _ = fmt.Fprintln(out)
		return false, noApprovalError(name)
	}
	return answer == "y" || answer == "yes", nil
}

func (e *Engine) logger() *slog.Logger {
	if e.Logger != nil {
		return e.Logger
	}
	return slog.New(slog.DiscardHandler)
}

// lockRun serializes this process against any other terragraph plan/apply/destroy/vendor
// targeting the same blueprint. In-process --parallelism is unaffected: it shares this
// one lock. See internal/runlock. The returned func releases a lock this call acquired;
// it is a no-op when LoadLocked already holds one.
func (e *Engine) lockRun() (func(), error) {
	if e.runLock != nil {
		return func() {}, nil
	}
	e.logger().Debug("acquiring blueprint lock", "dir", e.BaseDir)
	lock, err := runlock.Acquire(e.BaseDir, e.Stderr)
	if err != nil {
		return nil, fmt.Errorf("locking blueprint: %w", err)
	}
	return func() { _ = lock.Close() }, nil
}

var acquireRemoteLock = graphlock.Acquire

func (e *Engine) lockGraph() (func(), error) {
	if e.Blueprint == nil || e.Blueprint.Lock == nil {
		return func() {}, nil
	}
	e.logger().Debug("acquiring graph lock")
	held, err := acquireRemoteLock(context.Background(), e.Blueprint.Lock)
	if err != nil {
		return nil, fmt.Errorf("locking graph: %w", err)
	}
	return func() {
		if err := held.Close(); err != nil {
			w := e.Stderr
			if w == nil {
				w = os.Stderr
			}
			_, _ = fmt.Fprintf(w, "releasing graph lock: %v\n", err)
		}
	}, nil
}

// Load parses the blueprint at blueprintPath and builds its graph. blueprintPath may name a single file or a directory (every .hcl file directly inside it is merged, see blueprint.LoadPath); node sources are resolved relative to the resulting base directory. It does not take the process lock; use LoadLocked for plan/apply/destroy so graph.Build cannot inspect module files while vendor rewrites them.
func Load(blueprintPath string, binary exec.Binary, stdout, stderr io.Writer) (*Engine, error) {
	e, _, err := load(blueprintPath, binary, stdout, stderr, false)
	return e, err
}

// LoadLocked is Load after taking the blueprint process lock, and holds it across graph.Build so a concurrent vendor cannot rewrite module sources underneath Inspect. The caller must invoke the returned func when the run ends.
func LoadLocked(blueprintPath string, binary exec.Binary, stdout, stderr io.Writer) (*Engine, func(), error) {
	e, lock, err := load(blueprintPath, binary, stdout, stderr, true)
	if err != nil {
		return nil, nil, err
	}
	return e, func() {
		_ = lock.Close()
		e.runLock = nil
	}, nil
}

func load(blueprintPath string, binary exec.Binary, stdout, stderr io.Writer, takeLock bool) (*Engine, *runlock.Lock, error) {
	bp, dir, err := blueprint.LoadPath(blueprintPath)
	if err != nil {
		return nil, nil, err
	}

	// Absolute, so paths derived from it (DataDir in particular) are unambiguous no matter what working directory a terraform/tofu subprocess runs with. A relative TF_DATA_DIR would otherwise be resolved relative to the subprocess's own cwd (the node's source dir), not this process's, producing a nested, wrong path.
	baseDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("resolving blueprint directory: %w", err)
	}

	var lock *runlock.Lock
	if takeLock {
		lock, err = runlock.Acquire(baseDir, stderr)
		if err != nil {
			return nil, nil, fmt.Errorf("locking blueprint: %w", err)
		}
	}

	g, err := graph.Build(bp, baseDir)
	if err != nil {
		if lock != nil {
			_ = lock.Close()
		}
		return nil, nil, err
	}

	// The mode is blueprint-owned, reviewed configuration; wiring it here keeps graph.Build ignorant of it (severity is a validate-time concern only).
	g.ContractMode = bp.ContractMode

	return &Engine{
		Binary:    binary,
		BaseDir:   baseDir,
		Blueprint: bp,
		Graph:     g,
		Stdout:    stdout,
		Stderr:    stderr,
		runLock:   lock,
	}, lock, nil
}

// Validate returns every structural problem found in the graph (missing ports, two data edges targeting the same input, a data edge and vars both setting the same input, cycles, unresolved required variables, backend_config without a backend block, identical backend_config maps on a shared module directory) plus any tfvars orphan warnings (see tfVarsOrphans), stale local-backend state warnings (see stateOrphans) and shared-source runtime conflict warnings (see runtimeConflicts). Check each Problem's IsError(): only errors should block graph/plan/apply/destroy, warnings are advisory.
func (e *Engine) Validate() []graph.Problem {
	problems := graph.Validate(e.Graph)
	problems = append(problems, e.tfVarsOrphans()...)
	problems = append(problems, e.stateOrphans()...)
	problems = append(problems, e.runtimeConflicts()...)
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

// planPath returns where the engine has a node's verification plan written before deciding whether to apply it (see Runner.PlanChanges/ApplyPlan). It sits beside the engine's other managed per-node state rather than inside dataDir, which is Terraform's own (see dataDir): a plan file is terragraph's artifact, not part of Terraform's metadata.
//
// A saved plan embeds the resolved input values it was created with, in cleartext, exactly as the tfvars file does. It therefore lives under the same .terragraph/ directory that tfVarsPath's workdir default already requires be kept out of version control, and apply removes it once the run is over.
func (e *Engine) planPath(name string) string {
	return filepath.Join(e.BaseDir, ".terragraph", "plans", name+".tfplan")
}

// dataDir returns the node's isolated TF_DATA_DIR (see Runner.DataDir). Every node gets one, always (not just nodes with a shared Source), keeping the module's own directory untouched by tool-managed metadata and every node's .terraform/ state independent of the others'.
func (e *Engine) dataDir(name string) string {
	return filepath.Join(e.BaseDir, ".terragraph", "tfdata", name)
}

// runner builds a Runner for internal, non-buffered use (reading an upstream node's already-applied outputs). The per-node runners used for the actual plan/apply/destroy commands (see plan.go/apply.go/destroy.go) are built separately, against that node's own buffered output writer.
func (e *Engine) runner(name string) *exec.Runner {
	return &exec.Runner{Binary: e.runtimeFor(name), Dir: e.nodeDir(name), DataDir: e.dataDir(name), Env: e.envFor(name), Stdout: e.Stdout, Stderr: e.Stderr}
}

// envFor returns name's fully resolved extra environment variables (see graph.Node.Env): whatever an enclosing Use.Env cascade contributed, already merged with the node's own Env. Unlike runtimeFor, there is no further CLI-level fallback layer to apply on top: env has no CLI equivalent, so whatever the graph already resolved is final.
func (e *Engine) envFor(name string) map[string]string {
	return e.Graph.Nodes[name].Env
}

// runtimeFor resolves which binary a node actually runs against, applying each fallback layer in order until one supplies an answer: (1) the node's own resolved blueprint.Runtime, already following the blueprint.Node.Runtime -> enclosing blueprint.Use.Runtime cascade (see graph.Node.Runtime); (2) the top-level blueprint's own Default-marked runtime, if it declared one (deliberately never a group's own default: see blueprint.Runtime.Default); (3) e.Binary, the CLI's --tofu flag or its own built-in terraform default. A CLI flag can only ever fill a gap nothing else spoke to, never override an explicit choice made in the blueprint.
//
// A runtime block's `version` is deliberately not consulted. It once fed the incremental-apply cache key; with that cache gone (nothing is trusted without asking Terraform), it records what a node is expected to run against and has no effect on execution. See docs/blueprint.md.
func (e *Engine) runtimeFor(name string) exec.Binary {
	if rt := e.Graph.Nodes[name].Runtime; rt != nil {
		return exec.Binary(rt.Binary)
	}
	if e.Blueprint != nil {
		if rt, ok := e.Blueprint.DefaultRuntime(); ok {
			return exec.Binary(rt.Binary)
		}
	}
	return e.Binary
}

// runtimeConflicts warns about two or more nodes that share a module directory (see Node.BackendConfig, the mechanism for reusing one Source across instances) but resolve to different binaries. They also share that directory's single .terraform.lock.hcl, and Terraform/OpenTofu each rewrite it to their own registry host on every init (registry.terraform.io vs registry.opentofu.org), so whichever node happened to init last wins the file underneath the other and every run rewrites what the previous one wrote. Nothing else in the model can trigger this: every node's own .terraform/ metadata is already isolated per node (see dataDir), so this is specific to the shared-Source pattern.
func (e *Engine) runtimeConflicts() []graph.Problem {
	byDir := map[string][]string{}
	names := make([]string, 0, len(e.Graph.Nodes))
	for name := range e.Graph.Nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		dir := e.Graph.Nodes[name].Dir
		byDir[dir] = append(byDir[dir], name)
	}

	dirs := make([]string, 0, len(byDir))
	for dir := range byDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	var problems []graph.Problem
	for _, dir := range dirs {
		nodesInDir := byDir[dir]
		if len(nodesInDir) < 2 {
			continue
		}
		first := e.runtimeFor(nodesInDir[0])
		mixed := false
		for _, n := range nodesInDir[1:] {
			if e.runtimeFor(n) != first {
				mixed = true
				break
			}
		}
		if mixed {
			problems = append(problems, graph.Problem{
				Severity: graph.SeverityWarning,
				Message: fmt.Sprintf(
					"%s: nodes %s share this module directory but resolve to different runtimes; their .terraform.lock.hcl will conflict on every apply",
					dir, strings.Join(nodesInDir, ", "),
				),
			})
		}
	}
	return problems
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

// stateOrphans warns about a local-backend state file left in <BaseDir>/.terragraph/state by a node that no longer owns it (renamed or removed from the blueprint), so a rename doesn't silently strand the old state where a future node of that name would silently adopt it. Files are claimed by the basename of every node's resolved backend_config path, not by node name: a node may point an explicit path at a differently named file under this directory (see blueprint.md), and graph.Build default-fills the rest to <name>.tfstate anyway — so a basename no current node resolves to is an orphan. Never deletes anything; it is the user's call whether the node was renamed (rename it back) or truly abandoned (remove the file or re-import the state elsewhere).
func (e *Engine) stateOrphans() []graph.Problem {
	dir := filepath.Join(e.BaseDir, ".terragraph", "state")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // No state directory yet (fresh or never-applied project): nothing can be orphaned.
	}
	claimed := make(map[string]bool)
	for _, n := range e.Graph.Nodes {
		if p, ok := n.BackendConfig["path"]; ok {
			claimed[filepath.Base(p)] = true
		}
	}
	var stale []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		fname := entry.Name()
		if !strings.HasSuffix(fname, ".tfstate") {
			continue
		}
		if !claimed[fname] {
			stale = append(stale, fname)
		}
	}
	sort.Strings(stale)
	var problems []graph.Problem
	for _, fname := range stale {
		problems = append(problems, graph.Problem{
			Severity: graph.SeverityWarning,
			Message:  fmt.Sprintf("%s: state for a node that no longer exists in this blueprint; if the node was renamed, rename it back (or remove/import the state file if abandoned)", filepath.Join(dir, fname)),
		})
	}
	return problems
}
