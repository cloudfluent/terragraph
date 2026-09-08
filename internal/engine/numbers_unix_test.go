//go:build !windows

package engine

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

// loadNumberEngine captures the actual tfvars passed to a subprocess, so precision is checked after parsing, input validation, and serialization.
func loadNumberEngine(t *testing.T, blueprintText string) *Engine {
	t.Helper()
	dir := t.TempDir()
	write := func(path, contents string) {
		t.Helper()
		if err := osWriteFile(path, []byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "producer", "main.tf"), `output "payload" { value = {} }`)
	write(filepath.Join(dir, "consumer", "main.tf"), `variable "payload" {
  type = object({
    large = number
    negative = number
    decimal = number
    values = list(number)
    label = string
    enabled = bool
    empty = string
  })
}`)
	fake := filepath.Join(dir, "terraform-fake")
	write(fake, `#!/bin/sh
case "$1" in
  init) mkdir -p "$TF_DATA_DIR" ;;
  plan)
    for arg in "$@"; do
      case "$arg" in
        -var-file=*) cp "${arg#-var-file=}" "$TF_DATA_DIR/varfile-seen" || exit 1 ;;
      esac
    done
    ;;
  output)
    if [ -f output-unavailable ]; then exit 1; fi
    cat output.json || exit 1
    ;;
  *) exit 1 ;;
esac
`)
	if err := os.Chmod(fake, 0o755); err != nil {
		t.Fatal(err)
	}
	e, err := Load(writeBlueprint(t, dir, blueprintText), exec.Binary(fake), &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return e
}

// assertNumberInputs decodes into exact numeric tokens rather than float64, which would hide the rounding this regression guards against.
func assertNumberInputs(t *testing.T, e *Engine) {
	t.Helper()
	var got struct {
		Payload struct {
			Large    json.Number
			Negative json.Number
			Decimal  json.Number
			Values   []json.Number
			Label    string
			Enabled  bool
			Empty    *string
		}
	}
	if err := json.Unmarshal([]byte(varfileSeen(t, e, "b")), &got); err != nil {
		t.Fatalf("decoding received tfvars: %v", err)
	}
	p := got.Payload
	if p.Large != "9007199254740993" || p.Negative != "-9007199254740993" || p.Decimal != "0.1234567890123456789" {
		t.Fatalf("numbers = %s, %s, %s, want 9007199254740993, -9007199254740993, 0.1234567890123456789", p.Large, p.Negative, p.Decimal)
	}
	if len(p.Values) != 3 || p.Values[0] != "0" || p.Values[1] != "3" || p.Values[2] != "9007199254740993" {
		t.Fatalf("values = %v, want [0 3 9007199254740993]", p.Values)
	}
	if p.Label != "9007199254740993" || !p.Enabled || p.Empty != nil {
		t.Fatalf("non-numeric inputs = %+v, want string preserved, true, null", p)
	}
}

func TestPlan_LiteralNumbersReachTerraformExactly(t *testing.T) {
	e := loadNumberEngine(t, `node "b" {
  source = "./consumer"
  vars = {
    payload = {
      large = 9007199254740993
      negative = -9007199254740993
      decimal = 0.1234567890123456789
      values = [0, 3, 9007199254740993]
      label = "9007199254740993"
      enabled = true
      empty = null
    }
  }
}`)
	if _, err := e.Plan(Options{}); err != nil {
		t.Fatalf("Plan: %v", err)
	}
	assertNumberInputs(t, e)
}

// loadNumberEdgeEngine feeds the same mixed payload through a real output subprocess and a typed downstream module.
func loadNumberEdgeEngine(t *testing.T) *Engine {
	t.Helper()
	e := loadNumberEngine(t, `snapshots {}
node "a" { source = "./producer" }
node "b" { source = "./consumer" }
edge {
  from = node.a.output.payload
  to = node.b.input.payload
}`)
	if err := osWriteFile(filepath.Join(e.BaseDir, "producer", "output.json"), []byte(`{"payload":{"sensitive":false,"value":{
  "large":9007199254740993,
  "negative":-9007199254740993,
  "decimal":0.1234567890123456789,
  "values":[0,3,9007199254740993],
  "label":"9007199254740993",
  "enabled":true,
  "empty":null
}}}`)); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestPlan_LiveOutputNumbersReachTerraformExactly(t *testing.T) {
	e := loadNumberEdgeEngine(t)
	if _, err := e.Plan(Options{Node: "b"}); err != nil {
		t.Fatalf("Plan: %v", err)
	}
	assertNumberInputs(t, e)
}

func TestPlan_SnapshotNumbersReachTerraformExactlyAfterReload(t *testing.T) {
	e := loadNumberEdgeEngine(t)
	if _, err := e.Apply(Options{Node: "a"}); err != nil {
		t.Fatalf("Apply upstream: %v", err)
	}
	if err := osWriteFile(filepath.Join(e.BaseDir, "producer", "output-unavailable"), nil); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(filepath.Join(e.BaseDir, "blueprint.hcl"), e.Binary, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Load after upstream apply: %v", err)
	}
	if _, err := reloaded.Plan(Options{Node: "b"}); err != nil {
		t.Fatalf("Plan using snapshot: %v", err)
	}
	assertNumberInputs(t, reloaded)
}

func TestPlan_LiveOutputRejectsTrailingJSON(t *testing.T) {
	e := loadNumberEdgeEngine(t)
	path := filepath.Join(e.BaseDir, "producer", "output.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := osWriteFile(path, append(data, []byte("\n{}")...)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Plan(Options{Node: "b"}); err == nil || !strings.Contains(err.Error(), "expected a single JSON value") {
		t.Fatalf("Plan error = %v, want malformed live output rejected", err)
	}
}

func TestPlan_SnapshotRejectsTrailingJSON(t *testing.T) {
	e := loadNumberEdgeEngine(t)
	if _, err := e.Apply(Options{Node: "a"}); err != nil {
		t.Fatalf("Apply upstream: %v", err)
	}
	if err := osWriteFile(filepath.Join(e.BaseDir, "producer", "output-unavailable"), nil); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.BaseDir, ".terragraph", "outputs", "a.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := osWriteFile(path, append(data, []byte("\n{}")...)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Plan(Options{Node: "b"}); err == nil || !strings.Contains(err.Error(), "output -json") {
		t.Fatalf("Plan error = %v, want original live output failure without corrupt fallback", err)
	}
}
