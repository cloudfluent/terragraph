package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/cloudfluent/terragraph/internal/engine"
	"github.com/cloudfluent/terragraph/internal/exec"
	"github.com/hashicorp/hcl/v2"
	"github.com/spf13/cobra"
)

type sourceLocationDTO struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

func errorLocation(err error) *sourceLocationDTO {
	var diags hcl.Diagnostics
	if errors.As(err, &diags) {
		for _, d := range diags {
			if d.Subject != nil {
				return &sourceLocationDTO{File: d.Subject.Filename, Line: d.Subject.Start.Line, Column: d.Subject.Start.Column}
			}
		}
	}
	return nil
}

type diagnosticDTO struct {
	Source  *sourceLocationDTO `json:"source,omitempty"`
	Code    string             `json:"code"`
	Phase   string             `json:"phase"`
	Subject string             `json:"subject"`
	Message string             `json:"message"`
	Remedy  string             `json:"remedy,omitempty"`
}

type observedOutputDTO struct {
	Sensitive *bool           `json:"sensitive"`
	Redacted  bool            `json:"redacted"`
	Value     json.RawMessage `json:"value,omitempty"`
}

type observationDTO struct {
	Node        string                       `json:"node"`
	Runtime     string                       `json:"runtime"`
	Status      string                       `json:"status"`
	Backend     string                       `json:"backend,omitempty"`
	Identity    string                       `json:"state_identity,omitempty"`
	State       string                       `json:"state,omitempty"`
	Resources   *int                         `json:"resource_count,omitempty"`
	OutputCount *int                         `json:"output_count,omitempty"`
	Outputs     map[string]observedOutputDTO `json:"outputs,omitzero"`
	Diagnostics []diagnosticDTO              `json:"diagnostics"`
}

type observationResultDTO struct {
	SchemaVersion int              `json:"schema_version"`
	Nodes         []observationDTO `json:"nodes"`
	Diagnostics   []diagnosticDTO  `json:"diagnostics"`
}

func diagnosticToDTO(d engine.Diagnostic) diagnosticDTO {
	return diagnosticDTO{Code: d.Code, Phase: d.Phase, Subject: d.Subject, Message: d.Message, Remedy: d.Remedy}
}

