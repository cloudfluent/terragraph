// Command pluginpackage builds a first-party release directory without installing or trusting it in any blueprint.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	sdk "github.com/cloudfluent/terragraph/plugin"
	"github.com/cloudfluent/terragraph/plugins/debug"
	"github.com/cloudfluent/terragraph/plugins/secretsmanager"
)

func main() {
	choice := flag.String("plugin", "debug", "first-party plugin to package: debug or secretsmanager")
	output := flag.String("out", "", "new package directory; defaults to dist/plugins/PLUGIN")
	flag.Parse()
	descriptor, source := selectPlugin(*choice)
	if *output == "" {
		*output = filepath.Join("dist", "plugins", descriptor.Name)
	}
	if err := build(*output, descriptor, source); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// selectPlugin resolves the flag to one build target so adding a first-party plugin stays a one-line change here.
func selectPlugin(choice string) (sdk.Descriptor, string) {
	switch choice {
	case "secretsmanager":
		return secretsmanager.Descriptor(), "./plugins/secretsmanager/cmd"
	default:
		return debug.Descriptor(), "./plugins/debug/cmd"
	}
}

func build(output string, descriptor sdk.Descriptor, source string) error {
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
	if err := descriptor.Validate(); err != nil {
		return fmt.Errorf("plugin %s: %w", descriptor.Name, err)
	}
	cmd := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(temporary, descriptor.Executable), source)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("building %s plugin: %w", descriptor.Name, err)
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
