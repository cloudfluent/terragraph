// Package lsp adapts the editor-neutral language Workspace to the Language
// Server Protocol over stdio.
package lsp

import (
	"context"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"unicode/utf16"
	"unicode/utf8"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/cloudfluent/terragraph/internal/language"
)

// Serve runs one LSP connection until the client closes stdin.
func Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	server := &server{workspace: language.NewWorkspace(""), documents: map[string][]byte{}}
	_, conn, client := protocol.NewServer(ctx, server, jsonrpc2.NewStream(stdio{Reader: in, Writer: out}))
	server.client = client
	<-conn.Done()
	return conn.Err()
}

type stdio struct {
	io.Reader
	io.Writer
}

func (stdio) Close() error { return nil }

type server struct {
	protocol.UnimplementedServer
	mu        sync.RWMutex
	workspace *language.Workspace
	documents map[string][]byte
	client    protocol.Client
}

func (s *server) Initialize(_ context.Context, params *protocol.InitializeParams) (*protocol.InitializeResult, error) {
	if folders, ok := params.WorkspaceFolders.Get(); ok && len(folders) > 0 {
		s.workspace.SetRoot(filePath(string(folders[0].URI)))
	}
	full := protocol.TextDocumentSyncKindFull
	return &protocol.InitializeResult{Capabilities: protocol.ServerCapabilities{TextDocumentSync: full, CompletionProvider: &protocol.CompletionOptions{TriggerCharacters: []string{"."}}, DefinitionProvider: protocol.Boolean(true), PositionEncoding: protocol.PositionEncodingKindUTF16}, ServerInfo: protocol.ServerInfo{Name: "terragraph"}}, nil
}
func (s *server) DidOpen(_ context.Context, params *protocol.DidOpenTextDocumentParams) error {
	s.set(string(params.TextDocument.URI), []byte(params.TextDocument.Text))
	s.publishDiagnostics(context.Background(), params.TextDocument.URI)
	return nil
}

func (s *server) Shutdown(context.Context) error { return nil }
func (s *server) Exit(context.Context) error     { return nil }
func (s *server) DidChange(_ context.Context, params *protocol.DidChangeTextDocumentParams) error {
	for _, change := range params.ContentChanges {
		if whole, ok := change.(*protocol.TextDocumentContentChangeWholeDocument); ok {
			s.set(string(params.TextDocument.URI), []byte(whole.Text))
		}
	}
	s.publishDiagnostics(context.Background(), params.TextDocument.URI)
	return nil
}
func (s *server) DidClose(_ context.Context, params *protocol.DidCloseTextDocumentParams) error {
	path := filePath(string(params.TextDocument.URI))
	s.mu.Lock()
	delete(s.documents, path)
	s.mu.Unlock()
	s.workspace.CloseDocument(path)
	if s.client != nil {
		_ = s.client.PublishDiagnostics(context.Background(), &protocol.PublishDiagnosticsParams{URI: params.TextDocument.URI, Diagnostics: []protocol.Diagnostic{}})
	}
	return nil
}

