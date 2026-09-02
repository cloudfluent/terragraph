package module

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspect_VariableTypeAndRequired(t *testing.T) {
	dir := t.TempDir()
	src := `
variable "region" {
  type = string
}

variable "instance_count" {
  type    = number
  default = 1
}

variable "untyped" {
}

output "id" {
  value = "x"
}
`
	if err := os.WriteFile(filepath.Join(dir, "variables.tf"), []byte(src), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}

	schema, err := Inspect(dir)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}

	region := schema.Variables["region"]
	if region.Type != "string" || !region.Required {
		t.Fatalf("unexpected region variable: %+v", region)
	}

	count := schema.Variables["instance_count"]
	if count.Type != "number" || count.Required {
		t.Fatalf("unexpected instance_count variable (should have a default, not required): %+v", count)
	}

	untyped := schema.Variables["untyped"]
	if untyped.Type != "" || !untyped.Required {
		t.Fatalf("unexpected untyped variable: %+v", untyped)
	}

	if !schema.HasOutput("id") {
		t.Fatalf("expected output %q to be present", "id")
	}
	if schema.HasOutput("does_not_exist") {
		t.Fatalf("expected no output named does_not_exist")
	}
}
