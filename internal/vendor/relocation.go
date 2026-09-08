package vendor

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/module"
)

// checkSourceRelocation protects local state before replacing a source tree or changing its execution directory; state is never moved by vendoring.
func checkSourceRelocation(n blueprint.Node, dir string) error {
	treeDir := dir
	source, err := blueprint.ReadVendoredSource(dir)
	if err != nil {
		return err
	}
	if source != nil {
		dir, err = source.Directory(dir)
		if err != nil {
			return err
		}
	}
	schema, err := module.Inspect(dir, module.UnknownFiles)
	if err != nil {
		return fmt.Errorf("checking existing module before replacing its source (vendoring requires matching Terraform/OpenTofu declarations because it does not select a runtime): %w", err)
	}
	if schema.Backend != "" && schema.Backend != "local" {
		return nil
	}
	if schema.Backend == "local" && !schema.BackendConfigKnown {
		return fmt.Errorf("node.%s: local backend path cannot be determined before replacing the source directory; review the backend configuration and pin its state path before re-vendoring", n.Name)
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
	if err := checkLocalStatePath(n.Name, dir, treeDir, path); err != nil {
		return err
	}
	workspacePath := workspaceDir
	if !filepath.IsAbs(workspacePath) {
		workspacePath = filepath.Join(dir, workspacePath)
	}
	entries, err := os.ReadDir(workspacePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking local workspaces before replacing source directory: %w", err)
	}
	for _, entry := range entries {
		if err := checkLocalStatePath(n.Name, dir, treeDir, filepath.Join(workspaceDir, entry.Name(), "terraform.tfstate")); err != nil {
			return err
		}
	}
	return nil
}

// checkLocalStatePath checks each existing backup independently because its primary state may no longer exist.
func checkLocalStatePath(name, dir, treeDir, path string) error {
	relative := !filepath.IsAbs(path)
	if relative {
		path = filepath.Join(dir, path)
	}
	for _, candidate := range []string{path, path + ".backup"} {
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return fmt.Errorf("checking local state before replacing source directory: %w", err)
		}
		if !relative {
			within, err := pathWithin(treeDir, candidate)
			if err != nil {
				return fmt.Errorf("checking absolute local state before replacing source directory: %w", err)
			}
			if !within {
				continue
			}
		}
		return fmt.Errorf("node.%s: replacing the source directory would remove or change the local state path %s; migrate state outside the vendored tree and configure an absolute backend path before re-vendoring", name, candidate)
	}
	return nil
}

// pathWithin compares real ancestor identities so case aliases and symlinked paths cannot hide state inside the tree being replaced.
func pathWithin(dir, path string) (bool, error) {
	rootInfo, err := os.Stat(dir)
	if err != nil {
		return false, err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false, err
	}
	for _, spelling := range []string{path, resolved} {
		for parent := filepath.Dir(spelling); ; parent = filepath.Dir(parent) {
			info, err := os.Stat(parent)
			if err != nil {
				return false, err
			}
			if os.SameFile(rootInfo, info) {
				return true, nil
			}
			if filepath.Dir(parent) == parent {
				break
			}
		}
	}
	return false, nil
}
