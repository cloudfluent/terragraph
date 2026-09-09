package blueprint

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"
)

// PluginConfig is literal bootstrap data so selecting executable code never depends on executing that code.
type PluginConfig struct {
	Name    string
	Source  string
	Version string
	Config  map[string]any
}

func evaluationContext(contexts []*hcl.EvalContext) *hcl.EvalContext {
	if len(contexts) == 0 {
		return nil
	}
	return contexts[0]
}

var pluginSchema = &hcl.BodySchema{Attributes: []hcl.AttributeSchema{{Name: "source", Required: true}, {Name: "version", Required: true}, {Name: "config"}}}

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
