package module

import (
	"strings"
	"testing"
)

func TestInspect_CloudOverrideReplacesBackend(t *testing.T) {
	dir := t.TempDir()
	local := "terraform {\n backend \"local\" { path = \"old.tfstate\" }\n}\n"
	cloud := "terraform {\n cloud { organization = \"offline-fixture\" }\n}\n"
	writeInspectionFile(t, dir, "main.tf", local)
	writeInspectionFile(t, dir, "override.tf", cloud)
	schema, err := Inspect(dir, TerraformFiles)
	if err != nil {
		t.Fatal(err)
	}
	if schema.Backend != "cloud" || len(schema.BackendConfig) != 0 || !schema.BackendConfigKnown {
		t.Fatalf("effective backend = %q %v known=%v, want cloud with no backend attributes", schema.Backend, schema.BackendConfig, schema.BackendConfigKnown)
	}
	writeInspectionFile(t, dir, "main.tf", "terraform {\n backend \"local\" { path = var.discarded }\n}\n")
	writeInspectionFile(t, dir, "main.tofu", cloud)
	if _, err := Inspect(dir, UnknownFiles); err != nil {
		t.Fatalf("both modes select cloud after overrides: %v", err)
	}
}

func TestInspect_BackendOverrideReplacesCloud(t *testing.T) {
	dir := t.TempDir()
	local := "terraform {\n backend \"local\" { path = \"effective.tfstate\" }\n}\n"
	writeInspectionFile(t, dir, "main.tf", "terraform {\n cloud { organization = \"discarded-terraform\" }\n}\n")
	writeInspectionFile(t, dir, "main.tofu", "terraform {\n cloud { organization = \"discarded-tofu\" }\n}\n")
	writeInspectionFile(t, dir, "override.tf", local)
	schema, err := Inspect(dir, OpenTofuFiles)
	if err != nil {
		t.Fatal(err)
	}
	if schema.Backend != "local" || schema.BackendConfig["path"] != "effective.tfstate" || !schema.BackendConfigKnown {
		t.Fatalf("effective backend = %q %v known=%v, want replacement local backend", schema.Backend, schema.BackendConfig, schema.BackendConfigKnown)
	}
	if _, err := Inspect(dir, UnknownFiles); err != nil {
		t.Fatalf("discarded cloud declarations must not make identical effective backends ambiguous: %v", err)
	}
}

func TestInspect_UnknownRuntimeRejectsOppositeBackendOverrides(t *testing.T) {
	dir := t.TempDir()
	local := "terraform {\n backend \"local\" {}\n}\n"
	cloud := "terraform {\n cloud { organization = \"offline-fixture\" }\n}\n"
	writeInspectionFile(t, dir, "main.tf", local)
	writeInspectionFile(t, dir, "override.tf", cloud)
	writeInspectionFile(t, dir, "main.tofu", cloud)
	writeInspectionFile(t, dir, "override.tofu", local)
	_, err := Inspect(dir, UnknownFiles)
	if err == nil || !strings.Contains(err.Error(), "runtime binary is ambiguous") {
		t.Fatalf("opposite effective backend error = %v, want ambiguity diagnostic", err)
	}
}
