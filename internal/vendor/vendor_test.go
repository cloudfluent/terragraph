package vendor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

// fakeFetcher lets tests exercise dispatch and All's orchestration without any network access. writeContent is written into dst as main.tf on a successful Fetch, standing in for whatever a real fetcher would clone.
type fakeFetcher struct {
	prefix       string
	writeContent string
	err          error
	calls        *int
}

func (f fakeFetcher) Matches(src string) bool {
	return len(src) >= len(f.prefix) && src[:len(f.prefix)] == f.prefix
}

func (f fakeFetcher) Fetch(ctx context.Context, src, dst string) error {
	if f.calls != nil {
		*f.calls++
	}
	if f.err != nil {
		return f.err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dst, "main.tf"), []byte(f.writeContent), 0o644)
}

func withFetchers(t *testing.T, fs ...Fetcher) {
	t.Helper()
	orig := fetchers
	fetchers = fs
	t.Cleanup(func() { fetchers = orig })
}

func TestFetch_DispatchesToMatchingFetcher(t *testing.T) {
	calls := 0
	withFetchers(t, fakeFetcher{prefix: "fake::", writeContent: "x", calls: &calls})

	dst := filepath.Join(t.TempDir(), "out")
	if err := fetch(context.Background(), "fake::thing", dst); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected the fake fetcher to be called once, got %d", calls)
	}
}

func TestFetch_UnrecognizedSourceReturnsClearError(t *testing.T) {
	withFetchers(t, fakeFetcher{prefix: "fake::"})

	err := fetch(context.Background(), "totally-unsupported-thing", t.TempDir())
	if err == nil {
		t.Fatalf("expected an error for an unrecognized source")
	}
}

func TestAll_VendorsRemoteNodesOnly(t *testing.T) {
	withFetchers(t, fakeFetcher{prefix: "fake::", writeContent: "resource {}"})

	baseDir := t.TempDir()
	nodes := []blueprint.Node{
		{Name: "local", Source: "./stacks/local"},
		{Name: "remote", Source: "fake::https://example.com/repo"},
	}
	manifestPath := filepath.Join(baseDir, "vendor.yaml")

	results, err := All(nodes, baseDir, "vendor", manifestPath, Options{})
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(results) != 1 || results[0].Node != "remote" {
		t.Fatalf("expected only the remote node to be vendored, got %+v", results)
	}

	dst := filepath.Join(baseDir, "vendor", "remote")
	if _, err := os.Stat(filepath.Join(dst, "main.tf")); err != nil {
		t.Fatalf("expected vendored content at %s: %v", dst, err)
	}

	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	entry, ok := manifest["remote"]
	if !ok {
		t.Fatalf("expected a manifest entry for %q", "remote")
	}
	if entry.Source != nodes[1].Source {
		t.Fatalf("manifest entry Source = %q, want %q", entry.Source, nodes[1].Source)
	}
}

func TestAll_SkipsAlreadyVendoredWithoutForce(t *testing.T) {
	calls := 0
	withFetchers(t, fakeFetcher{prefix: "fake::", writeContent: "x", calls: &calls})

	baseDir := t.TempDir()
	nodes := []blueprint.Node{{Name: "remote", Source: "fake::https://example.com/repo"}}
	manifestPath := filepath.Join(baseDir, "vendor.yaml")

	if _, err := All(nodes, baseDir, "vendor", manifestPath, Options{}); err != nil {
		t.Fatalf("first All: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 fetch after the first run, got %d", calls)
	}

	results, err := All(nodes, baseDir, "vendor", manifestPath, Options{})
	if err != nil {
		t.Fatalf("second All: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected no additional fetch on the second run, got %d total calls", calls)
	}
	if len(results) != 1 || !results[0].Skipped {
		t.Fatalf("expected the second run to report Skipped, got %+v", results)
	}
}

func TestAll_ForceRefetchesAndPreservesExclude(t *testing.T) {
	withFetchers(t, fakeFetcher{prefix: "fake::", writeContent: "resource {}"})

	baseDir := t.TempDir()
	nodes := []blueprint.Node{{Name: "remote", Source: "fake::https://example.com/repo"}}
	manifestPath := filepath.Join(baseDir, "vendor.yaml")

	if _, err := All(nodes, baseDir, "vendor", manifestPath, Options{}); err != nil {
		t.Fatalf("first All: %v", err)
	}

	// Simulate the user hand-editing exclude into the manifest.
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	entry := manifest["remote"]
	entry.Exclude = []string{"*.md"}
	manifest["remote"] = entry
	if err := manifest.Save(manifestPath); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := All(nodes, baseDir, "vendor", manifestPath, Options{Force: true}); err != nil {
		t.Fatalf("forced All: %v", err)
	}

	final, err := LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("LoadManifest (final): %v", err)
	}
	if len(final["remote"].Exclude) != 1 || final["remote"].Exclude[0] != "*.md" {
		t.Fatalf("expected exclude to survive the re-vendor unchanged, got %v", final["remote"].Exclude)
	}
}

func TestAll_SourceChangeRefetchesWithoutForce(t *testing.T) {
	calls := 0
	withFetchers(t, fakeFetcher{prefix: "fake::", writeContent: "resource {}", calls: &calls})

	baseDir := t.TempDir()
	manifestPath := filepath.Join(baseDir, "vendor.yaml")

	nodeV1 := blueprint.Node{Name: "remote", Source: "fake::https://example.com/repo?ref=v1.0.0"}
	if _, err := All([]blueprint.Node{nodeV1}, baseDir, "vendor", manifestPath, Options{}); err != nil {
		t.Fatalf("first All: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 fetch after the first run, got %d", calls)
	}

	// Same node name, but the blueprint's declared source changed (a ref bump). This must trigger a re-fetch even without --force, since the manifest's recorded Source no longer matches.
	nodeV2 := blueprint.Node{Name: "remote", Source: "fake::https://example.com/repo?ref=v2.0.0"}
	results, err := All([]blueprint.Node{nodeV2}, baseDir, "vendor", manifestPath, Options{})
	if err != nil {
		t.Fatalf("second All: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected a second fetch when Source changed, got %d total calls", calls)
	}
	if len(results) != 1 || results[0].Skipped {
		t.Fatalf("expected the second run to actually vendor (not skip), got %+v", results)
	}

	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if manifest["remote"].Source != nodeV2.Source {
		t.Fatalf("manifest Source = %q, want %q", manifest["remote"].Source, nodeV2.Source)
	}
}

func TestAll_LocalOnlyBlueprintIsANoOp(t *testing.T) {
	withFetchers(t) // no fetchers registered; a call would panic-free error, but nothing should call fetch at all

	baseDir := t.TempDir()
	nodes := []blueprint.Node{{Name: "local", Source: "./stacks/local"}}
	results, err := All(nodes, baseDir, "vendor", filepath.Join(baseDir, "vendor.yaml"), Options{})
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no results for an all-local blueprint, got %+v", results)
	}
}
