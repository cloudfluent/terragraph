package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudfluent/terragraph/internal/exec"
)

// OpenNodeOperation preserves unavailable siblings while inspecting the selected leaf under the source lock.
func OpenNodeOperation(ctx context.Context, path string, binary exec.Binary, stdout, stderr io.Writer) (*Engine, func(), error) {
	e, held, err := load(ctx, path, binary, stdout, stderr, true, true)
	if err != nil {
		return nil, nil, err
	}
	return e, func() { _ = held.Close(); e.runLock = nil }, nil
}

// RunNode executes exactly one expanded leaf with command-specific preparation; it never expands a group or traverses the graph to mutate dependencies.
func (e *Engine) RunNode(name string, args []string) (resultErr error) {
	op, err := classifyOperation(args)
	if err != nil {
		return err
	}
	node := e.Graph.Nodes[name]
	if name == "" || node == nil {
		return fmt.Errorf("run requires --node naming one exact expanded leaf; use graph to inspect node names")
	}
	if node.ObservationError != nil || node.Schema == nil {
		return fmt.Errorf("node.%s: module is unavailable; restore or vendor this node's source", name)
	}
	if !node.Schema.BackendConfigKnown {
		return fmt.Errorf("node.%s: scoped operations require statically known backend configuration; use literal backend settings", name)
	}
	r := e.runner(name)
	r.Stdin = e.Stdin
	if err := r.ValidateOperationEnvironment(); err != nil {
		return err
	}
	unlock, err := e.lockRun()
	if err != nil {
		return err
	}
	defer unlock()
	for _, problem := range e.pathCollisions() {
		if problem.IsError() {
			return fmt.Errorf("run: %s", problem.Message)
		}
	}
	if err := e.checkRuntimeFiles(Options{Nodes: []string{name}}); err != nil {
		return err
	}
	if !op.init {
		if err := e.verifyBackendContext(name, r); err != nil {
			return err
		}
	}
	unlockGraph, err := e.lockGraph()
	if err != nil {
		return err
	}
	defer unlockGraph()
	s, err := e.beginExecution("run_"+strings.ReplaceAll(op.name, " ", "_"), []string{name}, nil, op.readOnly)
	if err != nil {
		return err
	}
	defer s.close()
	defer func() {
		if err := s.finish(resultErr); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	native := append([]string(nil), args...)
	if op.vars {
		vars, err := e.resolveLiveInputs(name)
		if err != nil {
			return err
		}
		path := e.tfVarsPath(name)
		if _, err := exec.WriteTFVars(path, vars); err != nil {
			return err
		}
		defer func() { _ = os.Remove(path) }()
		flags := exec.VarFileArgs(path, vars)
		if op.name == "import" {
			flags = append(flags, "-input=false")
		}
		native = append(append([]string{args[0]}, flags...), args[1:]...)
	}
	var backupPath string
	if op.backup {
		backupPath = filepath.Join(e.BaseDir, ".terragraph", "backups", s.record.ID+".tfstate")
		cleanup, err := prepareSavedPlan(backupPath)
		if err != nil {
			return err
		}
		// Remove only the reservation: the runtime must create a real backup before it is advertised as recovery evidence.
		cleanup()
		at := 1
		if args[0] == "state" {
			at = 2
		}
		flags := []string{"-lock=true", "-backup=" + backupPath}
		native = append(append(append([]string(nil), native[:at]...), flags...), native[at:]...)
	}
	phase := "operating"
	if op.readOnly {
		phase = "reading"
	}
	if op.init {
		phase = "initializing"
	}
	if err := s.transition(name, phase, "", ""); err != nil {
		return err
	}
	if op.init {
		// The explicit init command cannot migrate state or rewrite a provider lockfile inside the source directory.
		resultErr = r.InitRead(node.BackendConfig, args[1:]...)
		if resultErr == nil {
			resultErr = e.rememberBackendContext(name, r)
		}
	} else {
		resultErr = r.RunOperation(native...)
	}
	if op.backup {
		if err := e.persistOperationBackup(s, backupPath); err != nil {
			return errors.Join(resultErr, err)
		}
	}
	if resultErr != nil {
		phase = "indeterminate"
		if op.readOnly {
			phase = "failed"
		}
		return s.fail(name, phase, resultErr)
	}
	return s.transition(name, "completed", "", "")
}

func (e *Engine) persistOperationBackup(s *executionSession, path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > executionObjectLimit {
		return fmt.Errorf("native backup could not be archived; preserve %s and inspect execution history", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("native backup is empty; preserve %s and inspect execution history", path)
	}
	if _, err := s.store.write(e.context(), s.record.ID+".bin", data, ""); err != nil {
		return fmt.Errorf("archiving native backup failed; preserve %s: %w", path, err)
	}
	next := s.record
	next.Backup = true
	if err := s.publish(next); err != nil {
		return err
	}
	return os.Remove(path)
}

// ReadExecutionBackup returns native backup bytes only on explicit request; ordinary history never emits state values.
func (e *Engine) ReadExecutionBackup(id string, out io.Writer) error {
	unlock, err := e.lockRun()
	if err != nil {
		return err
	}
	defer unlock()
	store, err := e.openExecutionStore()
	if err != nil {
		return err
	}
	defer func() { _ = store.close() }()
	record, _, err := readExecutionRecord(e.context(), store, id)
	if err != nil {
		return err
	}
	scope, err := e.executionScope()
	if err != nil {
		return err
	}
	if record.Scope != scope {
		return fmt.Errorf("execution has no accessible native backup in this scope; use backend-native version history when available")
	}
	object, err := store.read(e.context(), id+".bin")
	if err != nil {
		return err
	}
	_, err = out.Write(object.Data)
	return err
}
