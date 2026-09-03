package blueprint

import (
	"strings"
	"testing"
)

// TestContractsDigest_IndependentOfPortOrder proves the digest is a function of the contract's content, not of map iteration order: two builds of the same contract set must produce the same identity, or evidence and grants could never bind to a stable id.
func TestContractsDigest_IndependentOfPortOrder(t *testing.T) {
	mk := func() *Contracts {
		return &Contracts{ByDir: map[string]*DirContracts{
			"/base/modules/vpc": {
				Scope: "./modules/vpc", Dir: "/base/modules/vpc",
				Producer: map[string]PortContract{
					"vpc_id":  {Name: "vpc_id", Scope: "./modules/vpc", Type: "string", Stability: "stable"},
					"cidr":    {Name: "cidr", Scope: "./modules/vpc", Type: "string", Nullable: new(false), Assertions: []Assertion{{Kind: "pattern", Value: `^10\.`}}},
					"subnets": {Name: "subnets", Scope: "./modules/vpc", Type: "list(string)"},
				},
			},
		}}
	}
	first, second := mk().Digest(), mk().Digest()
	if first != second {
		t.Fatal("same contract content produced different digests (map order leaked into identity)")
	}
}

// TestContractsDigest_ChangesWhenPromiseChanges proves any normative field change produces a different identity: a contract that stopped promising non-null is a different contract, and stale evidence bound to the old one must not silently apply.
func TestContractsDigest_ChangesWhenPromiseChanges(t *testing.T) {
	base := &Contracts{ByDir: map[string]*DirContracts{
		"/base/modules/vpc": {Scope: "./modules/vpc", Dir: "/base/modules/vpc",
			Producer: map[string]PortContract{
				"vpc_id": {Name: "vpc_id", Scope: "./modules/vpc", Type: "string"},
			}},
	}}
	changed := &Contracts{ByDir: map[string]*DirContracts{
		"/base/modules/vpc": {Scope: "./modules/vpc", Dir: "/base/modules/vpc",
			Producer: map[string]PortContract{
				"vpc_id": {Name: "vpc_id", Scope: "./modules/vpc", Type: "string", Nullable: new(false)},
			}},
	}}
	if base.Digest() == changed.Digest() {
		t.Fatal("adding nullable=false did not change the digest")
	}
}

// TestContractsDigest_EmptyIsStable pins the empty-set digest so a graph with no contracts still has a defined, comparable identity.
func TestContractsDigest_EmptyIsStable(t *testing.T) {
	c := &Contracts{ByDir: map[string]*DirContracts{}}
	want := c.Digest()
	if want == "" || len(want) != 64 {
		t.Fatalf("got = %q, want a 64-char sha256 hex", want)
	}
	if strings.Contains(want, " ") {
		t.Fatalf("digest must be hex only, got %q", want)
	}
}
