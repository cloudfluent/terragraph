//go:build !windows

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeObservationFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	fake := filepath.Join(dir, "runtime")
	writeFixtureFile(t, fake, `#!/bin/sh
echo CANARY_DIAGNOSTIC >&2
case "$1" in
init) exit 0 ;;
output) printf '%s\n' '{"large":{"value":9007199254740993,"sensitive":false},"secret":{"value":"CANARY_SECRET","sensitive":false},"unknown":{"value":"CANARY_UNKNOWN"},"null":{"value":null,"sensitive":false},"false":{"value":false,"sensitive":false}}' ;;
*) exit 1 ;;
esac
`)
	if err := os.Chmod(fake, 0700); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, filepath.Join(dir, "module", "main.tf"), "terraform {\n backend \"local\" {}\n}\noutput \"secret\" {\n value = \"CANARY_SECRET\"\n sensitive = true\n}\n")
	writeFixtureFile(t, filepath.Join(dir, "module", ".terraform.lock.hcl"), "")
	writeFixtureFile(t, filepath.Join(dir, ".terragraph", "state", "a.tfstate"), "{}")
	writeFixtureFile(t, filepath.Join(dir, "blueprint.hcl"), fmt.Sprintf("runtime \"fake\" {\n binary = %q\n}\nnode \"a\" {\n source = \"./module\"\n runtime = runtime.fake\n}\nnode \"b\" {\n source = \"./module\"\n runtime = runtime.fake\n}\n", fake))
	return filepath.Join(dir, "blueprint.hcl")
}

func TestOutput_RedactionAndPartialFailure(t *testing.T) {
	bp := writeObservationFixture(t)
	stdout, stderr, err := runCmdAt(t, bp, "output", "--output", "json")
	if err == nil {
		t.Fatal("missing sibling state must fail")
	}
	if strings.Contains(stdout+stderr+err.Error(), "CANARY") {
		t.Fatalf("sensitive canary leaked: %s %s %v", stdout, stderr, err)
	}
	if !strings.Contains(stdout, "9007199254740993") {
		t.Fatal("integer precision lost")
	}
	var result observationResultDTO
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) != 2 || result.Nodes[0].Status != "observed" || result.Nodes[1].Diagnostics[0].Code != "local_state_unavailable" {
		t.Fatalf("got = %+v", result)
	}
	if !result.Nodes[0].Outputs["secret"].Redacted || !result.Nodes[0].Outputs["unknown"].Redacted {
		t.Fatal("sensitivity weakened")
	}
}

func TestOutput_RawDisclosureAndPrecision(t *testing.T) {
	bp := writeObservationFixture(t)
	stdout, _, err := runCmdAt(t, bp, "output", "--node", "a", "large", "--raw")
	if err != nil || stdout != "9007199254740993" {
		t.Fatalf("got = %q, %v", stdout, err)
	}
	stdout, stderr, err := runCmdAt(t, bp, "output", "--node", "a", "unknown", "--raw")
	if err == nil || stdout != "" || !strings.Contains(stderr, "--show-sensitive") {
		t.Fatalf("got = %q %q %v", stdout, stderr, err)
	}
	stdout, _, err = runCmdAt(t, bp, "output", "--node", "a", "unknown", "--raw", "--show-sensitive")
	if err != nil || stdout != "CANARY_UNKNOWN" {
		t.Fatalf("got = %q, %v", stdout, err)
	}
}

func TestOutput_MissingNameAndArguments(t *testing.T) {
	bp := writeObservationFixture(t)
	stdout, _, err := runCmdAt(t, bp, "output", "--node", "a", "missing", "--output", "json")
	if err == nil || !strings.Contains(stdout, "output_not_found") {
		t.Fatalf("got = %q, %v", stdout, err)
	}
	stdout, _, err = runCmdAt(t, bp, "output", "large", "--output", "json")
	if err == nil || !strings.Contains(stdout, "invalid_arguments") {
		t.Fatalf("got = %q, %v", stdout, err)
	}
	stdout, _, err = runCmdAt(t, filepath.Join(t.TempDir(), "missing"), "output", "--output", "json")
	if err == nil || !strings.Contains(stdout, "observation_load_failed") {
		t.Fatalf("got = %q, %v", stdout, err)
	}
}

func TestStatus_SafeCountsAndUnavailableSibling(t *testing.T) {
	bp := writeObservationFixture(t)
	fake := filepath.Join(filepath.Dir(bp), "runtime")
	script := `#!/bin/sh
case "$1" in
init) exit 0 ;;
state) printf '%s\n' '{"version":4,"serial":1,"outputs":{"secret":{"value":"CANARY_SECRET"}},"resources":[{"instances":[{"attributes":{"secret":"CANARY_RESOURCE"}}]}]}' ;;
*) exit 1 ;;
esac
`
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runCmdAt(t, bp, "status", "--output", "json")
	if err == nil {
		t.Fatal("unavailable sibling must fail")
	}
	if strings.Contains(stdout+stderr, "CANARY") || strings.Contains(stdout, "applied") {
		t.Fatalf("unsafe status = %s %s", stdout, stderr)
	}
	var result observationResultDTO
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) != 2 || result.Nodes[0].State != "present" || *result.Nodes[0].Resources != 1 || *result.Nodes[0].OutputCount != 1 || result.Nodes[1].State != "unavailable" {
		t.Fatalf("got = %+v", result)
	}
	if !strings.HasPrefix(result.Nodes[0].Identity, "sha256:") {
		t.Fatal("missing sanitized identity")
	}
}

