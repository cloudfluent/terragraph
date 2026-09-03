package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

// PortEvidence is one port's observation outcome. Confidence is "observed" (a node's applied output carried a concrete value), "declared" (the module's own .tf states the constraint — meaningful for input variables, which declare types; outputs cannot), or "unknown" (the port exists but nothing has produced a value yet).
type PortEvidence struct {
	Scope      string `json:"scope"`
	Role       string `json:"role"`
	Port       string `json:"port"`
	Confidence string `json:"confidence"`
	Type       string `json:"type,omitempty"`
}

// Evidence is the terragraph.lock payload: an inventory of every port of every source directory in the graph — contracted or not — bound to the digest of the contract set it was observed against, so a lock from an older contracts.hcl can be told apart from current reality (see Propose's staleness guard).
type Evidence struct {
	Schema          int            `json:"schema"`
	ContractsDigest string         `json:"contracts_digest"`
	Ports           []PortEvidence `json:"ports"`
}

// LockFileName is the generated-evidence sibling of contracts.hcl: committed, diffed in review, never hand-edited, and regenerable at any time (see docs/contracts.md).
const LockFileName = "terragraph.lock"

// scopeFor returns the human spelling of a directory for evidence rows: the contracts.hcl scope when one exists (that is the identity-relevant path), otherwise the path relative to the blueprint base.
func scopeFor(e *Engine, dir string) string {
	if dc := e.Graph.Contracts.Lookup(dir); dc != nil {
		return dc.Scope
	}
	rel, err := filepath.Rel(e.BaseDir, dir)
	if err != nil {
		return dir
	}
	return "./" + filepath.ToSlash(rel)
}

// Observe walks every source directory in the graph once and reports what reality says about each port. A directory's outputs are read through its lexicographically-first node (deterministic choice; nodes sharing a source share one state by the backend_config rules), and a failed read means "not applied", not an error: unknown is an answer here, not a failure.
func (e *Engine) Observe() (*Evidence, error) {
	digest := (&blueprint.Contracts{}).Digest()
	if e.Graph.Contracts != nil {
		digest = e.Graph.Contracts.Digest()
	}

	dirs := make([]string, 0, len(e.Graph.Nodes))
	byDir := map[string][]string{}
	for name, n := range e.Graph.Nodes {
		if _, seen := byDir[n.Dir]; !seen {
			dirs = append(dirs, n.Dir)
		}
		byDir[n.Dir] = append(byDir[n.Dir], name)
	}
	sort.Strings(dirs)

	ev := &Evidence{Schema: 1, ContractsDigest: digest}
	for _, dir := range dirs {
		names := byDir[dir]
		sort.Strings(names)
		schema := e.Graph.Nodes[names[0]].Schema
		scope := scopeFor(e, dir)

		outputs, applied := map[string]any{}, false
		if out, err := e.runner(names[0]).Outputs(); err == nil {
			outputs, applied = out, true
		}

		outputNames := make([]string, 0, len(schema.Outputs))
		for name := range schema.Outputs {
			outputNames = append(outputNames, name)
		}
		sort.Strings(outputNames)
		for _, name := range outputNames {
			pe := PortEvidence{Scope: scope, Role: "producer", Port: name, Confidence: "unknown"}
			if applied {
				if val, ok := outputs[name]; ok {
					pe.Confidence, pe.Type = "observed", jsonTypeOf(val)
				}
			}
			ev.Ports = append(ev.Ports, pe)
		}

		inputNames := make([]string, 0, len(schema.Variables))
		for name := range schema.Variables {
			inputNames = append(inputNames, name)
		}
		sort.Strings(inputNames)
		for _, name := range inputNames {
			pe := PortEvidence{Scope: scope, Role: "consumer", Port: name, Confidence: "declared", Type: schema.Variables[name].Type}
			if pe.Type == "" {
				// A variable without a type constraint is still declared — just with nothing to say; unknown would lie (the port does exist).
				pe.Confidence = "declared"
			}
			ev.Ports = append(ev.Ports, pe)
		}
	}
	sort.Slice(ev.Ports, func(i, j int) bool {
		if ev.Ports[i].Scope != ev.Ports[j].Scope {
			return ev.Ports[i].Scope < ev.Ports[j].Scope
		}
		if ev.Ports[i].Role != ev.Ports[j].Role {
			return ev.Ports[i].Role < ev.Ports[j].Role
		}
		return ev.Ports[i].Port < ev.Ports[j].Port
	})
	return ev, nil
}

// WriteLock serializes ev beside the blueprint, owner-only: the file is committed evidence, and while it carries no secret values by construction (types and names only), a tool-written file with world-read permissions teaches the wrong habit for the artifacts that follow.
func (e *Engine) WriteLock(ev *Evidence) (string, error) {
	path := filepath.Join(e.BaseDir, LockFileName)
	data, err := json.MarshalIndent(ev, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encoding %s: %w", LockFileName, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return path, nil
}

// jsonTypeOf names the coarse shape of a value straight out of terraform's output JSON. Evidence is descriptive, never normative: precision beyond string/number/bool/list/object is the contract's job, not the lock's.
func jsonTypeOf(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "bool"
	case []any:
		return "list"
	case map[string]any:
		return "object"
	default:
		return "unknown"
	}
}
