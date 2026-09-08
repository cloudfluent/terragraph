//go:build !windows

package module

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileModeForBinary_AbsolutePathRequiresCanonicalIdentity(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	tofu := filepath.Join(dir, "tofu")
	terraform := filepath.Join(dir, "terraform")
	for _, path := range []string{tofu, terraform} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 97\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if got := FileModeForBinary(tofu); got != OpenTofuFiles {
		t.Fatalf("tofu absolute path mode = %v", got)
	}
	if got := FileModeForBinary(terraform); got != TerraformFiles {
		t.Fatalf("terraform absolute path mode = %v", got)
	}
	alias := filepath.Join(dir, "pinned-runtime")
	if err := os.Symlink(tofu, alias); err != nil {
		t.Fatal(err)
	}
	if got := FileModeForBinary(alias); got != OpenTofuFiles {
		t.Fatalf("same-file alias mode = %v", got)
	}
	other := filepath.Join(t.TempDir(), "tofu")
	if err := os.WriteFile(other, []byte("#!/bin/sh\nexit 98\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := FileModeForBinary(other); got != UnknownFiles {
		t.Fatalf("unrelated installation mode = %v, want unknown", got)
	}
	if err := os.Remove(terraform); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tofu, terraform); err != nil {
		t.Fatal(err)
	}
	if got := FileModeForBinary(alias); got != UnknownFiles {
		t.Fatalf("both canonical commands identify same file: mode = %v, want unknown", got)
	}
}
