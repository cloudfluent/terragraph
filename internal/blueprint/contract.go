package blueprint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// PortContract is one port's half of a contract: a producer's guarantee about an output, or a consumer's requirement for an input. Nullable/Sensitive are pointers because absence is a claim too — an absent nullable on a producer means "may be null", the lenient default the compatibility rules in docs/contracts.md build on — and a bare bool cannot tell "explicitly false" from "never said".
type PortContract struct {
	Name      string
	Scope     string // source path as written in the blueprint ("./modules/vpc", "github.com/org/repo//modules/vpc"); the identity-relevant spelling, never absolutized
	Type      string // Terraform type-constraint syntax; "" means unconstrained
	Nullable  *bool
	Sensitive *bool
}

// DirContracts is every contract sharing one module source. Scope is the human-spelled path used in messages and identity; Dir is the key graph nodes match against — the absolute, cleaned directory for local scopes ("./x", "../x"), or the declared source string verbatim for remote scopes, since a remote node's vendored directory is per-instance (vendor/<node-name>) while the contract belongs to the source everything vendored from.
type DirContracts struct {
	Scope    string
	Dir      string
	Producer map[string]PortContract // by output name
	Consumer map[string]PortContract // by input variable name
}

// Contracts is the whole contract set of one blueprint scope, keyed by resolved source directory so lookup at graph time is one map access per edge endpoint.
type Contracts struct {
	ByDir map[string]*DirContracts
}

// canonicalPort is the JSON-facing shape digest runs over. Pointers with omitempty keep "explicitly false" and "absent" distinct, which is the whole point of the pointer fields on PortContract.
type canonicalPort struct {
	Scope     string `json:"scope"`
	Port      string `json:"port"`
	Type      string `json:"type,omitempty"`
	Nullable  *bool  `json:"nullable,omitempty"`
	Sensitive *bool  `json:"sensitive,omitempty"`
}

type canonicalEntry struct {
	Role string        `json:"role"` // "producer" | "consumer"
	Port canonicalPort `json:"port"`
}

// Digest is the contract set's identity: sha256 hex over a canonical JSON form covering exactly the checked claims — scope, role, port, type, nullable, sensitive — with every port sorted by (scope, role, name). Entries are collected and sorted rather than marshalled straight from the maps so iteration order can never leak into identity.
//
// Nothing calls this yet. It is kept, rather than deleted and rewritten later, because the resume condition the run journal needs — "does this incomplete run's contract set still match?" — is exactly this value, and because the property its tests pin (reordering blocks must not change identity, changing any claim must) is easier to keep true continuously than to re-establish. Delete it if that consumer is abandoned.
func (c *Contracts) Digest() (string, error) {
	entries := make([]canonicalEntry, 0)
	for _, dc := range c.ByDir {
		for name, p := range dc.Producer {
			entries = append(entries, canonicalEntry{Role: "producer", Port: canonicalPort{Scope: dc.Scope, Port: name, Type: p.Type, Nullable: p.Nullable, Sensitive: p.Sensitive}})
		}
		for name, p := range dc.Consumer {
			entries = append(entries, canonicalEntry{Role: "consumer", Port: canonicalPort{Scope: dc.Scope, Port: name, Type: p.Type, Nullable: p.Nullable, Sensitive: p.Sensitive}})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Port.Scope != entries[j].Port.Scope {
			return entries[i].Port.Scope < entries[j].Port.Scope
		}
		if entries[i].Role != entries[j].Role {
			return entries[i].Role < entries[j].Role
		}
		return entries[i].Port.Port < entries[j].Port.Port
	})
	data, err := json.Marshal(entries)
	if err != nil {
		return "", fmt.Errorf("contracts digest: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// Lookup returns this directory's contracts, or nil when the source is uncontracted — the ordinary case.
func (c *Contracts) Lookup(dir string) *DirContracts {
	if c == nil {
		return nil
	}
	return c.ByDir[dir]
}
