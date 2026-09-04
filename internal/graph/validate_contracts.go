package graph

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/cloudfluent/terragraph/internal/blueprint"
)

// contractProblems reports every contract violation with a stable [C0xx] code (docs/contracts.md is the table of record). Severity is the blueprint's mode dial: warn (the default when no mode is set) keeps every code advisory, enforce escalates all of them to errors — the only way strictness can enter is reviewed blueprint configuration, never a default.
func contractProblems(g *Graph) []Problem {
	if g.Contracts == nil {
		return nil
	}
	used := map[string]bool{}
	for _, n := range g.Nodes {
		used[n.Dir] = true
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
		if !used[dc.Dir] {
			report("contract.[C006] %s: no node in this graph uses source %q; update the scope path or remove the contract", dc.Scope, dc.Scope)
			continue
		}
		var schemaOwner *Node
		for _, n := range g.Nodes {
			if n.Dir == dc.Dir {
				schemaOwner = n
				break
			}
		}
		for _, name := range sortedPorts(dc.Producer) {
			if !schemaOwner.Schema.HasOutput(name) {
				report("contract.[C001] producer %s.output.%s: module declares no such output; remove the promise or add the output", dc.Scope, name)
			}
		}
		for _, name := range sortedPorts(dc.Consumer) {
			if !schemaOwner.Schema.HasVariable(name) {
				report("contract.[C002] consumer %s.input.%s: module declares no such variable; remove the requirement or add the variable", dc.Scope, name)
			}
		}
	}

	// C003/C004/C005: edge-level, producer guarantee vs consumer requirement. Uncontracted endpoints on either side are the migration path and check nothing.
	for _, e := range g.Edges {
		if !e.IsDataEdge() {
			continue
		}
		from := g.Contracts.Lookup(g.Nodes[e.From.Node].Dir)
		to := g.Contracts.Lookup(g.Nodes[e.To.Node].Dir)
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
func sortedContracts(c *blueprint.Contracts) []*blueprint.DirContracts {
	out := make([]*blueprint.DirContracts, 0, len(c.ByDir))
	for _, dc := range c.ByDir {
		out = append(out, dc)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Scope < out[j-1].Scope; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func sortedPorts(m map[string]blueprint.PortContract) []string {
	out := make([]string, 0, len(m))
	for name := range m {
		out = append(out, name)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
