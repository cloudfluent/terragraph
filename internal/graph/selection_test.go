package graph

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func selectionFixture(t *testing.T) *Graph {
	t.Helper()
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "module", "main.tf"), "terraform {\n backend \"local\" {}\n}\noutput \"out\" { value = \"v\" }\nvariable \"in\" { default = \"\" }\nvariable \"extra\" { default = \"\" }\n")
	body := ""
	for _, name := range []string{"a", "b", "c", "d", "x", "p", "q"} {
		body += "node \"" + name + "\" { source = \"./module\" }\n"
	}
	body += `edge {
 from = node.a.output.out
 to = node.b.input.in
}
edge {
 from = node.b.output.out
 to = node.c.input.in
}
edge {
 from = node.x.output.out
 to = node.c.input.extra
}
edge {
 from = node.c
 to = node.d
}
edge {
 from = node.p
 to = node.q
}
`
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), body)
	return parseAndBuild(t, root)
}

func TestSelect_DownstreamPreservesBoundaryAndGraph(t *testing.T) {
	g := selectionFixture(t)
	before, _ := json.Marshal(g)
	s, err := Select(g, []string{"b"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Names(); !reflect.DeepEqual(got, []string{"b", "c", "d"}) {
		t.Fatalf("got = %v, want b c d", got)
	}
	if !reflect.DeepEqual(s.Nodes[2].Via, []string{"c"}) || len(s.BoundaryEdges) != 2 {
		t.Fatalf("got = %+v", s)
	}
	levels, err := SelectedLevels(g, s.Names(), false)
	if err != nil || !reflect.DeepEqual(levels, [][]string{{"b"}, {"c"}, {"d"}}) {
		t.Fatalf("got = %v, %v", levels, err)
	}
	dot := SelectionDOT(g, s)
	for _, want := range []string{"a (not selected)", "x (not selected)", "style=dashed", `"c" -> "d"`} {
		if !strings.Contains(dot, want) {
			t.Fatalf("got = %s, want %q", dot, want)
		}
	}
	if strings.Contains(dot, `"p"`) || strings.Contains(dot, `"q"`) {
		t.Fatalf("got = %s, want no unrelated nodes", dot)
	}
	after, _ := json.Marshal(g)
	if string(before) != string(after) {
		t.Fatal("selection mutated the source graph")
	}
}

func TestSelect_MultipleSeedsAreDeterministic(t *testing.T) {
	g := selectionFixture(t)
	a, err := Select(g, []string{"x", "b", "b"}, true)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Select(g, []string{"b", "x"}, true)
	if err != nil || !reflect.DeepEqual(a, b) {
		t.Fatalf("got = %+v, %v, want %+v", b, err, a)
	}
	if !reflect.DeepEqual(a.Nodes[1].Via, []string{"b", "x"}) {
		t.Fatalf("got = %+v", a.Nodes)
	}
}

func TestSelect_ExactShowsOutgoingBoundary(t *testing.T) {
	g := selectionFixture(t)
	s, err := Select(g, []string{"b"}, false)
	if err != nil || len(s.Nodes) != 1 || len(s.BoundaryEdges) != 2 {
		t.Fatalf("got = %+v, %v", s, err)
	}
	if s.BoundaryEdges[1].To.Node != "c" {
		t.Fatalf("got = %+v", s.BoundaryEdges)
	}
	s, err = Select(g, []string{"d"}, false)
	if err != nil || len(s.BoundaryEdges) != 1 || s.BoundaryEdges[0].IsDataEdge() {
		t.Fatalf("got = %+v, %v", s, err)
	}
}

func TestSelectedLevels_CompactsGapsAndReverses(t *testing.T) {
	g := selectionFixture(t)
	levels, err := SelectedLevels(g, []string{"a", "d"}, true)
	if err != nil || !reflect.DeepEqual(levels, [][]string{{"d"}, {"a"}}) {
		t.Fatalf("got = %v, %v", levels, err)
	}
	levels, err = SelectedLevels(g, []string{}, false)
	if err != nil || len(levels) != 0 {
		t.Fatalf("got = %v, %v, want empty selection", levels, err)
	}
}

func TestSelect_InvalidSeedsNeverFallBackToAll(t *testing.T) {
	g := selectionFixture(t)
	for _, seeds := range [][]string{{}, {""}, {"b", "missing"}, {"b,x"}, {"B"}, {" b"}} {
		s, err := Select(g, seeds, false)
		if err == nil || s != nil || !strings.Contains(err.Error(), ";") && !strings.Contains(err.Error(), "specify") {
			t.Fatalf("seeds = %q, got = %+v, %v", seeds, s, err)
		}
	}
	if s, err := Select(g, nil, true); err == nil || s != nil {
		t.Fatalf("got = %+v, %v", s, err)
	}
	if s, err := Select(g, nil, false); err != nil || s != nil {
		t.Fatalf("got = %+v, %v", s, err)
	}
}

func TestSelect_QualifiedLeafDoesNotSelectSibling(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, filepath.Join(root, "module", "main.tf"), "terraform {\n backend \"local\" {}\n}\n")
	writeFixtureFile(t, filepath.Join(root, "group", "group.hcl"), `group "g" {
 node "cluster" { source = "../module" }
 node "other" { source = "../module" }
}`)
	writeFixtureFile(t, filepath.Join(root, "blueprint.hcl"), `use "g" {
 as = "checkout"
 source = "./group"
}`)
	g := parseAndBuild(t, root)
	s, err := Select(g, []string{"checkout.cluster"}, true)
	if err != nil || !reflect.DeepEqual(s.Names(), []string{"checkout.cluster"}) {
		t.Fatalf("got = %+v, %v", s, err)
	}
	if _, err := Select(g, []string{"checkout"}, false); err == nil || !strings.Contains(err.Error(), "checkout.cluster") {
		t.Fatalf("got = %v", err)
	}
}

func TestSelection_RejectsCorruptMembership(t *testing.T) {
	g := selectionFixture(t)
	for _, mutate := range []func(*Selection){
		func(s *Selection) { s.SchemaVersion = 2 },
		func(s *Selection) { s.Nodes = s.Nodes[:1] },
		func(s *Selection) { s.Requested = []string{"p"} },
		func(s *Selection) { s.Nodes[1].Via = []string{"p"} },
		func(s *Selection) { s.Nodes[1].Via = []string{"d"}; s.Nodes[2].Via = []string{"c"} },
		func(s *Selection) { s.BoundaryEdges[0].From.Node = "b" },
	} {
		s, err := Select(g, []string{"b"}, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.ValidateMembership([]string{"b", "c", "d"}); err != nil {
			t.Fatal(err)
		}
		mutate(s)
		if err := s.ValidateMembership([]string{"b", "c", "d"}); err == nil {
			t.Fatalf("accepted corrupt metadata: %+v", s)
		}
	}
}

func TestSelect_UpstreamIncludesDataAndOrderingAncestors(t *testing.T) {
	g := selectionFixture(t)
	s, err := Select(g, []string{"d"}, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.Names(), []string{"a", "b", "c", "d", "x"}) || s.Mode != "upstream" {
		t.Fatalf("got = %+v, want ancestors of d", s)
	}
	if !reflect.DeepEqual(s.Nodes[0].Via, []string{"b"}) || s.Nodes[0].Reason != "upstream" {
		t.Fatalf("got = %+v, want upstream via b", s.Nodes[0])
	}
	if err := s.ValidateMembership(s.Names()); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var restored Selection
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if err := restored.ValidateMembership(s.Names()); err != nil {
		t.Fatal(err)
	}
	restored.Nodes[0].Reason = "downstream"
	if err := restored.ValidateMembership(s.Names()); err == nil {
		t.Fatal("want corrupted upstream metadata rejected")
	}
}

func TestSelect_UpstreamRequiresOneDirectionAndSeed(t *testing.T) {
	g := selectionFixture(t)
	if _, err := Select(g, nil, false, true); err == nil {
		t.Fatal("want missing seed rejected")
	}
	if _, err := Select(g, []string{"c"}, true, true); err == nil {
		t.Fatal("want ambiguous directions rejected")
	}
}
