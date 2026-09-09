package blueprint

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
)

// ParseBackendAddress preserves an empty object as an explicit opt-out instead of conflating it with an absent, inherited rule.
func ParseBackendAddress(attr *hcl.Attribute) (map[string]string, error) {
	if attr == nil {
		return nil, nil
	}
	fields, err := parseStringMapAttr(attr, "backend_address")
	if err != nil {
		return nil, fmt.Errorf("%w; use { s3_key_prefix = \"prod\" } or {} to disable generation", err)
	}
	for key, value := range fields {
		switch key {
		case "s3_key_prefix":
		case "s3_key_name":
			if value == "" || value == "." || value == ".." || strings.ContainsAny(value, `/\`) {
				return nil, fmt.Errorf("%s: backend_address.s3_key_name: must be a non-empty file name without path separators, not . or ..; use \"terraform.tfstate\" and put directories in s3_key_prefix", attr.Range)
			}
		default:
			return nil, fmt.Errorf("%s: backend_address.%s: unsupported field; use s3_key_prefix and s3_key_name for S3 key generation", attr.Range, key)
		}
	}
	return fields, nil
}
