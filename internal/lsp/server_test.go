package lsp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudfluent/terragraph/internal/language"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

type recordingClient struct {
	protocol.UnimplementedClient
	params *protocol.PublishDiagnosticsParams
}

func (c *recordingClient) PublishDiagnostics(_ context.Context, params *protocol.PublishDiagnosticsParams) error {
	c.params = params
	return nil
}

func TestPositionRoundTripUTF16(t *testing.T) {
	text := []byte("node \U0001f600\noutput")
	for _, offset := range []int{0, 5, len(text)} {
		if got := positionOffset(text, offsetPosition(text, offset)); got != offset {
			t.Fatalf("offset %d round-tripped as %d", offset, got)
		}
	}
}

func TestPositionOffsetUsesUTF16(t *testing.T) {
	text := []byte("a\U0001f600b")
	if got := positionOffset(text, protocol.Position{Line: 0, Character: 3}); got != len(text)-1 {
		t.Fatalf("offset = %d, want %d", got, len(text)-1)
	}
}

func TestFilePathDecodesWindowsDriveLetterURI(t *testing.T) {
	got := filePathFor("file:///C:/work/blueprint.hcl", uri.PlatformWindows)
	if want := `c:\work\blueprint.hcl`; got != want {
		t.Fatalf("file path = %q, want %q", got, want)
	}
}

func TestFilePathNormalizesWindowsDriveLetterPath(t *testing.T) {
	got := filePathFor(`C:\work\blueprint.hcl`, uri.PlatformWindows)
	if want := `c:\work\blueprint.hcl`; got != want {
		t.Fatalf("file path = %q, want %q", got, want)
	}
}

func TestCompletionReplacesOnlyTrailingTraversalSegment(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "stacks", "eks2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stacks", "eks2", "main.tf"), []byte(`variable "vpc_id" { type = string }`), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "blueprint.hcl")
	text := []byte("node \"eks2\" { source = \"./stacks/eks2\" }\nedge {\n  to = node.eks2.input.\n}\n")
	s := &server{workspace: language.NewWorkspace(dir), documents: map[string][]byte{}}
	s.set(path, text)
	result, err := s.Completion(context.Background(), &protocol.CompletionParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: uri.File(path)}, Position: protocol.Position{Line: 2, Character: uint32(len("  to = node.eks2.input."))}}})
	if err != nil {
		t.Fatal(err)
	}
	items := result.(protocol.CompletionItemSlice)
	if len(items) != 1 || items[0].Label != "vpc_id" {
		t.Fatalf("unexpected completion result: %#v", items)
	}
	got, _ := items[0].Detail.Get()
	if want := "string (required)"; got != want {
		t.Fatalf("completion detail = %q, want %q", got, want)
	}
	edit := items[0].TextEdit.(*protocol.TextEdit)
	if got, want := edit.Range.Start.Character, uint32(len("  to = node.eks2.input.")); got != want {
		t.Fatalf("completion replacement starts at %d, want %d", got, want)
	}
}

func TestDefinitionFindsNodeInAnotherBlueprintFile(t *testing.T) {
	dir := t.TempDir()
	nodes := filepath.Join(dir, "nodes.hcl")
	if err := os.WriteFile(nodes, []byte("node \"vpc\" { source = \"./stacks/vpc\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "edges.hcl")
	text := []byte("edge {\n  from = node.vpc.output.vpc_id\n  to = node.app.input.vpc_id\n}\n")
	if err := os.WriteFile(path, text, 0o644); err != nil {
		t.Fatal(err)
	}
	s := &server{workspace: language.NewWorkspace(dir), documents: map[string][]byte{}}
	s.set(path, text)
	result, err := s.Definition(context.Background(), &protocol.DefinitionParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: uri.File(path)}, Position: protocol.Position{Line: 1, Character: uint32(len("  from = node.v"))}}})
	if err != nil {
		t.Fatal(err)
	}
	location, ok := result.(*protocol.Location)
	if !ok {
		t.Fatalf("definition = %#v, want location", result)
	}
	if got, want := string(location.URI), string(uri.File(nodes)); got != want {
		t.Fatalf("definition URI = %q, want %q", got, want)
	}
}

func TestDidOpenPublishesReferenceDiagnostics(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "stacks", "vpc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stacks", "vpc", "main.tf"), []byte(`output "id" { value = "x" }`), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "blueprint.hcl")
	text := `node "vpc" { source = "./stacks/vpc" }
edge { from = node.vpc.input.id to = node.typo.input.id }`
	client := &recordingClient{}
	s := &server{workspace: language.NewWorkspace(dir), documents: map[string][]byte{}, client: client}
	if err := s.DidOpen(context.Background(), &protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: uri.File(path), Text: text}}); err != nil {
		t.Fatal(err)
	}
	if client.params == nil || len(client.params.Diagnostics) != 2 {
		t.Fatalf("diagnostics = %#v, want two errors", client.params)
	}
}
