package graph

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

// vendoredModuleDir accepts legacy flat copies but never falls back after finding malformed package metadata.
func vendoredModuleDir(dir string) (string, error) {
	source, err := blueprint.ReadVendoredSource(dir)
	if err != nil {
		return "", err
	}
	if source == nil {
		return dir, nil
	}
	return source.Directory(dir)
}

// SourceNode carries the same qualified leaf identity as Build without requiring its remote module to exist yet.
type SourceNode struct {
	blueprint.Node
	// LegacyDir retains an existing group-local copy until an explicit refresh can safely change its working directory.
	LegacyDir string
}

// SourceNodes shares group parsing, cycle detection and namespace rules with Build while omitting schema-dependent validation until after fetching.
func SourceNodes(bp *blueprint.Blueprint, baseDir string) ([]SourceNode, error) {
	return sourceNodes(bp, baseDir, "", nil, &resolveContext{})
}

func sourceNodes(bp *blueprint.Blueprint, baseDir, namespace string, ambientBackend map[string]string, rc *resolveContext) ([]SourceNode, error) {
	var nodes []SourceNode
	for _, n := range bp.Nodes {
		leaf := cloneNode(n)
		leaf.Name = qualifyName(namespace, n.Name)
		leaf.BackendConfig = mergeEnv(ambientBackend, leaf.BackendConfig)
		source := SourceNode{Node: leaf}
		if namespace != "" && blueprint.IsRemote(n.Source) {
			source.LegacyDir = legacyGroupSourceDir(baseDir, bp.VendorDirectory(), n.Name)
		}
		nodes = append(nodes, source)
	}
	for _, u := range bp.Uses {
		groupDir := filepath.Join(baseDir, u.Source)
		pop, err := rc.push(groupDir, u.GroupName)
		if err != nil {
			return nil, err
		}
		def, runtimes, _, vendor, err := loadGroupDef(rc, groupDir, u.GroupName)
		if err != nil {
			pop()
			return nil, err
		}
		inner := &blueprint.Blueprint{Nodes: def.Nodes, Uses: def.Uses, Runtimes: runtimes, Vendor: vendor}
		leaves, err := sourceNodes(inner, groupDir, qualifyName(namespace, u.As), mergeEnv(ambientBackend, u.BackendConfig), rc)
		pop()
		if err != nil {
			return nil, fmt.Errorf("use %q as %q: %w", u.GroupName, u.As, err)
		}
		nodes = append(nodes, leaves...)
	}
	return nodes, nil
}

// qualifyName is shared by source discovery and graph expansion so --node always identifies the same leaf.
func qualifyName(namespace, name string) string {
	if namespace == "" {
		return name
	}
	return namespace + "." + name
}

// remoteModuleDir falls back only when the root-managed entry is absent, never when a present entry is broken or has invalid metadata.
func remoteModuleDir(rootVendorDir, scopeDir, scopeVendorDir, qualifiedName, localName string) (string, error) {
	dir := filepath.Join(rootVendorDir, qualifiedName)
	if _, err := os.Lstat(dir); os.IsNotExist(err) {
		if qualifiedName != localName {
			dir = legacyGroupSourceDir(scopeDir, scopeVendorDir, localName)
		}
	} else if err != nil {
		return "", fmt.Errorf("reading vendored source: %w", err)
	}
	info, err := os.Stat(dir)
	if os.IsNotExist(err) {
		return "", fmt.Errorf("source is not vendored or is incomplete; run \"terragraph vendor --node %s\"", qualifiedName)
	}
	if err != nil {
		return "", fmt.Errorf("reading vendored source: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("vendored source %s is not a directory; remove it and run \"terragraph vendor --node %s\"", dir, qualifiedName)
	}
	return vendoredModuleDir(dir)
}

// legacyGroupSourceDir preserves the directory older graph builds actually executed before honoring a formerly ignored group vendor setting.
func legacyGroupSourceDir(scopeDir, vendorDir, name string) string {
	previous := filepath.Join(scopeDir, "vendor", name)
	if _, err := os.Lstat(previous); !os.IsNotExist(err) {
		return previous
	}
	return filepath.Join(scopeDir, vendorDir, name)
}
