package blueprint

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/typeexpr"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

var contractsTopSchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "producer", LabelNames: []string{"source"}},
		{Type: "consumer", LabelNames: []string{"source"}},
	},
}

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
		{Name: "stability", Required: false},
	},
	Blocks: []hcl.BlockHeaderSchema{{Type: "assert"}},
}

// assertSchema is a closed vocabulary, one attribute per predicate kind: extending the set is an addition to this schema and the docs, never a syntax change, because digests must stay comparable across releases (see docs/contracts.md).
var assertSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "nonempty", Required: false},
		{Name: "pattern", Required: false},
		{Name: "min_length", Required: false},
		{Name: "one_of", Required: false},
	},
}

// ParseContracts parses path — one file, or a directory whose .hcl files are merged (mirroring LoadPath) — and returns the contract set plus the base directory scopes resolve against. Sibling producer/consumer blocks with the same source collapse into one DirContracts; the same port declared twice across merged files is an error naming the port and the remedy, so a merge can never silently drop a promise.
func ParseContracts(path string) (*Contracts, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", fmt.Errorf("resolving contracts path: %w", err)
	}
	var files []string
	baseDir := filepath.Dir(path)
	if info.IsDir() {
		baseDir = path
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, "", fmt.Errorf("reading contracts directory: %w", err)
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".hcl") {
				files = append(files, filepath.Join(path, e.Name()))
			}
		}
		sort.Strings(files) // merge order is irrelevant to the result, but a stable order makes any future range-carrying error deterministic
	} else {
		files = []string{path}
	}

	c := &Contracts{ByDir: map[string]*DirContracts{}}
	parser := hclparse.NewParser()
	for _, file := range files {
		if err := parseContractsFile(parser, file, baseDir, c); err != nil {
			return nil, "", err
		}
	}
	return c, baseDir, nil
}

func parseContractsFile(parser *hclparse.Parser, file, baseDir string, c *Contracts) error {
	src, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("reading %s: %w", file, err)
	}
	f, diags := parser.ParseHCL(src, file)
	if diags.HasErrors() {
		return fmt.Errorf("parsing %s: %s", file, diags.Error())
	}
	content, diags := f.Body.Content(contractsTopSchema)
	if diags.HasErrors() {
		return fmt.Errorf("parsing %s: %s", file, diags.Error())
	}
	for _, block := range content.Blocks {
		role := block.Type // "producer" | "consumer"
		scope := block.Labels[0]
		if !strings.HasPrefix(scope, "./") && !strings.HasPrefix(scope, "../") {
			return fmt.Errorf("contract.%s.%s: source must be a relative path like \"./modules/vpc\" (as node sources are), got %q", role, scope, scope)
		}
		dir := filepath.Clean(filepath.Join(baseDir, scope))
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
			return fmt.Errorf("parsing %s: %s", file, diags.Error())
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
				return fmt.Errorf("contract.%s.%s: %s blocks belong in a %s block; move %q", role, scope, port.Type, owner, name)
			}
			pc, err := parsePortContract(port, role, kind, scope, name, file)
			if err != nil {
				return err
			}
			if _, dup := ports[name]; dup {
				return fmt.Errorf("contract.%s.%s.%s: declared more than once across %s; remove one", role, portKind, name, baseDir)
			}
			ports[name] = pc
		}
	}
	return nil
}

func parsePortContract(port *hcl.Block, role, kind, scope, name, file string) (PortContract, error) {
	pc := PortContract{Name: name, Scope: scope, Stability: "stable"}
	content, diags := port.Body.Content(portContractSchema)
	if diags.HasErrors() {
		return PortContract{}, fmt.Errorf("parsing %s: %s", file, diags.Error())
	}
	for _, attr := range content.Attributes {
		val, diags := attr.Expr.Value(nil)
		if diags.HasErrors() {
			return PortContract{}, fmt.Errorf("parsing %s: %s", file, diags.Error())
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
		case "stability":
			if val.Type() != cty.String {
				return PortContract{}, attrTypeError(role, kind, name, "stability", val, "a string")
			}
			pc.Stability = val.AsString()
			if pc.Stability != "stable" && pc.Stability != "volatile" {
				return PortContract{}, fmt.Errorf("contract.%s.%s.%s: stability must be \"stable\" or \"volatile\", got %q", role, kind, name, pc.Stability)
			}
		}
	}
	for _, ab := range content.Blocks {
		ac, diags := ab.Body.Content(assertSchema)
		if diags.HasErrors() {
			return PortContract{}, fmt.Errorf("parsing %s: %s", file, diags.Error())
		}
		for _, attr := range ac.Attributes {
			val, diags := attr.Expr.Value(nil)
			if diags.HasErrors() {
				return PortContract{}, fmt.Errorf("parsing %s: %s", file, diags.Error())
			}
			switch attr.Name {
			case "nonempty":
				if val.Type() != cty.Bool {
					return PortContract{}, attrTypeError(role, kind, name, "nonempty", val, "a bool")
				}
				pc.Assertions = append(pc.Assertions, Assertion{Kind: "nonempty", Value: fmt.Sprintf("%t", val.True())})
			case "pattern":
				if val.Type() != cty.String {
					return PortContract{}, attrTypeError(role, kind, name, "pattern", val, "a string")
				}
				pc.Assertions = append(pc.Assertions, Assertion{Kind: "pattern", Value: val.AsString()})
			case "min_length":
				if val.Type() != cty.Number {
					return PortContract{}, attrTypeError(role, kind, name, "min_length", val, "a number")
				}
				pc.Assertions = append(pc.Assertions, Assertion{Kind: "min_length", Value: val.AsBigFloat().String()})
			case "one_of":
				if !val.Type().IsListType() && !val.Type().IsSetType() && !val.Type().IsTupleType() {
					return PortContract{}, attrTypeError(role, kind, name, "one_of", val, "a list of strings")
				}
				parts := make([]string, 0)
				for it := val.ElementIterator(); it.Next(); {
					_, el := it.Element()
					if el.Type() != cty.String {
						return PortContract{}, attrTypeError(role, kind, name, "one_of", el, "a list of strings")
					}
					parts = append(parts, el.AsString())
				}
				sort.Strings(parts) // one_of is a set semantically; sorted spelling keeps the digest independent of listing order
				pc.Assertions = append(pc.Assertions, Assertion{Kind: "one_of", Value: strings.Join(parts, ",")})
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

var _ = cty.String // keeps the cty import honest if typeexpr's signatures shift; delete if lint objects
