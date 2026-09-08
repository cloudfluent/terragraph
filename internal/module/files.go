package module

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hashicorp/terraform-config-inspect/tfconfig"
)

// FileMode selects declarations without executing a possibly arbitrary runtime wrapper.
type FileMode uint8

const (
	TerraformFiles FileMode = iota
	OpenTofuFiles
	// UnknownFiles accepts a wrapper only when both runtimes expose the same static schema.
	UnknownFiles
)

// FileModeForBinary trusts canonical commands or an absolute path identifying exactly one of them; a wrapper's basename cannot establish its runtime.
func FileModeForBinary(binary string) FileMode {
	switch binary {
	case "terraform":
		return TerraformFiles
	case "tofu":
		return OpenTofuFiles
	}
	// Relative executable paths resolve in the subprocess working directory, which this static API deliberately does not guess.
	if !filepath.IsAbs(binary) {
		return UnknownFiles
	}
	target, err := os.Stat(binary)
	if err != nil {
		return UnknownFiles
	}
	mode := UnknownFiles
	matches := 0
	for name, candidate := range map[string]FileMode{"terraform": TerraformFiles, "tofu": OpenTofuFiles} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		canonical, err := os.Stat(path)
		if err == nil && os.SameFile(target, canonical) {
			mode = candidate
			matches++
		}
	}
	if matches != 1 {
		return UnknownFiles
	}
	return mode
}

type moduleFile struct {
	os.FileInfo
	physical string
	alias    string
	override bool
}

func (f moduleFile) Name() string { return f.alias }

// selectedFiles preserves physical filename order and only replaces same-format .tf counterparts, matching OpenTofu's loader.
func selectedFiles(dir string, mode FileMode) ([]moduleFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading module directory %s: %w", dir, err)
	}
	present := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			present[entry.Name()] = true
		}
	}
	var primary, overrides []moduleFile
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") || strings.HasSuffix(name, "~") || strings.HasPrefix(name, "#") && strings.HasSuffix(name, "#") {
			continue
		}
		ext := configurationExtension(name)
		if ext == "" {
			continue
		}
		tofu := strings.HasPrefix(ext, ".tofu")
		if tofu && mode != OpenTofuFiles {
			continue
		}
		if !tofu && mode == OpenTofuFiles && present[strings.TrimSuffix(name, ext)+strings.Replace(ext, ".tf", ".tofu", 1)] {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("reading module file %s: %w", filepath.Join(dir, name), err)
		}
		base := strings.TrimSuffix(name, ext)
		f := moduleFile{FileInfo: info, physical: filepath.Join(dir, name), alias: base + strings.Replace(ext, ".tofu", ".tf", 1), override: base == "override" || strings.HasSuffix(base, "_override")}
		if f.override {
			overrides = append(overrides, f)
		} else {
			primary = append(primary, f)
		}
	}
	return append(primary, overrides...), nil
}

func configurationExtension(name string) string {
	for _, ext := range []string{".tf.json", ".tofu.json", ".tf", ".tofu"} {
		if strings.HasSuffix(name, ext) {
			return ext
		}
	}
	return ""
}

// inspectionFS aliases selected .tofu files in memory because tfconfig only recognizes Terraform extensions; module files are never rewritten.
type inspectionFS struct{ files []moduleFile }

func (fs inspectionFS) physical(name string) string {
	for _, f := range fs.files {
		if filepath.Base(name) == f.alias {
			return f.physical
		}
	}
	return name
}
func (fs inspectionFS) Open(name string) (tfconfig.File, error) { return os.Open(fs.physical(name)) }
func (fs inspectionFS) ReadFile(name string) ([]byte, error)    { return os.ReadFile(fs.physical(name)) }
func (fs inspectionFS) ReadDir(_ string) ([]os.FileInfo, error) {
	infos := make([]os.FileInfo, len(fs.files))
	for i, f := range fs.files {
		infos[i] = f
	}
	return infos, nil
}
