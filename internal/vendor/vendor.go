// Package vendor fetches third-party module sources once into a local, git-committed directory instead of resolving them live during plan/apply. validate/graph/plan/apply/destroy never import this package: they only ever see the local directories it produces (see internal/graph's resolution of a remote node.Source into <vendorDir>/<name>/).
package vendor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

// Fetcher materializes one recognized kind of remote source into a local directory. Implementing this for a new source kind (e.g. a Terraform/OpenTofu Registry backend) requires no changes to callers. See fetchers below.
type Fetcher interface {
	Matches(src string) bool
	Fetch(ctx context.Context, src, dst string) error
}

var fetchers = []Fetcher{
	gitFetcher{},
	// A future registryFetcher (Terraform/OpenTofu Registry namespace/name/provider addresses + version resolution) would be added here (not implemented yet); only git sources are supported today.
}

// fetch finds a Fetcher that recognizes src and uses it to materialize src into dst, which must not already exist.
func fetch(ctx context.Context, src, dst string) error {
	for _, f := range fetchers {
		if f.Matches(src) {
			return f.Fetch(ctx, src, dst)
		}
	}
	return fmt.Errorf("no fetcher recognizes source %q (only git sources are supported today)", src)
}

// Options controls a vendoring run.
type Options struct {
	// Force re-fetches a node even if it's already vendored with a matching Source. Without it, an existing <vendorDir>/<name>/ whose manifest entry still has the same Source is left untouched. But a node whose blueprint Source changed since the last vendor (e.g. a ref bump) is always re-fetched, Force or not: that's a declared intent to get different content, not something to require a flag for.
	Force bool
}

// Result is the outcome of vendoring one node.
type Result struct {
	Node    string
	Skipped bool // already vendored with a matching Source, left alone
	Err     error
}

// All vendors every node in nodes whose Source IsRemote, into <baseDir>/<vendorDir>/<name>/, and updates the manifest at manifestPath. Nodes with a local Source are ignored entirely. The returned error covers manifest load/save failures, a whole-run concern distinct from a single node's fetch failure. A single node's fetch failure is captured per-Result instead, so one bad node doesn't stop the others from being vendored.
func All(nodes []blueprint.Node, baseDir, vendorDir, manifestPath string, opts Options) ([]Result, error) {
	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		return nil, err
	}

	var results []Result
	changed := false

	for _, n := range nodes {
		if !blueprint.IsRemote(n.Source) {
			continue
		}

		dst := filepath.Join(baseDir, vendorDir, n.Name)
		existing, hasEntry := manifest[n.Name]
		_, statErr := os.Stat(dst)
		dstExists := statErr == nil

		sourceChanged := hasEntry && existing.Source != n.Source
		if dstExists && !sourceChanged && !opts.Force {
			results = append(results, Result{Node: n.Name, Skipped: true})
			continue
		}

		entry, err := vendorOne(context.Background(), n, dst, existing)
		if err != nil {
			results = append(results, Result{Node: n.Name, Err: err})
			continue
		}

		manifest[n.Name] = entry
		changed = true
		results = append(results, Result{Node: n.Name})
	}

	if changed {
		if err := manifest.Save(manifestPath); err != nil {
			return results, err
		}
	}
	return results, nil
}

// vendorOne fetches n.Source into dst (clearing it first, if present) and prunes it (existing.Exclude, plus .git always). existing.Exclude is carried through unchanged; Source is refreshed to n.Source.
func vendorOne(ctx context.Context, n blueprint.Node, dst string, existing Entry) (Entry, error) {
	if err := os.RemoveAll(dst); err != nil {
		return Entry{}, fmt.Errorf("clearing %s: %w", dst, err)
	}

	if err := fetch(ctx, n.Source, dst); err != nil {
		return Entry{}, fmt.Errorf("fetching: %w", err)
	}

	if err := prune(dst, existing.Exclude); err != nil {
		return Entry{}, fmt.Errorf("pruning: %w", err)
	}

	return Entry{
		Source:  n.Source,
		Exclude: existing.Exclude,
	}, nil
}
