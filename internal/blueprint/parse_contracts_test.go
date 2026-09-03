package blueprint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeContractsFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// TestParseContracts_TwoSidedGraph proves the full phase-1 grammar round-trips: attributes, pointer tri-states, nested assert predicates, and scope resolution against the contracts file's directory.
func TestParseContracts_TwoSidedGraph(t *testing.T) {
	base := t.TempDir()
	writeContractsFile(t, filepath.Join(base, "contracts.hcl"), `
producer "./modules/vpc" {
  output "vpc_id" {
    type      = "string"
    nullable  = false
    stability = "stable"
    assert {
      nonempty = true
      pattern  = "^vpc-"
    }
  }
}
consumer "./modules/app" {
  input "vpc_id" {
    type      = "string"
    nullable  = false
    sensitive = true
  }
}
`)
	c, dir, err := ParseContracts(filepath.Join(base, "contracts.hcl"))
	if err != nil {
		t.Fatalf("ParseContracts: %v", err)
	}
	if dir != base {
		t.Fatalf("got base dir %q, want %q", dir, base)
	}
	dc := c.Lookup(filepath.Join(base, "modules", "vpc"))
	if dc == nil {
		t.Fatalf("no contracts resolved for %s", filepath.Join(base, "modules", "vpc"))
	}
	p := dc.Producer["vpc_id"]
	if p.Type != "string" || p.Nullable == nil || *p.Nullable {
		t.Fatalf("unexpected producer port: %+v", p)
	}
	if len(p.Assertions) != 2 {
		t.Fatalf("got = %d assertions, want 2", len(p.Assertions))
	}
	cons := dc.Consumer // consumer and producer share one source dir only if the same path is declared both ways; here app is a different dir
	app := c.Lookup(filepath.Join(base, "modules", "app"))
	if app == nil || app.Consumer["vpc_id"].Type != "string" {
		t.Fatalf("unexpected consumer side: %+v / %+v", cons, app)
	}
	if app.Consumer["vpc_id"].Sensitive == nil || !*app.Consumer["vpc_id"].Sensitive {
		t.Fatalf("consumer sensitive must be explicitly true: %+v", app.Consumer["vpc_id"])
	}
}

// TestParseContracts_DuplicatePortAcrossMergedFiles proves directory merge reports the collision with the remedy, instead of one file silently winning the map.
func TestParseContracts_DuplicatePortAcrossMergedFiles(t *testing.T) {
	base := t.TempDir()
	writeContractsFile(t, filepath.Join(base, "a.hcl"), `
producer "./modules/vpc" {
  output "vpc_id" { type = "string" }
}
`)
	writeContractsFile(t, filepath.Join(base, "b.hcl"), `
producer "./modules/vpc" {
  output "vpc_id" { type = "number" }
}
`)
	_, _, err := ParseContracts(base)
	if err == nil || !strings.Contains(err.Error(), "contract.producer.output.vpc_id") || !strings.Contains(err.Error(), "remove one") {
		t.Fatalf("got = %v, want duplicate-port error naming the port and the remedy", err)
	}
}

// TestParseContracts_RejectsBadStabilityAndAbsoluteScope proves enum and path-shape validation fail at parse time, where the file and range are known, not later at graph time.
func TestParseContracts_RejectsBadStabilityAndAbsoluteScope(t *testing.T) {
	base := t.TempDir()
	writeContractsFile(t, filepath.Join(base, "contracts.hcl"), `
producer "./modules/vpc" {
  output "vpc_id" { stability = "sometimes" }
}
`)
	if _, _, err := ParseContracts(filepath.Join(base, "contracts.hcl")); err == nil || !strings.Contains(err.Error(), "stability") {
		t.Fatalf("got = %v, want stability enum error", err)
	}
	writeContractsFile(t, filepath.Join(base, "contracts.hcl"), `
producer "/abs/modules/vpc" {
  output "vpc_id" { type = "string" }
}
`)
	if _, _, err := ParseContracts(filepath.Join(base, "contracts.hcl")); err == nil || !strings.Contains(err.Error(), "relative") {
		t.Fatalf("got = %v, want absolute-scope rejection", err)
	}
}

// TestParseContracts_RejectsUnparseableType proves a type attribute that is not a Terraform type constraint dies at parse, so a typo never reaches the cty comparison as a mystery string.
func TestParseContracts_RejectsUnparseableType(t *testing.T) {
	base := t.TempDir()
	writeContractsFile(t, filepath.Join(base, "contracts.hcl"), `
producer "./modules/vpc" {
  output "vpc_id" { type = "strin" }
}
`)
	if _, _, err := ParseContracts(filepath.Join(base, "contracts.hcl")); err == nil || !strings.Contains(err.Error(), "type") {
		t.Fatalf("got = %v, want type-constraint error", err)
	}
}

