package plugin

import "encoding/json"

// LifecyclePhases lists semantic transitions; an event is emitted only when the host reaches that transition.
var LifecyclePhases = []string{
	"config.evaluate", "config.expand", "graph.validate", "selection.ready", "source.vendor.before", "source.vendor.after",
	"run.prepare", "observation.prepare", "node.prepare", "credential.acquire", "input.resolve", "node.init.before", "node.init.after",
	"node.plan.ready", "node.mutation.admit", "node.mutation.finished", "node.outputs.ready", "node.read.before", "node.read.after",
	"native.operation.before", "native.operation.after", "node.finished", "level.finished", "run.finished", "credential.renew", "credential.release", "session.close",
}

// GraphNode exposes declaration identity without leaking resolved input or output values to observers.
type GraphNode struct {
	Name         string   `json:"name"`
	Source       string   `json:"source"`
	Inputs       []string `json:"inputs,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
}

// Expansion contributes explicit stable node identifiers before the ordinary graph validator runs.
type Expansion struct {
	Nodes []ExpandedNode `json:"nodes"`
	Edges []ExpandedEdge `json:"edges,omitempty"`
}

type ExpandedNode struct {
	Name   string           `json:"name"`
	Source string           `json:"source"`
	Vars   map[string]Value `json:"vars,omitempty"`
}

type ExpandedEdge struct {
	From   string `json:"from"`
	Output string `json:"output,omitempty"`
	To     string `json:"to"`
	Input  string `json:"input,omitempty"`
}

// CallRecord keeps extension delivery separate from infrastructure outcomes and omits returned secrets and arbitrary error text.
type CallRecord struct {
	ConfigDigest string             `json:"config_digest"`
	Report       json.RawMessage    `json:"report,omitempty"`
	Cleanup      *CredentialCleanup `json:"credential_cleanup,omitempty"`
	ID           string             `json:"id"`
	Alias        string             `json:"alias"`
	Feature      string             `json:"feature"`
	Kind         string             `json:"kind"`
	Effect       string             `json:"effect"`
	Digest       string             `json:"digest"`
	Mode         string             `json:"mode"`
	Event        Event              `json:"event"`
	Status       string             `json:"status"`
	Code         string             `json:"code,omitempty"`
	Decision     string             `json:"decision,omitempty"`
	Version      string             `json:"version,omitempty"`
}

// CredentialCleanup is private recovery material; CLI summaries must never serialize it directly.
type CredentialCleanup struct {
	Reference map[string]any `json:"reference,omitempty"`
	Lease     *Lease         `json:"lease,omitempty"`
}
