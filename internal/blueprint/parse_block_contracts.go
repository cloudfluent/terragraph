package blueprint

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

var contractsSideSchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "output", LabelNames: []string{"name"}},
		{Type: "input", LabelNames: []string{"name"}},
	},
}

var portContractSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "type", Required: false},
		{Name: "nullable", Required: false},
		{Name: "sensitive", Required: false},
	},
}

// parseContractSideBlock parses one `producer`/`consumer` block — a top-level blueprint block, or one inside a group body (see parseGroupBlock) — into c. Local scopes ("./x", "../x") resolve against baseDir, the directory of the file declaring the block, exactly like a node source in that same file; any other non-absolute source is a remote module source kept as written and keyed by the source string itself, which is also the key graph lookup uses for remote nodes (see graph.contractKey) — no filesystem resolution happens for remote scopes. Absolute filesystem paths are rejected: they would pin a contract to one machine's layout. seen detects the same (role, source, port) declared twice across everything merged into c, so a merge can never silently drop a promise.
func parseContractSideBlock(block *hcl.Block, baseDir string, c *Contracts, seen map[string]bool) error {
	role := block.Type // "producer" | "consumer"
	scope := block.Labels[0]
	if scope == "" {
		return fmt.Errorf("%s: contract %s source must not be empty", block.DefRange, role)
	}
	if filepath.IsAbs(scope) {
		return fmt.Errorf("%s: contract.%s.%s: source must be a relative path like \"./modules/vpc\" or a remote module source, got %q", block.DefRange, role, scope, scope)
	}
	dir := scope
	if strings.HasPrefix(scope, "./") || strings.HasPrefix(scope, "../") {
		dir = filepath.Clean(filepath.Join(baseDir, scope))
	}
	dc := c.ByDir[dir]
	if dc == nil {
		dc = &DirContracts{Scope: scope, Dir: dir, Producer: map[string]PortContract{}, Consumer: map[string]PortContract{}}
		c.ByDir[dir] = dc
	}
	ports := dc.Producer
	portKind := "output"
	if role == "consumer" {
		ports = dc.Consumer
		portKind = "input"
	}
	side, diags := block.Body.Content(contractsSideSchema)
	if diags.HasErrors() {
		return fmt.Errorf("%s: %s", block.DefRange, diags.Error())
	}
	for _, port := range side.Blocks {
		name := port.Labels[0]
		kind := "output"
		if role == "consumer" {
			kind = "input"
		}
		if port.Type != kind {
			owner := "consumer"
			if role == "consumer" {
				owner = "producer"
			}
			return fmt.Errorf("%s: contract.%s.%s: %s blocks belong in a %s block; move %q", block.DefRange, role, scope, port.Type, owner, name)
		}
		key := role + " " + dir + " " + name
		if seen[key] {
			return fmt.Errorf("%s: contract.%s.%s.%s declared more than once across merged files; remove one", block.DefRange, role, portKind, name)
		}
		seen[key] = true
		pc, err := parsePortContract(port, role, kind, scope, name)
		if err != nil {
			return err
		}
		ports[name] = pc
	}
	return nil
}

func parsePortContract(port *hcl.Block, role, kind, scope, name string) (PortContract, error) {
	pc := PortContract{Name: name, Scope: scope}
	content, diags := port.Body.Content(portContractSchema)
	if diags.HasErrors() {
		return PortContract{}, fmt.Errorf("%s: %s", port.DefRange, diags.Error())
	}
	for _, attr := range content.Attributes {
		val, diags := attr.Expr.Value(nil)
		if diags.HasErrors() {
			return PortContract{}, fmt.Errorf("%s: %s", port.DefRange, diags.Error())
		}
		switch attr.Name {
		case "type":
			if val.Type() != cty.String {
				return PortContract{}, attrTypeError(role, kind, name, "type", val, "a string")
			}
			pc.Type = val.AsString()
			if err := validateTypeConstraint(pc.Type); err != nil {
				return PortContract{}, fmt.Errorf("contract.%s.%s.%s: %w", role, kind, name, err)
			}
		case "nullable", "sensitive":
			if val.Type() != cty.Bool {
				return PortContract{}, attrTypeError(role, kind, name, attr.Name, val, "a bool")
			}
			if attr.Name == "nullable" {
				pc.Nullable = new(val.True())
			} else {
				pc.Sensitive = new(val.True())
			}
		}
	}
	return pc, nil
}

// attrTypeError guards every typed read in parsePortContract: cty's AsString/True/AsBigFloat panic on a wrong-typed value, and a parser panic takes down every command that loads the graph — a wrong-typed literal must die as a parse error at this trust boundary instead.
func attrTypeError(role, kind, name, attr string, val cty.Value, want string) error {
	return fmt.Errorf("contract.%s.%s.%s: %s must be %s, got %s", role, kind, name, attr, want, val.Type().FriendlyName())
}

// validateTypeConstraint fails fast on a type string that Terraform itself would reject, at parse time where the file and port are known — the same reason node variables' type constraints are checked before any graph exists.
func validateTypeConstraint(s string) error {
	expr, diags := hclsyntax.ParseExpression([]byte(s), "<type constraint>", hcl.InitialPos)
	if diags.HasErrors() {
		return fmt.Errorf("type %q is not a Terraform type constraint: %s", s, diags.Error())
	}
	if _, diags := typeexpr.TypeConstraint(expr); diags.HasErrors() {
		return fmt.Errorf("type %q is not a Terraform type constraint: %s", s, diags.Error())
	}
	return nil
}
