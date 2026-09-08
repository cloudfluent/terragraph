// Package lsp adapts the editor-neutral language Workspace to the Language
// Server Protocol over stdio.
package lsp

import (
	"context"
	"errors"
	"io"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode/utf16"
	"unicode/utf8"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/cloudfluent/terragraph/internal/language"
)

// Serve closes its input transport on exit so a client need not close stdin before waiting for the server process.
func Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	server := &server{workspace: language.NewWorkspace(""), documents: map[string][]byte{}, exit: make(chan struct{})}
	// NewServer starts dispatching immediately, so diagnostic handlers must wait until their client is installed.
	server.mu.Lock()
	_, conn, client := protocol.NewServer(ctx, server, jsonrpc2.NewStream(stdio{Reader: in, Writer: out}))
	server.client = client
	server.mu.Unlock()
	select {
	case <-conn.Done():
		return conn.Err()
	case <-server.exit:
		if err := conn.Close(); err != nil {
			return err
		}
		if !server.hasShutdown() {
			return errors.New("language-server: exit received before shutdown; send shutdown before exit")
		}
		return nil
	}
}

type stdio struct {
	io.Reader
	io.Writer
}

func (s stdio) Close() error {
	if closer, ok := s.Reader.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

type server struct {
	protocol.UnimplementedServer
	// An edit must not replace text between diagnostic calculation and byte-to-position conversion.
	documentMu sync.Mutex
	mu         sync.RWMutex
	workspace  *language.Workspace
	documents  map[string][]byte
	client     protocol.Client
	shutdown   bool
	exit       chan struct{}
}

func (s *server) Initialize(_ context.Context, params *protocol.InitializeParams) (*protocol.InitializeResult, error) {
	if s.hasShutdown() {
		return nil, jsonrpc2.ErrInvalidRequest
	}
	if folders, ok := params.WorkspaceFolders.Get(); ok && len(folders) > 0 {
		s.workspace.SetRoot(filePath(string(folders[0].URI)))
	}
	full := protocol.TextDocumentSyncKindFull
	return &protocol.InitializeResult{Capabilities: protocol.ServerCapabilities{TextDocumentSync: full, CompletionProvider: &protocol.CompletionOptions{TriggerCharacters: []string{"."}}, DefinitionProvider: protocol.Boolean(true), PositionEncoding: protocol.PositionEncodingKindUTF16}, ServerInfo: protocol.ServerInfo{Name: "terragraph"}}, nil
}
func (s *server) DidOpen(ctx context.Context, params *protocol.DidOpenTextDocumentParams) error {
	s.documentMu.Lock()
	defer s.documentMu.Unlock()
	s.set(string(params.TextDocument.URI), []byte(params.TextDocument.Text))
	s.publishDiagnostics(ctx)
	return nil
}

func (s *server) Shutdown(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdown {
		return jsonrpc2.ErrInvalidRequest
	}
	s.shutdown = true
	return nil
}

func (s *server) Exit(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exit != nil {
		select {
		case <-s.exit:
		default:
			close(s.exit)
		}
	}
	return nil
}

func (s *server) hasShutdown() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.shutdown
}
func (s *server) DidChange(ctx context.Context, params *protocol.DidChangeTextDocumentParams) error {
	s.documentMu.Lock()
	defer s.documentMu.Unlock()
	for _, change := range params.ContentChanges {
		if whole, ok := change.(*protocol.TextDocumentContentChangeWholeDocument); ok {
			s.set(string(params.TextDocument.URI), []byte(whole.Text))
		}
	}
	s.publishDiagnostics(ctx)
	return nil
}
func (s *server) DidClose(ctx context.Context, params *protocol.DidCloseTextDocumentParams) error {
	s.documentMu.Lock()
	defer s.documentMu.Unlock()
	path := filePath(string(params.TextDocument.URI))
	s.mu.Lock()
	delete(s.documents, path)
	client := s.client
	s.mu.Unlock()
	s.workspace.CloseDocument(path)
	if client != nil {
		_ = client.PublishDiagnostics(ctx, &protocol.PublishDiagnosticsParams{URI: params.TextDocument.URI, Diagnostics: []protocol.Diagnostic{}})
	}
	s.publishDiagnostics(ctx)
	return nil
}

func (s *server) Completion(ctx context.Context, params *protocol.CompletionParams) (protocol.CompletionResult, error) {
	if s.hasShutdown() {
		return nil, jsonrpc2.ErrInvalidRequest
	}
	s.documentMu.Lock()
	defer s.documentMu.Unlock()
	path := filePath(string(params.TextDocument.URI))
	text := s.workspace.Document(path)
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
	if s.hasShutdown() {
		return nil, jsonrpc2.ErrInvalidRequest
	}
	s.documentMu.Lock()
	defer s.documentMu.Unlock()
	path := filePath(string(params.TextDocument.URI))
	text := s.workspace.Document(path)
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

// A changed declaration or group export can affect any open consumer, including one in a different directory.
func (s *server) publishDiagnostics(ctx context.Context) {
	s.mu.RLock()
	paths := make([]string, 0, len(s.documents))
	for path := range s.documents {
		paths = append(paths, path)
	}
	s.mu.RUnlock()
	sort.Strings(paths)
	for _, path := range paths {
		s.publishDocumentDiagnostics(ctx, uri.File(path))
	}
}

func (s *server) publishDocumentDiagnostics(ctx context.Context, documentURI uri.URI) {
	s.mu.RLock()
	client := s.client
	s.mu.RUnlock()
	if client == nil {
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
	_ = client.PublishDiagnostics(ctx, &protocol.PublishDiagnosticsParams{URI: documentURI, Diagnostics: diagnostics})
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
	for offset < len(text) && character < position.Character && text[offset] != '\n' && text[offset] != '\r' {
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
