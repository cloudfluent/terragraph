package graph

import (
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

// contractKey is the string a node's contracts are looked up by: local scopes key by resolved directory, remote scopes by the declared source string verbatim. A remote node's Dir is vendor/<node-name>, so two instances of one remote module resolve to two directories — keying those by directory would demand a duplicate contract exactly where reuse matters most, and a per-node vendored path can never match the source-spelled scope parse produced.
func contractKey(n *Node) string {
	if blueprint.IsRemote(n.Source) {
		return n.Source
	}
	return n.Dir
}

// contractProblems reports every contract violation with a stable [C0xx] code (docs/contracts.md is the table of record). Severity is the blueprint's mode dial: warn (the default when no mode is set) keeps every code advisory, enforce escalates all of them to errors — the only way strictness can enter is reviewed blueprint configuration, never a default.
func contractProblems(g *Graph) []Problem {
	if g.Contracts == nil {
		return nil
	}
	// Nodes are grouped by contract key and sorted by name so the schema a contract is judged against never depends on map iteration order. It matters only for remote sources: nodes sharing a local directory share one cached *module.Schema, but two instances of one remote module live in vendor/<node-name> and are inspected separately, so re-vendoring a single node (vendor --node) can leave the two copies disagreeing. Picking by name makes the answer stable; the second copy still goes uninspected, which is a gap worth its own diagnostic rather than a silent tiebreak.
	byKey := map[string][]*Node{}
	for _, n := range g.Nodes {
		key := contractKey(n)
		byKey[key] = append(byKey[key], n)
	}
	for _, nodes := range byKey {
		sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	}

	// Enforce is the only mode that blocks, and it exists only as reviewed blueprint configuration — never as a default or a CLI flag someone passes once.
	severity := SeverityWarning
	if g.ContractMode == "enforce" {
		severity = SeverityError
	}

	var problems []Problem
	report := func(format string, args ...any) {
		problems = append(problems, Problem{Severity: severity, Message: fmt.Sprintf(format, args...)})
	}

	// C001/C002/C006: contracts against reality, independent of edges — a promise about a port the module never declared, or a scope nothing instantiates, is wrong whether or not anything consumes it yet.
	for _, dc := range sortedContracts(g.Contracts) {
		owners := byKey[dc.Dir]
		if len(owners) == 0 {
			report("contract.[C006] %s: no node in this graph uses source %q; update the scope path or remove the contract", dc.Scope, dc.Scope)
			continue
		}
		schemaOwner := owners[0]
		for _, name := range sortedPorts(dc.Producer) {
			if !schemaOwner.Schema.HasOutput(name) {
				report("contract.[C001] producer %s.output.%s: module declares no such output; remove the promise or add the output", dc.Scope, name)
				continue
			}
			// C009: the module's own sensitive flag is a fact Terraform already declares; a producer claiming the opposite is wrong about its own module. Only an explicit claim can contradict (absent stays no-claim).
			if p := dc.Producer[name]; p.Sensitive != nil && *p.Sensitive != schemaOwner.Schema.OutputDetails[name].Sensitive {
				report("contract.[C009] producer %s.output.%s claims sensitive = %t but the module declares sensitive = %t; fix the contract — the module is the declaration of record", dc.Scope, name, *p.Sensitive, schemaOwner.Schema.OutputDetails[name].Sensitive)
			}
		}
		for _, name := range sortedPorts(dc.Consumer) {
			if !schemaOwner.Schema.HasVariable(name) {
				report("contract.[C002] consumer %s.input.%s: module declares no such variable; remove the requirement or add the variable", dc.Scope, name)
				continue
			}
			v := schemaOwner.Schema.Variables[name]
			c := dc.Consumer[name]
			// C007: a variable's declared type constraint is the record; a consumer claiming a different type contradicts its own module. Both sides parsed (not string-compared) so spellings that normalize to one cty type agree; a variable with no constraint has nothing to contradict.
			if c.Type != "" && v.Type != "" {
				ct, err := parseCtyType(c.Type)
				if err != nil {
					report("contract.[C007] consumer %s.input.%s: %v", dc.Scope, name, err)
					continue
				}
				mt, err := parseCtyType(v.Type)
				if err != nil {
					report("contract.[C007] consumer %s.input.%s: module type %v", dc.Scope, name, err)
					continue
				}
				if !ct.Equals(mt) {
					report("contract.[C007] consumer %s.input.%s claims type %s but the module declares %s; fix the contract — the module is the declaration of record", dc.Scope, name, c.Type, v.Type)
				}
			}
			// C008: the input-side twin of C009 — explicit sensitive claim, either direction, against the variable's declared flag.
			if c.Sensitive != nil && *c.Sensitive != v.Sensitive {
				report("contract.[C008] consumer %s.input.%s claims sensitive = %t but the module declares sensitive = %t; fix the contract — the module is the declaration of record", dc.Scope, name, *c.Sensitive, v.Sensitive)
			}
		}
	}

	// C003/C004/C005: edge-level, producer guarantee vs consumer requirement. Uncontracted endpoints on either side are the migration path and check nothing.
	for _, e := range g.Edges {
		if !e.IsDataEdge() {
			continue
		}
		from := g.Contracts.Lookup(contractKey(g.Nodes[e.From.Node]))
		to := g.Contracts.Lookup(contractKey(g.Nodes[e.To.Node]))
		if from == nil || to == nil {
			continue
		}
		// Port-level, not just dir-level: a dir may be contracted on one side of this edge only, and a map miss yields a zero PortContract whose nil flags would speak for a port nobody promised (the phase-1 rule: uncontracted endpoints check nothing).
		p, okP := from.Producer[e.From.Name]
		c, okC := to.Consumer[e.To.Name]
		if !okP || !okC {
			continue
		}
		if p.Type != "" && c.Type != "" {
			pt, err := parseCtyType(p.Type)
			if err != nil {
				report("contract.[C003] producer %s.output.%s: %v", from.Scope, e.From.Name, err)
				continue
			}
			ct, err := parseCtyType(c.Type)
			if err != nil {
				report("contract.[C003] consumer %s.input.%s: %v", to.Scope, e.To.Name, err)
				continue
			}
			if !pt.Equals(ct) && convert.GetConversionUnsafe(pt, ct) == nil {
				report("contract.[C003] producer %s.output.%s (%s) -> consumer %s.input.%s (%s): types are not convertible; change one side", from.Scope, e.From.Name, p.Type, to.Scope, e.To.Name, c.Type)
			}
		}
		// Absent producer nullable means "may be null" (the lenient claim), so an explicit non-null requirement is violated by it; absent consumer nullable accepts null and can never be violated by nullability.
		if c.Nullable != nil && !*c.Nullable && (p.Nullable == nil || *p.Nullable) {
			report("contract.[C004] producer %s.output.%s may be null but consumer %s.input.%s requires non-null; the producer must promise nullable = false", from.Scope, e.From.Name, to.Scope, e.To.Name)
		}
		if p.Sensitive != nil && *p.Sensitive && (c.Sensitive == nil || !*c.Sensitive) {
			report("contract.[C005] producer %s.output.%s is sensitive but consumer %s.input.%s does not accept sensitive values; set sensitive = true on the consumer or stop marking the output", from.Scope, e.From.Name, to.Scope, e.To.Name)
		}
	}
	return problems
}

// parseCtyType is re-parsed here rather than cached because it runs once per contracted edge at validate time, not per run of terraform; blueprint's parse-time check already rejected unparseable strings, so an error here means the contract set changed underneath us.
func parseCtyType(s string) (cty.Type, error) {
	expr, diags := hclsyntax.ParseExpression([]byte(s), "<type constraint>", hcl.InitialPos)
	if diags.HasErrors() {
		return cty.Type{}, fmt.Errorf("type %q does not parse: %s", s, diags.Error())
	}
	t, diags := typeexpr.TypeConstraint(expr)
	if diags.HasErrors() {
		return cty.Type{}, fmt.Errorf("type %q does not parse: %s", s, diags.Error())
	}
	return t, nil
}

// sortedContracts / sortedPorts exist so problem order — and therefore test output and JSON — never depends on map iteration.
//
// Scope alone does not order the set: entries are keyed by Dir, and two of them can share a Scope spelling while resolving to different directories (the root blueprint and a group each writing "./modules/vpc" relative to their own file). Dir breaks that tie, because leaving it to the sort's stability hands the order straight back to the map this function exists to sort away from.
func sortedContracts(c *blueprint.Contracts) []*blueprint.DirContracts {
	out := make([]*blueprint.DirContracts, 0, len(c.ByDir))
	for _, dc := range c.ByDir {
		out = append(out, dc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		return out[i].Dir < out[j].Dir
	})
	return out
}

func sortedPorts(m map[string]blueprint.PortContract) []string {
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
