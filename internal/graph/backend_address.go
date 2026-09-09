package graph

import (
	"fmt"
	"strings"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/module"
)

// mergeBackendAddress lets a leaf change its file name without losing the instance prefix, while {} stops inherited generation.
func mergeBackendAddress(base, override map[string]string) map[string]string {
	if override != nil && len(override) == 0 {
		return override
	}
	return mergeEnv(base, override)
}

// fillBackendAddress runs after inheritance and qualification so explicit addresses survive and repeated groups cannot share generated keys.
func fillBackendAddress(n *blueprint.Node, schema *module.Schema) error {
	if len(n.BackendAddress) == 0 || schema == nil || schema.Backend != "s3" {
		return nil
	}
	if _, explicit := n.BackendConfig["key"]; explicit {
		return nil
	}
	if _, explicit := schema.BackendConfig["key"]; explicit || schema.BackendAttributes["key"] {
		return nil
	}
	if schema.BackendAttributes == nil {
		return fmt.Errorf("node.%s.backend_address: cannot establish whether the module declares a key; set backend_config.key explicitly or disable generation with backend_address = {}", n.Name)
	}
	if n.BackendConfig == nil {
		n.BackendConfig = make(map[string]string)
	}
	name := n.BackendAddress["s3_key_name"]
	if name == "" {
		name = "terraform.tfstate"
	}
	// S3 keys use literal forward slashes on every OS; filesystem path cleaning would silently change the selected namespace.
	key := n.Name + "/" + name
	if prefix := n.BackendAddress["s3_key_prefix"]; prefix != "" {
		key = strings.TrimSuffix(prefix, "/") + "/" + key
	}
	n.BackendConfig["key"] = key
	return nil
}
