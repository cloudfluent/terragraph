package lsp_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
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
