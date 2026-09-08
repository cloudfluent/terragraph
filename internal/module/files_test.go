package module

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeInspectionFile(t *testing.T, dir, name, source string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInspect_OpenTofuReplacesOnlySameFormatCounterparts(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"main.tf":         `output "ignored_tf" { value = "x" }`,
		"main.tofu":       `output "tofu_hcl" { value = "x" }`,
		"main.tf.json":    `{"output":{"ignored_json":{"value":"x"}}}`,
		"main.tofu.json":  `{"output":{"tofu_json":{"value":"x"}}}`,
		"cross.tf":        `output "cross_tf" { value = "x" }`,
		"cross.tofu.json": `{"output":{"cross_tofu_json":{"value":"x"}}}`,
		"other.tf.json":   `{"output":{"cross_tf_json":{"value":"x"}}}`,
		"other.tofu":      `output "cross_tofu" { value = "x" }`,
		".hidden.tofu":    `invalid configuration`,
		"ignored.tofu~":   `invalid configuration`,
	}
	for name, src := range files {
		writeInspectionFile(t, dir, name, src)
	}
	tofu, err := Inspect(dir, OpenTofuFiles)
	if err != nil {
		t.Fatal(err)
	}
	if !tofu.RequiresTofuFiles {
		t.Fatal("OpenTofu-selected files did not require runtime support")
	}
	for _, name := range []string{"tofu_hcl", "tofu_json", "cross_tf", "cross_tofu_json", "cross_tf_json", "cross_tofu"} {
		if !tofu.HasOutput(name) {
			t.Fatalf("missing OpenTofu output %q", name)
		}
	}
	if tofu.HasOutput("ignored_tf") || tofu.HasOutput("ignored_json") {
		t.Fatal("OpenTofu inspected replaced Terraform declarations")
	}
	terraform, err := Inspect(dir, TerraformFiles)
	if err != nil {
		t.Fatal(err)
	}
	if terraform.RequiresTofuFiles {
		t.Fatal("Terraform selection must not require OpenTofu file support")
	}
	if !terraform.HasOutput("ignored_tf") || !terraform.HasOutput("ignored_json") || terraform.HasOutput("tofu_hcl") || terraform.HasOutput("tofu_json") {
		t.Fatalf("Terraform outputs = %v", terraform.Outputs)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != len(files) {
		t.Fatalf("module files changed: %v, %v", entries, err)
	}
	for name, src := range files {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(data) != src {
			t.Fatalf("module file %s changed", name)
		}
	}
}

func TestInspect_UnknownRuntimeAcceptsEquivalentDeclarations(t *testing.T) {
	dir := t.TempDir()
	writeInspectionFile(t, dir, "main.tf.json", `{"variable":{"input":{"type":"number","default":9007199254740993}},"output":{"value":{"value":1}},"terraform":{"backend":{"s3":{"bucket":"fixture"}}}}`)
	writeInspectionFile(t, dir, "main.tofu.json", `{"terraform":{"backend":{"s3":{"bucket":"fixture"}}},"output":{"value":{"value":2}},"variable":{"input":{"default":9007199254740993,"type":"number"}}}`)
	if schema, err := Inspect(dir, UnknownFiles); err != nil {
		t.Fatalf("equivalent declarations: %v", err)
	} else if schema.RequiresTofuFiles {
		t.Fatal("unknown runtime must not claim OpenTofu-only support")
	}
}

func TestInspect_UnknownRuntimeRejectsDifferingDeclarations(t *testing.T) {
	for _, pair := range []struct{ name, terraform, tofu string }{
		{"ports", `output "tf" { value = 1 }`, `output "tofu" { value = 1 }`},
		{"types", "variable \"v\" { type = string }", "variable \"v\" { type = number }"},
		{"defaults", "variable \"v\" { default = 1 }", "variable \"v\" { default = 2 }"},
		{"precise defaults", "variable \"v\" { default = 9007199254740992 }", "variable \"v\" { default = 9007199254740993 }"},
		{"nested defaults", "variable \"v\" { default = { nested = [9007199254740992] } }", "variable \"v\" { default = { nested = [9007199254740993] } }"},
		{"null default", "variable \"v\" {}", "variable \"v\" { default = null }"},
		{"output sensitivity", "output \"v\" {\nvalue = 1\nsensitive = false\n}", "output \"v\" {\nvalue = 1\nsensitive = true\n}"},
		{"input sensitivity", "variable \"v\" { sensitive = false }", "variable \"v\" { sensitive = true }"},
		{"backend type", "terraform {\nbackend \"local\" {}\n}", "terraform {\nbackend \"s3\" {}\n}"},
		{"backend address", "terraform {\nbackend \"s3\" { key = \"one\" }\n}", "terraform {\nbackend \"s3\" { key = \"two\" }\n}"},
		{"backend object", "terraform {\nbackend \"s3\" { endpoints = { s3 = \"one\" } }\n}", "terraform {\nbackend \"s3\" { endpoints = { s3 = \"two\" } }\n}"},
		{"backend expression", "terraform {\nbackend \"s3\" { key = var.one }\n}", "terraform {\nbackend \"s3\" { key = var.two }\n}"},
	} {
		t.Run(pair.name, func(t *testing.T) {
			dir := t.TempDir()
			writeInspectionFile(t, dir, "main.tf", pair.terraform)
			writeInspectionFile(t, dir, "main.tofu", pair.tofu)
			_, err := Inspect(dir, UnknownFiles)
			if err == nil || !strings.Contains(err.Error(), "runtime binary is ambiguous") {
				t.Fatalf("Inspect error = %v, want actionable ambiguity error", err)
			}
		})
	}
}

func TestInspect_OpenTofuBackendUsesSelectedFile(t *testing.T) {
	dir := t.TempDir()
	writeInspectionFile(t, dir, "main.tf", "terraform {\nbackend \"s3\" { bucket = \"tf\" }\n}")
	writeInspectionFile(t, dir, "main.tofu", "terraform {\nbackend \"local\" { path = \"tofu.tfstate\" }\n}")
	schema, err := Inspect(dir, OpenTofuFiles)
	if err != nil {
		t.Fatal(err)
	}
	if schema.Backend != "local" || schema.BackendConfig["path"] != "tofu.tfstate" || !schema.BackendConfigKnown {
		t.Fatalf("backend = %q %v, known=%v", schema.Backend, schema.BackendConfig, schema.BackendConfigKnown)
	}
}

func TestInspect_OpenTofuSparseOverridePreservesPortMetadata(t *testing.T) {
	dir := t.TempDir()
	writeInspectionFile(t, dir, "main.tf", "variable \"v\" {\n type = string\n default = \"original\"\n sensitive = true\n}\noutput \"value\" {\n value = \"original\"\n sensitive = true\n}")
	writeInspectionFile(t, dir, "override.tofu", "variable \"v\" { description = \"overridden\" }\noutput \"value\" { value = \"changed\" }")
	schema, err := Inspect(dir, OpenTofuFiles)
	if err != nil {
		t.Fatal(err)
	}
	v := schema.Variables["v"]
	if v.Type != "string" || v.Required || !v.Sensitive || v.Description != "overridden" || !schema.OutputDetails["value"].Sensitive {
		t.Fatalf("sparse override lost metadata: input=%+v output=%+v", v, schema.OutputDetails["value"])
	}
}

func TestInspect_TFOnlyDoesNotRequireTofuSupport(t *testing.T) {
	dir := t.TempDir()
	writeInspectionFile(t, dir, "main.tf", `output "v" { value = 1 }`)
	writeInspectionFile(t, dir, ".hidden.tofu", `invalid ignored file`)
	schema, err := Inspect(dir, OpenTofuFiles)
	if err != nil {
		t.Fatal(err)
	}
	if schema.RequiresTofuFiles {
		t.Fatal("unselected .tofu file must not require runtime support")
	}
}
