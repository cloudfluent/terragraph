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

// TestParseFile_ContractBlocksRoundTrip proves the grammar round-trips from an ordinary blueprint file: attributes, pointer tri-states, and scope resolution against the file's own directory — the same base a node source in that file resolves against.
func TestParseFile_ContractBlocksRoundTrip(t *testing.T) {
	base := t.TempDir()
	writeContractsFile(t, filepath.Join(base, "blueprint.hcl"), `
producer "./modules/vpc" {
  output "vpc_id" {
    type      = "string"
    nullable  = false
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
	bp, err := ParseFile(filepath.Join(base, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if bp.Contracts == nil {
		t.Fatal("producer/consumer blocks did not produce Blueprint.Contracts")
	}
	dc := bp.Contracts.Lookup(filepath.Join(base, "modules", "vpc"))
	if dc == nil {
		t.Fatalf("no contracts resolved for %s", filepath.Join(base, "modules", "vpc"))
	}
	if dc.Scope != "./modules/vpc" {
		t.Fatalf("got scope %q, want the as-written spelling", dc.Scope)
	}
	p := dc.Producer["vpc_id"]
	if p.Type != "string" || p.Nullable == nil || *p.Nullable {
		t.Fatalf("unexpected producer port: %+v", p)
	}
	app := bp.Contracts.Lookup(filepath.Join(base, "modules", "app"))
	if app == nil || app.Consumer["vpc_id"].Type != "string" {
		t.Fatalf("unexpected consumer side: %+v", app)
	}
	if app.Consumer["vpc_id"].Sensitive == nil || !*app.Consumer["vpc_id"].Sensitive {
		t.Fatalf("consumer sensitive must be explicitly true: %+v", app.Consumer["vpc_id"])
	}
}

// TestParseContracts_DuplicatePortAcrossMergedFiles proves directory merge reports the collision naming the second file, instead of one file silently winning the map.
func TestParseContracts_DuplicatePortAcrossMergedFiles(t *testing.T) {
	base := t.TempDir()
	writeContractsFile(t, filepath.Join(base, "a.hcl"), `
producer "./modules/vpc" {
  output "vpc_id" { type = "string" }
}
`)
	writeContractsFile(t, filepath.Join(base, "b.hcl"), `
producer "./modules/vpc" {
  output "vpc_id" { type = "string" }
}
`)
	_, err := ParseDir(base)
	if err == nil || !strings.Contains(err.Error(), "b.hcl") || !strings.Contains(err.Error(), "vpc_id") || !strings.Contains(err.Error(), "remove one") {
		t.Fatalf("got = %v, want duplicate-port error naming the second file, the port, and the remedy", err)
	}
}

// TestParseContracts_RemoteScopeAccepted proves a contract may target a remote module source: it is keyed by the declared source string as written, since there is no local directory to resolve until R6 aligns graph lookup.
func TestParseContracts_RemoteScopeAccepted(t *testing.T) {
	base := t.TempDir()
	writeContractsFile(t, filepath.Join(base, "blueprint.hcl"), `
producer "github.com/org/repo//modules/vpc" {
  output "vpc_id" { type = "string" }
}
`)
	bp, err := ParseFile(filepath.Join(base, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	dc := bp.Contracts.Lookup("github.com/org/repo//modules/vpc")
	if dc == nil || dc.Producer["vpc_id"].Type != "string" {
		t.Fatalf("remote scope must parse and key by the source string: %+v", dc)
	}
	if dc.Scope != "github.com/org/repo//modules/vpc" || dc.Dir != "github.com/org/repo//modules/vpc" {
		t.Fatalf("remote scope spelling must be preserved verbatim: %+v", dc)
	}
}

// TestParseContracts_RejectsRemovedGrammar proves stability and assert, removed with the evidence layer they existed to serve, are unsupported rather than silently ignored: a stale file must fail loudly at parse, not quietly weaken into a contract that checks nothing.
func TestParseContracts_RejectsRemovedGrammar(t *testing.T) {
	base := t.TempDir()
	writeContractsFile(t, filepath.Join(base, "blueprint.hcl"), `
producer "./modules/vpc" {
  output "vpc_id" { stability = "stable" }
}
`)
	if _, err := ParseFile(filepath.Join(base, "blueprint.hcl")); err == nil || !strings.Contains(err.Error(), "Unsupported argument") || !strings.Contains(err.Error(), "stability") {
		t.Fatalf("got = %v, want unsupported-argument error for stability", err)
	}
	writeContractsFile(t, filepath.Join(base, "blueprint.hcl"), `
producer "./modules/vpc" {
  output "vpc_id" {
    assert { nonempty = true }
  }
}
`)
	if _, err := ParseFile(filepath.Join(base, "blueprint.hcl")); err == nil || !strings.Contains(err.Error(), "Unsupported block type") || !strings.Contains(err.Error(), "assert") {
		t.Fatalf("got = %v, want unsupported-block error for assert", err)
	}
}

// TestParseContracts_RejectsAbsoluteScope proves path-shape validation fails at parse time, where the file and range are known, not later at graph time: an absolute filesystem path pins a contract to one machine's layout.
func TestParseContracts_RejectsAbsoluteScope(t *testing.T) {
	base := t.TempDir()
	writeContractsFile(t, filepath.Join(base, "blueprint.hcl"), `
producer "/abs/modules/vpc" {
  output "vpc_id" { type = "string" }
}
`)
	if _, err := ParseFile(filepath.Join(base, "blueprint.hcl")); err == nil || !strings.Contains(err.Error(), "relative") {
		t.Fatalf("got = %v, want absolute-scope rejection", err)
	}
}

// TestParseContracts_RejectsUnparseableType proves a type attribute that is not a Terraform type constraint dies at parse, so a typo never reaches the cty comparison as a mystery string.
func TestParseContracts_RejectsUnparseableType(t *testing.T) {
	base := t.TempDir()
	writeContractsFile(t, filepath.Join(base, "blueprint.hcl"), `
producer "./modules/vpc" {
  output "vpc_id" { type = "strin" }
}
`)
	if _, err := ParseFile(filepath.Join(base, "blueprint.hcl")); err == nil || !strings.Contains(err.Error(), "type") {
		t.Fatalf("got = %v, want type-constraint error", err)
	}
}

// TestParseContracts_DigestStableAcrossBlockOrder proves reordering blocks in the file does not change identity, the property grants and evidence will rely on.
func TestParseContracts_DigestStableAcrossBlockOrder(t *testing.T) {
	base := t.TempDir()
	writeContractsFile(t, filepath.Join(base, "blueprint.hcl"), `
producer "./modules/vpc" {
  output "a" { type = "string" }
  output "b" { type = "number" }
}
consumer "./modules/app" {
  input "a" { type = "string" }
}
`)
	first, err := ParseFile(filepath.Join(base, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	writeContractsFile(t, filepath.Join(base, "blueprint.hcl"), `
consumer "./modules/app" {
  input "a" { type = "string" }
}
producer "./modules/vpc" {
  output "b" { type = "number" }
  output "a" { type = "string" }
}
`)
	second, err := ParseFile(filepath.Join(base, "blueprint.hcl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	firstDigest, err := first.Contracts.Digest()
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	secondDigest, err := second.Contracts.Digest()
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("block order changed the digest: %s vs %s", firstDigest, secondDigest)
	}
}

// TestParseContracts_RejectsWronglyTypedAttributes proves wrong-typed literals die as parse errors, not panics: cty accessors panic on mismatch and a parser panic takes down every graph-loading command.
func TestParseContracts_RejectsWronglyTypedAttributes(t *testing.T) {
	base := t.TempDir()
	for name, hcl := range map[string]string{
		"type":     "producer \"./m\" {\n  output \"id\" {\n    type = 5\n  }\n}\n",
		"nullable": "producer \"./m\" {\n  output \"id\" {\n    nullable = \"yes\"\n  }\n}\n",
	} {
		writeContractsFile(t, filepath.Join(base, "blueprint.hcl"), hcl)
		_, err := ParseFile(filepath.Join(base, "blueprint.hcl"))
		if err == nil || !strings.Contains(err.Error(), "must be") {
			t.Fatalf("%s: got = %v, want a typed-value parse error", name, err)
		}
	}
}

// TestParseContracts_RejectsUnknownNames proves a typo cannot silently weaken a contract: unknown attributes and blocks are parse errors, matching what the language server underlines.
func TestParseContracts_RejectsUnknownNames(t *testing.T) {
	base := t.TempDir()
	writeContractsFile(t, filepath.Join(base, "blueprint.hcl"), `
producer "./m" {
  output "id" {
    nulleable = false
  }
}
`)
	if _, err := ParseFile(filepath.Join(base, "blueprint.hcl")); err == nil || !strings.Contains(err.Error(), "Unsupported argument") {
		t.Fatalf("got = %v, want unsupported-argument error for nulleable", err)
	}
	writeContractsFile(t, filepath.Join(base, "blueprint.hcl"), `
producter "./m" {
  output "id" { type = "string" }
}
`)
	if _, err := ParseFile(filepath.Join(base, "blueprint.hcl")); err == nil || !strings.Contains(err.Error(), "Unsupported block type") {
		t.Fatalf("got = %v, want unsupported-block error for producter", err)
	}
}

// TestParseContracts_RejectsRoleMismatchedPorts proves an input block inside a producer (an easy slip) is refused with the remedy instead of being misfiled as an output guarantee that later surfaces as a misleading C001.
func TestParseContracts_RejectsRoleMismatchedPorts(t *testing.T) {
	base := t.TempDir()
	writeContractsFile(t, filepath.Join(base, "blueprint.hcl"), `
producer "./m" {
  input "vpc_id" {
    type = "string"
  }
}
`)
	_, err := ParseFile(filepath.Join(base, "blueprint.hcl"))
	if err == nil || !strings.Contains(err.Error(), "input blocks belong in a consumer block") {
		t.Fatalf("got = %v, want role-mismatch error naming the right owner", err)
	}
}

// TestParseGroupBlock_CarriesContracts proves the case the reserved filename could never express: producer/consumer blocks inside a group body parse into Group.Contracts, scoped to the group definition file's own directory — the same base the group's internal node sources resolve against.
func TestParseGroupBlock_CarriesContracts(t *testing.T) {
	base := t.TempDir()
	writeContractsFile(t, filepath.Join(base, "group.hcl"), `
group "net" {
  node "vpc" { source = "./vpc" }
  producer "./vpc" {
    output "cidr" { type = "string" }
  }
  consumer "./peer" {
    input "cidr" { type = "string" }
  }
}
`)
	bp, err := ParseFile(filepath.Join(base, "group.hcl"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(bp.Groups) != 1 {
		t.Fatalf("got %d groups, want 1", len(bp.Groups))
	}
	g := bp.Groups[0]
	if g.Contracts == nil {
		t.Fatal("group body producer/consumer did not produce Group.Contracts")
	}
	if g.Contracts.Lookup(filepath.Join(base, "vpc")) == nil {
		t.Fatalf("group contract scope must resolve against the group file's dir: %+v", g.Contracts)
	}
	if g.Contracts.Lookup(filepath.Join(base, "peer")) == nil {
		t.Fatalf("group consumer side missing: %+v", g.Contracts)
	}
}

// TestParseContracts_RejectsAbsoluteScopeOnEveryPlatform pins the rejection to the path's shape rather than the host's. filepath.IsAbs answers only for the running platform, so a POSIX-rooted scope sailed through on Windows and a drive-lettered one sails through everywhere else; either way one blueprint would mean different things on two machines, which is what this check exists to stop. Backslashes are doubled on the way into HCL, where a lone one is an escape selector.
func TestParseContracts_RejectsAbsoluteScopeOnEveryPlatform(t *testing.T) {
	for _, scope := range []string{"/abs/modules/vpc", `\abs\modules\vpc`, `C:\modules\vpc`, "c:modules/vpc"} {
		base := t.TempDir()
		path := filepath.Join(base, "blueprint.hcl")
		writeContractsFile(t, path, `
producer "`+strings.ReplaceAll(scope, `\`, `\\`)+`" {
  output "vpc_id" { type = "string" }
}
`)
		_, err := ParseFile(path)
		if err == nil || !strings.Contains(err.Error(), "relative") {
			t.Fatalf("scope %q: got = %v, want absolute-scope rejection", scope, err)
		}
	}
}
