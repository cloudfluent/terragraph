package blueprint

import (
	"strings"
	"testing"
)

func TestParseFile_RelationshipCanonicalizesEndpoints(t *testing.T) {
	path := writeTemp(t, `
node "b" { source = "./b" }
node "a" { source = "./a" }
relationship { between = [node.b, node.a] }
`)

	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(bp.Relationships) != 1 || bp.Relationships[0].Left != "a" || bp.Relationships[0].Right != "b" {
		t.Fatalf("relationships = %+v, want a -- b", bp.Relationships)
	}
}

func TestParseFile_RelationshipRejectsDuplicateUnorderedPair(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }
node "b" { source = "./b" }
relationship { between = [node.a, node.b] }
relationship { between = [node.b, node.a] }
`)

	_, err := ParseFile(path)
	if err == nil || !strings.Contains(err.Error(), "duplicate relationship") {
		t.Fatalf("error = %v, want duplicate relationship", err)
	}
}

func TestParseFile_RelationshipRejectsInvalidEndpoints(t *testing.T) {
	for _, body := range []string{
		`relationship { between = [node.a, node.a] }`,
		`relationship { between = [node.a] }`,
		`relationship { between = [node.a.output.x, node.b] }`,
		`relationship { between = [use.g, node.b] }`,
		`relationship { between = [node.a, node.missing] }`,
	} {
		path := writeTemp(t, `
node "a" { source = "./a" }
node "b" { source = "./b" }
`+body)
		if _, err := ParseFile(path); err == nil {
			t.Fatalf("ParseFile accepted invalid relationship %q", body)
		}
	}
}

func TestParseFile_GroupRelationship(t *testing.T) {
	path := writeTemp(t, `
group "pair" {
  node "a" { source = "./a" }
  node "b" { source = "./b" }
  relationship { between = [node.a, node.b] }
}
`)

	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(bp.Groups[0].Relationships) != 1 {
		t.Fatalf("relationships = %+v, want one", bp.Groups[0].Relationships)
	}
}

func TestParseDir_RelationshipMayReferenceNodesFromAnotherFile(t *testing.T) {
	dir := writeDirTemp(t, map[string]string{
		"nodes.hcl": `
node "a" { source = "./a" }
node "b" { source = "./b" }
`,
		"relationships.hcl": `relationship { between = [node.b, node.a] }`,
	})

	bp, err := ParseDir(dir)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}
	if len(bp.Relationships) != 1 || bp.Relationships[0].Left != "a" || bp.Relationships[0].Right != "b" {
		t.Fatalf("relationships = %+v, want a -- b", bp.Relationships)
	}
}
