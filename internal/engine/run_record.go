package engine

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

// runRecord omits errors, outputs, variables, and plans so an opt-in retry receipt cannot persist secret payloads.
type runRecord struct {
	Schema    int          `json:"schema"`
	Operation string       `json:"operation"`
	Graph     string       `json:"graph"`
	Nodes     []recordNode `json:"nodes"`
}
type recordNode struct {
	Node   string `json:"node"`
	Status string `json:"status"`
}

// graphIdentity binds retry selection to execution identity without storing the underlying environment or backend configuration.
func (e *Engine) graphIdentity() (string, error) {
	type nodeIdentity struct {
		Node    blueprint.Node
		Dir     string
		Runtime string
		Approve blueprint.Approve
		Env     map[string]string
	}
	nodes := map[string]nodeIdentity{}
	for name, node := range e.Graph.Nodes {
		nodes[name] = nodeIdentity{Node: node.Node, Dir: node.Dir, Runtime: string(e.runtimeFor(name)), Env: node.Env, Approve: node.Approve}
	}
	edges := append([]blueprint.Edge(nil), e.Graph.Edges...)
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From.String() != edges[j].From.String() {
			return edges[i].From.String() < edges[j].From.String()
		}
		return edges[i].To.String() < edges[j].To.String()
	})
	data, err := json.Marshal(struct {
		Base  string
		Nodes map[string]nodeIdentity
		Edges []blueprint.Edge
	}{e.BaseDir, nodes, edges})
	if err != nil {
		return "", fmt.Errorf("execution history: cannot fingerprint graph configuration; use JSON-compatible input values")
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

func (e *Engine) recordPath(operation string) (string, error) {
	if operation != "plan" && operation != "apply" && operation != "destroy" {
		return "", fmt.Errorf("execution history: unsupported operation %q", operation)
	}
	return filepath.Join(e.BaseDir, ".terragraph", "runs", "last-"+operation+".json"), nil
}

// writeRunRecord atomically replaces an owner-only receipt under the run lock so a crash leaves either the last checkpoint or the next one.
func (e *Engine) writeRunRecord(operation string, runs []NodeRun) error {
	path, err := e.recordPath(operation)
	if err != nil {
		return err
	}
	identity, err := e.graphIdentity()
	if err != nil {
		return err
	}
	record := runRecord{Schema: 1, Operation: operation, Graph: identity, Nodes: make([]recordNode, len(runs))}
	for i, run := range runs {
		record.Nodes[i] = recordNode{Node: run.Node, Status: run.Status}
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("execution history: encoding receipt: %w", err)
	}
	dir := filepath.Dir(path)
	cleanup, err := prepareSavedPlan(filepath.Join(dir, "prepare"))
	if err != nil {
		return fmt.Errorf("execution history: preparing private directory: %w", err)
	}
	cleanup()
	// Use the platform-protected creator because a Windows file needs its own ACL even inside a private directory.
	temporary := filepath.Join(dir, ".run-"+rand.Text()+".json")
	file, err := createPreparedPlan(temporary)
	if err != nil {
		return fmt.Errorf("execution history: creating receipt: %w", err)
	}
	defer func() { _ = os.Remove(temporary) }()
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return fmt.Errorf("execution history: writing receipt: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("execution history: syncing receipt: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("execution history: closing receipt: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("execution history: replacing receipt: %w", err)
	}
	return nil
}

// resumeOptions uses receipts only as target suggestions; current graph validation, live reads, refreshed plans, approval and destroy scope checks still apply.
func (e *Engine) resumeOptions(opts Options, operation string) (Options, error) {
	if opts.Node != "" || len(opts.Nodes) != 0 {
		return opts, fmt.Errorf("--resume cannot be combined with --node; use preview to inspect unfinished targets")
	}
	path, err := e.recordPath(operation)
	if err != nil {
		return opts, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return opts, fmt.Errorf("execution history: reading previous %s run: %w; run with --record-run first", operation, err)
	}
	var record runRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil || decoder.Decode(new(any)) != io.EOF {
		return opts, fmt.Errorf("execution history: invalid receipt; rerun without --resume")
	}
	identity, err := e.graphIdentity()
	if err != nil {
		return opts, err
	}
	if record.Schema != 1 || record.Operation != operation || record.Graph != identity || record.Nodes == nil {
		return opts, fmt.Errorf("execution history: receipt does not match the current graph and operation; select targets explicitly and rerun")
	}
	seen := map[string]bool{}
	for _, node := range record.Nodes {
		if _, ok := e.Graph.Nodes[node.Node]; !ok || seen[node.Node] {
			return opts, fmt.Errorf("execution history: unknown or duplicate node; rerun without --resume")
		}
		seen[node.Node] = true
		switch node.Status {
		case StatusFailed, StatusNotRun, StatusRunning:
			opts.Nodes = append(opts.Nodes, node.Node)
		case StatusApplied, StatusUnchanged:
			if operation != "apply" {
				return opts, fmt.Errorf("execution history: invalid status for %s; rerun without --resume", operation)
			}
		case StatusDestroyed:
			if operation != "destroy" {
				return opts, fmt.Errorf("execution history: invalid status for %s; rerun without --resume", operation)
			}
		case StatusPlanned:
			if operation != "plan" {
				return opts, fmt.Errorf("execution history: invalid status for %s; rerun without --resume", operation)
			}
		default:
			return opts, fmt.Errorf("execution history: unknown status; rerun without --resume")
		}
	}
	if len(opts.Nodes) == 0 {
		return opts, fmt.Errorf("execution history: no unfinished nodes; run without --resume to check current infrastructure")
	}
	// Successful ancestors must be freshly planned again before their outputs can authorize a resumed apply.
	if operation != "destroy" {
		opts.IncludeDependencies = true
	}
	opts.Resume = false
	opts.RecordRun = true
	return opts, nil
}

func (e *Engine) prepareOptions(opts Options, operation string) (Options, error) {
	opts.operation = operation
	if err := e.validateOptions(opts); err != nil {
		return opts, err
	}
	if opts.Resume {
		var err error
		opts, err = e.resumeOptions(opts, operation)
		if err != nil {
			return opts, err
		}
	}
	_, err := e.selection(opts)
	return opts, err
}
