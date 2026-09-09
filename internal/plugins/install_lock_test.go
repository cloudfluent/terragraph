package plugins_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/cli"
	"github.com/cloudfluent/terragraph/internal/engine"
	"github.com/cloudfluent/terragraph/internal/exec"
	"github.com/cloudfluent/terragraph/internal/plugins"
	"github.com/cloudfluent/terragraph/internal/runlock"
)

type cancelWaitWriter struct{ cancel context.CancelFunc }

func (w cancelWaitWriter) Write(p []byte) (int, error) { w.cancel(); return len(p), nil }

func uninstalledBlueprint(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blueprint.hcl"), []byte("plugin \"test\" {\n source = \"example/fixture\"\n version = \"~> 1.2\"\n}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadLocked_LocksBeforePluginResolution(t *testing.T) {
	dir := uninstalledBlueprint(t)
	held, err := runlock.TryAcquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _, err = engine.LoadLockedContext(ctx, filepath.Join(dir, "blueprint.hcl"), exec.Terraform, io.Discard, cancelWaitWriter{cancel})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got = %v, want cancellation while waiting for install lock", err)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, err = engine.LoadLockedContext(context.Background(), dir, exec.Terraform, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("missing plugin package accepted")
	}
	lock, err := runlock.TryAcquire(dir)
	if err != nil {
		t.Fatalf("failed load leaked lock: %v", err)
	}
	_ = lock.Close()
}

func TestVendor_LocksBeforePluginResolution(t *testing.T) {
	dir := uninstalledBlueprint(t)
	held, err := runlock.TryAcquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := cli.NewRootCmd("test")
	cmd.SetArgs([]string{"--blueprint", dir, "vendor"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(cancelWaitWriter{cancel})
	err = cmd.ExecuteContext(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got = %v, want cancellation while waiting for install lock", err)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	cmd = cli.NewRootCmd("test")
	cmd.SetArgs([]string{"--blueprint", dir, "vendor"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err == nil {
		t.Fatal("missing plugin package accepted")
	}
	lock, err := runlock.TryAcquire(dir)
	if err != nil {
		t.Fatalf("failed vendor leaked lock: %v", err)
	}
	_ = lock.Close()
}

func TestInstall_RepairsDamagedLockedPackage(t *testing.T) {
	dir := installFixture(t, "")
	configs, _, err := blueprint.LoadPlugins(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := plugins.Resolve(dir, configs[0])
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(dir, "terragraph.plugins.lock.json")
	before, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Path, []byte("tampered"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := plugins.Install(dir, configs[0], fixtureDir, true); err != nil {
		t.Fatalf("repair: %v", err)
	}
	repaired, err := plugins.Resolve(dir, configs[0])
	if err != nil || repaired.Digest != p.Digest {
		t.Fatalf("got = %v, %v, want verified original digest", repaired.Digest, err)
	}
	after, err := os.ReadFile(lockPath)
	if err != nil || string(after) != string(before) {
		t.Fatalf("locked repair changed lock: %v", err)
	}
}
