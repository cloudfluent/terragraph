package exec

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WriteTFVars (re)writes the ephemeral tfvars file at path from the values resolved for a node's input edges (see engine.Engine.tfVarsPath for where path comes from). If vars is empty, any stale file from a previous run is removed instead: a node with no incoming data edges should never load an outdated value. Terraform never auto-loads this file; callers pass it explicitly via VarFileArgs, since two nodes can share a directory (see Node.BackendConfig) and auto-loading by a fixed name would let them clobber each other.
func WriteTFVars(path string, vars map[string]any) (string, error) {
	if len(vars) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("removing stale tfvars file %s: %w", path, err)
		}
		return path, nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("creating directory for tfvars file %s: %w", path, err)
	}
	data, err := json.MarshalIndent(vars, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encoding tfvars for %s: %w", path, err)
	}
	// 0600, not the 0644 default: resolved inputs include upstream outputs, which are frequently secrets, and this file is per-run scratch that nothing but the owner (and the terraform run acting as them) ever needs to read.
	// os.WriteFile applies perm only when creating, so a leftover 0644 from an older terragraph would stay world-readable for the whole run; removing first makes every write a create, keeping the owner-only mode true on the upgrade and crash-restart paths too.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("removing stale tfvars file %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("writing tfvars file %s: %w", path, err)
	}
	return path, nil
}

// VarFileArgs returns the -var-file flag pointing at the file WriteTFVars just wrote, or nil if vars was empty (WriteTFVars removed any stale file in that case, so there is nothing to load).
func VarFileArgs(path string, vars map[string]any) []string {
	if len(vars) == 0 {
		return nil
	}
	return []string{"-var-file=" + path}
}