func (s *server) Completion(ctx context.Context, params *protocol.CompletionParams) (protocol.CompletionResult, error) {
	path := filePath(string(params.TextDocument.URI))
	s.mu.RLock()
	text := append([]byte(nil), s.documents[path]...)
	s.mu.RUnlock()
	if len(text) == 0 {
		text, _ = os.ReadFile(path)
	}
	offset := positionOffset(text, params.Position)
	candidates := s.workspace.Complete(ctx, path, offset)
	items := make(protocol.CompletionItemSlice, 0, len(candidates))
	for _, candidate := range candidates {
		start, newText := completionEdit(text, candidate)
		item := protocol.CompletionItem{Label: candidate.Label, Detail: protocol.NewOptional(candidate.Detail), Kind: protocol.CompletionItemKindProperty, TextEdit: &protocol.TextEdit{Range: protocol.Range{Start: offsetPosition(text, start), End: offsetPosition(text, candidate.End)}, NewText: newText}}
		if candidate.Documentation != "" {
			item.Documentation = protocol.String(candidate.Documentation)
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *server) Definition(ctx context.Context, params *protocol.DefinitionParams) (protocol.DefinitionResult, error) {
	path := filePath(string(params.TextDocument.URI))
	s.mu.RLock()
	text := append([]byte(nil), s.documents[path]...)
	s.mu.RUnlock()
	if len(text) == 0 {
		text, _ = os.ReadFile(path)
	}
	target, ok := s.workspace.Definition(ctx, path, positionOffset(text, params.Position))
	if !ok {
		return nil, nil
	}
	targetText := s.workspace.Document(target.Path)
	return &protocol.Location{URI: uri.File(target.Path), Range: protocol.Range{Start: offsetPosition(targetText, target.Start), End: offsetPosition(targetText, target.End)}}, nil
}

// completionEdit replaces only the segment after the final dot. Editors use
// that range to filter the completion list, so replacing the full traversal
// would make a label such as "vpc_id" fail to match "node.eks2.input.".
func completionEdit(text []byte, candidate language.Completion) (int, string) {
	fragment := string(text[candidate.Start:candidate.End])
	if lastDot := strings.LastIndex(fragment, "."); lastDot >= 0 {
		return candidate.Start + lastDot + 1, candidate.Label
	}
	return candidate.Start, candidate.Insert
}

func (s *server) set(rawURI string, text []byte) {
	path := filePath(rawURI)
	s.mu.Lock()
	s.documents[path] = append([]byte(nil), text...)
	s.mu.Unlock()
	s.workspace.SetDocument(path, text)
}

func (s *server) publishDiagnostics(ctx context.Context, documentURI uri.URI) {
	if s.client == nil {
		return
	}
	path := filePath(string(documentURI))
	text := s.workspace.Document(path)
	items := s.workspace.Diagnose(ctx, path)
	diagnostics := make([]protocol.Diagnostic, 0, len(items))
	for _, item := range items {
		diagnostics = append(diagnostics, protocol.Diagnostic{
			Range:    protocol.Range{Start: offsetPosition(text, item.Start), End: offsetPosition(text, item.End)},
			Severity: protocol.DiagnosticSeverityError,
			Source:   protocol.NewOptional("terragraph"),
			Message:  protocol.String(item.Message),
		})
	}
	_ = s.client.PublishDiagnostics(ctx, &protocol.PublishDiagnosticsParams{URI: documentURI, Diagnostics: diagnostics})
}
func filePath(raw string) string {
	platform := uri.PlatformPOSIX
	if runtime.GOOS == "windows" {
		platform = uri.PlatformWindows
	}
	return filePathFor(raw, platform)
}

func filePathFor(raw string, platform uri.Platform) string {
	parsed, err := uri.Parse(raw)
	if err == nil && parsed.Scheme() == "file" {
		return uri.FsPathFor(parsed, platform, false)
	}
	if platform == uri.PlatformWindows && len(raw) >= 2 && raw[1] == ':' {
		return strings.ToLower(raw[:1]) + raw[1:]
	}
	return raw
}

func positionOffset(text []byte, position protocol.Position) int {
	line, character, offset := uint32(0), uint32(0), 0
	for offset < len(text) && line < position.Line {
		if text[offset] == '\n' {
			line++
			character = 0
		}
		offset++
	}
	for offset < len(text) && character < position.Character {
		r, size := utf8.DecodeRune(text[offset:])
		units := uint32(1)
		if r > 0xffff {
			units = 2
		}
		if character+units > position.Character {
			break
		}
		character += units
		offset += size
	}
	return offset
}
func offsetPosition(text []byte, offset int) protocol.Position {
	if offset > len(text) {
		offset = len(text)
	}
	line, character, index := uint32(0), uint32(0), 0
	for index < offset {
		if text[index] == '\n' {
			line++
			character = 0
			index++
			continue
		}
		r, size := utf8.DecodeRune(text[index:])
		character += uint32(len(utf16.Encode([]rune{r})))
		index += size
	}
	return protocol.Position{Line: line, Character: character}
}
