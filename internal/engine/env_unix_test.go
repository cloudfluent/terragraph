//go:build !windows

package engine

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

func TestEngine_RejectsManagedDataDirEnvAfterProgrammaticMutation(t *testing.T) {
	for _, command := range []string{"plan", "apply", "destroy"} {
		t.Run(command, func(t *testing.T) {
			root := t.TempDir()
			writeModule(t, filepath.Join(root, "module"))
			path := writeBlueprint(t, root, `node "a" { source = "./module" }`)
			marker := filepath.Join(root, "started")
			binary := filepath.Join(root, "custom-wrapper")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\n: > '"+marker+"'\nexit 0\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			e, err := Load(path, exec.Binary(binary), io.Discard, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			e.Graph.Nodes["a"].Env = map[string]string{"Tf_Data_Dir": "shared"}
			switch command {
			case "plan":
				_, err = e.Plan(Options{})
			case "apply":
				_, err = e.Apply(Options{AutoApprove: true})
			case "destroy":
				_, err = e.Destroy(Options{AutoApprove: true})
			}
			if err == nil || !strings.Contains(err.Error(), "env.Tf_Data_Dir") || !strings.Contains(err.Error(), "remove this env entry") {
				t.Fatalf("error = %v, want managed env conflict with removal remedy", err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("runtime marker stat = %v, want no subprocess execution", err)
			}
		})
	}
}

func TestPlan_RejectsLaterManagedEnvBeforeAnyNodeRuns(t *testing.T) {
	root := t.TempDir()
	writeModule(t, filepath.Join(root, "module"))
	path := writeBlueprint(t, root, `
node "a" { source = "./module" }
node "b" { source = "./module" }
edge {
  from = node.a
  to = node.b
}
`)
	marker := filepath.Join(root, "started")
	binary := filepath.Join(root, "custom-wrapper")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n: > '"+marker+"'\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	e, err := Load(path, exec.Binary(binary), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	e.Graph.Nodes["b"].Env = map[string]string{"TF_DATA_DIR": "shared"}
	_, err = e.Plan(Options{})
	if err == nil || !strings.Contains(err.Error(), "node.b: env.TF_DATA_DIR") {
		t.Fatalf("error = %v, want node.b managed env conflict before execution", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("runtime marker stat = %v, want no node execution", err)
	}
}

func TestPlan_ManagedEnvErrorCannotFallBackToSnapshot(t *testing.T) {
	e := loadFallbackEngine(t, true)
	writeFallbackSnapshot(t, e, "a", "public-value")
	e.Graph.Nodes["a"].Env = map[string]string{"TF_DATA_DIR": "shared"}
	runs, err := e.Plan(Options{Node: "b"})
	if err == nil || !strings.Contains(err.Error(), "node.a: env.TF_DATA_DIR") || len(runs.Nodes) != 0 {
		t.Fatalf("runs = %v, error = %v, want managed upstream env conflict before fallback or execution", runs, err)
	}
}
