// Package plugins verifies executable packages and contains their RPC sessions; the engine retains all infrastructure scheduling and authorization decisions.
package plugins

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	sdk "github.com/cloudfluent/terragraph/plugin"
)

// Package pins both metadata and executable bytes so a changed descriptor cannot silently grant new capabilities.
type Package struct {
	Descriptor       sdk.Descriptor
	Path             string
	Digest           string
	ExecutableDigest string
}

// Inspect reads metadata without running code, including when the executable is incompatible with this platform.
func Inspect(dir string) (Package, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return Package{}, fmt.Errorf("opening plugin package: %w", err)
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open("plugin.json")
	if err != nil {
		return Package{}, fmt.Errorf("reading plugin descriptor: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(f, sdk.MaxMessageSize+1))
	_ = f.Close()
	if err != nil {
		return Package{}, fmt.Errorf("reading plugin descriptor: %w", err)
	}
	if len(data) > sdk.MaxMessageSize {
		return Package{}, fmt.Errorf("plugin descriptor exceeds size limit")
	}
	var d sdk.Descriptor
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&d); err != nil {
		return Package{}, fmt.Errorf("decoding plugin descriptor: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Package{}, fmt.Errorf("plugin descriptor has trailing content")
	}
	if err := d.Validate(); err != nil {
		return Package{}, err
	}
	exe, err := root.Open(d.Executable)
	if err != nil {
		return Package{}, fmt.Errorf("opening plugin executable: %w", err)
	}
	defer func() { _ = exe.Close() }()
	info, err := exe.Stat()
	if err != nil {
		return Package{}, fmt.Errorf("inspecting plugin executable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Package{}, fmt.Errorf("plugin executable must be a regular file")
	}
	h := sha256.New()
	if _, err := io.Copy(h, exe); err != nil {
		return Package{}, fmt.Errorf("hashing plugin executable: %w", err)
	}
	executableDigest := hex.EncodeToString(h.Sum(nil))
	_, _ = h.Write(data)
	path, err := filepath.Abs(filepath.Join(dir, d.Executable))
	if err != nil {
		return Package{}, fmt.Errorf("resolving plugin executable: %w", err)
	}
	return Package{Descriptor: d, Path: path, Digest: hex.EncodeToString(h.Sum(nil)), ExecutableDigest: executableDigest}, nil
}

// Platform prevents a lock entry from accidentally selecting an executable built for another OS or architecture.
func Platform() string { return runtime.GOOS + "_" + runtime.GOARCH }
