// Package cache implements content-addressed incremental apply: a node is skipped if neither its own source files nor its resolved input values have changed since the last successful apply, a Merkle-DAG-style build cache, in the spirit of Bazel/Nix, rather than heuristic staleness tracking.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// isSourceFile reports whether a file actually represents the module's configuration. This is deliberately an allowlist, not a blocklist: a module's own directory commonly accumulates files that are not source (a local_file resource's own output artifact, a lock file created fresh by the first-ever init), and those must never affect the hash, or a node would never be considered "unchanged" even when nothing meaningful actually changed (its own last apply's side effects would look like a source change to the very next run).
func isSourceFile(name string) bool {
	if name == ".terraform.lock.hcl" {
		return true
	}
	return strings.HasSuffix(name, ".tf") || strings.HasSuffix(name, ".tf.json")
}

// HashDir returns a deterministic hash of a module directory's source files, independent of filesystem iteration order.
func HashDir(dir string) (string, error) {
	var relPaths []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".terraform" {
				return filepath.SkipDir
			}
			return nil
		}
		if !isSourceFile(d.Name()) {
			return nil
		}
		relPaths = append(relPaths, rel)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walking %s: %w", dir, err)
	}
	sort.Strings(relPaths)

	h := sha256.New()
	for _, rel := range relPaths {
		data, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", rel, err)
		}
		_, _ = fmt.Fprintf(h, "%s\x00", rel)
		h.Write(data)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// HashInputs returns a deterministic hash of a resolved input-value map. encoding/json always emits map keys in sorted order, so this is stable regardless of Go map iteration order at any nesting level.
func HashInputs(vars map[string]any) (string, error) {
	data, err := json.Marshal(vars)
	if err != nil {
		return "", fmt.Errorf("encoding inputs: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// Combine folds a node's source hash, input hash, and execution identity (an opaque string identifying everything else that affects what running it actually does without affecting source or resolved input values, currently the runtime it runs against plus any extra environment variables; see engine's resolvedRuntime.cacheIdentity/envIdentity, empty if the caller has no notion of either) into the single hash that determines whether it needs to be re-applied.
func Combine(sourceHash, inputHash, executionIdentity string) string {
	sum := sha256.Sum256([]byte(sourceHash + "\x00" + inputHash + "\x00" + executionIdentity))
	return hex.EncodeToString(sum[:])
}