func TestStatus_EmptyAndIndeterminate(t *testing.T) {
	bp := writeObservationFixture(t)
	fake := filepath.Join(filepath.Dir(bp), "runtime")
	for _, serial := range []int{0, 1} {
		script := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\ninit) exit 0 ;;\nstate) printf '%%s\\n' '{\"version\":4,\"serial\":%d,\"outputs\":{},\"resources\":[]}' ;;\nesac\n", serial)
		if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		stdout, _, err := runCmdAt(t, bp, "status", "--node", "a", "--output", "json")
		want := "indeterminate"
		if serial == 1 {
			want = "empty"
		}
		if err != nil || !strings.Contains(stdout, "\"state\":\""+want+"\"") {
			t.Fatalf("got = %q, %v, want %s", stdout, err, want)
		}
	}
}

func TestObservation_SyntaxFailureRetainsSafeLocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blueprint.hcl")
	writeFixtureFile(t, path, "node \"broken\" {\n")
	stdout, _, err := runCmdAt(t, path, "output", "--output", "json")
	if err == nil {
		t.Fatal("invalid syntax succeeded")
	}
	var result observationResultDTO
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Source == nil || result.Diagnostics[0].Source.File != path {
		t.Fatalf("got = %+v", result)
	}
}

func TestOutput_SharedSourceAndDottedLeafUseOwnContext(t *testing.T) {
	bp := writeObservationFixture(t)
	dir := filepath.Dir(bp)
	fake := filepath.Join(dir, "runtime")
	writeFixtureFile(t, fake, `#!/bin/sh
case "$1" in
init)
 case "$TF_DATA_DIR" in *read-*/checkout.cluster) expected=group-state ;; *read-*/a) expected=a-state ;; *) exit 19 ;; esac
 case "$*" in *"$expected"*) ;; *) exit 20 ;; esac
 case "$TF_DATA_DIR" in *checkout.cluster) [ "$TG_GROUP_ENV" = inherited ] || exit 21 ;; esac
 exit 0 ;;
output)
 case "$TF_DATA_DIR" in *checkout.cluster) value=cluster ;; *) value=a ;; esac
 printf '{"identity":{"value":"%s","sensitive":false}}\n' "$value" ;;
*) exit 1 ;;
esac
`)
	writeFixtureFile(t, filepath.Join(dir, "module", "main.tf"), "terraform {\n backend \"local\" {}\n}\nvariable \"required_for_apply\" { type = string }\n")
	writeFixtureFile(t, filepath.Join(dir, "group", "group.hcl"), "group \"service\" {\n node \"cluster\" { source = \"../module\" }\n}\n")
	writeFixtureFile(t, filepath.Join(dir, "a-state"), "{}")
	writeFixtureFile(t, filepath.Join(dir, "group-state"), "{}")
	writeFixtureFile(t, bp, fmt.Sprintf("runtime \"chosen\" { binary = %q }\nnode \"a\" {\n source = \"./module\"\n runtime = runtime.chosen\n backend_config = { path = %q }\n}\nuse \"service\" {\n as = \"checkout\"\n source = \"./group\"\n runtime = runtime.chosen\n env = { TG_GROUP_ENV = \"inherited\" }\n backend_config = { path = %q }\n}\n", fake, filepath.Join(dir, "a-state"), filepath.Join(dir, "group-state")))
	stdout, _, err := runCmdAt(t, bp, "output", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	var result observationResultDTO
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) != 2 || string(result.Nodes[0].Outputs["identity"].Value) != "\"a\"" || string(result.Nodes[1].Outputs["identity"].Value) != "\"cluster\"" {
		t.Fatalf("got = %+v", result)
	}
}

func TestOutput_MissingLockfileDoesNotInitialize(t *testing.T) {
	bp := writeObservationFixture(t)
	dir := filepath.Dir(bp)
	if err := os.Remove(filepath.Join(dir, "module", ".terraform.lock.hcl")); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "started")
	writeFixtureFile(t, filepath.Join(dir, "runtime"), "#!/bin/sh\ntouch '"+marker+"'\nexit 99\n")
	stdout, _, err := runCmdAt(t, bp, "output", "--node", "a", "--output", "json")
	if err == nil || !strings.Contains(stdout, "lockfile_required") {
		t.Fatalf("got = %s, %v", stdout, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("missing lockfile reached init")
	}
}
