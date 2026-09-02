package blueprint

import (
	"encoding/json"
	"fmt"
	"os"
)

// NodeLayout is pure UI metadata for a single node: its position on the canvas. It carries no graph-logic meaning and is never consulted by the engine when executing the graph.
type NodeLayout struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Layout maps node name -> canvas position. Stored separately from the blueprint so that moving a box around never shows up in a logical diff of blueprint.hcl.
type Layout map[string]NodeLayout

// LoadLayout reads a layout file. A missing file is not an error: it just means no layout has been saved yet (e.g. the graph was never opened in the UI), and callers should treat that as an empty Layout.
func LoadLayout(path string) (Layout, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Layout{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading layout file: %w", err)
	}

	var layout Layout
	if err := json.Unmarshal(data, &layout); err != nil {
		return nil, fmt.Errorf("parsing layout file: %w", err)
	}
	return layout, nil
}

// SaveLayout writes the layout file, creating or overwriting it.
func SaveLayout(path string, layout Layout) error {
	data, err := json.MarshalIndent(layout, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding layout file: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing layout file: %w", err)
	}
	return nil
}
