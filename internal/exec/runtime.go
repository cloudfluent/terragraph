package exec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/hashicorp/go-version"
)

// RequireTofuFiles refuses runtimes that cannot read the .tofu declarations used by static validation before any input value or saved output is consumed.
func (r *Runner) RequireTofuFiles() error {
	var stdout bytes.Buffer
	probe := *r
	probe.Stdout = &stdout
	probe.Stderr = io.Discard
	probe.Stdin = nil
	if err := probe.run("version", "-json"); err != nil {
		return fmt.Errorf("cannot verify .tofu file support; use OpenTofu 1.8.0 or newer with working version -json output, or use only .tf/.tf.json files: %w", err)
	}
	var result struct {
		Version string `json:"terraform_version"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return fmt.Errorf("cannot verify .tofu file support; use OpenTofu 1.8.0 or newer with valid version -json output, or use only .tf/.tf.json files")
	}
	core, _, _ := strings.Cut(result.Version, "-")
	core, _, _ = strings.Cut(core, "+")
	actual, err := version.NewSemver(result.Version)
	minimum := version.Must(version.NewSemver("1.8.0"))
	if err != nil || len(strings.Split(core, ".")) != 3 || actual.LessThan(minimum) {
		return fmt.Errorf("selected .tofu/.tofu.json files require OpenTofu 1.8.0 or newer; upgrade the runtime or use only .tf/.tf.json files")
	}
	return nil
}
