package exec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// BackendContext fingerprints the actual native cache so an external reconfiguration cannot silently retarget a scoped operation.
func (r *Runner) BackendContext() (string, error) {
	data, err := os.ReadFile(filepath.Join(r.DataDir, "terraform.tfstate"))
	if os.IsNotExist(err) {
		data = []byte("implicit-local:" + r.Dir)
	} else if err != nil {
		return "", fmt.Errorf("reading initialized backend context: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
