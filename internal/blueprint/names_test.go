package blueprint

import "testing"

// A node/group/use-instance name ends up as part of a filesystem path (see Blueprint.TFVarsLocation), so a name containing a path separator or a ".." segment must be rejected at parse time rather than silently escaping the intended directory.
func TestParseFile_NodeNameRejectsPathTraversal(t *testing.T) {
	cases := []string{"../escape", "a/b", "a.b", ""}
	for _, name := range cases {
		path := writeTemp(t, `node "`+name+`" { source = "./a" }`)
		if _, err := ParseFile(path); err == nil {
			t.Errorf("expected an error for node name %q", name)
		}
	}
}

func TestParseFile_GroupNameRejectsPathTraversal(t *testing.T) {
	path := writeTemp(t, `
group "../escape" {
  export {}
}
`)
	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error for a group name containing a path separator")
	}
}

func TestParseFile_UseInstanceNameRejectsPathTraversal(t *testing.T) {
	path := writeTemp(t, `
use "svc" {
  as     = "a/b"
  source = "./groups/svc"
}
`)
	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error for a use instance name containing a path separator")
	}
}

func TestParseFile_ValidNamesAccepted(t *testing.T) {
	path := writeTemp(t, `
node "vpc-prod_1" { source = "./a" }
`)
	if _, err := ParseFile(path); err != nil {
		t.Fatalf("expected letters/digits/underscore/hyphen name to be accepted: %v", err)
	}
}
