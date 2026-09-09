package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudfluent/terragraph/internal/exec"
	"github.com/cloudfluent/terragraph/internal/graph"
	"github.com/cloudfluent/terragraph/internal/plugins"
)

// planBundle is an immutable private envelope; native plan bytes remain the sole authority for the changes that can be applied.
type planBundle struct {
	SchemaVersion int       `json:"schema_version"`
	ID            string    `json:"id"`
	ExecutionID   string    `json:"execution_id"`
	Node          string    `json:"node"`
	Binding       string    `json:"binding"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	Changed       bool      `json:"changed"`
	Plan          []byte    `json:"plan"`
}

// planBinding checks absolute source/data paths as required by native saved plans; storage transport does not imply path portability.
func (e *Engine) planBinding(name string, r *exec.Runner, vars map[string]any) (string, error) {
	runtime, err := r.PlanIdentity()
	if err != nil {
		return "", err
	}
	target, err := e.executionTarget(name)
	if err != nil {
		return "", err
	}
	files, err := e.planSourceFiles(name)
	if err != nil {
		return "", err
	}
	contracts, err := e.Graph.Contracts.Digest()
	if err != nil {
		return "", err
	}
	binding, err := executionDigest(struct {
		Contracts, ContractMode string
		Target                  string
		Runtime                 exec.PlanRuntimeIdentity
		Binary                  exec.Binary
		Dir, DataDir            string
		Files                   map[string]string
		Vars                    map[string]any
	}{contracts, e.Graph.ContractMode, target, runtime, r.Binary, r.Dir, r.DataDir, files, vars})
	if err != nil {
		return "", err
	}
	if e.Blueprint != nil && len(e.Blueprint.Plugins) > 0 {
		p, err := plugins.Binding(e.BaseDir, e.Blueprint.Plugins)
		if err != nil {
			return "", err
		}
		return executionDigest(struct {
			Binding, Plugins string
			Credentials      map[string]string
		}{binding, p, e.plugins.CredentialIdentities(name)})
	}
	return binding, nil
}

// planSourceFiles includes ordinary data files alongside configuration so changing file() inputs inside a source tree invalidates a retained plan.
func (e *Engine) planSourceFiles(name string) (map[string]string, error) {
	base := e.nodeDir(name)
	state, _ := graph.LocalStatePath(e.Graph.Nodes[name])
	files := map[string]string{}
	var total int64
	err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == base {
			return nil
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".terragraph", ".terraform":
				return filepath.SkipDir
			}
			return nil
		}
		if path == state || path == state+".backup" || strings.HasSuffix(entry.Name(), ".tfstate") || strings.HasSuffix(entry.Name(), ".tfstate.backup") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("node.%s: retained plans require regular source files; replace source symlinks or use ordinary apply", name)
		}
		total += info.Size()
		if total > executionObjectLimit {
			return fmt.Errorf("node.%s: source inputs exceed retained-plan verification limit; reduce the source tree or use ordinary apply", name)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		hash := sha256.New()
		_, readErr := io.Copy(hash, io.LimitReader(file, executionObjectLimit+1))
		closeErr := file.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		files[rel] = hex.EncodeToString(hash.Sum(nil))
		return nil
	})
	return files, err
}

func (e *Engine) retainPlan(session *executionSession, plan *preparedNodePlan, vars map[string]any) (string, error) {
	binding, err := e.planBinding(plan.name, plan.runner, vars)
	if err != nil {
		return "", err
	}
	if plan.binding != "" && binding != plan.binding {
		return "", fmt.Errorf("node.%s: inputs changed while planning; create a fresh plan", plan.name)
	}
	bytes, err := os.ReadFile(plan.path)
	if err != nil {
		return "", fmt.Errorf("reading retained plan: %w", err)
	}
	session.mu.Lock()
	executionID := session.record.ID
	session.mu.Unlock()
	now := time.Now().UTC()
	bundle := planBundle{SchemaVersion: 1, ID: newExecutionID("plan"), ExecutionID: executionID, Node: plan.name, Binding: binding, CreatedAt: now, ExpiresAt: now.Add(e.Blueprint.ExecutionSettings().PlanTTL), Changed: plan.changed, Plan: bytes}
	data, err := json.Marshal(bundle)
	if err != nil {
		return "", err
	}
	if _, err := session.store.write(e.context(), bundle.ID+".bin", data, ""); err != nil {
		return "", err
	}
	if err := session.transition(plan.name, "planned", "", bundle.ID); err != nil {
		return "", err
	}
	return bundle.ID, nil
}

func (e *Engine) readPlanBundle(session *executionSession, node ExecutionNode) (planBundle, error) {
	object, err := session.store.read(e.context(), node.PlanID+".bin")
	if err != nil {
		return planBundle{}, WithDiagnostic(err, Diagnostic{Code: "saved_plan_read_failed", Category: "artifact", Phase: "artifact", Subject: "node." + node.Name, RelatedExecutionID: session.record.ID, Remedy: "restore the retained plan or cancel and create a fresh plan"})
	}
	var bundle planBundle
	if err := json.Unmarshal(object.Data, &bundle); err != nil {
		return planBundle{}, WithDiagnostic(fmt.Errorf("reading retained plan: %w", err), Diagnostic{Code: "saved_plan_incompatible", Category: "artifact", Phase: "artifact", Subject: "node." + node.Name, RelatedExecutionID: session.record.ID, Remedy: "cancel and create a fresh plan"})
	}
	if bundle.SchemaVersion != 1 || bundle.ID != node.PlanID || bundle.Node != node.Name || bundle.ExecutionID != session.record.ID || len(bundle.Plan) == 0 {
		return planBundle{}, WithDiagnostic(fmt.Errorf("plan bundle is incompatible or does not belong to this execution; create a fresh plan"), Diagnostic{Code: "saved_plan_incompatible", Category: "artifact", Phase: "artifact", RelatedExecutionID: session.record.ID, Remedy: "create a fresh plan"})
	}
	if !time.Now().UTC().Before(bundle.ExpiresAt) {
		return planBundle{}, WithDiagnostic(fmt.Errorf("plan %s expired; cancel the execution and create a fresh plan", bundle.ID), Diagnostic{Code: "saved_plan_expired", Category: "artifact", Phase: "artifact", RelatedExecutionID: session.record.ID, Remedy: "cancel the execution and create a fresh plan"})
	}
	return bundle, nil
}
