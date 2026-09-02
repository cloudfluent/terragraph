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
	edit := items[0].TextEdit.(*protocol.TextEdit)
	if got, want := edit.Range.Start.Character, uint32(len("  to = node.eks2.input.")); got != want {
		t.Fatalf("completion replacement starts at %d, want %d", got, want)
	}
}
