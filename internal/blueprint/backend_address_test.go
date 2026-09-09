package blueprint

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseFile_BackendAddressScopes(t *testing.T) {
	bp, err := ParseFile(writeTemp(t, `
node "generated" {
  source = "./module"
  backend_address = { s3_key_prefix = "prod", s3_key_name = "state.json" }
}
node "inherited" { source = "./module" }
node "disabled" {
  source = "./module"
  backend_address = {}
}
use "service" {
  as = "checkout"
  source = "./group"
  backend_address = { s3_key_prefix = "dev" }
}
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := bp.Nodes[0].BackendAddress["s3_key_prefix"]; got != "prod" {
		t.Fatalf("pattern = %q, want %q", got, "prod")
	}
	if bp.Nodes[1].BackendAddress != nil || bp.Nodes[2].BackendAddress == nil || bp.Nodes[2].BackendAddress["s3_key_prefix"] != "" {
		t.Fatalf("rules = %+v, want distinct inherited and disabled rules", bp.Nodes)
	}
	if got := bp.Uses[0].BackendAddress["s3_key_prefix"]; got != "dev" {
		t.Fatalf("pattern = %q, want %q", got, "dev")
	}
}

func TestParseFile_BackendAddressRejectsInvalidRules(t *testing.T) {
	for _, tc := range []struct{ name, rule, want string }{
		{"null", `null`, "map/object"},
		{"scalar", `"prod"`, "map/object"},
		{"list", `[]`, "map/object"},
		{"unknown backend", `{ gcs_prefix = "prod" }`, "unsupported field"},
		{"old pattern", `{ s3_key = "prod/{node}" }`, "unsupported field"},
		{"null prefix", `{ s3_key_prefix = null }`, "must not be null"},
		{"null name", `{ s3_key_name = null }`, "must not be null"},
		{"empty name", `{ s3_key_name = "" }`, "non-empty file name"},
		{"directory name", `{ s3_key_name = "nested/state" }`, "without path separators"},
		{"windows directory name", `{ s3_key_name = "nested\\state" }`, "without path separators"},
		{"parent name", `{ s3_key_name = ".." }`, "not . or .."},
		{"current name", `{ s3_key_name = "." }`, "not . or .."},
		{"interpolation", `{ s3_key_prefix = "${node.name}" }`, "literal map"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseFile(writeTemp(t, fmt.Sprintf("node \"app\" {\n source = \"./module\"\n backend_address = %s\n}\n", tc.rule)))
			if err == nil || !strings.Contains(err.Error(), "node.app:") || !strings.Contains(err.Error(), "backend_address") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want node address diagnostic containing %q", err, tc.want)
			}
		})
	}
}

func TestParseFile_BackendAddressUseErrorNamesInstance(t *testing.T) {
	_, err := ParseFile(writeTemp(t, `use "service" {
  as = "checkout"
  source = "./group"
  backend_address = { s3_key_name = "" }
}`))
	if err == nil || !strings.Contains(err.Error(), "use.checkout:") {
		t.Fatalf("error = %v, want use.checkout address diagnostic", err)
	}
}
