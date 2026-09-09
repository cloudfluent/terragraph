package cli

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/cloudfluent/terragraph/internal/engine"
	"github.com/cloudfluent/terragraph/internal/graph"
	"github.com/cloudfluent/terragraph/internal/vendor"
)

// problemDTO is the JSON-facing shape of a graph.Problem. graph.Problem.Severity is an untyped int (Severity), which would marshal as a bare 0/1; this gives it a stable string representation instead.
type problemDTO struct {
	Code     string `json:"code"`
	Category string `json:"category"`
	Phase    string `json:"phase"`
	Subject  string `json:"subject"`
	Remedy   string `json:"remedy,omitempty"`
	Severity string `json:"severity"` // "error" | "warning"
	Message  string `json:"message"`
}

// validateResult is the JSON payload for `terragraph validate --output json`.
type validateResult struct {
	SchemaVersion int          `json:"schema_version"`
	Valid         bool         `json:"valid"`
	Problems      []problemDTO `json:"problems"`
}

// graphResult is the JSON payload for `terragraph graph --output json` (list format only; DOT has no JSON form).
type graphResult struct {
	SchemaVersion int             `json:"schema_version"`
	Diagnostics   []diagnosticDTO `json:"diagnostics"`
	Levels        [][]string      `json:"levels"`
	Selection     *selectionDTO   `json:"selection,omitempty"`
}

// vendorResultDTO is the JSON-facing shape of one vendor.Result. vendor.Result.Err is an error interface, which encoding/json can't marshal usefully; this flattens it to a status string plus an optional message.
type vendorResultDTO struct {
	Diagnostics []diagnosticDTO `json:"diagnostics"`
	Node        string          `json:"node"`
	Status      string          `json:"status"` // "vendored" | "skipped" | "error"
	Error       string          `json:"error,omitempty"`
}

func problemsToDTO(problems []graph.Problem) []problemDTO {
	out := make([]problemDTO, len(problems))
	for i, p := range problems {
		severity := "warning"
		if p.IsError() {
			severity = "error"
		}
		code := p.Code
		if code == "" {
			code = "validation_failed"
		}
		out[i] = problemDTO{Severity: severity, Code: code, Category: "validation", Phase: "validation", Subject: p.Subject, Remedy: p.Remedy, Message: p.Message}
	}
	return out
}

func vendorResultsToDTO(results []vendor.Result) []vendorResultDTO {
	out := make([]vendorResultDTO, len(results))
	for i, r := range results {
		switch {
		case r.Err != nil:
			out[i] = vendorResultDTO{Node: r.Node, Status: "error", Error: r.Err.Error(), Diagnostics: errorDiagnostics(r.Err, engine.Diagnostic{Code: "vendor_failed", Category: "artifact", Phase: "vendor", Subject: "node." + r.Node})}
		case r.Skipped:
			out[i] = vendorResultDTO{Node: r.Node, Status: "skipped", Diagnostics: []diagnosticDTO{}}
		default:
			out[i] = vendorResultDTO{Node: r.Node, Status: "vendored", Diagnostics: []diagnosticDTO{}}
		}
	}
	return out
}

// nodeRunDTO is the JSON-facing shape of an engine.NodeRun. Err is an error interface, which encoding/json can't marshal usefully, so it flattens to an optional message the way vendorResultDTO does.
type nodeRunDTO struct {
	Diagnostics []diagnosticDTO `json:"diagnostics"`
	Node        string          `json:"node"`
	Level       int             `json:"level"`
	Status      string          `json:"status"` // planned | applied | unchanged | destroyed | failed | "not run"
	Error       string          `json:"error,omitempty"`
	Review      *planReviewDTO  `json:"review,omitempty"`
}

// runResult is the JSON payload for `terragraph plan|apply|destroy --output json`.
type runResult struct {
	SchemaVersion int             `json:"schema_version"`
	ExecutionID   string          `json:"execution_id,omitempty"`
	Diagnostics   []diagnosticDTO `json:"diagnostics"`
	Nodes         []nodeRunDTO    `json:"nodes"`
	Selection     *selectionDTO   `json:"selection,omitempty"`
}

func nodeRunsToDTO(runs []engine.NodeRun) []nodeRunDTO {
	out := make([]nodeRunDTO, len(runs))
	for i, r := range runs {
		dto := nodeRunDTO{Node: r.Node, Level: r.Level, Status: r.Status, Review: reviewToDTO(r.Review), Diagnostics: diagnosticsToDTO(r.Diagnostics)}
		if r.Err != nil {
			dto.Error = r.Err.Error()
		}
		out[i] = dto
	}
	return out
}

// writeJSON records the attempt before writing so a broken pipe never triggers a second result.
func writeJSON(cmd *cobra.Command, v any) error {
	if cmd.Context() != nil {
		if state, ok := cmd.Context().Value(invocationKey{}).(*invocation); ok {
			state.attempted = true
		}
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(v)
}

// vendorFailureDTO only wraps partial results when a global failure cannot be assigned to one node.
type vendorFailureDTO struct {
	SchemaVersion int               `json:"schema_version"`
	Results       []vendorResultDTO `json:"results"`
	Diagnostics   []diagnosticDTO   `json:"diagnostics"`
}
