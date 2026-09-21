package secretsmanager

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudfluent/terragraph/internal/plugins"
	sdk "github.com/cloudfluent/terragraph/plugin"
	"github.com/zclconf/go-cty/cty"
)

// e2eForwardedEnv carries only what the AWS default chain reads, mirroring what a runtime access.environment allowlist would forward; the host drops all other ambient variables.
var e2eForwardedEnv = []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_PROFILE", "HOME"}

// forwardedEnvironment renders KEY=VALUE pairs for the names that are actually set, because plugins.Open replaces the whole process environment.
func forwardedEnvironment() []string {
	var environment []string
	for _, name := range e2eForwardedEnv {
		if value, ok := os.LookupEnv(name); ok {
			environment = append(environment, name+"="+value)
		}
	}
	return environment
}

// buildPackage produces the same two files tools/pluginpackage emits (executable plus its own --descriptor output) so the e2e runs the shipped artifact, not the test process.
func buildPackage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	descriptor := Descriptor()
	build := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(dir, descriptor.Executable), "./cmd")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building package: %v: %s", err, output)
	}
	emit := exec.Command(filepath.Join(dir, descriptor.Executable), "--descriptor")
	descriptorJSON, err := emit.Output()
	if err != nil {
		t.Fatalf("emitting descriptor: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), descriptorJSON, 0644); err != nil {
		t.Fatalf("writing plugin.json: %v", err)
	}
	return dir
}

// TestE2E_PackagedReadResolvesRealSecret runs the locally built package through the real host session (verify, launch, gRPC, configure) and resolves a live secret; env-gated because it needs AWS or localstack.
func TestE2E_PackagedReadResolvesRealSecret(t *testing.T) {
	if os.Getenv("TG_E2E_AWS") != "1" {
		t.Skip("set TG_E2E_AWS=1 with TG_E2E_SECRET_ID (plus TG_E2E_REGION, or TG_E2E_ENDPOINT for localstack) to run this against a real Secrets Manager")
	}
	secretID := os.Getenv("TG_E2E_SECRET_ID")
	if secretID == "" {
		t.Fatalf("TG_E2E_AWS=1 requires TG_E2E_SECRET_ID; set it to the secret name or ARN the resolver should fetch")
	}
	region := os.Getenv("TG_E2E_REGION")
	if region == "" {
		region = "us-east-1"
	}
	packageDir := buildPackage(t)
	pkg, err := plugins.Inspect(packageDir)
	if err != nil {
		t.Fatalf("inspecting built package: %v", err)
	}
	session, err := plugins.Open(context.Background(), pkg, t.TempDir(), forwardedEnvironment())
	if err != nil {
		t.Fatalf("opening host session for built package: %v", err)
	}
	t.Cleanup(session.Close)

	config := map[string]any{"region": region}
	if endpoint := os.Getenv("TG_E2E_ENDPOINT"); endpoint != "" {
		config["endpoint"] = endpoint
	}
	if _, err := session.Call(context.Background(), sdk.Request{Action: "configure", Config: config}, 10*time.Second); err != nil {
		t.Fatalf("configure through host session: %v", err)
	}

	reference := map[string]any{"secret_id": secretID}
	if pointer := os.Getenv("TG_E2E_SECRET_JSON_POINTER"); pointer != "" {
		reference["json_pointer"] = pointer
	}
	if field := os.Getenv("TG_E2E_SECRET_FIELD"); field != "" {
		reference["field"] = field
	}
	response, err := session.Call(context.Background(), sdk.Request{Action: "resolve", Feature: "read", Reference: reference}, 60*time.Second)
	if err != nil {
		t.Fatalf("resolve through host session: %v", err)
	}
	if response.Fault != nil {
		t.Fatalf("resolve faulted with code %q; check the secret exists and the credentials can read it", response.Fault.Code)
	}
	if response.Value == nil || !response.Value.Sensitive {
		t.Fatalf("resolve value = %#v, want a sensitive typed value", response.Value)
	}
	value, err := response.Value.Cty()
	if err != nil || value.Type() != cty.String || value.AsString() == "" {
		t.Fatalf("resolved value = %v (%v), want a non-empty string", value, err)
	}
	if want := os.Getenv("TG_E2E_SECRET_VALUE"); want != "" && value.AsString() != want {
		t.Fatalf("resolved value = %q, want TG_E2E_SECRET_VALUE %q", value.AsString(), want)
	}
}
