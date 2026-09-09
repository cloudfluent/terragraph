//go:build !windows

package engine

import (
	"bytes"
	"errors"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func operationTestEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	e, _, log := loadApplyTestEngine(t)
	script := `#!/bin/sh
printf '%s\n' "$TF_DATA_DIR" >> "$TG_COMMAND_LOG"
printf '%s\n' "$@" >> "$TG_COMMAND_LOG"
for arg in "$@"; do
 case "$arg" in -backup=*) printf 'NATIVE_BACKUP_CANARY' > "${arg#-backup=}" ;; esac
done
printf 'native-result\n'
printf 'native-diagnostic\n' >&2
exit "${TG_NATIVE_EXIT:-0}"
`
	if err := os.WriteFile(string(e.Binary), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := e.RunNode("cached", []string{"init"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	e.Stdout.(*bytes.Buffer).Reset()
	e.Stderr.(*bytes.Buffer).Reset()
	return e, log
}

func TestRunNode_StateReadDoesNotInitOrInjectVars(t *testing.T) {
	e, log := operationTestEngine(t)
	if err := e.RunNode("cached", []string{"state", "list"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil || strings.Contains(string(data), "init") || strings.Contains(string(data), "-var-file") || !strings.Contains(string(data), e.dataDir("cached")) {
		t.Fatalf("got = %s, %v", data, err)
	}
	if e.Stdout.(*bytes.Buffer).String() != "native-result\n" || e.Stderr.(*bytes.Buffer).String() != "native-diagnostic\n" {
		t.Fatal("native streams were changed")
	}
}

func TestRunNode_MutationArchivesBackupAndPreservesExitCode(t *testing.T) {
	e, _ := operationTestEngine(t)
	t.Setenv("TG_NATIVE_EXIT", "23")
	err := e.RunNode("cached", []string{"state", "rm", "terraform_data.old"})
	var native *osexec.ExitError
	if !errors.As(err, &native) || native.ExitCode() != 23 {
		t.Fatalf("got = %v", err)
	}
	records, err := e.ListExecutions()
	if err != nil || len(records) != 2 || !records[0].Backup || records[0].Status != "needs_recovery" {
		t.Fatalf("got = %+v, %v", records, err)
	}
	var backup bytes.Buffer
	if err := e.ReadExecutionBackup(records[0].ID, &backup); err != nil {
		t.Fatal(err)
	}
	if backup.String() != "NATIVE_BACKUP_CANARY" {
		t.Fatalf("got = %s", backup.String())
	}
	if _, err := os.Stat(filepath.Join(e.BaseDir, ".terragraph", "backups", records[0].ID+".tfstate")); !os.IsNotExist(err) {
		t.Fatal("archived backup scratch was not removed")
	}
}

func TestRunNode_ReadAllowedDuringUnknownButMutationBlocked(t *testing.T) {
	e, _ := operationTestEngine(t)
	unlock, err := e.lockRun()
	if err != nil {
		t.Fatal(err)
	}
	s, err := e.beginExecution("apply", []string{"cached"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.transition("cached", "indeterminate", "", ""); err != nil {
		t.Fatal(err)
	}
	s.close()
	unlock()
	if err := e.RunNode("cached", []string{"state", "list"}); err != nil {
		t.Fatal(err)
	}
	if err := e.RunNode("cached", []string{"import", "terraform_data.x", "id"}); err == nil {
		t.Fatal("mutation bypassed recovery barrier")
	}
}

func TestRunNode_RejectsScopeAndLockBypasses(t *testing.T) {
	e, log := operationTestEngine(t)
	for _, args := range [][]string{{"apply"}, {"destroy"}, {"state", "push", "other.tfstate"}, {"state", "mv", "-state=other", "a", "b"}, {"state", "rm", "-lock=false", "a"}, {"import", "-config=other", "a", "id"}, {"init", "-migrate-state"}} {
		if err := e.RunNode("cached", args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("rejected arguments reached native runtime")
	}
	t.Setenv("TF_CLI_ARGS_state", "-state=other")
	if err := e.RunNode("cached", []string{"state", "list"}); err == nil {
		t.Fatal("ambient override accepted")
	}
}

func TestRunNode_RefusesChangedBackendDeclaration(t *testing.T) {
	e, log := operationTestEngine(t)
	e.Graph.Nodes["cached"].BackendConfig["path"] = filepath.Join(e.BaseDir, "other.tfstate")
	if err := e.RunNode("cached", []string{"state", "rm", "terraform_data.old"}); err == nil {
		t.Fatal("changed target accepted")
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("changed target reached native runtime")
	}
}

func TestRunNode_RefusesExternallyReconfiguredCache(t *testing.T) {
	e, log := operationTestEngine(t)
	if err := os.WriteFile(filepath.Join(e.dataDir("cached"), "terraform.tfstate"), []byte(`{"backend":{"type":"http"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := e.RunNode("cached", []string{"state", "list"}); err == nil {
		t.Fatal("changed native cache accepted")
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatal("changed cache reached native runtime")
	}
}
