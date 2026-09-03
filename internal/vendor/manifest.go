package vendor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// Entry records what to vendor for one node.
//
// Exclude is the one field here the *user* owns, not the tool: it starts empty the first time a node is vendored, and a subsequent vendor run only ever reads it (to prune with) and writes it back unchanged; it never computes or overwrites it. To customize what's pruned from a specific vendored module, hand-edit its Exclude in the saved manifest, then re-vendor that node with --force.
//
// Source mirrors the node's declared source at the time it was last vendored. It's how a vendor run notices the blueprint's declared source changed (e.g. a ref bump) since the last vendor, and re-fetches automatically even without --force.
type Entry struct {
	Source  string   `yaml:"source"`
	Exclude []string `yaml:"exclude,omitempty"`
}

// Manifest is the in-memory form of a vendor manifest: node name -> what to vendor for it.
type Manifest map[string]Entry

// manifestFile is the on-disk shape: a sorted slice, not a bare map, so Marshal produces byte-identical output regardless of Go map iteration order or any internal key-ordering behavior gopkg.in/yaml.v3 does not document. Determinism here is in service of keeping vendor.yaml diffs meaningful.
type manifestFile struct {
	Modules []manifestFileEntry `yaml:"modules"`
}

type manifestFileEntry struct {
	Name  string `yaml:"name"`
	Entry `yaml:",inline"`
}

// LoadManifest reads a vendor manifest. A missing file is not an error: it just means no node has ever been vendored through it yet.
func LoadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Manifest{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading vendor manifest: %w", err)
	}

	var mf manifestFile
	if err := yaml.Unmarshal(data, &mf); err != nil {
		return nil, fmt.Errorf("parsing vendor manifest: %w", err)
	}

	m := make(Manifest, len(mf.Modules))
	for _, e := range mf.Modules {
		m[e.Name] = e.Entry
	}
	return m, nil
}

// Save writes the manifest, with entries sorted by node name for a stable, diff-friendly result.
func (m Manifest) Save(path string) error {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)

	mf := manifestFile{Modules: make([]manifestFileEntry, 0, len(names))}
	for _, name := range names {
		mf.Modules = append(mf.Modules, manifestFileEntry{Name: name, Entry: m[name]})
	}

	data, err := yaml.Marshal(mf)
	if err != nil {
		return fmt.Errorf("encoding vendor manifest: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating vendor manifest directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing vendor manifest: %w", err)
	}
	return nil
}
