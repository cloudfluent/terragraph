package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/cloudfluent/terragraph/internal/engine"
	"github.com/cloudfluent/terragraph/internal/graph"
	"github.com/spf13/cobra"
)

type invocationKey struct{}

// invocation records emission attempts so a writer failure cannot trigger a second JSON document.
type invocation struct {
	attempted bool
	fallback  engine.Diagnostic
}

type errorResultDTO struct {
	SchemaVersion int             `json:"schema_version"`
	Diagnostics   []diagnosticDTO `json:"diagnostics"`
}

// Execute covers Cobra's pre-dispatch failures as well as command execution with one JSON emission boundary.
func Execute(ctx context.Context, root *cobra.Command, args []string) error {
	state := &invocation{fallback: engine.Diagnostic{Code: "invalid_arguments", Category: "arguments", Phase: "arguments", Subject: "command"}}
	root.SetArgs(args)
	root.SetContext(context.WithValue(ctx, invocationKey{}, state))
	selected := jsonRequested(root, args)
	err := root.ExecuteContext(root.Context())
	if err != nil && selected && !state.attempted {
		diagnostics := errorDiagnostics(err, state.fallback)
		if writeErr := writeJSON(root, errorResultDTO{SchemaVersion: 1, Diagnostics: diagnostics}); writeErr != nil {
			return errors.Join(err, writeErr)
		}
	}
	return err
}

// jsonRequested uses flag metadata to avoid interpreting a string value or native argument as output selection.
func jsonRequested(root *cobra.Command, args []string) bool {
	cmd, _, err := root.Find(args)
	if err != nil || cmd == nil || cmd.Flags().Lookup("output") == nil {
		return false
	}
	selected, invalid := false, false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if arg == "--help" || arg == "-h" || arg == "--version" {
			return false
		}
		if !strings.HasPrefix(arg, "--") {
			continue
		}
		name, value, equal := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			flag = cmd.InheritedFlags().Lookup(name)
		}
		if flag == nil {
			flag = root.PersistentFlags().Lookup(name)
		}
		if flag == nil {
			continue
		}
		if !equal && flag.NoOptDefVal == "" {
			if i+1 >= len(args) {
				if name == "output" {
					invalid = true
				}
				break
			}
			i++
			value = args[i]
		}
		if name == "output" {
			if value != "json" {
				invalid = true
			} else {
				selected = true
			}
		}
	}
	return selected && !invalid
}

func categoryForPhase(phase string) string {
	switch phase {
	case "arguments", "selection", "render":
		return "arguments"
	case "load", "configuration":
		return "configuration"
	case "validation":
		return "validation"
	case "policy":
		return "policy"
	case "recovery":
		return "recovery"
	case "record", "journal", "history":
		return "record"
	case "artifact":
		return "artifact"
	case "prepare", "init", "inputs", "plan", "apply", "destroy", "inspect", "execute":
		return "runtime"
	default:
		return "unknown"
	}
}

func diagnosticsToDTO(diagnostics []engine.Diagnostic) []diagnosticDTO {
	out := make([]diagnosticDTO, 0, len(diagnostics))
	for _, d := range diagnostics {
		out = append(out, diagnosticToDTO(d))
	}
	return out
}

// validationError retains all preflight findings so automation need not parse stderr or rerun validate.
type validationError struct {
	cause    error
	problems []graph.Problem
}

func (e *validationError) Error() string { return e.cause.Error() }
func (e *validationError) Unwrap() error { return e.cause }

func errorDiagnostics(err error, fallback engine.Diagnostic) []diagnosticDTO {
	var validation *validationError
	if errors.As(err, &validation) {
		return problemDiagnostics(validation.problems)
	}
	out := diagnosticsToDTO(engine.Diagnostics(err, fallback))
	for i := range out {
		out[i].Source = errorLocation(err)
	}
	return out
}

// runDiagnostics leaves node failures beside their results while retaining independent finalization failures.
func runDiagnostics(result engine.RunResult, err error) []diagnosticDTO {
	excluded := make([]error, 0, len(result.Nodes))
	for _, node := range result.Nodes {
		if node.Err != nil {
			excluded = append(excluded, node.Err)
		}
	}
	return diagnosticsToDTO(engine.Diagnostics(err, engine.Diagnostic{Code: "runtime_failed", Category: "runtime", Phase: "execute", Subject: "execution"}, excluded...))
}

func problemDiagnostics(problems []graph.Problem) []diagnosticDTO {
	out := []diagnosticDTO{}
	for _, p := range problems {
		severity := "warning"
		if p.IsError() {
			severity = "error"
		}
		code := p.Code
		if code == "" {
			code = "validation_failed"
		}
		out = append(out, diagnosticToDTO(engine.Diagnostic{Code: code, Category: "validation", Severity: severity, Phase: "validation", Subject: p.Subject, Message: p.Message, Remedy: p.Remedy}))
	}
	return out
}

func diagnosticPhase(cmd *cobra.Command, phase string) {
	if state, ok := cmd.Context().Value(invocationKey{}).(*invocation); ok {
		state.fallback = engine.Diagnostic{Code: phase + "_failed", Category: categoryForPhase(phase), Phase: phase, Subject: cmd.Name()}
	}
}
