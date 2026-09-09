// Command pluginpackage builds a first-party release directory without installing or trusting it in any blueprint.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cloudfluent/terragraph/plugins/debug"
)

func main() {
	output := flag.String("out", "dist/plugins/debug", "new package directory")
	flag.Parse()
	if err := build(*output); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func build(output string) error {
	if _, err := os.Stat(output); err == nil {
		return fmt.Errorf("output directory exists; choose a new directory")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(filepath.Dir(output), ".package-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(temporary) }()
	descriptor := debug.Descriptor()
	cmd := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(temporary, descriptor.Executable), "./plugins/debug/cmd")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("building debug plugin: %w", err)
	}
	data, err := json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(temporary, "plugin.json"), append(data, '\n'), 0644); err != nil {
		return err
	}
	if err := os.Rename(temporary, output); err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, output)
	return err
}
