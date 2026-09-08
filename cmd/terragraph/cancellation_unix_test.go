//go:build !windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cloudfluent/terragraph/internal/runlock"
)

// TestMain reuses the race-instrumented test executable as both the real CLI entrypoint and a cooperating runtime without requiring Terraform or a scripting runtime.
func TestMain(m *testing.M) {
	if mode := os.Getenv("TG_CANCEL_HELPER"); mode != "" {
		if mode == "cli" {
			var args []string
			if json.Unmarshal([]byte(os.Getenv("TG_CANCEL_ARGS")), &args) != nil {
				os.Exit(2)
			}
			os.Args = append([]string{os.Args[0]}, args...)
			main()
			os.Exit(0)
		}
		cancellationRuntime(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func cancellationRuntime(mode string) {
	dir := os.Getenv("TG_CANCEL_DIR")
	scenario := os.Getenv("TG_CANCEL_CASE")
	mark := func(name string) { _ = os.WriteFile(filepath.Join(dir, name), []byte("ready"), 0o600) }
	if mode == "child" {
		if scenario == "ignore" {
			signal.Ignore(os.Interrupt)
			mark("child-ready")
			for {
				time.Sleep(time.Second)
			}
		}
		interrupts := make(chan os.Signal, 1)
		signal.Notify(interrupts, os.Interrupt)
		mark("child-ready")
		if scenario != "normal" {
			<-interrupts
		}
		time.Sleep(250 * time.Millisecond)
		mark("child-finished")
		return
	}
	if len(os.Args) < 2 {
		os.Exit(2)
	}
	mark("started-" + filepath.Base(os.Getenv("TF_DATA_DIR")))
	_ = os.WriteFile(filepath.Join(dir, "runtime-pgid"), []byte(strconv.Itoa(os.Getpid())), 0o600)
	switch os.Args[1] {
	case "init":
		if scenario == "approval" || strings.HasPrefix(scenario, "destroy") {
			return
		}
		interrupts := make(chan os.Signal, 1)
		signal.Notify(interrupts, os.Interrupt)
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), "TG_CANCEL_HELPER=child")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if child.Start() != nil {
			os.Exit(2)
		}
		if scenario == "normal" {
			return
		}
		if scenario == "ignore" {
			for {
				<-interrupts
			}
		}
		<-interrupts
	case "plan":
		if scenario == "approval" {
			for _, arg := range os.Args[2:] {
				if strings.HasPrefix(arg, "-out=") {
					_ = os.WriteFile(strings.TrimPrefix(arg, "-out="), []byte("plan"), 0o600)
				}
			}
			os.Exit(2)
		}
		if scenario == "normal" {
			if _, err := os.Stat(filepath.Join(dir, "child-finished")); err != nil {
				os.Exit(3)
			}
		}
	case "show":
		fmt.Print(`{"resource_changes":[{"address":"fixture.item","change":{"actions":["create"]}}]}`)
	case "apply":
		mark("applied")
	case "destroy":
		if scenario == "destroy-no-read" {
			return
		}
		interrupts := make(chan os.Signal, 1)
		signal.Notify(interrupts, os.Interrupt)
		go func() { <-interrupts; os.Exit(0) }()
		mark("destroy-waiting")
		var b [1]byte
		n, _ := os.Stdin.Read(b[:])
		if n == 0 || b[0] != 'y' {
			os.Exit(1)
		}
	case "output":
		fmt.Print(`{}`)
	default:
		os.Exit(2)
	}
}

type cancellationFixture struct {
	dir    string
	bp     string
	stdout string
	stderr string
	tty    bool
}

func newCancellationFixture(t *testing.T, scenario string) cancellationFixture {
	t.Helper()
	dir := t.TempDir()
	module := filepath.Join(dir, "module")
	if err := os.Mkdir(module, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, "main.tf"), []byte("terraform {\n backend \"local\" {}\n}\nvariable \"value\" { type = string }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bp := fmt.Sprintf("runtime \"fixture\" { binary = %q }\n", exe)
	for _, name := range []string{"a", "b", "c"} {
		bp += fmt.Sprintf("node %q {\n source = \"./module\"\n runtime = runtime.fixture\n vars = { value = \"fixture\" }\n env = { TG_CANCEL_HELPER = \"runtime\", TG_CANCEL_DIR = %q, TG_CANCEL_CASE = %q }\n}\n", name, dir, scenario)
	}
	bp += "edge {\n from = node.a\n to = node.c\n}\n"
	path := filepath.Join(dir, "blueprint.hcl")
	if err := os.WriteFile(path, []byte(bp), 0o600); err != nil {
		t.Fatal(err)
	}
	return cancellationFixture{dir: dir, bp: path, stdout: filepath.Join(dir, "stdout"), stderr: filepath.Join(dir, "stderr")}
}

// startCancellationCLI uses real files for all streams because buffers introduce exec pipe goroutines that can hide a wrapper exiting before its children.
func startCancellationCLI(t *testing.T, f cancellationFixture, args ...string) (*exec.Cmd, <-chan error) {
	t.Helper()
	in, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = in.Close(); _ = writer.Close() })
	return startCancellationCLIWithInput(t, f, in, args...)
}

