package cli

import (
	"encoding/json"
	"io"

	"github.com/cloudfluent/terragraph/internal/graph"
	"github.com/cloudfluent/terragraph/internal/vendor"
)

// problemDTO is the JSON-facing shape of a graph.Problem. graph.Problem.Severity is an untyped int (Severity), which would marshal as a bare 0/1; this gives it a stable string representation instead.
type problemDTO struct {
	Severity string `json:"severity"` // "error" | "warning"
	Message  string `json:"message"`
}

// validateResult is the JSON payload for `terragraph validate --output json`.
type validateResult struct {
	Valid    bool         `json:"valid"`
	Problems []problemDTO `json:"problems"`
}

// graphResult is the JSON payload for `terragraph graph --output json` (list format only; DOT has no JSON form).
type graphResult struct {
	Levels [][]string `json:"levels"`
}

// vendorResultDTO is the JSON-facing shape of one vendor.Result. vendor.Result.Err is an error interface, which encoding/json can't marshal usefully; this flattens it to a status string plus an optional message.
type vendorResultDTO struct {
	Node   string `json:"node"`
	Status string `json:"status"` // "vendored" | "skipped" | "error"
	Error  string `json:"error,omitempty"`
}

func problemsToDTO(problems []graph.Problem) []problemDTO {
	out := make([]problemDTO, len(problems))
	for i, p := range problems {
		severity := "warning"
		if p.IsError() {
			severity = "error"
		}
		out[i] = problemDTO{Severity: severity, Message: p.Message}
	}
	return out
}

func vendorResultsToDTO(results []vendor.Result) []vendorResultDTO {
	out := make([]vendorResultDTO, len(results))
	for i, r := range results {
		switch {
		case r.Err != nil:
			out[i] = vendorResultDTO{Node: r.Node, Status: "error", Error: r.Err.Error()}
		case r.Skipped:
			out[i] = vendorResultDTO{Node: r.Node, Status: "skipped"}
		default:
			out[i] = vendorResultDTO{Node: r.Node, Status: "vendored"}
		}
	}
	return out
}

// portEvidenceDTO mirrors engine.PortEvidence; a separate DTO keeps the JSON contract explicit rather than marshalling engine types by accident.
type portEvidenceDTO struct {
	Scope      string `json:"scope"`
	Role       string `json:"role"`
	Port       string `json:"port"`
	Confidence string `json:"confidence"`
	Type       string `json:"type,omitempty"`
}

// observeResult is the JSON payload for `terragraph observe --output json`.
type observeResult struct {
	Lock   string            `json:"lock"`
	Digest string            `json:"digest"`
	Ports  []portEvidenceDTO `json:"ports"`
}

// writeJSON encodes v to w as a single JSON value followed by a newline.
func writeJSON(w io.Writer, v any) error {
	return json.NewEncoder(w).Encode(v)
}
