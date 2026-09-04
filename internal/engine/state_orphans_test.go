package engine

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
	"github.com/cloudfluent/terragraph/internal/graph"
)

// loadLiveNode loads a one-node blueprint whose module uses the local backend, so graph.Build fills BackendConfig path = <baseDir>/.terragraph/state/live.tfstate.
func loadLiveNode(t *testing.T) *Engine {
	t.Helper()
	baseDir := t.TempDir()
	writeModule(t, filepath.Join(baseDir, "stacks", "live"))
	path := writeBlueprint(t, baseDir, `
node "live" { source = "./stacks/live" }
`)

	e, err := Load(path, exec.Terraform, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return e
}

func TestStateOrphans_WarnsAboutStrayStateFile(t *testing.T) {
	e := loadLiveNode(t)

	// Simulates the local-backend state left behind by a node that has since been renamed or removed from the blueprint.
	stray := filepath.Join(e.BaseDir, ".terragraph", "state", "ghost.tfstate")
	if err := os.MkdirAll(filepath.Dir(stray), 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	if err := os.WriteFile(stray, []byte(`{"version":4}`), 0o644); err != nil {
		t.Fatalf("writing stray state fixture: %v", err)
	}

	var orphans []graph.Problem
	for _, p := range e.Validate() {
		if p.IsError() {
			t.Fatalf("unexpected error-level problem: %s", p.Message)
		}
		if strings.Contains(p.Message, "ghost.tfstate") {
			orphans = append(orphans, p)
		}
	}
	if len(orphans) != 1 {
		t.Fatalf("expected exactly one warning about ghost.tfstate, got %d: %+v", len(orphans), orphans)
	}
	msg := orphans[0].Message
	if !strings.Contains(msg, "rename") || (!strings.Contains(msg, "remove") && !strings.Contains(msg, "import")) {
		t.Fatalf("warning should name a remedy (rename plus remove/import), got: %s", msg)
	}
}

func TestStateOrphans_NoStrayFileNoNewProblems(t *testing.T) {
	e := loadLiveNode(t)

	if problems := e.Validate(); len(problems) != 0 {
		t.Fatalf("expected no problems without a stray state file, got: %+v", problems)
	}
}
