package blueprint

import "testing"

func TestParseFile_NodeEnv(t *testing.T) {
	path := writeTemp(t, `
node "a" {
  source = "./a"
  env = {
    AWS_PROFILE = "prod"
    AWS_REGION  = "ap-northeast-2"
  }
}
`)

	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	env := bp.Nodes[0].Env
	if env["AWS_PROFILE"] != "prod" || env["AWS_REGION"] != "ap-northeast-2" {
		t.Fatalf("unexpected env: %+v", env)
	}
}

func TestParseFile_NodeEnvNotAMapRejected(t *testing.T) {
	path := writeTemp(t, `
node "a" {
  source = "./a"
  env    = "not-a-map"
}
`)
	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error for a non-map env value")
	}
}

func TestParseFile_NodeEnvNullValueRejected(t *testing.T) {
	path := writeTemp(t, `
node "a" {
  source = "./a"
  env = {
    AWS_PROFILE = null
  }
}
`)
	if _, err := ParseFile(path); err == nil {
		t.Fatalf("expected an error for a null env value")
	}
}

func TestParseFile_NoEnvAttrLeavesNilEnv(t *testing.T) {
	path := writeTemp(t, `
node "a" { source = "./a" }
`)
	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if bp.Nodes[0].Env != nil {
		t.Fatalf("expected nil Env when no env attribute is set, got %+v", bp.Nodes[0].Env)
	}
}

func TestParseFile_UseEnv(t *testing.T) {
	path := writeTemp(t, `
use "g" {
  as     = "inst"
  source = "./groups/g"
  env = {
    AWS_PROFILE = "prod"
  }
}
`)
	bp, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if bp.Uses[0].Env["AWS_PROFILE"] != "prod" {
		t.Fatalf("unexpected use env: %+v", bp.Uses[0].Env)
	}
}
