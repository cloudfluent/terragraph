package engine

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cloudfluent/terragraph/internal/exec"
)

func observationFixture(t *testing.T, backend string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "module"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"blueprint.hcl":                                "node \"first\" { source = \"./module\" }\nnode \"second\" { source = \"./module\" }\n",
		filepath.Join("module", "main.tf"):             "terraform {\n backend \"" + backend + "\" {}\n}\noutput \"large\" { value = 9007199254740993 }\noutput \"secret\" {\n value = \"CANARY\"\n sensitive = true\n}\n",
		filepath.Join("module", ".terraform.lock.hcl"): "",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const observationState = `{"version":4,"terraform_version":"1.5.7","serial":1,"lineage":"bbd09282-e69c-4c51-bdb5-f74971840b9a","outputs":{"large":{"value":9007199254740993,"type":"number","sensitive":false},"secret":{"value":"CANARY","type":"string","sensitive":false}},"resources":[]}`

func TestObservation_RealLocalRead(t *testing.T) {
	binary := os.Getenv("TERRAGRAPH_TEST_RUNTIME")
	if binary == "" {
		t.Skip("set TERRAGRAPH_TEST_RUNTIME to run real backend evidence")
	}
	dir := observationFixture(t, "local")
	stateDir := filepath.Join(dir, ".terragraph", "state")
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(stateDir, "first.tfstate")
	if err := os.WriteFile(statePath, []byte(observationState), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenObservation(context.Background(), filepath.Join(dir, "blueprint.hcl"), exec.Binary(binary), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	cache := s.dir
	defer s.Close()
	result := s.Read("first", false)
	if result.Diagnostic != nil {
		t.Fatalf("diagnostic = %+v", result.Diagnostic)
	}
	if got := result.Outputs["large"].Value; got == nil || got.(interface{ String() string }).String() != "9007199254740993" {
		t.Fatalf("got = %v, want exact integer", got)
	}
	if result.Outputs["secret"].Sensitive == nil || !*result.Outputs["secret"].Sensitive {
		t.Fatal("stale runtime sensitivity weakened current module")
	}
	state := s.Read("first", true)
	if state.Diagnostic != nil || state.State != "present" || state.OutputCount != 2 {
		t.Fatalf("got = %+v", state)
	}
	missing := s.Read("second", false)
	if missing.Diagnostic == nil || missing.Diagnostic.Code != "local_state_unavailable" {
		t.Fatalf("got = %+v", missing)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "second.tfstate")); !os.IsNotExist(err) {
		t.Fatalf("missing state was created: %v", err)
	}
	got, err := os.ReadFile(statePath)
	if err != nil || string(got) != observationState {
		t.Fatal("state changed during observation")
	}
	if _, err := os.Stat(filepath.Join(dir, ".terragraph", "tfdata")); !os.IsNotExist(err) {
		t.Fatal("execution cache touched")
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "module"))
	if len(entries) != 2 {
		t.Fatalf("module entries = %v", entries)
	}
	s.Close()
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Fatal("read cache survived close")
	}
}

