package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDetailFixture builds the nested-group, twice-instantiated-group tree the golden envelope
// and the narrowing/origin tests inspect: two `use "service"` instances of one group whose own
// group body instantiates an inner group, so export mapping paths stay traceable and each
// instance reports its own vars declaration location (#94 acceptance #3).
func writeDetailFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `runtime "pinned" {
  binary  = "terraform"
  version = ">= 1.0"
}

node "vpc" {
  source = "./modules/vpc"
  vars = {
    cidr = "10.0.0.0/16"
  }
}

use "service" {
  as     = "checkout"
  source = "./groups/service"
  vars = {
    cluster_name = "checkout"
  }
}

use "service" {
  as     = "payments"
  source = "./groups/service"
  runtime = runtime.pinned
  vars = {
    cluster_name = "payments"
  }
}

edge {
  from = node.vpc.output.vpc_id
  to   = use.checkout.input.vpc_id
}

edge {
  from = node.vpc.output.vpc_id
  to   = use.payments.input.vpc_id
}
`)
	writeFixtureFile(t, filepath.Join(root, "groups", "service", "group.hcl"), `group "service" {
  node "cluster" {
    source = "../../modules/cluster"
  }

  use "inner" {
    as     = "core"
    source = "../inner"
  }

  edge {
    from = node.cluster.output.cluster_id
    to   = use.core.input.cluster_id
  }

  export {
    input "vpc_id" {
      to = node.cluster.input.vpc_id
    }
    input "cluster_name" {
      to = node.cluster.input.cluster_name
    }
    output "cluster_id" {
      from = node.cluster.output.cluster_id
    }
  }
}
`)
	writeFixtureFile(t, filepath.Join(root, "groups", "inner", "group.hcl"), `group "inner" {
  node "nodegroup" {
    source = "../../modules/nodegroup"
  }

  export {
    input "cluster_id" {
      to = node.nodegroup.input.cluster_id
    }
  }
}
`)
	writeFixtureFile(t, filepath.Join(root, "modules", "vpc", "variables.tf"), `variable "cidr" {
  type = string
}
`)
	writeFixtureFile(t, filepath.Join(root, "modules", "vpc", "outputs.tf"), `output "vpc_id" {
  value = "vpc"
}
`)
	writeFixtureFile(t, filepath.Join(root, "modules", "cluster", "variables.tf"), `variable "vpc_id" {
  type = string
}

variable "cluster_name" {
  type = string
}
`)
	writeFixtureFile(t, filepath.Join(root, "modules", "cluster", "main.tf"), `terraform {
  backend "local" {}
}
`)
	writeFixtureFile(t, filepath.Join(root, "modules", "cluster", "outputs.tf"), `output "cluster_id" {
  value = "cluster"
}
`)
	writeFixtureFile(t, filepath.Join(root, "modules", "nodegroup", "variables.tf"), `variable "cluster_id" {
  type = string
}
`)
	writeFixtureFile(t, filepath.Join(root, "modules", "nodegroup", "main.tf"), `terraform {
  backend "local" {}
}
`)
	return root
}

func runDetailCmd(t *testing.T, root string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return runRootCmd(t, append([]string{"--blueprint", filepath.Join(root, "blueprint.hcl"), "graph", "--detail"}, args...)...)
}

