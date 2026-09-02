package blueprint

import "fmt"

// Approve is how much of a node's plan may be applied without someone saying so for that specific run.
//
// It exists because walking the graph and committing changes are two different things. terragraph automates the first: it runs nodes in dependency order and feeds each one's real outputs into the next. The second is not automation, it is a decision — and in a graph it is a decision nobody can make up front, because a downstream node's plan cannot be produced until its upstream has actually been applied (see docs/execution-model.md). So the decision is delegated in advance, per node, in terms of what the plan turns out to contain.
//
// The levels are defined by which of Terraform's planned actions they permit; see engine's plan gate.
type Approve string

const (
	// ApproveNone permits nothing: the node is planned and never applied.
	ApproveNone Approve = "none"
	// ApproveSafe permits create and update. This is the default, and it is a workable one rather than an obstacle: a first-ever bootstrap is all creates, ordinary steady-state work is updates, and reconciling drift is a create too (a resource that has vanished remotely is dropped from state by the refresh, so the plan rebuilds it rather than deleting anything).
	ApproveSafe Approve = "safe"
	// ApproveAll additionally permits replace and delete — the two that cannot be undone, and the two a change cascading from an upstream node is most likely to produce unexpectedly.
	ApproveAll Approve = "all"
)

// ParseApprove validates an approve level written in a blueprint or passed on the command line.
func ParseApprove(s string) (Approve, error) {
	switch Approve(s) {
	case ApproveNone, ApproveSafe, ApproveAll:
		return Approve(s), nil
	default:
		return "", fmt.Errorf("unknown approve level %q (want %q, %q or %q)", s, ApproveNone, ApproveSafe, ApproveAll)
	}
}

// Permits reports whether this level allows a planned action. Terraform spells a replacement as a delete and a create together, which reaches here as both actions separately; delete is the one that decides it either way, so no special case is needed.
func (a Approve) Permits(action string) bool {
	switch action {
	case "no-op", "read":
		return true
	case "create", "update":
		return a == ApproveSafe || a == ApproveAll
	case "delete":
		return a == ApproveAll
	default:
		// An action Terraform grew after this was written. Treat it the way the two destructive ones are treated rather than waving it through.
		return a == ApproveAll
	}
}
