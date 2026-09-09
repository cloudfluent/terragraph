package plugins_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cloudfluent/terragraph/internal/blueprint"
	"github.com/cloudfluent/terragraph/internal/engine"
	"github.com/cloudfluent/terragraph/internal/exec"
	"github.com/cloudfluent/terragraph/internal/plugins"
	sdk "github.com/cloudfluent/terragraph/plugin"
	"github.com/zclconf/go-cty/cty"
)

var fixtureDir string

func fixtureDescriptor() sdk.Descriptor {
	executable := "plugin-fixture"
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	features := []sdk.Feature{}
	for _, name := range []string{"identity", "panic", "hang", "failure", "secret", "environment", "logs", "wait", "flood", "retryable", "oversize"} {
		features = append(features, sdk.Feature{Name: name, Kind: "function", Effect: "pure", Parameters: []sdk.Parameter{{Name: "value", Type: json.RawMessage(`"string"`)}}, ResultType: json.RawMessage(`"string"`)})
	}
	return sdk.Descriptor{Name: "fixture", Version: "1.2.0", Protocol: sdk.ProtocolVersion, Executable: executable, Features: features}
}

func TestMain(m *testing.M) {
	if strings.Contains(filepath.Base(os.Args[0]), "terraform-lifecycle") {
		runLifecycleTerraform()
		return
	}
	if os.Getenv("TERRAGRAPH_PLUGIN") == "terragraph-plugin-v1" && strings.Contains(filepath.Base(os.Args[0]), "lifecycle-plugin") {
		serveLifecycleFixture()
		return
	}
	if os.Getenv("TERRAGRAPH_PLUGIN") == "terragraph-plugin-v1" {
		sdk.Serve(fixtureDescriptor(), func(ctx context.Context, r sdk.Request) (sdk.Response, error) {
			if r.Action == "configure" {
				return sdk.Response{}, nil
			}
			switch r.Feature {
			case "logs":
				sdk.Logger(ctx).Debug("debug detail")
				sdk.Logger(ctx).Info("public progress", "token", sdk.Secret("private-secret-token"), "plugin", "spoof")
			case "retryable":
				return sdk.Response{Fault: &sdk.Fault{Code: "provider_unavailable", Retryable: true}}, nil
			case "oversize":
				v, _ := sdk.EncodeValue(cty.StringVal(strings.Repeat("x", 5<<20)), false)
				return sdk.Response{Value: &v}, nil
			case "flood":
				for i := 0; i < 10000; i++ {
					sdk.Logger(ctx).Info("flood", "index", i)
				}
			case "wait":
				sdk.Logger(ctx).Info("waiting for release")
				path, _ := r.Arguments[0].Decode()
				ticker := time.NewTicker(5 * time.Millisecond)
				defer ticker.Stop()
				for {
					if _, err := os.Stat(path.(string)); err == nil {
						break
					}
					select {
					case <-ctx.Done():
						return sdk.Response{}, ctx.Err()
					case <-ticker.C:
					}
				}

			case "panic":
				panic("private-secret-panic")
			case "failure":
				return sdk.Response{}, fmt.Errorf("private-secret-error")
			case "hang":
				<-ctx.Done()
				return sdk.Response{}, ctx.Err()
			case "environment":
				value, _ := sdk.EncodeValue(cty.StringVal(os.Getenv("PLUGIN_PRIVATE_TOKEN")), false)
				return sdk.Response{Value: &value}, nil
			case "secret":
				value, _ := sdk.EncodeValue(cty.StringVal("private-secret-value"), true)
				return sdk.Response{Value: &value}, nil
			}
			return sdk.Response{Value: &r.Arguments[0]}, nil
		})
		return
	}
	dir, err := os.MkdirTemp("", "terragraph-plugin-fixture-")
	if err != nil {
		panic(err)
	}
	fixtureDir = dir
	d := fixtureDescriptor()
	data, _ := json.Marshal(d)
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), data, 0600); err != nil {
		panic(err)
	}
	path, err := os.Executable()
	if err != nil {
		panic(err)
	}
	in, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	out, err := os.OpenFile(filepath.Join(dir, d.Executable), os.O_CREATE|os.O_WRONLY, 0700)
	if err != nil {
		panic(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		panic(err)
	}
	_ = in.Close()
	_ = out.Close()
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func openFixture(t *testing.T) *plugins.Session {
	t.Helper()
	p, err := plugins.Inspect(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := plugins.Open(context.Background(), p, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func request(feature string) sdk.Request {
	v, _ := sdk.EncodeValue(cty.StringVal("hello"), false)
	return sdk.Request{Action: "function", Feature: feature, Arguments: []sdk.Value{v}}
}

func TestSession_PanicQuarantinesProcess(t *testing.T) {
	s := openFixture(t)
	_, err := s.Call(context.Background(), request("panic"), time.Second)
	if err == nil || strings.Contains(err.Error(), "private-secret") {
		t.Fatalf("got = %v, want sanitized fatal failure", err)
	}
	_, err = s.Call(context.Background(), request("identity"), time.Second)
	if err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("got = %v, want quarantined session", err)
	}
}

func TestSession_TimeoutQuarantinesProcess(t *testing.T) {
	s := openFixture(t)
	start := time.Now()
	_, err := s.Call(context.Background(), request("hang"), 20*time.Millisecond)
	if err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("got = %v after %v, want bounded failure", err, time.Since(start))
	}
	_, err = s.Call(context.Background(), request("identity"), time.Second)
	if err == nil {
		t.Fatal("timed out session was reused")
	}
}

func TestSession_OrdinaryFailureDoesNotLeakOrQuarantine(t *testing.T) {
	s := openFixture(t)
	_, err := s.Call(context.Background(), request("failure"), time.Second)
	if err == nil || strings.Contains(err.Error(), "private-secret") {
		t.Fatalf("got = %v, want sanitized failure", err)
	}
	r, err := s.Call(context.Background(), request("identity"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	v, err := r.Value.Decode()
	if err != nil || v != "hello" {
		t.Fatalf("got = %v, %v, want hello", v, err)
	}
}

func TestSession_DoesNotInheritCredentials(t *testing.T) {
	t.Setenv("PLUGIN_PRIVATE_TOKEN", "private-secret-token")
	s := openFixture(t)
	r, err := s.Call(context.Background(), request("environment"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	v, err := r.Value.Decode()
	if err != nil || v != "" {
		t.Fatalf("got = %v, %v, want empty environment", v, err)
	}
}

func installFixture(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	declaration := "plugin \"test\" {\n source = \"example/fixture\"\n version = \"~> 1.2\"\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "blueprint.hcl"), []byte(declaration+body), 0600); err != nil {
		t.Fatal(err)
	}
	configs, _, err := blueprint.LoadPlugins(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := plugins.Install(dir, configs[0], fixtureDir, false); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestEvaluate_FunctionsReachNodeAndGroupVars(t *testing.T) {
	dir := installFixture(t, "node \"app\" {\n source = \"./module\"\n vars = { name = test_identity(\"application\") }\n}\nuse \"component\" {\n as = \"shared\"\n source = \"./group\"\n}\n")
	if err := os.Mkdir(filepath.Join(dir, "module"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "module", "main.tf"), []byte("variable \"name\" { type = string }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "group"), 0700); err != nil {
		t.Fatal(err)
	}
	group := "group \"component\" {\n node \"child\" {\n source = \"../module\"\n vars = { name = test_identity(\"child\") }\n }\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "group", "group.hcl"), []byte(group), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := engine.Load(dir, exec.Terraform, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if e.Graph.Nodes["app"].Vars["name"] != "application" {
		t.Fatalf("got = %v, want application", e.Graph.Nodes["app"].Vars)
	}
	if e.Graph.Nodes["shared.child"].Vars["name"] != "child" {
		t.Fatalf("got = %v, want child", e.Graph.Nodes)
	}
	entries, err := os.ReadDir(filepath.Join(dir, ".terragraph", "plugins", "work"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("got = %v, want cleaned session work directories", entries)
	}
}

func TestEvaluate_RejectsSensitiveFunctionResult(t *testing.T) {
	dir := installFixture(t, "node \"app\" {\n source = \"./module\"\n vars = { name = test_secret(\"input\") }\n}\n")
	_, err := engine.Load(dir, exec.Terraform, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "non-sensitive") || strings.Contains(err.Error(), "private-secret-value") {
		t.Fatalf("got = %v, want sanitized sensitive-value rejection", err)
	}
}

func TestResolve_RejectsTamperedPackage(t *testing.T) {
	dir := installFixture(t, "")
	configs, _, _ := blueprint.LoadPlugins(dir)
	p, err := plugins.Resolve(dir, configs[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Path, []byte("tampered"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err = plugins.Resolve(dir, configs[0])
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("got = %v, want checksum rejection", err)
	}
}

func TestInstall_LockedRejectsChangedSource(t *testing.T) {
	dir := installFixture(t, "")
	configs, _, _ := blueprint.LoadPlugins(dir)
	configs[0].Source = "another/fixture"
	if err := plugins.Install(dir, configs[0], fixtureDir, true); err == nil {
		t.Fatal("changed source accepted by locked install")
	}
}

func TestMetadata_DoesNotNeedPluginPackage(t *testing.T) {
	dir := t.TempDir()
	body := "plugin \"missing\" {\n source = \"example/missing\"\n version = \"1.0.0\"\n}\nnode \"app\" {\n source = \"missing\"\n vars = { name = missing_name(\"app\") }\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "blueprint.hcl"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	_, close, err := engine.OpenExecutionHistory(context.Background(), dir, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	close()
}
