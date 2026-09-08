package graph

import "github.com/cloudfluent/terragraph/internal/blueprint"

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
