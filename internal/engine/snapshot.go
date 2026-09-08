package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/cloudfluent/terragraph/internal/exec"
)

// snapshotPath returns where apply publishes name's output snapshot: <BaseDir>/.terragraph/outputs/<name>.json, beside the engine's other managed per-node state (plans, tfdata, state). Local and regenerable by every apply, so it is gitignored output, never committed evidence.
func (e *Engine) snapshotPath(name string) string {
	return filepath.Join(e.BaseDir, ".terragraph", "outputs", name+".json")
}

// snapshotFile is the on-disk shape of an output snapshot. Schema lets a future reader refuse a format it does not understand instead of guessing at it.
type snapshotFile struct {
	Schema  int            `json:"schema"`
	Node    string         `json:"node"`
	Outputs map[string]any `json:"outputs"`
	// Withheld retains only port names so fallback can explain an omitted secret without persisting its value.
	Withheld []string `json:"withheld,omitempty"`
}

// snapshotOutputAllowed requires known non-sensitive metadata so a missing detail can never silently authorize persistent storage.
func (e *Engine) snapshotOutputAllowed(node, output string) bool {
	detail, known := e.Graph.Nodes[node].Schema.OutputDetails[output]
	return known && !detail.Sensitive
}

// writeSnapshot persists only consumed non-sensitive outputs when opted in; withheld port names explain omissions, and a write failure fails the node rather than silently weakening later fallback.
func (e *Engine) writeSnapshot(name string, outputs exec.Outputs) error {
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
	var withheld []string
	for out := range consumed {
		output, present := outputs[out]
		if !e.snapshotOutputAllowed(name, out) || !present || output.Sensitive == nil || *output.Sensitive {
			withheld = append(withheld, out)
			continue
		}
		published[out] = output.Value
	}
	if len(published) == 0 && len(withheld) == 0 {
		// The edge set can change between applies (an edge removed, a rename): a prior file whose consumers are all gone is a stale secret with no reader, so "no consumers → no file" must hold on re-apply too, not only on first write.
		if err := os.Remove(e.snapshotPath(name)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("node %s: removing stale output snapshot: %w", name, err)
		}
		return nil
	}

	path := e.snapshotPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("node %s: creating output snapshot directory: %w", name, err)
	}
	// encoding/json sorts map keys; sorting port names keeps withheld-only snapshots deterministic too.
	sort.Strings(withheld)
	data, err := json.MarshalIndent(snapshotFile{Schema: 2, Node: name, Outputs: published, Withheld: withheld}, "", "  ")
	if err != nil {
		return fmt.Errorf("node %s: encoding output snapshot: %w", name, err)
	}
	data = append(data, '\n')
	// Remove before write, like exec.WriteTFVars for the same reason: os.WriteFile only applies 0o600 on create, so a rewrite over an existing wider mode (or a leftover from a checkout with different umask behavior) would keep the old bits for the file's lifetime — these values are the same class of upstream outputs tfvars carry.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("node %s: removing prior output snapshot: %w", name, err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("node %s: writing output snapshot: %w", name, err)
	}
	return nil
}

// readSnapshot strips withheld values and treats missing, corrupt, or incompatible files as a miss so snapshot failures never replace the original live-read diagnostic.
func (e *Engine) readSnapshot(name string) (snapshotFile, bool) {
	data, err := os.ReadFile(e.snapshotPath(name))
	if err != nil {
		e.logger().Debug("no output snapshot to fall back on", "node", name, "err", err)
		return snapshotFile{}, false
	}
	var f snapshotFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	// Every writer emits an outputs object, even when empty; a missing or null object is corruption, not an output lookup miss.
	if err := decoder.Decode(&f); err != nil || (f.Schema != 1 && f.Schema != 2) || f.Node != name || f.Outputs == nil {
		e.logger().Debug("output snapshot present but unreadable, ignoring it", "node", name, "err", err)
		return snapshotFile{}, false
	}
	if decoder.Decode(new(any)) != io.EOF {
		e.logger().Debug("output snapshot has trailing data, ignoring it", "node", name)
		return snapshotFile{}, false
	}
	// Version 1 never checked runtime sensitivity, so its values cannot be trusted even when the current static declaration says public.
	for out := range f.Outputs {
		if f.Schema == 1 || !e.snapshotOutputAllowed(name, out) {
			delete(f.Outputs, out)
			f.Withheld = append(f.Withheld, out)
		}
	}
	// Withheld names remain authoritative until apply republishes the value, even if the declaration has since become non-sensitive.
	for _, out := range f.Withheld {
		delete(f.Outputs, out)
	}
	return f, true
}
