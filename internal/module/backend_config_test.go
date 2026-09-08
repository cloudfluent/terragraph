package module

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspect_BackendLiteralAddress(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
terraform {
  backend "s3" {
    bucket = "example"
    key = "state"
    encrypt = true
  }
}
`), 0600); err != nil {
		t.Fatal(err)
	}
	schema, err := Inspect(dir)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !schema.BackendConfigKnown || schema.BackendConfig["bucket"] != "example" || schema.BackendConfig["key"] != "state" || schema.BackendConfig["encrypt"] != "true" {
		t.Fatalf("got = %+v, want complete scalar backend attributes", schema)
	}
}

func TestInspect_BackendExpressionRemainsUnknown(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
terraform {
  backend "s3" {
    bucket = "example"
    key = var.state_key
  }
}
`), 0600); err != nil {
		t.Fatal(err)
	}
	schema, err := Inspect(dir)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if schema.BackendConfigKnown || schema.BackendConfig["bucket"] != "example" {
		t.Fatalf("got = %+v, want incomplete scalar projection", schema)
	}
	if _, exists := schema.BackendConfig["key"]; exists {
		t.Fatalf("got = %v, want no guessed key", schema.BackendConfig)
	}
}

func TestInspect_JSONBackendLiteralAddress(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf.json"), []byte(`{"terraform":{"backend":{"s3":{"bucket":"example","key":"state"}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	schema, err := Inspect(dir)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if !schema.BackendConfigKnown || schema.BackendConfig["bucket"] != "example" || schema.BackendConfig["key"] != "state" {
		t.Fatalf("got = %+v, want complete JSON backend attributes", schema)
	}
}
