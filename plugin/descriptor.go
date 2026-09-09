package plugin

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/hashicorp/go-version"
	ctyjson "github.com/zclconf/go-cty/cty/json"
)

var identifier = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Validate rejects unsupported contracts before package code can execute.
func (d Descriptor) Validate() error {
	if !identifier.MatchString(d.Name) {
		return fmt.Errorf("invalid plugin name")
	}
	if _, err := version.NewVersion(d.Version); err != nil {
		return fmt.Errorf("invalid plugin version: %w", err)
	}
	if d.Protocol != ProtocolVersion {
		return fmt.Errorf("unsupported plugin protocol %d; use protocol %d", d.Protocol, ProtocolVersion)
	}
	if d.Executable == "" || d.Executable == "." || filepath.Base(d.Executable) != d.Executable || regexp.MustCompile(`[/\\:]`).MatchString(d.Executable) {
		return fmt.Errorf("plugin executable must be a filename")
	}
	seen := map[string]bool{}
	for _, f := range d.Features {
		if !identifier.MatchString(f.Name) || seen[f.Name] {
			return fmt.Errorf("invalid or duplicate plugin feature name")
		}
		seen[f.Name] = true
		switch f.Kind {
		case "function":
			if f.Effect != "pure" || len(f.Events) != 0 || f.Sensitive {
				return fmt.Errorf("feature.%s: functions must be pure and non-sensitive", f.Name)
			}
			if _, err := ctyjson.UnmarshalType(f.ResultType); err != nil {
				return fmt.Errorf("feature.%s: invalid result type: %w", f.Name, err)
			}
			params := map[string]bool{}
			for _, p := range f.Parameters {
				if !identifier.MatchString(p.Name) || params[p.Name] {
					return fmt.Errorf("feature.%s: invalid or duplicate parameter name", f.Name)
				}
				params[p.Name] = true
				if _, err := ctyjson.UnmarshalType(p.Type); err != nil {
					return fmt.Errorf("feature.%s: invalid parameter type: %w", f.Name, err)
				}
			}
		case "gate", "observer", "input_resolver", "credential_provider", "validator", "expansion":
			if f.Effect != "pure" && f.Effect != "read_only" && f.Effect != "idempotent_external" && f.Effect != "non_idempotent_external" {
				return fmt.Errorf("feature.%s: invalid effect", f.Name)
			}
			if f.Kind == "validator" || f.Kind == "expansion" {
				if f.Effect != "pure" {
					return fmt.Errorf("feature.%s: static features must be pure", f.Name)
				}
			}
			if (f.Kind == "gate" || f.Kind == "validator" || f.Kind == "observer") && len(f.Events) == 0 {
				return fmt.Errorf("feature.%s: declare lifecycle events", f.Name)
			}
			for _, phase := range f.Events {
				if !slices.Contains(LifecyclePhases, phase) {
					return fmt.Errorf("feature.%s: unsupported event %q", f.Name, phase)
				}
				if f.Kind == "gate" && !slices.Contains([]string{"graph.validate", "selection.ready", "run.prepare", "node.prepare", "source.vendor.before", "node.init.before", "node.plan.ready", "node.mutation.admit", "native.operation.before", "level.finished"}, phase) {
					return fmt.Errorf("feature.%s: event %s cannot block a completed operation", f.Name, phase)
				}
				if f.Kind == "gate" && phase == "graph.validate" && f.Effect != "pure" {
					return fmt.Errorf("feature.%s: static graph gates must be pure; move external checks to run.prepare", f.Name)
				}
				if f.Kind == "validator" && phase != "graph.validate" {
					return fmt.Errorf("feature.%s: validators run at graph.validate", f.Name)
				}
			}

		default:
			return fmt.Errorf("feature.%s: unsupported kind %q", f.Name, f.Kind)
		}
	}
	return nil
}
