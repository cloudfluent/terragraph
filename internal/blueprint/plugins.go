package blueprint

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"
)

// PluginConfig is literal bootstrap data so selecting executable code never depends on executing that code.
type PluginConfig struct {
	Name        string
	Source      string
	Version     string
	Config      map[string]any
	Features    map[string]PluginFeature
	Environment []string
	PlanAccess  bool
}

func evaluationContext(contexts []*hcl.EvalContext) *hcl.EvalContext {
	if len(contexts) == 0 {
		return nil
	}
	return contexts[0]
}

type PluginFeature struct {
	Mode    string
	Timeout time.Duration
}

var pluginSchema = &hcl.BodySchema{Attributes: []hcl.AttributeSchema{{Name: "source", Required: true}, {Name: "version", Required: true}, {Name: "config"}}, Blocks: []hcl.BlockHeaderSchema{{Type: "feature", LabelNames: []string{"name"}}, {Type: "access"}}}

func parsePluginBlock(block *hcl.Block) (PluginConfig, error) {
	p := PluginConfig{Name: block.Labels[0]}
	if !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(p.Name) {
		return p, fmt.Errorf("plugin.%s: alias must use lowercase letters, digits and underscores", p.Name)
	}
	body, diags := block.Body.Content(pluginSchema)
	if diags.HasErrors() {
		return p, fmt.Errorf("plugin.%s: %w", p.Name, diags)
	}
	for key, target := range map[string]*string{"source": &p.Source, "version": &p.Version} {
		v, d := body.Attributes[key].Expr.Value(nil)
		if d.HasErrors() || v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
			return p, fmt.Errorf("plugin.%s.%s: must be a literal string", p.Name, key)
		}
		*target = v.AsString()
		if *target == "" {
			return p, fmt.Errorf("plugin.%s.%s: must not be empty", p.Name, key)
		}
	}
	if attr := body.Attributes["config"]; attr != nil {
		var err error
		p.Config, err = parseVarsAttr(attr)
		if err != nil {
			return p, fmt.Errorf("plugin.%s.config: %w", p.Name, err)
		}
	}
	for _, block := range body.Blocks {
		switch block.Type {
		case "feature":
			b, d := block.Body.Content(&hcl.BodySchema{Attributes: []hcl.AttributeSchema{{Name: "mode"}, {Name: "timeout"}}})
			if d.HasErrors() {
				return p, fmt.Errorf("plugin.%s.feature: %w", p.Name, d)
			}
			if p.Features == nil {
				p.Features = map[string]PluginFeature{}
			}
			name := block.Labels[0]
			if _, ok := p.Features[name]; ok {
				return p, fmt.Errorf("plugin.%s.feature.%s: duplicate feature", p.Name, name)
			}
			f := PluginFeature{}
			for key, attr := range b.Attributes {
				v, d := attr.Expr.Value(nil)
				if d.HasErrors() || v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
					return p, fmt.Errorf("plugin.%s.feature.%s.%s: must be a literal string", p.Name, name, key)
				}
				if key == "mode" {
					f.Mode = v.AsString()
					if f.Mode != "enforce" && f.Mode != "advisory" && f.Mode != "best_effort" {
						return p, fmt.Errorf("plugin.%s.feature.%s: invalid mode", p.Name, name)
					}
				} else {
					duration, err := time.ParseDuration(v.AsString())
					if err != nil || duration <= 0 || duration > 5*time.Minute {
						return p, fmt.Errorf("plugin.%s.feature.%s.timeout: use a duration greater than zero and at most 5m", p.Name, name)
					}
					f.Timeout = duration
				}
			}
			p.Features[name] = f
		case "access":
			if p.Environment != nil {
				return p, fmt.Errorf("plugin.%s.access: duplicate block", p.Name)
			}
			p.Environment = []string{}
			b, d := block.Body.Content(&hcl.BodySchema{Attributes: []hcl.AttributeSchema{{Name: "environment"}, {Name: "plan"}}})
			if d.HasErrors() {
				return p, fmt.Errorf("plugin.%s.access: %w", p.Name, d)
			}
			if attr := b.Attributes["environment"]; attr != nil {
				v, d := attr.Expr.Value(nil)
				if d.HasErrors() || v.IsNull() || !v.IsWhollyKnown() || (!v.Type().IsTupleType() && !v.Type().IsListType()) {
					return p, fmt.Errorf("plugin.%s.access.environment: use a literal list of environment names", p.Name)
				}
				for _, v := range v.AsValueSlice() {
					if v.IsNull() || v.Type() != cty.String || !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(v.AsString()) {
						return p, fmt.Errorf("plugin.%s.access.environment: invalid environment name", p.Name)
					}
					p.Environment = append(p.Environment, v.AsString())
				}
			}
			if attr := b.Attributes["plan"]; attr != nil {
				v, d := attr.Expr.Value(nil)
				if d.HasErrors() || v.IsNull() || !v.IsKnown() || v.Type() != cty.Bool {
					return p, fmt.Errorf("plugin.%s.access.plan: use a literal boolean", p.Name)
				}
				p.PlanAccess = v.True()
			}
		}
	}
	return p, nil
}

// LoadPlugins discovers declarations without evaluating node expressions or starting plugin processes.
func LoadPlugins(path string) ([]PluginConfig, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", fmt.Errorf("resolving blueprint path: %w", err)
	}
	dir := filepath.Dir(path)
	files := []string{path}
	if info.IsDir() {
		dir = path
		files = nil
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, "", fmt.Errorf("reading blueprint directory: %w", err)
		}
		for _, entry := range entries {
			if !entry.IsDir() && IsBlueprintFilename(entry.Name()) {
				files = append(files, filepath.Join(path, entry.Name()))
			}
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, "", err
	}
	var configs []PluginConfig
	seen := map[string]bool{}
	for _, file := range files {
		parsed, d := hclparse.NewParser().ParseHCLFile(file)
		if d.HasErrors() {
			return nil, "", fmt.Errorf("parsing blueprint: %w", d)
		}
		body, _, d := parsed.Body.PartialContent(&hcl.BodySchema{Blocks: []hcl.BlockHeaderSchema{{Type: "plugin", LabelNames: []string{"name"}}}})
		if d.HasErrors() {
			return nil, "", fmt.Errorf("reading plugin declarations: %w", d)
		}
		for _, block := range body.Blocks {
			config, err := parsePluginBlock(block)
			if err != nil {
				return nil, "", err
			}
			if seen[config.Name] {
				return nil, "", fmt.Errorf("plugin.%s: duplicate declaration", config.Name)
			}
			seen[config.Name] = true
			configs = append(configs, config)
		}
	}
	return configs, abs, nil
}