func TestObservation_RealHTTPReadOnly(t *testing.T) {
	binary := os.Getenv("TERRAGRAPH_TEST_RUNTIME")
	if binary == "" {
		t.Skip("set TERRAGRAPH_TEST_RUNTIME to run real backend evidence")
	}
	var mu sync.Mutex
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		methods = append(methods, r.Method)
		mu.Unlock()
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		if r.URL.Path == "/missing" {
			w.WriteHeader(404)
			return
		}
		if r.URL.Path == "/denied" {
			w.WriteHeader(401)
			return
		}
		_, _ = io.WriteString(w, observationState)
	}))
	defer server.Close()
	dir := observationFixture(t, "http")
	if err := os.WriteFile(filepath.Join(dir, "module", "terraform.tfstate"), []byte(observationState), 0600); err != nil {
		t.Fatal(err)
	}
	blueprint := "node \"first\" {\n source = \"./module\"\n backend_config = { address = \"" + server.URL + "/state\" }\n}\nnode \"second\" {\n source = \"./module\"\n backend_config = { address = \"" + server.URL + "/missing\" }\n}\nnode \"denied\" {\n source = \"./module\"\n backend_config = { address = \"" + server.URL + "/denied\" }\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "blueprint.hcl"), []byte(blueprint), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenObservation(context.Background(), filepath.Join(dir, "blueprint.hcl"), exec.Binary(binary), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := s.Read("first", false); got.Diagnostic != nil || len(got.Outputs) != 2 {
		t.Fatalf("got = %+v", got)
	}
	if got := s.Read("first", true); got.Diagnostic != nil || got.State != "present" {
		t.Fatalf("got = %+v", got)
	}
	if got := s.Read("second", false); got.Diagnostic != nil || len(got.Outputs) != 0 {
		t.Fatalf("got = %+v", got)
	}
	if got := s.Read("second", true); got.Diagnostic != nil || got.State != "indeterminate" {
		t.Fatalf("got = %+v", got)
	}
	if got := s.Read("denied", true); got.Diagnostic == nil {
		t.Fatalf("got = %+v, want credential failure", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(methods) == 0 {
		t.Fatal("no runtime backend requests")
	}
	for _, method := range methods {
		if method != "GET" {
			t.Fatalf("got = %s, want GET only", method)
		}
	}
}

func TestObservation_PartialModuleAndWiring(t *testing.T) {
	dir := observationFixture(t, "local")
	f, err := os.OpenFile(filepath.Join(dir, "blueprint.hcl"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(f, "node \"broken\" { source = \"./missing\" }\nedge {\n from = node.first.output.nonexistent\n to = node.second.input.nonexistent\n}\n")
	_ = f.Close()
	s, err := OpenObservation(context.Background(), filepath.Join(dir, "blueprint.hcl"), exec.Terraform, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.engine.Graph.Nodes["first"].Schema == nil {
		t.Fatal("readable sibling lost")
	}
	if got := s.Read("broken", false); got.Diagnostic == nil || got.Diagnostic.Code != "module_unavailable" {
		t.Fatalf("got = %+v", got)
	}
	if _, err := s.Names("unknown"); err == nil || !strings.Contains(err.Error(), "first") {
		t.Fatalf("got = %v", err)
	}
}

func TestObservation_SessionsCoordinateAndClean(t *testing.T) {
	dir := observationFixture(t, "local")
	path := filepath.Join(dir, "blueprint.hcl")
	first, err := OpenObservation(context.Background(), path, exec.Terraform, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	firstDir := first.dir
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := OpenObservation(ctx, path, exec.Terraform, io.Discard); err == nil {
		t.Fatal("cancelled read acquired source lock")
	}
	first.Close()
	second, err := OpenObservation(context.Background(), path, exec.Terraform, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if second.dir == firstDir {
		t.Fatal("sessions reused a cache")
	}
	if _, err := os.Stat(firstDir); !os.IsNotExist(err) {
		t.Fatal("first cache survived")
	}
}

func TestObservation_DottedGroupIgnoresApplyInputs(t *testing.T) {
	dir := observationFixture(t, "local")
	group := filepath.Join(dir, "group")
	if err := os.Mkdir(group, 0700); err != nil {
		t.Fatal(err)
	}
	definition := "group \"service\" {\n node \"cluster\" {\n source = \"../module\"\n }\n}\n"
	if err := os.WriteFile(filepath.Join(group, "group.hcl"), []byte(definition), 0600); err != nil {
		t.Fatal(err)
	}
	bp := "runtime \"chosen\" { binary = \"tofu\" }\nuse \"service\" {\n as = \"checkout\"\n source = \"./group\"\n runtime = runtime.chosen\n env = { REGION = \"test\" }\n backend_config = { path = \"custom.tfstate\" }\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "blueprint.hcl"), []byte(bp), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenObservation(context.Background(), filepath.Join(dir, "blueprint.hcl"), exec.Terraform, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	names, err := s.Names("checkout.cluster")
	if err != nil || len(names) != 1 {
		t.Fatalf("got = %v, %v", names, err)
	}
	if _, err := s.Names("checkout"); err == nil || !strings.Contains(err.Error(), "checkout.cluster") {
		t.Fatalf("got = %v", err)
	}
	n := s.engine.Graph.Nodes[names[0]]
	if s.engine.runtimeFor(n.Name) != exec.OpenTofu || n.Env["REGION"] != "test" || n.BackendConfig["path"] != "custom.tfstate" {
		t.Fatalf("wrong resolved context: %+v", n)
	}
}

func TestObservation_RefusesUnverifiedPreparation(t *testing.T) {
	dir := observationFixture(t, "s3")
	s, err := OpenObservation(context.Background(), filepath.Join(dir, "blueprint.hcl"), exec.Terraform, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := s.Read("first", false); got.Diagnostic == nil || got.Diagnostic.Code != "unsupported_backend" {
		t.Fatalf("got = %+v", got)
	}
	s.engine.Graph.Nodes["first"].Env = map[string]string{"TF_WORKSPACE": "new"}
	if got := s.Read("first", false); got.Diagnostic == nil || got.Diagnostic.Code != "unsupported_workspace" {
		t.Fatalf("got = %+v", got)
	}
}

func TestReviewPlan_RealOutputOnly(t *testing.T) {
	binary := os.Getenv("TERRAGRAPH_TEST_RUNTIME")
	if binary == "" {
		t.Skip("set TERRAGRAPH_TEST_RUNTIME for real saved-plan evidence")
	}
	dir := observationFixture(t, "local")
	e, err := Load(filepath.Join(dir, "blueprint.hcl"), exec.Binary(binary), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := e.ReviewPlan(Options{Nodes: []string{"first"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	review := runs[0].Review
	if !review.Evidence || !*review.HasChanges || len(review.Resources) != 0 || len(review.Outputs) != 2 || review.PolicyDecision != "pass" {
		t.Fatalf("got = %+v", review)
	}
	if _, err := os.Stat(e.planPath("first")); !os.IsNotExist(err) {
		t.Fatal("inspection plan was not removed")
	}
	if _, err := os.Stat(filepath.Join(dir, ".terragraph", "state", "first.tfstate")); !os.IsNotExist(err) {
		t.Fatal("plan applied state")
	}
}