// TestGraphDetail_JSONEnvelopeMatchesGoldenFixture pins the whole envelope for the nested-group,
// twice-instantiated fixture: same bytes across two runs (map-order independence), and against a
// pretty-printed committed golden with the temp root normalized away.
func TestGraphDetail_JSONEnvelopeMatchesGoldenFixture(t *testing.T) {
	root := writeDetailFixture(t)
	first, _, err := runDetailCmd(t, root, "--output", "json")
	if err != nil {
		t.Fatalf("graph --detail --output json: %v", err)
	}
	second, _, err := runDetailCmd(t, root, "--output", "json")
	if err != nil || first != second {
		t.Fatalf("got = %q, %v; want byte-identical rerun of %q", second, err, first)
	}

	golden, rerr := os.ReadFile(filepath.Join("testdata", "graph_detail_golden.json"))
	if rerr != nil {
		t.Fatalf("reading golden: %v", rerr)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte(first), "", "  "); err != nil {
		t.Fatalf("indenting: %v", err)
	}
	if got, want := strings.TrimRight(strings.ReplaceAll(pretty.String(), root, "<root>"), "\n"), strings.TrimRight(string(golden), "\n"); got != want {
		t.Fatalf("golden mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// originsRuntime is the runtime slice of the envelope the origin tests read, named so both the
// decode helper and the structure test can share it.
type originsRuntime struct {
	Binary             string             `json:"binary"`
	Name               *string            `json:"name"`
	Version            *string            `json:"version"`
	Origin             string             `json:"origin"`
	Location           *sourceLocationDTO `json:"location"`
	DefinitionLocation *sourceLocationDTO `json:"definition_location"`
	Use                *struct {
		Name string `json:"name"`
	} `json:"use"`
}

// originsNode is the per-node slice of the envelope the origin tests read; a named struct because
// anonymous ones cannot be handed out of the decode helper.
type originsNode struct {
	Name    string         `json:"name"`
	Runtime originsRuntime `json:"runtime"`
	Approve struct {
		Effective string `json:"effective"`
		Origin    string `json:"origin"`
	} `json:"approve"`
}

// TestGraphDetail_EnvelopeStructureAndProvenance checks the fixture's structural claims the golden
// cannot self-verify: instance-distinct vars declarations, mapping hops surviving to the innermost
// leaf, use-origin runtime carrying the use block reference, and non-null arrays everywhere.
func TestGraphDetail_EnvelopeStructureAndProvenance(t *testing.T) {
	root := writeDetailFixture(t)
	out, _, err := runDetailCmd(t, root, "--output", "json")
	if err != nil {
		t.Fatalf("graph --detail: %v", err)
	}
	var res struct {
		SchemaVersion int                    `json:"schema_version"`
		Valid         bool                   `json:"valid"`
		Selection     struct{ Node *string } `json:"selection"`
		Levels        [][]string             `json:"levels"`
		Nodes         []struct {
			Name   string `json:"name"`
			Inputs []struct {
				Name   string `json:"name"`
				Source struct {
					Kind        string `json:"kind"`
					Subject     string `json:"subject"`
					MappingPath []struct {
						Via string `json:"via"`
					} `json:"mapping_path"`
				} `json:"source"`
			} `json:"inputs"`
			Runtime originsRuntime `json:"runtime"`
		} `json:"nodes"`
		Edges       []json.RawMessage `json:"edges"`
		Diagnostics []json.RawMessage `json:"diagnostics"`
		Limitations []string          `json:"limitations"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parsing envelope: %v (%s)", err, out)
	}
	if res.SchemaVersion != 1 || !res.Valid || res.Selection.Node != nil || len(res.Diagnostics) != 0 {
		t.Fatalf("got = %s", out)
	}
	if strings.Contains(out, `"levels": null`) || len(res.Levels) != 3 {
		t.Fatalf("got levels = %v (%s)", res.Levels, out)
	}
	if got, want := strings.Join(res.Levels[2], ","), "checkout.core.nodegroup,payments.core.nodegroup"; got != want {
		t.Fatalf("got level 3 = %q, want %q", got, want)
	}
	if len(res.Nodes) != 5 || len(res.Edges) != 4 || len(res.Limitations) != 3 {
		t.Fatalf("got %d nodes, %d edges, %d limitations (%s)", len(res.Nodes), len(res.Edges), len(res.Limitations), out)
	}

	inputsByNode := map[string]map[string]string{}
	nodegroupVia := map[string]int{}
	for _, n := range res.Nodes {
		inputsByNode[n.Name] = map[string]string{}
		for _, in := range n.Inputs {
			inputsByNode[n.Name][in.Name] = in.Source.Subject + "|" + in.Source.Kind
			if n.Name == "checkout.core.nodegroup" && in.Name == "cluster_id" {
				for _, m := range in.Source.MappingPath {
					nodegroupVia[m.Via] = len(nodegroupVia)
				}
			}
		}
	}
	// Two instances of one group must report their own vars declaration, not a shared one.
	if got, want := inputsByNode["checkout.cluster"]["cluster_name"], "use.checkout.vars.cluster_name|use_vars"; got != want {
		t.Fatalf("got checkout cluster_name = %q, want %q", got, want)
	}
	if got, want := inputsByNode["payments.cluster"]["cluster_name"], "use.payments.vars.cluster_name|use_vars"; got != want {
		t.Fatalf("got payments cluster_name = %q, want %q", got, want)
	}
	if got, want := inputsByNode["vpc"]["cidr"], "node.vpc.vars.cidr|node_vars"; got != want {
		t.Fatalf("got vpc cidr = %q, want %q", got, want)
	}
	// The nodegroup leaf's supply crossed the outer group's edge into use.core.input.cluster_id.
	if got, want := inputsByNode["checkout.core.nodegroup"]["cluster_id"], "node.checkout.cluster.output.cluster_id|edge"; got != want {
		t.Fatalf("got nodegroup cluster_id = %q, want edge supply %q", got, want)
	}
	if _, ok := nodegroupVia["use.core.input.cluster_id"]; !ok {
		t.Fatalf("got mapping hops = %v, want the use.core export hop preserved", nodegroupVia)
	}
	// The payments use block selects the pinned runtime; origin must name that use, not the node.
	paymentsRuntime := false
	for _, n := range res.Nodes {
		if n.Name == "payments.cluster" {
			paymentsRuntime = n.Runtime.Origin == "use" && n.Runtime.Use != nil && n.Runtime.Use.Name == "payments"
		}
	}
	if !paymentsRuntime {
		t.Fatalf("got = %s, want payments.* runtime at use origin naming the payments use", out)
	}
}

// TestGraphDetail_NodeNarrowsToOneLeafWithIncidentEdges proves --node's scope rule: exactly the
// selected leaf in nodes, only directly incident edges, but full-graph levels and relationships.
func TestGraphDetail_NodeNarrowsToOneLeafWithIncidentEdges(t *testing.T) {
	root := writeDetailFixture(t)
	out, _, err := runDetailCmd(t, root, "--node", "checkout.cluster", "--output", "json")
	if err != nil {
		t.Fatalf("graph --detail --node: %v", err)
	}
	var res struct {
		Selection struct{ Node *string } `json:"selection"`
		Levels    [][]string             `json:"levels"`
		Nodes     []struct {
			Name      string `json:"name"`
			Relations struct {
				DependsOn   []string `json:"depends_on"`
				Descendants []string `json:"descendants"`
			} `json:"relations"`
		} `json:"nodes"`
		Edges []struct {
			From struct{ Node string } `json:"from"`
			To   struct{ Node string } `json:"to"`
		} `json:"edges"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parsing envelope: %v (%s)", err, out)
	}
	if res.Selection.Node == nil || *res.Selection.Node != "checkout.cluster" || len(res.Nodes) != 1 || res.Nodes[0].Name != "checkout.cluster" {
		t.Fatalf("got = %s", out)
	}
	if len(res.Edges) != 2 {
		t.Fatalf("got %d edges, want only the two incident to checkout.cluster: %s", len(res.Edges), out)
	}
	for _, e := range res.Edges {
		if e.From.Node != "checkout.cluster" && e.To.Node != "checkout.cluster" {
			t.Fatalf("got non-incident edge %+v in %s", e, out)
		}
	}
	if len(res.Levels) != 3 || len(res.Levels[0]) != 1 {
		t.Fatalf("got levels = %v, want the full graph's three levels", res.Levels)
	}
	if got, want := strings.Join(res.Nodes[0].Relations.Descendants, ","), "checkout.core.nodegroup"; got != want {
		t.Fatalf("got descendants = %q, want full-graph %q", got, want)
	}

	text, _, err := runDetailCmd(t, root, "--node", "checkout.cluster")
	if err != nil {
		t.Fatalf("graph --detail --node text: %v", err)
	}
	if !strings.Contains(text, "node checkout.cluster") || strings.Contains(text, "node payments.cluster") {
		t.Fatalf("got = %q, want only the checkout.cluster block", text)
	}
}

// TestGraphDetail_GroupPrefixYieldsCandidateLeaves proves a group instance name never silently
// selects the whole group: valid=false, a select-phase diagnostic, matching leaf candidates, and a
// nonzero exit, with full-graph levels still present because the graph itself is constructible.
func TestGraphDetail_GroupPrefixYieldsCandidateLeaves(t *testing.T) {
	root := writeDetailFixture(t)
	out, _, err := runDetailCmd(t, root, "--node", "checkout", "--output", "json")
	if err == nil {
		t.Fatalf("group prefix accepted: %s", out)
	}
	var res struct {
		Valid       bool                   `json:"valid"`
		Selection   struct{ Node *string } `json:"selection"`
		Levels      [][]string             `json:"levels"`
		Nodes       []json.RawMessage      `json:"nodes"`
		Diagnostics []struct {
			Code    string `json:"code"`
			Phase   string `json:"phase"`
			Subject string `json:"subject"`
			Remedy  string `json:"remedy"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parsing envelope: %v (%s)", err, out)
	}
	if res.Valid || res.Selection.Node == nil || *res.Selection.Node != "checkout" || len(res.Nodes) != 0 {
		t.Fatalf("got = %s", out)
	}
	var d struct {
		Code    string `json:"code"`
		Phase   string `json:"phase"`
		Subject string `json:"subject"`
		Remedy  string `json:"remedy"`
	}
	found := false
	for _, cand := range res.Diagnostics {
		if cand.Code == "unknown_node" {
			d, found = cand, true
		}
	}
	if !found {
		t.Fatalf("got diagnostics = %+v, want an unknown_node selection diagnostic", res.Diagnostics)
	}
	if d.Phase != "select" || d.Subject != "node.checkout" {
		t.Fatalf("got diagnostic = %+v", d)
	}
	for _, leaf := range []string{"checkout.cluster", "checkout.core.nodegroup"} {
		if !strings.Contains(d.Remedy, leaf) {
			t.Fatalf("got remedy = %q, want candidate %q listed", d.Remedy, leaf)
		}
	}
	if len(res.Levels) != 3 {
		t.Fatalf("got levels = %v, want the constructible graph's levels retained", res.Levels)
	}
}

// TestGraphDetail_RepeatedNodeRejected proves v1's one-leaf rule is an argument error (envelope in
// JSON mode), never a silent ignore of the extra selection.
func TestGraphDetail_RepeatedNodeRejected(t *testing.T) {
	root := writeDetailFixture(t)
	out, _, err := runDetailCmd(t, root, "--node", "vpc", "--node", "checkout.cluster", "--output", "json")
	if err == nil {
		t.Fatalf("repeated --node accepted: %s", out)
	}
	if !strings.Contains(out, `"code": "invalid_arguments"`) && !strings.Contains(out, `"code":"invalid_arguments"`) {
		t.Fatalf("got = %s, want an invalid_arguments envelope", out)
	}
	if _, _, err := runDetailCmd(t, root, "--node", "vpc", "--node", "checkout.cluster"); err == nil || !strings.Contains(err.Error(), "exactly one leaf") {
		t.Fatalf("got = %v, want a text-mode argument error", err)
	}
}

// TestGraphDetail_PositionalArgsRejected keeps validateRunArgs' guarantee under --detail: a
// positional target is refused instead of being read as a node selection.
func TestGraphDetail_PositionalArgsRejected(t *testing.T) {
	root := writeDetailFixture(t)
	if _, _, err := runDetailCmd(t, root, "checkout.cluster"); err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
		t.Fatalf("got = %v, want positional rejection", err)
	}
}

// TestGraphDetail_FlagCombinationsRejected covers the option-combination rows: --format dot with
// --detail, --approve without --detail, and --downstream with --detail all fail naming a remedy.
func TestGraphDetail_FlagCombinationsRejected(t *testing.T) {
	root := writeDetailFixture(t)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--format", "dot"}, "--format dot cannot render detailed inspection"},
		{[]string{"--downstream"}, "--downstream selects an execution scope"},
	}
	for _, tc := range cases {
		_, stderr, err := runDetailCmd(t, root, tc.args...)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("args %v: got = %v, %q", tc.args, err, stderr)
		}
	}
	plain, _, err := runRootCmd(t, "--blueprint", filepath.Join(root, "blueprint.hcl"), "graph", "--approve", "all")
	if err == nil || !strings.Contains(err.Error(), "--approve is only valid with --detail") || plain != "" {
		t.Fatalf("got = %q, %v, want an argument error naming the --detail remedy", plain, err)
	}
	// The dot+detail combination still gets the envelope with a remedy when JSON output is possible.
	out, _, err := runDetailCmd(t, root, "--format", "dot", "--output", "json")
	if err == nil || !strings.Contains(out, "invalid_arguments") || !strings.Contains(out, "--format dot cannot render detailed inspection") {
		t.Fatalf("got = %q, %v, want an invalid_arguments envelope carrying the remedy", out, err)
	}
}

// TestGraphDetail_WarningsStayValidAndExitZero proves the warnings-only row: an unresolved required
// variable is reported as a warning diagnostic while valid stays true and the command succeeds.
func TestGraphDetail_WarningsStayValidAndExitZero(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `node "a" { source = "./m" }
`)
	writeFixtureFile(t, filepath.Join(root, "m", "main.tf"), `variable "x" {
  type = string
}

output "id" { value = "x" }
`)
	out, _, err := runDetailCmd(t, root, "--output", "json")
	if err != nil {
		t.Fatalf("warnings-only detail: %v", err)
	}
	var res struct {
		Valid       bool       `json:"valid"`
		Levels      [][]string `json:"levels"`
		Diagnostics []struct {
			Code     string `json:"code"`
			Severity string `json:"severity"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parsing envelope: %v (%s)", err, out)
	}
	if !res.Valid || len(res.Levels) != 1 || len(res.Diagnostics) == 0 || res.Diagnostics[0].Code != "required_input_unwired" || res.Diagnostics[0].Severity != "warning" {
		t.Fatalf("got = %s", out)
	}
}

// TestGraphDetail_ValidationErrorsRetainResult proves the validation-error row: valid=false with a
// nonzero exit, but the constructed nodes, edges, and levels stay in the envelope.
func TestGraphDetail_ValidationErrorsRetainResult(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `node "a" { source = "./m" }
node "b" { source = "./m2" }

edge {
  from = node.a.output.missing
  to   = node.b.input.also_missing
}
`)
	writeFixtureFile(t, filepath.Join(root, "m", "main.tf"), `output "id" { value = "x" }
`)
	writeFixtureFile(t, filepath.Join(root, "m2", "main.tf"), `output "id" { value = "x" }
`)
	out, _, err := runDetailCmd(t, root, "--output", "json")
	if err == nil {
		t.Fatalf("validation errors accepted: %s", out)
	}
	var res struct {
		Valid       bool                    `json:"valid"`
		Levels      [][]string              `json:"levels"`
		Nodes       []struct{ Name string } `json:"nodes"`
		Edges       []json.RawMessage       `json:"edges"`
		Diagnostics []struct{ Code string } `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parsing envelope: %v (%s)", err, out)
	}
	if res.Valid || len(res.Nodes) != 2 || len(res.Edges) != 1 || len(res.Levels) != 2 {
		t.Fatalf("got = %s, want retained nodes/edges/levels with valid=false", out)
	}
	if len(res.Diagnostics) < 2 || res.Diagnostics[0].Code != "missing_input" {
		t.Fatalf("got diagnostics = %+v", res.Diagnostics)
	}
}

// TestGraphDetail_CycleNullsLevelsKeepsReachability proves the cycle row: levels=null (never an
// empty plan), connections and reachability retained, and the node itself excluded from its own
// relationship lists despite sitting on the cycle.
func TestGraphDetail_CycleNullsLevelsKeepsReachability(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `node "a" { source = "./m" }
node "b" { source = "./m2" }

edge {
  from = node.a
  to   = node.b
}

edge {
  from = node.b
  to   = node.a
}
`)
	writeFixtureFile(t, filepath.Join(root, "m", "main.tf"), `output "id" { value = "x" }
`)
	writeFixtureFile(t, filepath.Join(root, "m2", "main.tf"), `output "id" { value = "x" }
`)
	out, _, err := runDetailCmd(t, root, "--output", "json")
	if err == nil {
		t.Fatalf("cycle accepted: %s", out)
	}
	if !strings.Contains(out, `"levels":null`) {
		t.Fatalf("got = %s, want levels null on cycle", out)
	}
	var res struct {
		Valid bool `json:"valid"`
		Nodes []struct {
			Name      string `json:"name"`
			Relations struct {
				Ancestors   []string `json:"ancestors"`
				Descendants []string `json:"descendants"`
			} `json:"relations"`
		} `json:"nodes"`
		Edges       []json.RawMessage       `json:"edges"`
		Diagnostics []struct{ Code string } `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parsing envelope: %v (%s)", err, out)
	}
	if res.Valid || len(res.Nodes) != 2 || len(res.Edges) != 2 {
		t.Fatalf("got = %s, want the constructed graph retained", out)
	}
	for _, n := range res.Nodes {
		for _, rel := range [][]string{n.Relations.Ancestors, n.Relations.Descendants} {
			for _, name := range rel {
				if name == n.Name {
					t.Fatalf("node %s appears in its own relationships: %s", n.Name, out)
				}
			}
		}
	}
	if res.Diagnostics[0].Code != "dependency_cycle" {
		t.Fatalf("got diagnostics = %+v", res.Diagnostics)
	}
}

// TestGraphDetail_BuildFailureEnvelope proves the parse/build-failure row: valid=false, levels=null,
// empty nodes/edges, and the cause with a remedy — no partial reparse.
func TestGraphDetail_BuildFailureEnvelope(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `node "a" { source = "./missing" }
`)
	out, _, err := runDetailCmd(t, root, "--output", "json")
	if err == nil {
		t.Fatalf("build failure accepted: %s", out)
	}
	var res struct {
		Valid       bool              `json:"valid"`
		Levels      *[][]string       `json:"levels"`
		Nodes       []json.RawMessage `json:"nodes"`
		Edges       []json.RawMessage `json:"edges"`
		Diagnostics []struct {
			Code   string `json:"code"`
			Phase  string `json:"phase"`
			Remedy string `json:"remedy"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("parsing envelope: %v (%s)", err, out)
	}
	if res.Valid || res.Levels != nil || len(res.Nodes) != 0 || len(res.Edges) != 0 {
		t.Fatalf("got = %s, want an empty invalid envelope", out)
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != "load_failed" || res.Diagnostics[0].Phase != "load" || res.Diagnostics[0].Remedy == "" {
		t.Fatalf("got diagnostics = %+v", res.Diagnostics)
	}
	if _, _, err := runDetailCmd(t, root); err == nil {
		t.Fatal("text mode succeeded on a build failure")
	}
}

// TestGraphDetail_SettingOriginsMatchExecution proves the origin rows for both resolvers: explicit
// --approve safe vs the omitted built-in are distinct origins, --tofu is the cli origin, a node's
// own runtime attributes to the node with the definition location separate from the selection
// location, and --approve all never overrides a declared approve=safe (#94 acceptance #8).
func TestGraphDetail_SettingOriginsMatchExecution(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `runtime "pinned" {
  binary  = "tofu"
  version = ">= 1.8.0"
}

node "plain" {
  source = "./m"
}

node "guard" {
  source  = "./m"
  approve = "safe"
  runtime = runtime.pinned
}
`)
	writeFixtureFile(t, filepath.Join(root, "m", "main.tf"), `terraform {
  backend "local" {}
}

output "id" { value = "x" }
`)

	runOrigins := func(t *testing.T, args ...string) map[string]originsNode {
		t.Helper()
		out, _, err := runRootCmd(t, append([]string{"--blueprint", filepath.Join(root, "blueprint.hcl"), "graph", "--detail"}, args...)...)
		if err != nil {
			t.Fatalf("detail %v: %v", args, err)
		}
		var res struct {
			Nodes []originsNode `json:"nodes"`
		}
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatalf("parsing envelope: %v (%s)", err, out)
		}
		m := map[string]originsNode{}
		for _, n := range res.Nodes {
			m[n.Name] = n
		}
		return m
	}

	// Omitted --approve: built-in safe; plain node runtime: built-in terraform with no locations.
	nodes := runOrigins(t, "--output", "json")
	if a := nodes["plain"].Approve; a.Effective != "safe" || a.Origin != "builtin" {
		t.Fatalf("got plain approve = %+v", a)
	}
	if r := nodes["plain"].Runtime; r.Binary != "terraform" || r.Origin != "builtin" || r.Location != nil || r.DefinitionLocation != nil {
		t.Fatalf("got plain runtime = %+v", r)
	}
	// Explicit --approve safe is the same policy through the cli origin.
	nodes = runOrigins(t, "--approve", "safe", "--output", "json")
	if a := nodes["plain"].Approve; a.Effective != "safe" || a.Origin != "cli" {
		t.Fatalf("got explicit approve = %+v, want origin cli distinct from builtin", a)
	}
	// --approve all cannot widen a declared safe: the declaration wins.
	nodes = runOrigins(t, "--approve", "all", "--output", "json")
	if a := nodes["guard"].Approve; a.Effective != "safe" || a.Origin != "node" {
		t.Fatalf("got guard approve = %+v, want declared safe at node origin", a)
	}
	// A node's own runtime reference: node origin, name/version carried, selection and definition
	// locations distinct and non-null.
	if r := nodes["guard"].Runtime; r.Binary != "tofu" || r.Origin != "node" || r.Name == nil || *r.Name != "pinned" || r.Version == nil || *r.Version != ">= 1.8.0" || r.Location == nil || r.DefinitionLocation == nil || *r.Location == *r.DefinitionLocation {
		t.Fatalf("got guard runtime = %+v", r)
	}
	// --tofu fills only gaps: plain flips to the cli origin, guard keeps its node declaration.
	nodes = runOrigins(t, "--tofu", "--output", "json")
	if r := nodes["plain"].Runtime; r.Binary != "tofu" || r.Origin != "cli" {
		t.Fatalf("got plain runtime = %+v, want cli origin under --tofu", r)
	}
	if r := nodes["guard"].Runtime; r.Origin != "node" {
		t.Fatalf("got guard runtime = %+v, want the declaration to keep beating --tofu", r)
	}
}

// TestGraphDetail_TextRendersOriginsAndLimitations pins the human-facing qualifiers: built-in
// approve carries the not-authorization phrase verbatim, origins name their declaration sites, and
// every node block closes on the limitation line.
func TestGraphDetail_TextRendersOriginsAndLimitations(t *testing.T) {
	root := writeDetailFixture(t)
	out, _, err := runDetailCmd(t, root)
	if err != nil {
		t.Fatalf("detail text: %v", err)
	}
	for _, want := range []string{
		"runtime: terraform (built-in default)",
		"approve: safe (built-in default; not execution authorization)",
		"group: checkout (service)",
		"supplied by: use.checkout.vars.cluster_name",
		"via: use.checkout.input.cluster_name",
		"edge: node.vpc.output.vpc_id",
		"descendants to review: checkout.core.nodegroup",
		"limitation: downstream resource changes require plan evidence",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("got = %q, want %q in the text rendering", out, want)
		}
	}
	// The payments instance's use-selected runtime names the use block, not the node.
	if !strings.Contains(out, "from use payments, declared at ") {
		t.Fatalf("got = %q, want the payments runtime attributed to its use block", out)
	}
}

// detailCanaries are synthetic secret values planted in every value-bearing position #94 forbids
// echoing: node vars, a module variable default, env, backend_config, and both credential shapes of
// a remote source URL. None may appear in any stdout/stderr surface, success or failure.
var detailCanaries = []string{
	"CANARY-SECRET-vars-9f1a",
	"CANARY-SECRET-default-77bd",
	"CANARY-SECRET-env-3c2d",
	"CANARY-SECRET-backend-5e4f",
	"CANARY-SECRET-urlpass-1b2e",
	"CANARY-SECRET-query-4f5c",
}

const detailCredURL = "git::https://token-user:CANARY-SECRET-urlpass-1b2e@github.com/acme/mod.git?token=CANARY-SECRET-query-4f5c"

// TestGraphDetail_SecretsNeverAppear is the canary: success and failure paths, text and JSON, never
// echo vars/default/env/backend_config values, and the credential-bearing URL shows up sanitized.
func TestGraphDetail_SecretsNeverAppear(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `node "db" {
  source = "./modules/db"
  vars = {
    password = "CANARY-SECRET-vars-9f1a"
  }
  env = {
    CANARY_KEY = "CANARY-SECRET-env-3c2d"
  }
  backend_config = {
    token = "CANARY-SECRET-backend-5e4f"
  }
}

node "remote" {
  source = "`+detailCredURL+`"
}
`)
	writeFixtureFile(t, filepath.Join(root, "modules", "db", "main.tf"), `terraform {
  backend "local" {}
}

variable "password" {
  type      = string
  sensitive = true
}

variable "region" {
  type    = string
  default = "CANARY-SECRET-default-77bd"
}

output "endpoint" { value = "x" }
`)
	writeFixtureFile(t, filepath.Join(root, "vendor", "remote", "main.tf"), `output "id" { value = "y" }
`)

	for _, mode := range []string{"text", "json"} {
		args := []string{}
		if mode == "json" {
			args = append(args, "--output", "json")
		}
		out, stderr, err := runRootCmd(t, append([]string{"--blueprint", filepath.Join(root, "blueprint.hcl"), "graph", "--detail"}, args...)...)
		if err != nil {
			t.Fatalf("%s success path: %v (stderr %q)", mode, err, stderr)
		}
		for _, canary := range detailCanaries {
			if strings.Contains(out, canary) || strings.Contains(stderr, canary) {
				t.Fatalf("%s output leaked %q:\n%s\n%s", mode, canary, out, stderr)
			}
		}
		if !strings.Contains(out, `"declared":"git::https://github.com/acme/mod.git"`) && !strings.Contains(out, "module: git::https://github.com/acme/mod.git") {
			t.Fatalf("%s output missing the sanitized URL:\n%s", mode, out)
		}
		if mode == "json" && !strings.Contains(out, `"sanitized_remote":true`) {
			t.Fatalf("json output missing sanitized_remote flag:\n%s", out)
		}
	}

	// Failure path: an unvendored remote source fails the build without echoing the URL's
	// credentials in the error, the envelope, or stderr.
	broken := t.TempDir()
	writeFixtureFile(t, filepath.Join(broken, "blueprint.hcl"), `node "ghost" {
  source = "`+detailCredURL+`"
  vars = {
    password = "CANARY-SECRET-vars-9f1a"
  }
}
`)
	for _, mode := range []string{"text", "json"} {
		args := []string{}
		if mode == "json" {
			args = append(args, "--output", "json")
		}
		out, stderr, err := runRootCmd(t, append([]string{"--blueprint", filepath.Join(broken, "blueprint.hcl"), "graph", "--detail"}, args...)...)
		if err == nil {
			t.Fatalf("%s failure path accepted: %s", mode, out)
		}
		for _, canary := range detailCanaries {
			if strings.Contains(out, canary) || strings.Contains(stderr, canary) {
				t.Fatalf("%s failure output leaked %q:\n%s\n%s", mode, canary, out, stderr)
			}
		}
	}
}

// TestGraphDetail_PlainGraphOutputsUnchanged is the cheap compatibility guard: the three pre-detail
// surfaces still succeed (content assertions live in the existing graph tests).
func TestGraphDetail_PlainGraphOutputsUnchanged(t *testing.T) {
	root := writeDetailFixture(t)
	bp := filepath.Join(root, "blueprint.hcl")
	listOut, _, err := runRootCmd(t, "--blueprint", bp, "graph")
	if err != nil || !strings.HasPrefix(listOut, "level 1: vpc\n") {
		t.Fatalf("got = %q, %v, want plain level output", listOut, err)
	}
	jsonA, _, err := runRootCmd(t, "--blueprint", bp, "graph", "--output", "json")
	if err != nil || !strings.HasPrefix(jsonA, `{"schema_version":1,`) {
		t.Fatalf("got = %q, %v, want the existing levels payload", jsonA, err)
	}
	jsonB, _, err := runRootCmd(t, "--blueprint", bp, "graph", "--output", "json")
	if err != nil || jsonA != jsonB {
		t.Fatalf("plain json output not stable: %q vs %q", jsonA, jsonB)
	}
	dot, _, err := runRootCmd(t, "--blueprint", bp, "graph", "--format", "dot")
	if err != nil || !strings.Contains(dot, "digraph") {
		t.Fatalf("got = %q, %v, want DOT output", dot, err)
	}
}
