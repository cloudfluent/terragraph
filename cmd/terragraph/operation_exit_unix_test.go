//go:build !windows

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestRun_PreservesNativeExitCode(t *testing.T) {
	if path := os.Getenv("TG_EXIT_TEST_BLUEPRINT"); path != "" {
		os.Args = []string{"terragraph", "--blueprint", path, "run", "--node", "one", "--", "init"}
		main()
		return
	}
	dir := t.TempDir()
	runtime := filepath.Join(dir, "runtime")
	if err := os.WriteFile(runtime, []byte("#!/bin/sh\nprintf 'native-stdout\\n'\nprintf 'native-stderr\\n' >&2\nexit 23\n"), 0700); err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(dir, "module")
	if err := os.Mkdir(module, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, "main.tf"), []byte("terraform {\n backend \"local\" {}\n}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "blueprint.hcl")
	source := "runtime \"native\" {\n binary = " + strconv.Quote(runtime) + "\n default = true\n}\nnode \"one\" { source = \"./module\" }\n"
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestRun_PreservesNativeExitCode$")
	child.Env = append(os.Environ(), "TG_EXIT_TEST_BLUEPRINT="+path)
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	err := child.Run()
	var exited *exec.ExitError
	if !errors.As(err, &exited) || exited.ExitCode() != 23 || stdout.String() != "native-stdout\n" || !bytes.Contains(stderr.Bytes(), []byte("native-stderr")) {
		t.Fatalf("got = %v, stdout %q, stderr %q", err, stdout.String(), stderr.String())
	}
}
