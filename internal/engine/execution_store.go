package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

var (
	errExecutionMissing  = errors.New("execution object is missing; inspect current state before starting a new execution")
	errExecutionConflict = errors.New("execution object changed; reload it before retrying")
	executionKeyPattern  = regexp.MustCompile(`^(run|plan)-[a-f0-9]{32}\.(json|bin)$`)
)

const executionObjectLimit = 128 << 20

// executionStore separates optional plan bytes from journals; callers hold the blueprint and configured graph locks across execution decisions.
type executionStore interface {
	read(context.Context, string) (executionObject, error)
	write(context.Context, string, []byte, string) (string, error)
	remove(context.Context, string, string) error
	list(context.Context) ([]string, error)
	close() error
}

type executionObject struct {
	// Revision is the store API token; the S3 body carries a fresh nonce to prevent identical-write ABA, but only the response ETag authorizes later writes.
	Revision string `json:"revision"`
	Data     []byte `json:"data"`
}

// localExecutionStore serializes worker updates within the process; the existing blueprint lock serializes independent processes.
type localExecutionStore struct {
	root *os.Root
	mu   sync.Mutex
}

func newExecutionID(prefix string) string {
	var data [16]byte
	_, _ = rand.Read(data[:])
	return prefix + "-" + hex.EncodeToString(data[:])
}

func checkExecutionKey(key string) error {
	if !executionKeyPattern.MatchString(key) {
		return fmt.Errorf("invalid execution object %q; select an ID returned by plan", key)
	}
	return nil
}

func openLocalExecutionStore(base string) (*localExecutionStore, error) {
	dir := filepath.Join(base, ".terragraph", "executions")
	cleanup, err := prepareSavedPlan(filepath.Join(dir, ".prepare"))
	if err != nil {
		return nil, fmt.Errorf("preparing execution directory: %w", err)
	}
	cleanup()
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("opening execution directory: %w", err)
	}
	return &localExecutionStore{root: root}, nil
}

func (s *localExecutionStore) close() error { return s.root.Close() }

func (s *localExecutionStore) read(ctx context.Context, key string) (executionObject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked(ctx, key)
}

func (s *localExecutionStore) readLocked(ctx context.Context, key string) (executionObject, error) {
	if err := ctx.Err(); err != nil {
		return executionObject{}, err
	}
	if err := checkExecutionKey(key); err != nil {
		return executionObject{}, err
	}
	info, err := s.root.Lstat(key)
	if errors.Is(err, os.ErrNotExist) {
		return executionObject{}, errExecutionMissing
	}
	if err != nil {
		return executionObject{}, err
	}
	if !info.Mode().IsRegular() {
		return executionObject{}, fmt.Errorf("execution object is not a regular file; restore it from a trusted copy")
	}
	file, err := s.root.Open(key)
	if err != nil {
		return executionObject{}, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, executionObjectLimit+1))
	if err != nil {
		return executionObject{}, err
	}
	if len(data) > executionObjectLimit {
		return executionObject{}, fmt.Errorf("execution object exceeds size limit; inspect the stored object")
	}
	var obj executionObject
	if err := json.Unmarshal(data, &obj); err != nil {
		return executionObject{}, fmt.Errorf("decoding execution object: %w", err)
	}
	if obj.Revision == "" {
		return executionObject{}, fmt.Errorf("execution object has no revision; restore a valid record")
	}
	return obj, nil
}

// write uses a fresh revision even for identical bytes so a stale writer cannot mistake a later object for its original version.
func (s *localExecutionStore) write(ctx context.Context, key string, data []byte, revision string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := checkExecutionKey(key); err != nil {
		return "", err
	}
	old, err := s.readLocked(ctx, key)
	if err != nil && !errors.Is(err, errExecutionMissing) {
		return "", err
	}
	if (errors.Is(err, errExecutionMissing) && revision != "") || (err == nil && (revision == "" || revision != old.Revision)) {
		return "", errExecutionConflict
	}
	obj := executionObject{Revision: newExecutionID("rev"), Data: data}
	encoded, err := json.Marshal(obj)
	if err != nil {
		return "", err
	}
	if len(encoded) > executionObjectLimit {
		return "", fmt.Errorf("execution object exceeds size limit; reduce the plan size")
	}
	temp := ".pending-" + obj.Revision
	file, err := s.root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	defer func() { _ = s.root.Remove(temp) }()
	_, writeErr := file.Write(encoded)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := s.root.Rename(temp, key); err != nil {
		return "", err
	}
	if err := syncExecutionDirectory(s.root.Name()); err != nil {
		return "", err
	}
	return obj.Revision, nil
}

func (s *localExecutionStore) remove(ctx context.Context, key, revision string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, err := s.readLocked(ctx, key)
	if err != nil {
		return err
	}
	if revision == "" || revision != obj.Revision {
		return errExecutionConflict
	}
	if err := s.root.Remove(key); err != nil {
		return err
	}
	return syncExecutionDirectory(s.root.Name())
}

func (s *localExecutionStore) list(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir, err := s.root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	keys := []string{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pending-") {
			continue
		}
		if executionKeyPattern.MatchString(entry.Name()) {
			if !entry.Type().IsRegular() {
				return nil, fmt.Errorf("execution directory contains an unsafe object; restore regular files")
			}
			keys = append(keys, entry.Name())
		}
	}
	sort.Strings(keys)
	return keys, nil
}