func newObservationCmd(kind string, path *string, binaryOf func() exec.Binary) *cobra.Command {
	var node, format string
	var raw, showSensitive bool
	status := kind == "status"
	cmd := &cobra.Command{Use: kind, Short: "Observe current node " + kind + " with isolated runtime context"}
	if !status {
		cmd.Use = "output [name]"
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		result := observationResultDTO{SchemaVersion: 1, Nodes: []observationDTO{}, Diagnostics: []diagnosticDTO{}}
		finish := func(err error) error {
			if format == "json" {
				if writeErr := writeJSON(cmd.OutOrStdout(), result); writeErr != nil {
					return writeErr
				}
			}
			return err
		}
		failure := func(code, phase, message, remedy string) error {
			result.Diagnostics = append(result.Diagnostics, diagnosticDTO{Code: code, Phase: phase, Subject: kind, Message: message, Remedy: remedy})
			return finish(fmt.Errorf("%s: %s; %s", kind, message, remedy))
		}
		if format != "text" && format != "json" {
			return failure("invalid_arguments", "arguments", "unknown output format", "choose text or json")
		}
		if (status && len(args) != 0) || len(args) > 1 || (len(args) == 1 && node == "") {
			return failure("invalid_arguments", "arguments", "an output name requires exactly one --node", "use output --node NAME OUTPUT")
		}
		if raw && (format == "json" || len(args) != 1 || node == "") {
			return failure("invalid_arguments", "arguments", "--raw requires one named scalar and conflicts with --output json", "use output --node NAME OUTPUT --raw")
		}
		session, err := engine.OpenObservation(cmd.Context(), *path, binaryOf(), cmd.ErrOrStderr())
		if err != nil {
			// Parsing errors can quote source expressions; keep this public failure free of source values.
			result.Diagnostics = append(result.Diagnostics, diagnosticDTO{Code: "observation_load_failed", Phase: "load", Subject: kind, Message: "could not load or lock the blueprint", Remedy: "check blueprint syntax, source directories, and local lock availability", Source: errorLocation(err)})
			return finish(fmt.Errorf("%s: could not load or lock the blueprint; check syntax, source directories, and local lock availability", kind))
		}
		defer session.Close()
		names, err := session.Names(node)
		if err != nil {
			return failure("unknown_node", "selection", err.Error(), "select an exact expanded leaf")
		}
		failed := 0
		for _, name := range names {
			observed := session.Read(name, status)
			dto := observationDTO{Node: name, Runtime: observed.Runtime, Status: "observed", Diagnostics: []diagnosticDTO{}}
			if status {
				dto.Backend, dto.Identity, dto.State = observed.Backend, observed.Identity, observed.State
			}
			if observed.Diagnostic != nil {
				dto.Status = "failed"
				dto.Diagnostics = append(dto.Diagnostics, diagnosticToDTO(*observed.Diagnostic))
			} else if status {
				dto.Backend, dto.Identity, dto.State = observed.Backend, observed.Identity, observed.State
				dto.Resources, dto.OutputCount = &observed.Resources, &observed.OutputCount
			} else {
				dto.Outputs = map[string]observedOutputDTO{}
				outputs := observed.Outputs
				if len(args) == 1 {
					value, ok := outputs[args[0]]
					if !ok {
						dto.Status = "failed"
						dto.Diagnostics = append(dto.Diagnostics, diagnosticDTO{Code: "output_not_found", Phase: "selection", Subject: "node." + name, Message: "named output does not exist", Remedy: "list this node's outputs without a positional name"})
					}
					outputs = exec.Outputs{}
					if ok {
						outputs[args[0]] = value
					}
				}
				keys := make([]string, 0, len(outputs))
				for key := range outputs {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					output := outputs[key]
					hidden := !showSensitive && (output.Sensitive == nil || *output.Sensitive)
					value := observedOutputDTO{Sensitive: output.Sensitive, Redacted: hidden}
					if !hidden {
						value.Value, err = json.Marshal(output.Value)
						if err != nil {
							return err
						}
					}
					dto.Outputs[key] = value
					if raw {
						var text string
						var rawErr error
						if hidden {
							rawErr = fmt.Errorf("value is sensitive or its sensitivity is unknown; pass --show-sensitive to disclose it")
						} else {
							text, rawErr = rawScalar(output.Value)
						}
						if rawErr != nil {
							dto.Status = "failed"
							dto.Diagnostics = append(dto.Diagnostics, diagnosticDTO{Code: "raw_unavailable", Phase: "render", Subject: "node." + name, Message: rawErr.Error(), Remedy: "request a scalar output with the required disclosure opt-in"})
						} else {
							if _, err := fmt.Fprint(cmd.OutOrStdout(), text); err != nil {
								return err
							}
						}
					}
				}
			}
			if dto.Status == "failed" {
				failed++
			}
			result.Nodes = append(result.Nodes, dto)
			if format == "text" && !raw {
				if status && dto.Status != "failed" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (%s, %s), %d resources, %d outputs\n", name, dto.State, dto.Runtime, dto.Backend, observed.Resources, observed.OutputCount)
				}
				if !status && dto.Status != "failed" {
					keys := make([]string, 0, len(dto.Outputs))
					for key := range dto.Outputs {
						keys = append(keys, key)
					}
					sort.Strings(keys)
					if len(keys) == 0 {
						_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s: no outputs\n", name)
					}
					for _, key := range keys {
						value := dto.Outputs[key]
						rendered := "(sensitive)"
						if !value.Redacted {
							bytes, _ := json.Marshal(value.Value)
							rendered = string(bytes)
						}
						_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s %s = %s\n", name, key, rendered)
					}
				}
			}
			if format != "json" {
				for _, d := range dto.Diagnostics {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s: %s; %s\n", name, d.Code, d.Message, d.Remedy)
				}
			}
		}
		if failed > 0 {
			return finish(fmt.Errorf("%s: %d node(s) failed", kind, failed))
		}
		return finish(nil)
	}
	cmd.Flags().StringVar(&node, "node", "", "restrict to an exact expanded leaf node")
	cmd.Flags().StringVar(&format, "output", "text", "output format: text or json")
	if !status {
		cmd.Flags().BoolVar(&raw, "raw", false, "print one named scalar without JSON encoding")
		cmd.Flags().BoolVar(&showSensitive, "show-sensitive", false, "explicitly disclose sensitive and unknown-sensitivity values")
	}
	return cmd
}

func rawScalar(value any) (string, error) {
	switch value := value.(type) {
	case string:
		return value, nil
	case json.Number:
		return value.String(), nil
	case bool:
		if value {
			return "true", nil
		}
		return "false", nil
	default:
		return "", fmt.Errorf("--raw requires a string, number, or boolean; use text or JSON for collections and null")
	}
}
