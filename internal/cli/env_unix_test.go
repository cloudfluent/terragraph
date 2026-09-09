//go:build !windows

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCommands_RejectManagedDataDirEnvBeforeRuntime(t *testing.T) {
	for _, command := range []string{"validate", "plan", "apply", "destroy"} {
		for _, owner := range []string{"node", "use", "group node"} {
			t.Run(command+"/"+owner, func(t *testing.T) {
				bp := writeRunFixture(t)
				dir := filepath.Dir(bp)
				marker := filepath.Join(dir, "runtime-started")
				writeFixtureFile(t, filepath.Join(dir, "terraform-fake"), "#!/bin/sh\n: > '"+marker+"'\nexit 0\n")
				body := `node "a" {
  source = "./module"
  env = { TF_DATA_DIR = "shared" }
}`
				if owner != "node" {
					body = `use "g" {
  as = "instance"
  source = "./group"
  env = { tf_data_dir = "shared" }
}`
					group := "group \"g\" {\n node \"a\" {\n source = \"../module\"\n }\n}\n"
					if owner == "group node" {
						body = strings.ReplaceAll(body, `env = { tf_data_dir = "shared" }`, "")
						group = strings.ReplaceAll(group, "source = \"../module\"", "source = \"../module\"\n env = { Tf_Data_Dir = \"shared\" }")
					}
					writeFixtureFile(t, filepath.Join(dir, "group", "group.hcl"), group)
				}
				writeFixtureFile(t, bp, fmt.Sprintf("runtime \"custom\" {\n binary = %q\n default = true\n}\n%s\n", filepath.Join(dir, "terraform-fake"), body))
				args := []string{command, "--output", "json"}
				if command == "apply" || command == "destroy" {
					args = append(args, "--auto-approve")
				}
				stdout, _, err := runCmdAt(t, bp, args...)
				if err == nil || !strings.Contains(err.Error(), "TF_DATA_DIR is managed per node") || !strings.Contains(err.Error(), "remove this env entry") {
					t.Fatalf("error = %v, want managed env conflict with removal remedy", err)
				}
				if command == "plan" {
					if !strings.Contains(stdout, "plan_load_failed") {
						t.Fatalf("missing structured load failure: %s", stdout)
					}
				} else if !strings.Contains(stdout, "load_failed") {
					t.Fatalf("stdout = %q, want structured load failure", stdout)
				}
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatalf("runtime marker stat = %v, want no subprocess execution", err)
				}
			})
		}
	}
}

func TestPlan_ManagedDataDirsAndOrdinaryEnvReachCustomRuntime(t *testing.T) {
	bp := writeRunFixture(t)
	dir := filepath.Dir(bp)
	logPath := filepath.Join(dir, "runtime-env.log")
	t.Setenv("TF_DATA_DIR", filepath.Join(dir, "host-shared"))
	t.Setenv("TG_R04_VALUE", "host")
	t.Setenv("TG_R04_INHERITED", "host-only")
	t.Setenv("TG_R04_LOG", logPath)
	writeFixtureFile(t, filepath.Join(dir, "terraform-fake"), `#!/bin/sh
printf '%s\t%s\t%s\t%s\n' "$TF_DATA_DIR" "$TG_R04_VALUE" "$TG_R04_INHERITED" "$*" >> "$TG_R04_LOG"
if [ "$1" = show ]; then printf '%s\n' '{"format_version":"1.2","resource_changes":[]}'; fi
exit 0
`)
	writeFixtureFile(t, filepath.Join(dir, "remote", "main.tf"), "terraform {\n backend \"s3\" {}\n}\n")
	writeFixtureFile(t, filepath.Join(dir, "group", "group.hcl"), `group "g" {
  node "inherited" { source = "../module" }
  node "explicit" {
    source = "../module"
    env = { TG_R04_VALUE = "node" }
  }
}`)
	writeFixtureFile(t, bp, fmt.Sprintf(`runtime "custom" {
  binary = %q
  default = true
}
use "g" {
  as = "instance"
  source = "./group"
  env = { TG_R04_VALUE = "group" }
}
node "remote" {
  source = "./remote"
  backend_config = { bucket = "fixture-only", key = "remote-state" }
}
`, filepath.Join(dir, "terraform-fake")))
	if _, _, err := runCmdAt(t, bp, "plan"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	wantValues := map[string]string{"instance.explicit": "node", "instance.inherited": "group", "remote": "host"}
	seen := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 4 {
			t.Fatalf("runtime observation = %q, want four fields", line)
		}
		name := filepath.Base(fields[0])
		want, known := wantValues[name]
		if !known || fields[0] != filepath.Join(dir, ".terragraph", "tfdata", name) || fields[1] != want || fields[2] != "host-only" {
			t.Fatalf("runtime observation = %q, want isolated data dir and merged normal env", line)
		}
		if strings.HasPrefix(fields[3], "init ") {
			backend := "-backend-config=path=" + filepath.Join(dir, ".terragraph", "state", name+".tfstate")
			if name == "remote" {
				backend = "-backend-config=bucket=fixture-only -backend-config=key=remote-state"
			}
			if !strings.Contains(fields[3], backend) {
				t.Fatalf("init args = %q, want backend config %q", fields[3], backend)
			}
		}
		seen[name]++
	}
	for name := range wantValues {
		if seen[name] != 3 {
			t.Fatalf("runtime calls for %q = %d, want init, plan, and show", name, seen[name])
		}
	}
}
