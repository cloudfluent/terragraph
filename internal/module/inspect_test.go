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
	if schema.Backend != "" {
		t.Fatalf("expected no backend, got %q", schema.Backend)
	}
}

func TestInspect_BackendTypes(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		content string
		want    string
	}{
		{
			name:    "no backend block",
			file:    "main.tf",
			content: `output "id" { value = "x" }`,
			want:    "",
		},
		{
			name: "empty local",
			file: "main.tf",
			content: `
terraform {
  backend "local" {}
}
output "id" { value = "x" }
`,
			want: "local",
		},
		{
			name: "s3",
			file: "main.tf",
			content: `
terraform {
  backend "s3" {}
}
output "id" { value = "x" }
`,
			want: "s3",
		},
		{
			name: "cloud",
			file: "main.tf",
			content: `
terraform {
  cloud {}
}
output "id" { value = "x" }
`,
			want: "cloud",
		},
		{
			name: "tf.json local",
			file: "terraform.tf.json",
			content: `{
  "terraform": {
    "backend": {
      "local": {}
    }
  },
  "output": {
    "id": { "value": "x" }
  }
}
`,
			want: "local",
		},
		{
			name: "tf.json s3",
			file: "terraform.tf.json",
			content: `{
  "terraform": {
    "backend": {
      "s3": {}
    }
  }
}
`,
			want: "s3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, tt.file), []byte(tt.content), 0o644); err != nil {
				t.Fatalf("writing fixture: %v", err)
			}
			schema, err := Inspect(dir)
			if err != nil {
				t.Fatalf("Inspect: %v", err)
			}
			if schema.Backend != tt.want {
				t.Fatalf("Backend = %q, want %q", schema.Backend, tt.want)
			}
		})
	}
}
