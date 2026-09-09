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
	if isAbsoluteAnywhere(scope) {
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
		if attr.Name == "type" {
			expr := attr.Expr
			val, vd := expr.Value(nil)
			if !vd.HasErrors() && val.IsKnown() && !val.IsNull() && val.Type() == cty.String {
				var pd hcl.Diagnostics
				expr, pd = hclsyntax.ParseExpression([]byte(val.AsString()), attr.Expr.Range().Filename, attr.Expr.Range().Start)
				if pd.HasErrors() {
					return PortContract{}, fmt.Errorf("contract.%s.%s.%s: type must be a Terraform type constraint: %s", role, kind, name, pd.Error())
				}
			}
			typ, td := typeexpr.TypeConstraint(expr)
			if td.HasErrors() {
				return PortContract{}, fmt.Errorf("contract.%s.%s.%s: type must be a Terraform type constraint: %s", role, kind, name, td.Error())
			}
			pc.Type = constraintString(typ)
			continue
		}
		val, diags := attr.Expr.Value(nil)
		if diags.HasErrors() {
			return PortContract{}, fmt.Errorf("%s: %s", port.DefRange, diags.Error())
		}
		switch attr.Name {
		case "nullable", "sensitive":
			if val.IsNull() || !val.IsKnown() || val.Type() != cty.Bool {
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

// isAbsoluteAnywhere reports whether scope reads as an absolute filesystem path on any host, not just this one. filepath.IsAbs answers for the running platform: "/abs/modules/vpc" is absolute on Unix but not on Windows, and "C:\modules\vpc" the reverse. Deferring to it would let a blueprint be rejected on one machine and silently accepted as a remote module source on another, which is exactly the machine-dependent contract identity the check exists to prevent.
func isAbsoluteAnywhere(scope string) bool {
	if filepath.IsAbs(scope) || strings.HasPrefix(scope, "/") || strings.HasPrefix(scope, `\`) {
		return true
	}
	// A Windows volume name ("C:", "C:\x", "C:x"): absolute or drive-relative, never a portable module source, and invisible to a Unix filepath.IsAbs.
	return len(scope) >= 2 && scope[1] == ':' &&
		((scope[0] >= 'a' && scope[0] <= 'z') || (scope[0] >= 'A' && scope[0] <= 'Z'))
}
