package vendor

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/module"
)

// checkSourceRelocation refuses to change a working directory while local state still depends on that directory; state is never moved by vendoring.
func checkSourceRelocation(n blueprint.Node, dir string) error {
	schema, err := module.Inspect(dir)
	if err != nil {
		return fmt.Errorf("checking existing module before changing its directory: %w", err)
	}
	if schema.Backend != "" && schema.Backend != "local" {
		return nil
	}
	if schema.Backend == "local" && !schema.BackendConfigKnown {
		return fmt.Errorf("node.%s: local backend path cannot be determined before changing the source directory; review the backend configuration and pin its state path before re-vendoring", n.Name)
	}
	path, workspaceDir := "terraform.tfstate", "terraform.tfstate.d"
	if schema.Backend == "local" {
		for _, config := range []map[string]string{schema.BackendConfig, n.BackendConfig} {
			if config["path"] != "" {
				path = config["path"]
			}
			if config["workspace_dir"] != "" {
				workspaceDir = config["workspace_dir"]
			}
		}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	} else if !pathWithin(dir, path) {
		path = ""
	}
	if path != "" {
		if err := refuseExistingState(n.Name, path); err != nil {
			return err
		}
	}
	if filepath.IsAbs(workspaceDir) {
		if !pathWithin(dir, workspaceDir) {
			return nil
		}
	} else {
		workspaceDir = filepath.Join(dir, workspaceDir)
	}
	entries, err := os.ReadDir(workspaceDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking local workspaces before changing source directory: %w", err)
	}
	for _, entry := range entries {
		if err := refuseExistingState(n.Name, filepath.Join(workspaceDir, entry.Name(), "terraform.tfstate")); err != nil {
			return err
		}
	}
	return nil
}

func refuseExistingState(name, path string) error {
	for _, candidate := range []string{path, path + ".backup"} {
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return fmt.Errorf("checking local state before changing source directory: %w", err)
		}
		return fmt.Errorf("node.%s: changing the source directory would remove or change the local state path %s; migrate state outside the vendored tree and configure an absolute backend path before re-vendoring", name, candidate)
	}
	return nil
}

// pathWithin also covers absolute state paths that would disappear when the old vendor tree is replaced.
func pathWithin(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err == nil && filepath.IsLocal(rel) {
		return true
	}
	resolvedDir, dirErr := filepath.EvalSymlinks(dir)
	resolvedPath, pathErr := filepath.EvalSymlinks(path)
	if dirErr == nil && pathErr == nil {
		rel, err = filepath.Rel(resolvedDir, resolvedPath)
		if err == nil && filepath.IsLocal(rel) {
			return true
		}
	}
	rootInfo, err := os.Stat(dir)
	if err != nil {
		return false
	}
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		if info, err := os.Stat(parent); err == nil && os.SameFile(rootInfo, info) {
			return true
		}
		if filepath.Dir(parent) == parent {
			return false
		}
	}
}
