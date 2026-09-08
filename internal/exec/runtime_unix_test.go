//go:build !windows

package exec

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequireTofuFiles_VersionBoundaryAndUnverifiableResponses(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		wantError      bool
	}{
		{"old", `{"terraform_version":"1.7.3"}`, true},
		{"prerelease", `{"terraform_version":"1.8.0-rc1"}`, true},
		{"minimum", `{"terraform_version":"1.8.0"}`, false},
		{"current", `{"terraform_version":"1.11.0"}`, false},
		{"missing", `{}`, true},
		{"null", `{"terraform_version":null}`, true},
		{"number", `{"terraform_version":108}`, true},
		{"unknown", `{"terraform_version":"synthetic-private-detail"}`, true},
		{"partial", `{"terraform_version":"2"}`, true},
		{"trailing", `{"terraform_version":"1.11.0"} {}`, true},
		{"malformed", `synthetic-private-detail`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "tofu")
			script := "#!/bin/sh\nif [ \"$1\" != version ] || [ \"$2\" != -json ]; then exit 81; fi\nprintf '%s' \"$TG_VERSION_RESPONSE\"\nprintf 'synthetic-private-stderr' >&2\n"
			if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			r := Runner{Binary: Binary(path), Dir: dir, Env: map[string]string{"TG_VERSION_RESPONSE": tc.response}, Stdout: &stdout, Stderr: &stderr}
			err := r.RequireTofuFiles()
			if (err != nil) != tc.wantError {
				t.Fatalf("RequireTofuFiles = %v, want error=%v", err, tc.wantError)
			}
			if err != nil && (!strings.Contains(err.Error(), "OpenTofu 1.8.0") || strings.Contains(err.Error(), "synthetic-private")) {
				t.Fatalf("error lacks remedy or exposes probe contents: %v", err)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("probe contaminated output: %q %q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestRequireTofuFiles_FailedProbePreservesErrorWithoutOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tofu")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'synthetic-private-detail'\nprintf 'synthetic-private-stderr' >&2\nexit 9\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	r := Runner{Binary: Binary(path), Dir: dir, Stdout: &stdout, Stderr: &stderr}
	err := r.RequireTofuFiles()
	if err == nil || !strings.Contains(err.Error(), "exit status 9") || strings.Contains(err.Error(), "synthetic-private") || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("probe error=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
}
