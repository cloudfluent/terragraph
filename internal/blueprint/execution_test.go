package blueprint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func parseExecutionFixture(t *testing.T, body string) (*Blueprint, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "blueprint.hcl")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return ParseFile(path)
}

func TestParse_ExecutionLocalDefaults(t *testing.T) {
	bp, err := parseExecutionFixture(t, `execution {}`)
	if err != nil {
		t.Fatal(err)
	}
	cfg := bp.ExecutionSettings()
	if cfg.Bucket != "" || cfg.PlanTTL != 24*time.Hour || cfg.RecordRetention != 720*time.Hour {
		t.Fatalf("got = %+v", cfg)
	}
}

func TestParse_ExecutionS3RequiresSharedLock(t *testing.T) {
	_, err := parseExecutionFixture(t, "execution {\n bucket = \"bucket\"\n prefix = \"executions\"\n region = \"us-east-1\"\n}\n")
	if err == nil || !strings.Contains(err.Error(), "shared lock") {
		t.Fatalf("got = %v", err)
	}
}

func TestParse_ExecutionCannotExpireGraphLock(t *testing.T) {
	_, err := parseExecutionFixture(t, "execution {\n bucket = \"bucket\"\n prefix = \"executions\"\n region = \"us-east-1\"\n}\nlock {\n s3 {\n bucket = \"bucket\"\n key = \"executions/lock\"\n region = \"us-east-1\"\n }\n}\n")
	if err == nil || !strings.Contains(err.Error(), "contains the graph lock") {
		t.Fatalf("got = %v", err)
	}
}

func TestParse_ExecutionDurationsAndRemoteStorage(t *testing.T) {
	bp, err := parseExecutionFixture(t, "execution {\n bucket = \"bucket\"\n prefix = \"executions/\"\n region = \"us-east-1\"\n plan_ttl = \"6h\"\n record_retention = \"48h\"\n}\nlock {\n s3 {\n bucket = \"bucket\"\n key = \"locks/graph\"\n region = \"us-east-1\"\n }\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	cfg := bp.ExecutionSettings()
	if cfg.Prefix != "executions" || cfg.PlanTTL != 6*time.Hour || cfg.RecordRetention != 48*time.Hour {
		t.Fatalf("got = %+v", cfg)
	}
}

func TestParse_ExecutionRejectsInvalidDurationsAndPartialS3(t *testing.T) {
	for _, body := range []string{`execution { plan_ttl = "0h" }`, `execution { record_retention = "forever" }`, `execution { bucket = "bucket" }`, `execution { plan_ttl = null }`, `execution { plan_ttl = 3 }`, `execution {} execution {}`} {
		if _, err := parseExecutionFixture(t, body); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}
