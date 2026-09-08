//go:build !windows

package engine

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApply_RuntimeSensitiveInputErrorsWithholdPayload(t *testing.T) {
	for _, metadata := range []string{`"sensitive":true,`, `"sensitive":null,`, "", `"sensitive":false,`} {
		t.Run(metadata, func(t *testing.T) {
			for _, node := range []string{"b", ""} {
				t.Run("node="+node, func(t *testing.T) {
					e := loadFallbackEngine(t, false)
					modulePath := filepath.Join(e.BaseDir, "module", "main.tf")
					src, err := os.ReadFile(modulePath)
					if err != nil {
						t.Fatal(err)
					}
					src = bytes.Replace(src, []byte(`default = ""`), []byte(`type = map(object({ count = number }))
  default = {}`), 1)
					if err := os.WriteFile(modulePath, src, 0o600); err != nil {
						t.Fatal(err)
					}
					scriptPath := filepath.Join(e.BaseDir, "terraform-fake")
					script, err := os.ReadFile(scriptPath)
					if err != nil {
						t.Fatal(err)
					}
					script = bytes.ReplaceAll(script, []byte(`"sensitive":false,`), []byte(metadata))
					script = bytes.ReplaceAll(script, []byte(`"value":"%s"`), []byte(`"value":{"PRIVATE_PAYLOAD_KEY":"%s"}`))
					if err := os.WriteFile(scriptPath, script, 0o755); err != nil {
						t.Fatal(err)
					}
					e, err = Load(filepath.Join(e.BaseDir, "blueprint.hcl"), e.Binary, e.Stdout, e.Stderr)
					if err != nil {
						t.Fatal(err)
					}
					runs, err := e.Apply(Options{Node: node, AutoApprove: true, Parallelism: 2})
					if metadata == `"sensitive":false,` {
						if err == nil || !strings.Contains(err.Error(), "PRIVATE_PAYLOAD_KEY") || strings.Contains(err.Error(), "value details withheld") {
							t.Fatalf("error = %v, want detailed public input error", err)
						}
						return
					}
					if err == nil || !strings.Contains(err.Error(), "value details withheld") {
						t.Fatalf("error = %v, want redacted runtime-sensitive input error", err)
					}
					if strings.Contains(e.Stdout.(*bytes.Buffer).String()+e.Stderr.(*bytes.Buffer).String(), "PRIVATE_PAYLOAD") {
						t.Fatal("sensitive payload appears in engine streams")
					}
					reported := []error{err}
					for _, run := range runs {
						reported = append(reported, run.Err)
					}
					for _, report := range reported {
						for cause := report; cause != nil; cause = errors.Unwrap(cause) {
							if strings.Contains(cause.Error(), "PRIVATE_PAYLOAD") {
								t.Fatal("sensitive payload remains in error chain")
							}
						}
					}
				})
			}
		})
	}
}