// TestParseContracts_DigestStableAcrossBlockOrder proves reordering blocks in the file does not change identity, the property grants and evidence will rely on.
func TestParseContracts_DigestStableAcrossBlockOrder(t *testing.T) {
	base := t.TempDir()
	writeContractsFile(t, filepath.Join(base, "contracts.hcl"), `
producer "./modules/vpc" {
  output "a" { type = "string" }
  output "b" { type = "number" }
}
consumer "./modules/app" {
  input "a" { type = "string" }
}
`)
	first, _, err := ParseContracts(filepath.Join(base, "contracts.hcl"))
	if err != nil {
		t.Fatalf("ParseContracts: %v", err)
	}
	writeContractsFile(t, filepath.Join(base, "contracts.hcl"), `
consumer "./modules/app" {
  input "a" { type = "string" }
}
producer "./modules/vpc" {
  output "b" { type = "number" }
  output "a" { type = "string" }
}
`)
	second, _, err := ParseContracts(filepath.Join(base, "contracts.hcl"))
	if err != nil {
		t.Fatalf("ParseContracts: %v", err)
	}
	if first.Digest() != second.Digest() {
		t.Fatalf("block order changed the digest: %s vs %s", first.Digest(), second.Digest())
	}
}

// TestParseDir_SkipsContractsFile proves a directory blueprint and its sibling contracts.hcl coexist: the reserved filename is not blueprint content, so the merge must skip it instead of dying on its producer/consumer blocks.
func TestParseDir_SkipsContractsFile(t *testing.T) {
	dir := t.TempDir()
	writeContractsFile(t, filepath.Join(dir, "nodes.hcl"), `node "a" { source = "./m" }`)
	writeContractsFile(t, filepath.Join(dir, "contracts.hcl"), `
producer "./m" {
  output "id" { type = "string" }
}
`)
	bp, err := ParseDir(dir)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	if _, ok := bp.NodeByName("a"); !ok {
		t.Fatal("node a missing from merged blueprint")
	}
}

// TestParseContracts_RejectsWronglyTypedAttributes proves wrong-typed literals die as parse errors, not panics: cty accessors panic on mismatch and a parser panic takes down every graph-loading command.
func TestParseContracts_RejectsWronglyTypedAttributes(t *testing.T) {
	base := t.TempDir()
	for name, hcl := range map[string]string{
		"type":       "producer \"./m\" {\n  output \"id\" {\n    type = 5\n  }\n}\n",
		"nullable":   "producer \"./m\" {\n  output \"id\" {\n    nullable = \"yes\"\n  }\n}\n",
		"one_of":     "producer \"./m\" {\n  output \"id\" {\n    assert {\n      one_of = \"nope\"\n    }\n  }\n}\n",
		"min_length": "producer \"./m\" {\n  output \"id\" {\n    assert {\n      min_length = \"x\"\n    }\n  }\n}\n",
	} {
		writeContractsFile(t, filepath.Join(base, "contracts.hcl"), hcl)
		_, _, err := ParseContracts(filepath.Join(base, "contracts.hcl"))
		if err == nil || !strings.Contains(err.Error(), "must be") {
			t.Fatalf("%s: got = %v, want a typed-value parse error", name, err)
		}
	}
}

// TestParseContracts_RejectsUnknownNames proves a typo cannot silently weaken a contract: unknown attributes and blocks are parse errors, matching what the language server underlines.
func TestParseContracts_RejectsUnknownNames(t *testing.T) {
	base := t.TempDir()
	writeContractsFile(t, filepath.Join(base, "contracts.hcl"), `
producer "./m" {
  output "id" {
    nulleable = false
  }
}
`)
	if _, _, err := ParseContracts(filepath.Join(base, "contracts.hcl")); err == nil || !strings.Contains(err.Error(), "Unsupported argument") {
		t.Fatalf("got = %v, want unsupported-argument error for nulleable", err)
	}
	writeContractsFile(t, filepath.Join(base, "contracts.hcl"), `
producter "./m" {
  output "id" { type = "string" }
}
`)
	if _, _, err := ParseContracts(filepath.Join(base, "contracts.hcl")); err == nil || !strings.Contains(err.Error(), "Unsupported block type") {
		t.Fatalf("got = %v, want unsupported-block error for producter", err)
	}
}

// TestParseContracts_RejectsRoleMismatchedPorts proves an input block inside a producer (an easy slip) is refused with the remedy instead of being misfiled as an output guarantee that later surfaces as a misleading C001.
func TestParseContracts_RejectsRoleMismatchedPorts(t *testing.T) {
	base := t.TempDir()
	writeContractsFile(t, filepath.Join(base, "contracts.hcl"), `
producer "./m" {
  input "vpc_id" {
    type = "string"
  }
}
`)
	_, _, err := ParseContracts(filepath.Join(base, "contracts.hcl"))
	if err == nil || !strings.Contains(err.Error(), "input blocks belong in a consumer block") {
		t.Fatalf("got = %v, want role-mismatch error naming the right owner", err)
	}
}
