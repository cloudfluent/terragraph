package blueprint

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
)

// LoadMetadata keeps recovery and lock release available when plugin binaries or module inputs cannot be evaluated.
func LoadMetadata(path string) (*Blueprint, string, error) {
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
			return nil, "", err
		}
		for _, entry := range entries {
			if !entry.IsDir() && IsBlueprintFilename(entry.Name()) {
				files = append(files, filepath.Join(path, entry.Name()))
			}
		}
		if len(files) == 0 {
			return nil, "", fmt.Errorf("blueprint directory %q: no configuration files; add a .hcl file or use --blueprint", path)
		}
	}
	bp := &Blueprint{}
	for _, file := range files {
		parsed, diags := hclparse.NewParser().ParseHCLFile(file)
		if diags.HasErrors() {
			return nil, "", fmt.Errorf("parsing blueprint: %w", diags)
		}
		body, _, diags := parsed.Body.PartialContent(&hcl.BodySchema{Blocks: []hcl.BlockHeaderSchema{{Type: "lock"}, {Type: "execution"}}})
		if diags.HasErrors() {
			return nil, "", fmt.Errorf("reading blueprint metadata: %w", diags)
		}
		for _, block := range body.Blocks {
			switch block.Type {
			case "lock":
				if bp.Lock != nil {
					return nil, "", fmt.Errorf("duplicate lock block")
				}
				bp.Lock, err = parseLockBlock(block)
			case "execution":
				if bp.Execution != nil {
					return nil, "", fmt.Errorf("duplicate execution block")
				}
				bp.Execution, err = parseExecutionBlock(block)
			}
			if err != nil {
				return nil, "", err
			}
		}
	}
	if err := validateExecutionConfig(bp); err != nil {
		return nil, "", err
	}
	return bp, dir, nil
}
