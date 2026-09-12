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

func TestInspect_DeclarationLocationsAndDefaultPresence(t *testing.T) {
	dir := t.TempDir()
	writeInspectionFile(t, dir, "variables.tf", "variable \"region\" {\n  type = string\n}\nvariable \"instance_count\" {\n  type    = number\n  default = 2\n}\nvariable \"untyped\" {\n}\n")
	writeInspectionFile(t, dir, "outputs.tf", "output \"id\" {\n  value = 1\n}\noutput \"sensitive_id\" {\n  value     = 1\n  sensitive = true\n}\n")

	schema, err := Inspect(dir)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}

	region := schema.Variables["region"]
	if got, want := region.Loc, (Loc{File: "variables.tf", Line: 1, Column: 1}); got != want {
		t.Fatalf("region Loc = %+v, want %+v", got, want)
	}
	if region.HasDefault || !region.Required {
		t.Fatalf("region has_default = %t, required = %t; want no default and required", region.HasDefault, region.Required)
	}

	count := schema.Variables["instance_count"]
	if got, want := count.Loc, (Loc{File: "variables.tf", Line: 4, Column: 1}); got != want {
		t.Fatalf("instance_count Loc = %+v, want %+v", got, want)
	}
	if !count.HasDefault || count.Required {
		t.Fatalf("instance_count has_default = %t, required = %t; want default present and not required", count.HasDefault, count.Required)
	}

	// An untyped variable stays a successful inspection with an empty type, never a module-read failure.
	untyped := schema.Variables["untyped"]
	if got, want := untyped.Loc, (Loc{File: "variables.tf", Line: 8, Column: 1}); got != want {
		t.Fatalf("untyped Loc = %+v, want %+v", got, want)
	}
	if untyped.Type != "" || untyped.HasDefault {
		t.Fatalf("untyped variable = %+v; want empty type without a default", untyped)
	}

	if got, want := schema.OutputDetails["id"].Loc, (Loc{File: "outputs.tf", Line: 1, Column: 1}); got != want {
		t.Fatalf("id Loc = %+v, want %+v", got, want)
	}
	if got, want := schema.OutputDetails["sensitive_id"].Loc, (Loc{File: "outputs.tf", Line: 4, Column: 1}); got != want {
		t.Fatalf("sensitive_id Loc = %+v, want %+v", got, want)
	}
}

func TestInspect_OpenTofuDeclarationLocationsUseSelectedFile(t *testing.T) {
	dir := t.TempDir()
	writeInspectionFile(t, dir, "main.tf", "variable \"v\" {\n  type = string\n}\noutput \"id\" {\n  value = 1\n}\n")
	writeInspectionFile(t, dir, "main.tofu", "\nvariable \"v\" {\n  type    = string\n  default = \"tofu\"\n}\noutput \"id\" {\n  value = 1\n}\n")

	tofu, err := Inspect(dir, OpenTofuFiles)
	if err != nil {
		t.Fatalf("Inspect(OpenTofuFiles): %v", err)
	}
	v := tofu.Variables["v"]
	if got, want := v.Loc, (Loc{File: "main.tofu", Line: 2, Column: 1}); got != want {
		t.Fatalf("OpenTofu v Loc = %+v, want %+v", got, want)
	}
	if !v.HasDefault {
		t.Fatal("OpenTofu v has_default = false, want the .tofu declaration's default")
	}
	if got, want := tofu.OutputDetails["id"].Loc, (Loc{File: "main.tofu", Line: 6, Column: 1}); got != want {
	}

	terraform, err := Inspect(dir, TerraformFiles)
	if err != nil {
		t.Fatalf("Inspect(TerraformFiles): %v", err)
	}
	v = terraform.Variables["v"]
	if got, want := v.Loc, (Loc{File: "main.tf", Line: 1, Column: 1}); got != want {
		t.Fatalf("Terraform v Loc = %+v, want %+v", got, want)
	}
	if v.HasDefault || !v.Required {
		t.Fatalf("Terraform v has_default = %t, required = %t; want the .tf declaration without a default", v.HasDefault, v.Required)
	}
}

func TestInspect_OverrideKeepsBaseDeclarationLocation(t *testing.T) {
	dir := t.TempDir()
	writeInspectionFile(t, dir, "main.tf", "variable \"region\" {\n  type = string\n}\noutput \"id\" {\n  value = 1\n}\n")
	writeInspectionFile(t, dir, "override.tf", "variable \"region\" {\n  default = \"eu-central-1\"\n}\nvariable \"extra\" {\n  type = string\n}\n")

	schema, err := Inspect(dir, TerraformFiles)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}

	region := schema.Variables["region"]
	if got, want := region.Loc, (Loc{File: "main.tf", Line: 1, Column: 1}); got != want {
		t.Fatalf("region Loc = %+v, want the base declaration in main.tf: %+v", got, want)
	}
	if !region.HasDefault || region.Required {
		t.Fatalf("region has_default = %t, required = %t; want sparse override default merged in", region.HasDefault, region.Required)
	}

	extra := schema.Variables["extra"]
	if got, want := extra.Loc, (Loc{File: "override.tf", Line: 4, Column: 1}); got != want {
		t.Fatalf("override-only extra Loc = %+v, want %+v", got, want)
	}

	if got, want := schema.OutputDetails["id"].Loc, (Loc{File: "main.tf", Line: 4, Column: 1}); got != want {
		t.Fatalf("id Loc = %+v, want %+v", got, want)
	}
}

func TestInspect_UnknownRuntimeIgnoresLocationDifferences(t *testing.T) {
	dir := t.TempDir()
	writeInspectionFile(t, dir, "main.tf", "variable \"v\" {\n  type = string\n}\noutput \"id\" {\n  value = 1\n}\n")
	writeInspectionFile(t, dir, "main.tofu", "\n\nvariable \"v\" {\n  type = string\n}\noutput \"id\" {\n  value = 1\n}\n")
	if _, err := Inspect(dir, UnknownFiles); err != nil {
		t.Fatalf("equivalent declarations at different locations must stay equivalent: %v", err)
	}
}

func TestInspect_MissingModuleStillFails(t *testing.T) {
	if _, err := Inspect(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("Inspect on a missing module directory must fail")
	}
}
