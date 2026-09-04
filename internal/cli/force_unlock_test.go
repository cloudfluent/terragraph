package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// runForceUnlock runs force-unlock against a freshly written one-node blueprint fixture and returns stdout, stderr, and any error. No terraform runs and no S3 is touched: these tests cover flag/validation wiring only.
func runForceUnlock(t *testing.T, blueprint string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	dir := t.TempDir()
	writeFixtureFile(t, filepath.Join(dir, "blueprint.hcl"), blueprint)
	writeFixtureFile(t, filepath.Join(dir, "stacks/a/outputs.tf"), "output \"x\" { value = \"hi\" }\n")
	root := NewRootCmd("test")
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(append([]string{"--blueprint", filepath.Join(dir, "blueprint.hcl"), "force-unlock"}, args...))
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

const forceUnlockLockHCL = `
lock {
  s3 {
    bucket = "acme-tfstate"
    key    = "terragraph/prod.lock"
    region = "ap-northeast-2"
  }
}
node "a" { source = "./stacks/a" }
`

func TestForceUnlock_NoLockBlockErrors(t *testing.T) {
	_, _, err := runForceUnlock(t, `node "a" { source = "./stacks/a" }`)
	if err == nil {
		t.Fatal("expected an error without a lock block")
	}
	if !strings.Contains(err.Error(), "this blueprint declares no graph lock") {
		t.Fatalf("err = %v, want no-graph-lock message", err)
	}
}

func TestForceUnlock_RefusesWithoutYes(t *testing.T) {
	_, _, err := runForceUnlock(t, forceUnlockLockHCL)
	if err == nil {
		t.Fatal("expected a refusal without --yes")
	}
	for _, want := range []string{"acme-tfstate", "terragraph/prod.lock", "--yes"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want mention of %q", err, want)
		}
	}
}

// The command this reaches for exists because a run was interrupted, and the natural next step is a fresh checkout — which has nothing vendored. Building the graph stats every node source, so it aborted before ever looking at the lock block. Parsing the blueprint is enough: the refusal below proves it got as far as the lock.
func TestForceUnlock_WorksOnAnUnvendoredCheckout(t *testing.T) {
	dir := t.TempDir()
	writeFixtureFile(t, filepath.Join(dir, "blueprint.hcl"), `
node "eks" { source = "github.com/acme/eks" }

lock {
  s3 {
    bucket = "acme-tfstate"
    key    = "terragraph/prod.lock"
    region = "ap-northeast-2"
  }
}
`)

	root := NewRootCmd("test")
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs([]string{"--blueprint", filepath.Join(dir, "blueprint.hcl"), "force-unlock"})
	err := root.Execute()

	if err == nil {
		t.Fatal("expected the --yes refusal")
	}
	if strings.Contains(err.Error(), "not vendored") {
		t.Fatalf("force-unlock should not need vendored modules, got: %v", err)
	}
	if !strings.Contains(err.Error(), "--yes") || !strings.Contains(err.Error(), "terragraph/prod.lock") {
		t.Fatalf("expected a refusal naming the lock object, got: %v", err)
	}
}
