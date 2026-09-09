//go:build !windows

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlan_BackendAddressReachesInit(t *testing.T) {
	bp := writeRunFixture(t)
	dir := filepath.Dir(bp)
	logPath := filepath.Join(dir, "backend-init.log")
	t.Setenv("TG_ADDRESS_LOG", logPath)
	writeFixtureFile(t, filepath.Join(dir, "terraform-fake"), `#!/bin/sh
if [ "$1" = init ]; then printf '%s\t%s\n' "$TF_DATA_DIR" "$*" >> "$TG_ADDRESS_LOG"; fi
if [ "$1" = show ]; then printf '%s\n' '{"format_version":"1.2","resource_changes":[]}'; fi
exit 0
`)
	modulePath := filepath.Join(dir, "remote", "main.tf")
	moduleBody := "terraform {\n backend \"s3\" {}\n}\n"
	writeFixtureFile(t, modulePath, moduleBody)
	writeFixtureFile(t, filepath.Join(dir, "group", "group.hcl"), `group "service" {
  node "app" {
    source = "../remote"
    backend_address = { s3_key_name = "state.json" }
  }
  node "legacy" {
    source = "../remote"
    backend_config = { key = "legacy.tfstate" }
  }
}`)
	writeFixtureFile(t, bp, fmt.Sprintf(`runtime "custom" {
  binary = %q
  default = true
}
use "service" {
  as = "checkout"
  source = "./group"
  backend_config = { bucket = "fixture-only", region = "ap-northeast-2" }
  backend_address = { s3_key_prefix = "prod" }
}
`, filepath.Join(dir, "terraform-fake")))
	if _, _, err := runCmdAt(t, bp, "plan"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("init calls = %q, want two calls", data)
	}
	for _, line := range lines {
		fields := strings.SplitN(line, "\t", 2)
		if len(fields) != 2 {
			t.Fatalf("init observation = %q, want data dir and arguments", line)
		}
		name := filepath.Base(fields[0])
		key, exists := map[string]string{"checkout.app": "prod/checkout.app/state.json", "checkout.legacy": "legacy.tfstate"}[name]
		if !exists || fields[0] != filepath.Join(dir, ".terragraph", "tfdata", name) || !strings.Contains(fields[1], "-backend-config=key="+key+" ") {
			t.Fatalf("init observation = %q, want isolated node data dir and key %q", line, key)
		}
		if !strings.Contains(fields[1], "-backend-config=bucket=fixture-only") || strings.Contains(fields[1], "s3_key_") {
			t.Fatalf("init arguments = %q, want resolved backend settings only", fields[1])
		}
	}
	if data, err := os.ReadFile(modulePath); err != nil || string(data) != moduleBody {
		t.Fatalf("module contents = %q, error = %v, want unchanged source", data, err)
	}
}
