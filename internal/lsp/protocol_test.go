package lsp_test

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudfluent/terragraph/internal/lsp"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

type protocolClient struct {
	protocol.UnimplementedClient
	diagnostics chan *protocol.PublishDiagnosticsParams
}

func (c *protocolClient) PublishDiagnostics(_ context.Context, params *protocol.PublishDiagnosticsParams) error {
	c.diagnostics <- params
	return nil
}

func startProtocol(t *testing.T) (context.Context, protocol.Server, *protocolClient, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	serverSide, clientSide := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- lsp.Serve(ctx, serverSide, serverSide); close(done) }()
	client := &protocolClient{diagnostics: make(chan *protocol.PublishDiagnosticsParams, 32)}
	_, conn, remote := protocol.NewClient(ctx, client, jsonrpc2.NewStream(clientSide))
	t.Cleanup(func() {
		cancel()
		_ = clientSide.Close()
		_ = serverSide.Close()
		_ = conn.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("server did not stop after closing the transport")
		}
	})
	result, err := remote.Initialize(ctx, &protocol.InitializeParams{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Capabilities.PositionEncoding != protocol.PositionEncodingKindUTF16 {
		t.Fatalf("position encoding = %v, want UTF-16", result.Capabilities.PositionEncoding)
	}
	if err := remote.Initialized(ctx, &protocol.InitializedParams{}); err != nil {
		t.Fatal(err)
	}
	return ctx, remote, client, done
}

func waitDiagnostics(t *testing.T, ctx context.Context, client *protocolClient, document uri.URI) []protocol.Diagnostic {
	t.Helper()
	for {
		select {
		case params := <-client.diagnostics:
			if params.URI == document {
				return params.Diagnostics
			}
		case <-ctx.Done():
			t.Fatalf("waiting for diagnostics for %s: %v", document, ctx.Err())
		}
	}
}

func protocolFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestServe_GroupDefinitionAndCompletionUseLocalScope(t *testing.T) {
	dir := t.TempDir()
	protocolFile(t, filepath.Join(dir, "inner", "main.tf"), `output "inner_id" { value = "ok" }`)
	protocolFile(t, filepath.Join(dir, "outer", "main.tf"), `output "outer_id" { value = "ok" }`)
	path := filepath.Join(dir, "group.hcl")
	text := `node "same" { source = "./outer" }
group "g" {
 node "same" { source = "./inner" }
 export {
  output "id" { from = node.same.output.inner_id }
 }
}`
	document := uri.File(path)
	ctx, remote, client, _ := startProtocol(t)
	if err := remote.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: document, Text: text}}); err != nil {
		t.Fatal(err)
	}
	if got := waitDiagnostics(t, ctx, client, document); len(got) != 0 {
		t.Fatalf("diagnostics = %#v, want none", got)
	}
	result, err := remote.Definition(ctx, &protocol.DefinitionParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: document}, Position: protocol.Position{Line: 4, Character: 28}}})
	if err != nil {
		t.Fatal(err)
	}
	location, ok := result.(*protocol.Location)
	if !ok || location.URI != document || location.Range.Start.Line != 2 {
		t.Fatalf("definition = %#v, want group node on line 2", result)
	}
	resultCompletion, err := remote.Completion(ctx, &protocol.CompletionParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: document}, Position: protocol.Position{Line: 4, Character: uint32(len(`  output "id" { from = node.same.output.`))}}})
	if err != nil {
		t.Fatal(err)
	}
	items, ok := resultCompletion.(protocol.CompletionItemSlice)
	if !ok || len(items) != 1 || items[0].Label != "inner_id" {
		t.Fatalf("completion = %#v, want inner_id", resultCompletion)
	}
}

func TestServe_SiblingDiagnosticsRefreshOnChangeAndClose(t *testing.T) {
	dir := t.TempDir()
	declaration := uri.File(filepath.Join(dir, "group.hcl"))
	reference := uri.File(filepath.Join(dir, "blueprint.hcl"))
	old := `node "old" { source = "./m" }`
	text := "edge {\n from = node.old\n to = node.old\n}"
	protocolFile(t, filepath.Join(dir, "group.hcl"), old)
	ctx, remote, client, _ := startProtocol(t)
	if err := remote.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: declaration, Text: old, Version: 1}}); err != nil {
		t.Fatal(err)
	}
	_ = waitDiagnostics(t, ctx, client, declaration)
	if err := remote.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: reference, Text: text, Version: 1}}); err != nil {
		t.Fatal(err)
	}
	if got := waitDiagnostics(t, ctx, client, reference); len(got) != 0 {
		t.Fatalf("initial diagnostics = %#v, want none", got)
	}
	if err := remote.DidChange(ctx, &protocol.DidChangeTextDocumentParams{TextDocument: protocol.VersionedTextDocumentIdentifier{URI: declaration, Version: 2}, ContentChanges: []protocol.TextDocumentContentChangeEvent{&protocol.TextDocumentContentChangeWholeDocument{Text: `node "new" { source = "./m" }`}}}); err != nil {
		t.Fatal(err)
	}
	if got := waitDiagnostics(t, ctx, client, reference); len(got) != 2 {
		t.Fatalf("changed diagnostics = %#v, want two missing old references", got)
	}
	if err := remote.DidClose(ctx, &protocol.DidCloseTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: declaration}}); err != nil {
		t.Fatal(err)
	}
	if got := waitDiagnostics(t, ctx, client, reference); len(got) != 0 {
		t.Fatalf("closed overlay diagnostics = %#v, want disk declaration restored", got)
	}
}

