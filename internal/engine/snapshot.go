package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// snapshotPath returns where apply publishes name's output snapshot:
// <BaseDir>/.terragraph/outputs/<name>.json, beside the engine's other managed
// per-node state (plans, tfdata, state). Local and regenerable by every apply,
// so it is gitignored output, never committed evidence.
func (e *Engine) snapshotPath(name string) string {
	return filepath.Join(e.BaseDir, ".terragraph", "outputs", name+".json")
}

// snapshotFile is the on-disk shape of an output snapshot. Schema lets a future
// reader refuse a format it does not understand instead of guessing at it.
type snapshotFile struct {
	Schema  int            `json:"schema"`
	Node    string         `json:"node"`
	Outputs map[string]any `json:"outputs"`
}

// writeSnapshot publishes name's outputs as a local snapshot, but only when the
// graph opted in via its `snapshots { }` block (see Graph.Snapshots): a graph
// that did not ask writes nothing and cannot fail here, keeping apply
// byte-identical to a snapshot-unaware run.
//
// Only outputs a data edge actually consumes are published. A value no edge
// reads is publishable surface with zero consumers — sensitive or merely
// internal, it has no business in a file whose whole point is feeding later
// resolution — so an unconsumed output is dropped and a node with no downstream
// consumer gets no file at all.
//
// A write failure fails the node it happened on: the graph asked for snapshots,
// and silently degrading to live-outputs-only would change what a later run can
// fall back on (see the last-resort read in inputs.go) without telling anyone.
func (e *Engine) writeSnapshot(name string, outputs map[string]any) error {
	if !e.Graph.Snapshots {
		return nil
	}

	consumed := make(map[string]bool)
	for _, edge := range e.Graph.Edges {
		if edge.IsDataEdge() && edge.From.Node == name {
			consumed[edge.From.Name] = true
		}
	}

	published := make(map[string]any, len(consumed))
	for out, val := range outputs {
		if consumed[out] {
			published[out] = val
		}
	}
	if len(published) == 0 {
		return nil
	}

	path := e.snapshotPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("node %s: creating output snapshot directory: %w", name, err)
	}
	// encoding/json sorts map keys, so repeated applies of an unchanged graph
	// write byte-identical files.
	data, err := json.MarshalIndent(snapshotFile{Schema: 1, Node: name, Outputs: published}, "", "  ")
	if err != nil {
		return fmt.Errorf("node %s: encoding output snapshot: %w", name, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("node %s: writing output snapshot: %w", name, err)
	}
	return nil
}

// readSnapshot returns the outputs a prior run published for name, or false
// when there is nothing usable: no file, or one that does not decode into a
// schema this reader understands. Every miss is a debug log and a nil, never
// an error — a fallback that could fail on its own would be a second source
// of truth competing with the live read it sits behind (see the last-resort
// use in inputs.go, which gates on Graph.Snapshots before calling this).
func (e *Engine) readSnapshot(name string) (map[string]any, bool) {
	data, err := os.ReadFile(e.snapshotPath(name))
	if err != nil {
		e.logger().Debug("no output snapshot to fall back on", "node", name, "err", err)
		return nil, false
	}
	var f snapshotFile
	if err := json.Unmarshal(data, &f); err != nil || f.Schema != 1 || f.Node != name {
		e.logger().Debug("output snapshot present but unreadable, ignoring it", "node", name, "err", err)
		return nil, false
	}
	return f.Outputs, true
}
