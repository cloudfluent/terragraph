package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Store maps node name -> the combined hash (Combine(sourceHash, inputHash)) recorded at that node's last successful apply.
type Store map[string]string

// Load reads a cache file. A missing file is not an error: it just means no node has ever been applied through this cache yet.
func Load(path string) (Store, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Store{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading cache file: %w", err)
	}

	var s Store
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing cache file: %w", err)
	}
	return s, nil
}

// Save writes the cache file, creating its parent directory if needed.
func (s Store) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating cache directory: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding cache file: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing cache file: %w", err)
	}
	return nil
}