func startCancellationCLIWithInput(t *testing.T, f cancellationFixture, in *os.File, args ...string) (*exec.Cmd, <-chan error) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	argv, err := json.Marshal(append(args, "--blueprint", f.bp))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), "TG_CANCEL_HELPER=cli", "TG_CANCEL_ARGS="+string(argv), "GORACE=atexit_sleep_ms=0")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if f.tty {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	}
	out, err := os.Create(f.stdout)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.Create(f.stderr)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		if data, err := os.ReadFile(filepath.Join(f.dir, "runtime-pgid")); err == nil {
			if pgid, err := strconv.Atoi(string(data)); err == nil {
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
			}
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = out.Close()
		_ = stderr.Close()
	})
	return cmd, done
}

func waitCancellationFile(t *testing.T, path, text string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && strings.Contains(string(data), text) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	stderr, _ := os.ReadFile(filepath.Join(filepath.Dir(path), "stderr"))
	t.Fatalf("timed out waiting for %s: %s", path, stderr)
}

func waitCancellationExit(t *testing.T, done <-chan error, wantFailure bool) {
	t.Helper()
	select {
	case err := <-done:
		if (err != nil) != wantFailure {
			t.Fatalf("exit error = %v, want failure %t", err, wantFailure)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("CLI did not finish cancellation")
	}
}

func TestMain_CancellationWaitsForChildrenAndStopsQueuedNodes(t *testing.T) {
	for _, group := range []bool{false, true} {
		t.Run(fmt.Sprintf("process-group-%t", group), func(t *testing.T) {
			f := newCancellationFixture(t, "grace")
			cmd, done := startCancellationCLI(t, f, "plan", "--output", "json")
			waitCancellationFile(t, filepath.Join(f.dir, "child-ready"), "ready")
			if group {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
			} else {
				_ = cmd.Process.Signal(syscall.SIGTERM)
			}
			waitCancellationExit(t, done, true)
			if _, err := os.Stat(filepath.Join(f.dir, "child-finished")); err != nil {
				t.Fatal("child lost its graceful shutdown window")
			}
			var report struct {
				Nodes []struct{ Node, Status string } `json:"nodes"`
			}
			data, err := os.ReadFile(f.stdout)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &report); err != nil {
				t.Fatal(err)
			}
			if len(report.Nodes) != 3 || report.Nodes[0].Status != "failed" || report.Nodes[1].Status != "not run" || report.Nodes[2].Status != "not run" {
				t.Fatalf("nodes = %+v, want failed/not run/not run", report.Nodes)
			}
			if _, err := os.Stat(filepath.Join(f.dir, ".terragraph", "vars", "a.tfvars.json")); !os.IsNotExist(err) {
				t.Fatalf("tfvars survived: %v", err)
			}
			lock, err := runlock.TryAcquire(f.dir)
			if err != nil {
				t.Fatalf("lock survived: %v", err)
			}
			_ = lock.Close()
		})
	}
}

func TestMain_NormalWrapperWaitsForChildren(t *testing.T) {
	f := newCancellationFixture(t, "normal")
	_, done := startCancellationCLI(t, f, "plan", "--node", "a")
	waitCancellationExit(t, done, false)
}

