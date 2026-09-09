package plugins

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/hashicorp/go-version"
)

const lockFilename = "terragraph.plugins.lock.json"

// LockEntry binds a reviewed declaration to immutable package bytes for exactly one platform.
type LockEntry struct {
	Alias    string `json:"alias"`
	Source   string `json:"source"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
	Digest   string `json:"digest"`
}

type lockFile struct {
	Protocol int         `json:"protocol"`
	Packages []LockEntry `json:"packages"`
}

func readLock(dir string) (lockFile, error) {
	data, err := os.ReadFile(filepath.Join(dir, lockFilename))
	if os.IsNotExist(err) {
		return lockFile{Protocol: 1}, nil
	}
	if err != nil {
		return lockFile{}, fmt.Errorf("reading plugin lock: %w", err)
	}
	var lock lockFile
	if err := json.Unmarshal(data, &lock); err != nil {
		return lock, fmt.Errorf("decoding plugin lock: %w", err)
	}
	if lock.Protocol != 1 {
		return lock, fmt.Errorf("unsupported plugin lock protocol")
	}
	seen := map[string]bool{}
	for _, entry := range lock.Packages {
		key := entry.Alias + ":" + entry.Platform
		if seen[key] || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(entry.Digest) {
			return lock, fmt.Errorf("invalid plugin lock entry; regenerate the lock with plugin install")
		}
		seen[key] = true
	}
	return lock, nil
}

func matches(config blueprint.PluginConfig, p Package) error {
	constraint, err := version.NewConstraint(config.Version)
	if err != nil {
		return fmt.Errorf("plugin.%s.version: %w", config.Name, err)
	}
	v, err := version.NewVersion(p.Descriptor.Version)
	if err != nil || !constraint.Check(v) {
		return fmt.Errorf("plugin.%s: package version does not satisfy %q", config.Name, config.Version)
	}
	return nil
}

// Resolve never downloads or updates packages while a graph command is running.
func Resolve(dir string, config blueprint.PluginConfig) (Package, error) {
	lock, err := readLock(dir)
	if err != nil {
		return Package{}, err
	}
	for _, entry := range lock.Packages {
		if entry.Alias != config.Name || entry.Platform != Platform() {
			continue
		}
		if entry.Source != config.Source {
			return Package{}, fmt.Errorf("plugin.%s: source differs from lock; run plugin install", config.Name)
		}
		p, err := Inspect(filepath.Join(dir, ".terragraph", "plugins", "packages", entry.Digest, Platform()))
		if err != nil {
			return p, fmt.Errorf("plugin.%s: %w; run plugin install", config.Name, err)
		}
		if p.Digest != entry.Digest || p.Descriptor.Version != entry.Version {
			return Package{}, fmt.Errorf("plugin.%s: package checksum mismatch; reinstall from a trusted package", config.Name)
		}
		return p, matches(config, p)
	}
	return Package{}, fmt.Errorf("plugin.%s: no locked package for %s; run plugin install", config.Name, Platform())
}

// Install accepts an explicitly supplied local release package; fetching and archive extraction are not implicit trust decisions.
func Install(dir string, config blueprint.PluginConfig, sourceDir string, locked bool) error {
	p, err := Inspect(sourceDir)
	if err != nil {
		return err
	}
	if err := matches(config, p); err != nil {
		return err
	}
	lock, err := readLock(dir)
	if err != nil {
		return err
	}
	entry := LockEntry{Alias: config.Name, Source: config.Source, Version: p.Descriptor.Version, Platform: Platform(), Digest: p.Digest}
	index := -1
	for i, old := range lock.Packages {
		if old.Alias == entry.Alias && old.Platform == entry.Platform {
			index = i
		}
	}
	if locked && (index < 0 || lock.Packages[index] != entry) {
		return fmt.Errorf("plugin.%s: package does not match lock; supply the locked release package", config.Name)
	}
	parent := filepath.Join(dir, ".terragraph", "plugins", "packages", p.Digest)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(parent, ".install-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	for _, name := range []string{"plugin.json", p.Descriptor.Executable} {
		if err := copyPackageFile(filepath.Join(sourceDir, name), filepath.Join(tmp, name), name == p.Descriptor.Executable); err != nil {
			return err
		}
	}
	copied, err := Inspect(tmp)
	if err != nil {
		return err
	}
	if copied.Digest != p.Digest {
		return fmt.Errorf("plugin package changed during installation; retry from an immutable release")
	}
	destination := filepath.Join(parent, Platform())
	if existing, err := Inspect(destination); err == nil && existing.Digest == p.Digest {
		// Existing verified bytes can be reused without replacing an executable in use on Windows.
	} else if err := os.Rename(tmp, destination); err != nil {
		return fmt.Errorf("publishing plugin package: %w; remove the damaged package directory and reinstall", err)
	}
	if locked {
		return nil
	}
	if index < 0 {
		lock.Packages = append(lock.Packages, entry)
	} else {
		lock.Packages[index] = entry
	}
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".plugins-lock-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, lockFilename))
}

func copyPackageFile(source, destination string, executable bool) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	mode := os.FileMode(0600)
	if executable {
		mode = 0700
	}
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}
