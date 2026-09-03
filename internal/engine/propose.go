package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

// draftPort is one port's draft line: the name, the type reality reported (empty when nothing constrained it), and the confidence that type came with.
type draftPort struct{ name, typ, confidence string }

// Propose drafts the contracts.hcl entries reality supports but nobody declared yet, from the committed lock — never from a live run, and never onto disk: the draft goes to stdout, and turning it into a contract is a human's reviewed commit (see docs/contracts.md).
func (e *Engine) Propose() (string, error) {
	raw, err := os.ReadFile(filepath.Join(e.BaseDir, LockFileName))
	if err != nil {
		return "", fmt.Errorf("no %s beside the blueprint; run terragraph observe first: %w", LockFileName, err)
	}
	var ev Evidence
	if err := json.Unmarshal(raw, &ev); err != nil {
		return "", fmt.Errorf("parsing %s: %w (run terragraph observe to regenerate it)", LockFileName, err)
	}
	current := (&blueprint.Contracts{}).Digest()
	if e.Graph.Contracts != nil {
		current = e.Graph.Contracts.Digest()
	}
	if ev.ContractsDigest != current {
		return "", fmt.Errorf("%s was generated against a different contract set; run terragraph observe again", LockFileName)
	}

	byScope := map[string]*[2]map[string]bool{} // scope -> [producer-set, consumer-set] of already-contracted ports
	if e.Graph.Contracts != nil {
		for _, dc := range e.Graph.Contracts.ByDir {
			pair := &[2]map[string]bool{{}, {}}
			for name := range dc.Producer {
				pair[0][name] = true
			}
			for name := range dc.Consumer {
				pair[1][name] = true
			}
			byScope[dc.Scope] = pair
		}
	}

	drafts := map[string][2][]draftPort{}
	for _, p := range ev.Ports {
		idx := 0
		if p.Role == "consumer" {
			idx = 1
		}
		if pair, ok := byScope[p.Scope]; ok && pair[idx][p.Port] {
			continue
		}
		entry := drafts[p.Scope]
		entry[idx] = append(entry[idx], draftPort{name: p.Port, typ: p.Type, confidence: p.Confidence})
		drafts[p.Scope] = entry
	}

	scopes := make([]string, 0, len(drafts))
	for scope := range drafts {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)

	var b strings.Builder
	b.WriteString("# Draft contracts from terragraph.lock — review, edit, and commit as contracts.hcl.\n")
	b.WriteString("# terragraph never writes this file for you; observation proposes, review decides.\n")
	for _, scope := range scopes {
		pair := drafts[scope]
		if role := pair[0]; len(role) > 0 {
			fmt.Fprintf(&b, "\nproducer %q {\n", scope)
			for _, p := range role {
				writeDraftPort(&b, "output", p)
			}
			b.WriteString("}\n")
		}
		if cons := pair[1]; len(cons) > 0 {
			fmt.Fprintf(&b, "\nconsumer %q {\n", scope)
			for _, p := range cons {
				writeDraftPort(&b, "input", p)
			}
			b.WriteString("}\n")
		}
	}
	if len(scopes) == 0 {
		b.WriteString("\n# every port already carries a contract; nothing to propose\n")
	}
	return b.String(), nil
}

func writeDraftPort(b *strings.Builder, kind string, p draftPort) {
	fmt.Fprintf(b, "  %s %q {\n", kind, p.name)
	if p.typ != "" {
		fmt.Fprintf(b, "    type = %q # %s\n", p.typ, p.confidence)
	} else {
		fmt.Fprintf(b, "    # type unconstrained (%s)\n", p.confidence)
	}
	b.WriteString("  }\n")
}