func TestMain_CancellationEscalatesForUncooperativeGroup(t *testing.T) {
	f := newCancellationFixture(t, "ignore")
	cmd, done := startCancellationCLI(t, f, "plan", "--node", "a")
	waitCancellationFile(t, filepath.Join(f.dir, "child-ready"), "ready")
	start := time.Now()
	_ = cmd.Process.Signal(syscall.SIGTERM)
	waitCancellationExit(t, done, true)
	if elapsed := time.Since(start); elapsed < 5*time.Second {
		t.Fatalf("grace = %s, want at least five seconds", elapsed)
	}
}

func TestMain_CancellationInterruptsApprovalInput(t *testing.T) {
	f := newCancellationFixture(t, "approval")
	cmd, done := startCancellationCLI(t, f, "apply", "--node", "a")
	waitCancellationFile(t, f.stdout, "Apply these changes")
	_ = cmd.Process.Signal(syscall.SIGTERM)
	waitCancellationExit(t, done, true)
	if _, err := os.Stat(filepath.Join(f.dir, "applied")); !os.IsNotExist(err) {
		t.Fatal("apply ran after cancellation")
	}
	if _, err := os.Stat(filepath.Join(f.dir, ".terragraph", "plans", "a.tfplan")); !os.IsNotExist(err) {
		t.Fatal("saved plan survived cancellation")
	}
}

func TestMain_CancellationInterruptsDestroyInput(t *testing.T) {
	f := newCancellationFixture(t, "destroy")
	cmd, done := startCancellationCLI(t, f, "destroy", "--node", "a")
	waitCancellationFile(t, filepath.Join(f.dir, "destroy-waiting"), "ready")
	_ = cmd.Process.Signal(syscall.SIGTERM)
	waitCancellationExit(t, done, true)
}

func TestMain_CancellationStopsLockWaiter(t *testing.T) {
	f := newCancellationFixture(t, "normal")
	lock, err := runlock.Acquire(f.dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Close() }()
	cmd, done := startCancellationCLI(t, f, "plan")
	waitCancellationFile(t, f.stderr, "waiting for another")
	_ = cmd.Process.Signal(syscall.SIGTERM)
	waitCancellationExit(t, done, true)
	if other, err := runlock.TryAcquire(f.dir); !errors.Is(err, runlock.ErrHeld) {
		_ = other.Close()
		t.Fatalf("owner lock = %v, want still held", err)
	}
}

func TestMain_NormalDestroyDoesNotWaitForUnreadPipe(t *testing.T) {
	f := newCancellationFixture(t, "destroy-no-read")
	_, done := startCancellationCLI(t, f, "destroy", "--node", "a")
	waitCancellationExit(t, done, false)
}

func TestMain_ApprovalInputFileAndPipe(t *testing.T) {
	for _, command := range []string{"apply", "destroy"} {
		for _, kind := range []string{"file", "pipe"} {
			for _, answer := range []string{"yes\n", "no\n", ""} {
				t.Run(fmt.Sprintf("%s-%s-%q", command, kind, answer), func(t *testing.T) {
					scenario := "approval"
					if command == "destroy" {
						scenario = "destroy"
					}
					f := newCancellationFixture(t, scenario)
					var in *os.File
					if kind == "pipe" {
						var writer *os.File
						var err error
						in, writer, err = os.Pipe()
						if err != nil {
							t.Fatal(err)
						}
						_, _ = writer.WriteString(answer)
						_ = writer.Close()
					} else {
						path := filepath.Join(f.dir, "input")
						if err := os.WriteFile(path, []byte(answer), 0o600); err != nil {
							t.Fatal(err)
						}
						var err error
						in, err = os.Open(path)
						if err != nil {
							t.Fatal(err)
						}
					}
					t.Cleanup(func() { _ = in.Close() })
					_, done := startCancellationCLIWithInput(t, f, in, command, "--node", "a")
					waitCancellationExit(t, done, answer != "yes\n")
				})
			}
		}
	}
}

func TestMain_NonExecutionCommandKeepsNativeTermination(t *testing.T) {
	f := newCancellationFixture(t, "normal")
	cmd, done := startCancellationCLI(t, f, "lsp")
	time.Sleep(100 * time.Millisecond)
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case err := <-done:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
			t.Fatalf("exit = %v, want native SIGTERM", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("non-execution command ignored SIGTERM")
	}
}
