package blueprint

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseFile_EnvCannotSetManagedDataDir(t *testing.T) {
	for _, kind := range []string{"node", "use"} {
		for _, key := range []string{"TF_DATA_DIR", "tf_data_dir", "Tf_Data_Dir", "TF_DATA_DIR=shared"} {
			t.Run(kind+"/"+key, func(t *testing.T) {
				attrs := `source = "./module"`
				if kind == "use" {
					attrs += "\nas = \"instance\""
				}
				path := writeTemp(t, fmt.Sprintf("%s \"a\" {\n%s\nenv = { %q = \"\" }\n}\n", kind, attrs, key))
				_, err := ParseFile(path)
				if err == nil || !strings.Contains(err.Error(), "env."+key) || !strings.Contains(err.Error(), "remove") || !strings.Contains(err.Error(), path) {
					t.Fatalf("error = %v, want reserved env key, source location, and removal remedy", err)
				}
			})
		}
	}
}

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

func TestParseFile_NodeEnvListRejected(t *testing.T) {
	path := writeTemp(t, `node "a" {
  source = "./a"
  env = ["value"]
}`)
	_, err := ParseFile(path)
	if err == nil || !strings.Contains(err.Error(), "env must be a map/object of strings") || !strings.Contains(err.Error(), path) {
		t.Fatalf("error = %v, want env map/object error with file location", err)
	}
}
