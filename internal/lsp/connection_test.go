package lsp

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// diagnosticClient observes protocol notifications without sharing mutable state with the connection's reader goroutine.
type diagnosticClient struct {
	protocol.UnimplementedClient
	documents chan uri.URI
}

func (c *diagnosticClient) PublishDiagnostics(_ context.Context, params *protocol.PublishDiagnosticsParams) error {
	c.documents <- params.URI
	return nil
}

func TestServe_NullDocumentKeepsConnectionResponsive(t *testing.T) {
	dir := t.TempDir()
	module := filepath.Join(dir, "module")
	if err := os.MkdirAll(module, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, "main.tf"), []byte(`variable "name" { type = string }`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	serverPipe, clientPipe := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, serverPipe, serverPipe) }()
	t.Cleanup(func() {
		defer cancel()
		_ = clientPipe.Close()
		_ = serverPipe.Close()
		select {
		case <-done:
		case <-ctx.Done():
			t.Error("Serve did not stop after transport close")
		}
	})
	client := &diagnosticClient{documents: make(chan uri.URI, 4)}
	_, conn, remote := protocol.NewClient(ctx, client, jsonrpc2.NewStream(clientPipe))
	defer func() { _ = conn.Close() }()
	if _, err := remote.Initialize(ctx, &protocol.InitializeParams{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	documentURI := uri.File(filepath.Join(dir, "blueprint.hcl"))
	text := "node \"bad\" { source = true ? null : \"./module\" }\nnode \"good\" {\n source = \"./module\"\n vars = {\n  \n }\n}\n"
	if err := remote.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: documentURI, LanguageID: "hcl", Version: 1, Text: text}}); err != nil {
		t.Fatalf("didOpen: %v", err)
	}
	waitForDiagnostics := func() {
		t.Helper()
		select {
		case got := <-client.documents:
			if got != documentURI {
				t.Fatalf("diagnostics URI = %q, want %q", got, documentURI)
			}
		case <-ctx.Done():
			t.Fatal("diagnostics were not published")
		}
	}
	waitForDiagnostics()
	params := &protocol.CompletionParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: documentURI},
		Position:     protocol.Position{Line: 4, Character: 2},
	}}
	assertCompletion := func() {
		t.Helper()
		result, err := remote.Completion(ctx, params)
		if err != nil {
			t.Fatalf("completion: %v", err)
		}
		items, ok := result.(protocol.CompletionItemSlice)
		if !ok || len(items) != 1 || items[0].Label != "name" {
			t.Fatalf("completion = %+v, want name", result)
		}
	}
	assertCompletion()
	text = "node \"bad\" { source = \"./module\" }\nnode \"good\" {\n source = \"./module\"\n vars = {\n  \n }\n}\n"
	if err := remote.DidChange(ctx, &protocol.DidChangeTextDocumentParams{
		TextDocument:   protocol.VersionedTextDocumentIdentifier{TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: documentURI}, Version: 2},
		ContentChanges: []protocol.TextDocumentContentChangeEvent{&protocol.TextDocumentContentChangeWholeDocument{Text: text}},
	}); err != nil {
		t.Fatalf("didChange: %v", err)
	}
	waitForDiagnostics()
	assertCompletion()
	if err := remote.DidClose(ctx, &protocol.DidCloseTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: documentURI}}); err != nil {
		t.Fatalf("didClose: %v", err)
	}
	waitForDiagnostics()
}