func TestServe_GroupExportEditsRefreshConsumerDiagnostics(t *testing.T) {
	dir := t.TempDir()
	groupPath := filepath.Join(dir, "g", "group.hcl")
	group := uri.File(groupPath)
	root := uri.File(filepath.Join(dir, "blueprint.hcl"))
	original := `group "g" {
 node "a" { source = "./m" }
 export {
  input "old" { to = node.a.input.id }
 }
}`
	protocolFile(t, groupPath, original)
	text := `use "g" {
 as = "g"
 source = "./g"
 vars = { old = "value" }
}`
	ctx, remote, client, _ := startProtocol(t)
	if err := remote.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: group, Text: original, Version: 1}}); err != nil {
		t.Fatal(err)
	}
	_ = waitDiagnostics(t, ctx, client, group)
	if err := remote.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: root, Text: text, Version: 1}}); err != nil {
		t.Fatal(err)
	}
	if got := waitDiagnostics(t, ctx, client, root); len(got) != 0 {
		t.Fatalf("initial consumer diagnostics = %#v, want none", got)
	}
	changed := strings.ReplaceAll(original, "\"old\"", "\"fresh\"")
	if err := remote.DidChange(ctx, &protocol.DidChangeTextDocumentParams{TextDocument: protocol.VersionedTextDocumentIdentifier{URI: group, Version: 2}, ContentChanges: []protocol.TextDocumentContentChangeEvent{&protocol.TextDocumentContentChangeWholeDocument{Text: changed}}}); err != nil {
		t.Fatal(err)
	}
	got := waitDiagnostics(t, ctx, client, root)
	if len(got) != 1 || !strings.Contains(string(got[0].Message.(protocol.String)), "Unknown input old") {
		t.Fatalf("consumer diagnostics = %#v, want unknown old input", got)
	}
}

func TestServe_ShutdownThenExitStopsWithoutClientEOF(t *testing.T) {
	ctx, remote, _, done := startProtocol(t)
	if err := remote.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := remote.Exit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve error = %v, want clean shutdown", err)
		}
	case <-time.After(time.Second):
		t.Fatal("server is still running after shutdown and exit with client transport open")
	}
}

func TestServe_ExitWithoutShutdownReportsFailure(t *testing.T) {
	ctx, remote, _, done := startProtocol(t)
	if err := remote.Exit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Serve error = nil, want exit without shutdown failure")
		}
	case <-time.After(time.Second):
		t.Fatal("server is still running after exit")
	}
}

func TestServe_RequestsAfterShutdownAreRejected(t *testing.T) {
	ctx, remote, _, _ := startProtocol(t)
	if err := remote.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := remote.Completion(ctx, &protocol.CompletionParams{})
	if !errors.Is(err, jsonrpc2.ErrInvalidRequest) {
		t.Fatalf("completion after shutdown error = %v, want InvalidRequest", err)
	}
}

func TestServe_CompletionClampsToTheRequestedLine(t *testing.T) {
	ctx, remote, client, _ := startProtocol(t)
	document := uri.File(filepath.Join(t.TempDir(), "blueprint.hcl"))
	text := "# 한글 😀\r\nnode \"a\" {\r\n source = \"./m\"\r\n}\r\n"
	if err := remote.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: document, Text: text}}); err != nil {
		t.Fatal(err)
	}
	_ = waitDiagnostics(t, ctx, client, document)
	result, err := remote.Completion(ctx, &protocol.CompletionParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: document}, Position: protocol.Position{Line: 1, Character: 999}}})
	if err != nil {
		t.Fatal(err)
	}
	items, ok := result.(protocol.CompletionItemSlice)
	if !ok {
		t.Fatalf("completion = %#v, want node attributes", result)
	}
	found := false
	for _, item := range items {
		if item.Label == "node" {
			t.Fatalf("completion = %#v, want node attributes rather than top-level blocks", items)
		}
		if item.Label == "source" {
			found = true
		}
		edit := item.TextEdit.(*protocol.TextEdit)
		if edit.Range.Start.Line != 1 || edit.Range.End.Line != 1 {
			t.Fatalf("edit range = %#v, want requested line 1", edit.Range)
		}
	}
	if !found {
		t.Fatalf("completion = %#v, want source", items)
	}
}
