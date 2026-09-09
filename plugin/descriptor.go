package plugin

import (
	"fmt"
	"path/filepath"
	"regexp"

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
		default:
			return fmt.Errorf("feature.%s: unsupported kind %q", f.Name, f.Kind)
		}
	}
	return nil
}
