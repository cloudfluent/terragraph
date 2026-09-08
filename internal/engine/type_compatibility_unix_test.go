//go:build !windows

package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

// loadInputTypeEngine uses a real declaration and captures the subprocess's tfvars so accepting a value cannot silently rewrite it.
func loadInputTypeEngine(t *testing.T, constraint, literal string) *Engine {
	t.Helper()
	dir := t.TempDir()
	if err := osWriteFile(filepath.Join(dir, "module", "main.tf"), []byte(fmt.Sprintf("variable \"value\" { type = %s }\n", constraint))); err != nil {
		t.Fatal(err)
	}
	bp := writeBlueprint(t, dir, fmt.Sprintf("node \"a\" {\n source = \"./module\"\n vars = { value = %s }\n}\n", literal))
	binary := filepath.Join(dir, "terraform-fake")
	if err := os.WriteFile(binary, []byte(`#!/bin/sh
case "$1" in
  init) mkdir -p "$TF_DATA_DIR" ;;
  plan)
    for arg in "$@"; do
      case "$arg" in
        -var-file=*) cp "${arg#-var-file=}" "$TF_DATA_DIR/varfile-seen" || exit 1 ;;
      esac
    done
    ;;
  *) exit 1 ;;
esac
`), 0o700); err != nil {
		t.Fatal(err)
	}
	e, err := Load(bp, exec.Binary(binary), &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return e
}

// assertInputTypeAccepted checks the exact encoded input, leaving Terraform responsible for applying defaults, coercions, and object projection.
func assertInputTypeAccepted(t *testing.T, e *Engine) {
	t.Helper()
	want, err := json.Marshal(e.Graph.Nodes["a"].Vars["value"])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Plan(Options{}); err != nil {
		t.Fatalf("Plan: %v", err)
	}
	var vars map[string]json.RawMessage
	if err := json.Unmarshal([]byte(varfileSeen(t, e, "a")), &vars); err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if err := json.Compact(&got, vars["value"]); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("passed value = %s, want original %s", got.Bytes(), want)
	}
}

func TestPlan_AnyInputAcceptsScalar(t *testing.T) {
	assertInputTypeAccepted(t, loadInputTypeEngine(t, "any", `"fixture"`))
}

func TestPlan_AnyInputAcceptsHeterogeneousValues(t *testing.T) {
	assertInputTypeAccepted(t, loadInputTypeEngine(t, "any", `[9007199254740993, "fixture", { enabled = true }, null]`))
}

func TestPlan_MapAnyInputAcceptsConvertibleValues(t *testing.T) {
	assertInputTypeAccepted(t, loadInputTypeEngine(t, "map(any)", `{ count = 9007199254740993, label = "fixture", empty = null }`))
}

func TestPlan_OptionalInputDefaultsAcceptMissingAndNullAttributes(t *testing.T) {
	assertInputTypeAccepted(t, loadInputTypeEngine(t, `object({ missing = optional(string, "default"), empty = optional(number, 2) })`, `{ empty = null }`))
}

func TestPlan_OptionalInputDefaultsAcceptNestedCollections(t *testing.T) {
	constraint := `object({ items = list(object({ name = string, settings = optional(object({ enabled = optional(bool, true) }), {}) })) })`
	assertInputTypeAccepted(t, loadInputTypeEngine(t, constraint, `{ items = [{ name = "fixture" }] }`))
}

func TestPlan_OptionalInputDefaultsPreserveTopLevelNull(t *testing.T) {
	assertInputTypeAccepted(t, loadInputTypeEngine(t, `object({ name = optional(string, "default") })`, `null`))
}

func TestPlan_ObjectInputAcceptsAdditionalAttributes(t *testing.T) {
	assertInputTypeAccepted(t, loadInputTypeEngine(t, `object({ name = string })`, `{ name = "fixture", extra = true }`))
}

func TestPlan_InputAcceptsPrimitiveCoercion(t *testing.T) {
	constraint := `object({ count = number, label = string, enabled = bool })`
	assertInputTypeAccepted(t, loadInputTypeEngine(t, constraint, `{ count = "9007199254740993", label = 12, enabled = "true" }`))
}

func TestPlan_MapAnyInputRejectsIncompatibleValues(t *testing.T) {
	e := loadInputTypeEngine(t, "map(any)", `{ count = 1, details = { enabled = true } }`)
	if _, err := e.Plan(Options{}); err == nil || !strings.Contains(err.Error(), "node.a.input.value") {
		t.Fatalf("error = %v, want incompatible map input rejected", err)
	}
}

func TestPlan_ObjectInputRejectsMissingRequiredAttribute(t *testing.T) {
	e := loadInputTypeEngine(t, `object({ name = string })`, `{}`)
	if _, err := e.Plan(Options{}); err == nil || !strings.Contains(err.Error(), "node.a.input.value") {
		t.Fatalf("error = %v, want missing required attribute rejected", err)
	}
}
