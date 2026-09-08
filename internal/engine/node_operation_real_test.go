package engine

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

func TestRunNode_RealImportAndSameStateMoves(t *testing.T) {
	binary := os.Getenv("TERRAGRAPH_TEST_RUNTIME")
	if binary == "" {
		t.Skip("set TERRAGRAPH_TEST_RUNTIME for native state-operation evidence")
	}
	dir := t.TempDir()
	module := filepath.Join(dir, "module")
	if err := os.Mkdir(module, 0700); err != nil {
		t.Fatal(err)
	}
	source := `terraform {
 backend "local" {}
}
resource "terraform_data" "first" {}
resource "terraform_data" "second" {}
`
	if err := os.WriteFile(filepath.Join(module, "main.tf"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	path := writeBlueprint(t, dir, `node "one" { source = "./module" }`)
	var output bytes.Buffer
	e, err := Load(path, exec.Binary(binary), &output, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"import", "terraform_data.first", "12345678-1234-1234-1234-123456789abc"}, {"state", "mv", "terraform_data.first", "terraform_data.second"}, {"state", "list"}, {"state", "rm", "terraform_data.second"}} {
		if err := e.RunNode("one", args); err != nil {
			t.Fatalf("%v: %v; output %s", args, err, output.String())
		}
	}
	entries, err := os.ReadDir(module)
	if err != nil || len(entries) != 1 || entries[0].Name() != "main.tf" {
		t.Fatalf("module was modified: %v, %v", entries, err)
	}
	records, err := e.ListExecutions()
	if err != nil {
		t.Fatal(err)
	}
	backups := 0
	for _, record := range records {
		if record.Backup {
			backups++
		}
	}
	if backups == 0 {
		t.Fatal("native state backups were not archived")
	}
}
