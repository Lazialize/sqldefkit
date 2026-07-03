package lsp

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/Lazialize/sqldefkit/internal/diag"
	"github.com/Lazialize/sqldefkit/internal/pos"
)

// Server is a running LSP server instance bound to a stdio-like transport.
type Server struct {
	r    *reader
	w    *writer
	log  *log
	sess *session

	shutdownReceived bool
}

// NewServer creates a Server that reads JSON-RPC messages from in and
// writes responses/notifications to out, logging diagnostics/errors to
// errOut.
func NewServer(in io.Reader, out io.Writer, errOut io.Writer) *Server {
	l := newLog(errOut)
	return &Server{
		r:    newReader(in),
		w:    newWriter(out),
		log:  l,
		sess: newSession(l),
	}
}

// Run reads and dispatches messages until the stream ends or an "exit"
// notification is processed. It returns the process exit code the LSP
// spec mandates: 0 if "shutdown" was received before "exit" (or before
// the stream closed), 1 otherwise.
func (s *Server) Run() int {
	for {
		body, err := s.r.readMessage()
		if err != nil {
			if err == io.EOF {
				break
			}
			s.log.Printf("read error: %v", err)
			break
		}

		exit, done := s.dispatch(body)
		if done {
			return exit
		}
	}

	if s.shutdownReceived {
		return 0
	}
	return 1
}

// dispatch decodes and handles one message. done reports whether the
// server should stop (an "exit" notification was processed); exit is the
// process exit code to use in that case.
func (s *Server) dispatch(body []byte) (exit int, done bool) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Printf("panic handling message: %v", r)
		}
	}()

	var msg requestMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		s.log.Printf("invalid JSON-RPC message: %v", err)
		return 0, false
	}

	if msg.Method == "exit" {
		if s.shutdownReceived {
			return 0, true
		}
		return 1, true
	}

	isRequest := len(msg.ID) > 0

	if isRequest {
		s.handleRequest(msg)
	} else {
		s.handleNotification(msg)
	}
	return 0, false
}

// handleRequest handles a message that expects a response, recovering
// from any panic in the specific handler so one bad request can't take
// down the server (the client gets an internal-error response instead).
func (s *Server) handleRequest(msg requestMessage) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Printf("panic in request handler %s: %v", msg.Method, r)
			_ = s.w.writeResponse(msg.ID, nil, &responseError{
				Code:    codeInternalError,
				Message: fmt.Sprintf("internal error: %v", r),
			})
		}
	}()

	switch msg.Method {
	case "initialize":
		s.handleInitialize(msg)
	case "shutdown":
		s.shutdownReceived = true
		_ = s.w.writeResponse(msg.ID, struct{}{}, nil)
	case "textDocument/definition":
		s.handleDefinition(msg)
	default:
		_ = s.w.writeResponse(msg.ID, nil, &responseError{
			Code:    codeMethodNotFound,
			Message: fmt.Sprintf("method not found: %s", msg.Method),
		})
	}
}

// handleNotification handles a message with no response expected.
// Unknown notifications are silently ignored, per LSP convention and this
// project's spec.
func (s *Server) handleNotification(msg requestMessage) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Printf("panic in notification handler %s: %v", msg.Method, r)
		}
	}()

	switch msg.Method {
	case "initialized":
		// no-op
	case "textDocument/didOpen":
		s.handleDidOpen(msg)
	case "textDocument/didChange":
		s.handleDidChange(msg)
	case "textDocument/didSave":
		// Full sync means didChange already carries current content;
		// nothing to do even if the client included text.
	case "textDocument/didClose":
		s.handleDidClose(msg)
	case "$/cancelRequest":
		// Requests are handled synchronously in order; nothing to cancel.
	default:
		// ignore unknown notifications
	}
}

func (s *Server) handleInitialize(msg requestMessage) {
	result := initializeResult{
		Capabilities: serverCapabilities{
			TextDocumentSync: textDocumentSyncOptions{
				OpenClose: true,
				Change:    textDocumentSyncKindFull,
			},
			DefinitionProvider: true,
			PositionEncoding:   positionEncodingUTF16,
		},
		ServerInfo: serverInfo{Name: "sqldefkit"},
	}
	_ = s.w.writeResponse(msg.ID, result, nil)
}

func (s *Server) handleDidOpen(msg requestMessage) {
	var params didOpenParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		s.log.Printf("didOpen: bad params: %v", err)
		return
	}
	path, ok := uriToPath(params.TextDocument.URI)
	if !ok {
		return
	}
	proj, ok := s.sess.openDocument(path, params.TextDocument.Text)
	if !ok {
		// No project owns this file: publish empty diagnostics so any
		// stale state from a previous server session is cleared, and
		// stop (definition requests will naturally return null since
		// there's no project to look up).
		s.publishEmpty(params.TextDocument.URI)
		return
	}
	s.runAndPublish(proj)
}

func (s *Server) handleDidChange(msg requestMessage) {
	var params didChangeParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		s.log.Printf("didChange: bad params: %v", err)
		return
	}
	if len(params.ContentChanges) == 0 {
		return
	}
	path, ok := uriToPath(params.TextDocument.URI)
	if !ok {
		return
	}
	// Full sync: take the last content change's text.
	text := params.ContentChanges[len(params.ContentChanges)-1].Text
	s.sess.changeDocument(path, text)

	proj, ok := s.sess.resolveProject(path)
	if !ok {
		s.publishEmpty(params.TextDocument.URI)
		return
	}
	s.runAndPublish(proj)
}

func (s *Server) handleDidClose(msg requestMessage) {
	var params didCloseParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		s.log.Printf("didClose: bad params: %v", err)
		return
	}
	path, ok := uriToPath(params.TextDocument.URI)
	if !ok {
		return
	}
	proj, hadProject := s.sess.closeDocument(path)

	// Publish empty diagnostics for the closed doc itself.
	s.publishEmpty(params.TextDocument.URI)

	if hadProject {
		s.runAndPublish(proj)
	}
}

// publishEmpty sends an empty publishDiagnostics for uri, clearing any
// diagnostics an editor may be showing for it.
func (s *Server) publishEmpty(uri string) {
	_ = s.w.writeNotification("textDocument/publishDiagnostics", publishDiagnosticsParams{
		URI:         uri,
		Diagnostics: []diagnostic{},
	})
}

// runAndPublish re-runs the check engine for proj and publishes
// diagnostics for every currently open file in it (including empty
// arrays for clean files).
func (s *Server) runAndPublish(proj *project) {
	result := s.sess.runProject(proj)
	for absPath, diags := range result.perFile {
		content, _ := s.sess.docContent(absPath)
		lspDiags := make([]diagnostic, 0, len(diags))
		for _, d := range diags {
			lspDiags = append(lspDiags, toLSPDiagnostic(d, content))
		}
		_ = s.w.writeNotification("textDocument/publishDiagnostics", publishDiagnosticsParams{
			URI:         pathToURI(absPath),
			Diagnostics: lspDiags,
		})
	}
}

// toLSPDiagnostic converts a diag.Diagnostic (byte-based position) into
// an LSP diagnostic (0-based, UTF-16 range), extending the range to cover
// the identifier at the diagnostic's position when content is available.
func toLSPDiagnostic(d diag.Diagnostic, content string) diagnostic {
	r := rangeForPosition(content, d.Pos)
	sev := 1
	if d.Severity == diag.Warning {
		sev = 2
	}
	return diagnostic{
		Range:    r,
		Severity: sev,
		Message:  d.Message,
		Source:   "sqldefkit",
	}
}

// rangeForPosition computes an LSP range for p against content: the start
// is p's line/col converted to UTF-16; the end extends to cover the
// identifier token starting at p.Offset (falling back to a zero-length
// range at the start if content is unavailable or scanning fails).
func rangeForPosition(content string, p pos.Position) lspRange {
	line := p.Line - 1
	if line < 0 {
		line = 0
	}
	lt := lineText(content, p.Line)
	startChar := byteColToUTF16(lt, p.Col)
	start := lspPosition{Line: line, Character: startChar}

	if content == "" || p.Offset < 0 || p.Offset > len(content) {
		return lspRange{Start: start, End: start}
	}

	end := identifierSpan(content, p.Offset)
	if end <= p.Offset {
		return lspRange{Start: start, End: start}
	}
	endCol := p.Col + (end - p.Offset)
	endChar := byteColToUTF16(lt, endCol)
	return lspRange{Start: start, End: lspPosition{Line: line, Character: endChar}}
}

func (s *Server) handleDefinition(msg requestMessage) {
	var params definitionParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		_ = s.w.writeResponse(msg.ID, nil, &responseError{Code: codeInvalidRequest, Message: "invalid params"})
		return
	}

	path, ok := uriToPath(params.TextDocument.URI)
	if !ok {
		_ = s.w.writeResponse(msg.ID, json.RawMessage("null"), nil)
		return
	}

	proj, ok := s.sess.resolveProject(path)
	if !ok {
		_ = s.w.writeResponse(msg.ID, json.RawMessage("null"), nil)
		return
	}

	content, ok := s.sess.docContent(path)
	if !ok {
		_ = s.w.writeResponse(msg.ID, json.RawMessage("null"), nil)
		return
	}

	root := proj.cfg.SchemaDir
	if root == "" {
		root = proj.cfg.Dir
	}

	lt := lineText(content, params.Position.Line+1)
	byteCol := utf16ColToByteCol(lt, params.Position.Character)
	offset := offsetForLineCol(content, params.Position.Line+1, byteCol)

	def, ok := s.sess.findDefinition(proj, root, path, offset)
	if !ok {
		_ = s.w.writeResponse(msg.ID, json.RawMessage("null"), nil)
		return
	}

	defAbsPath := filepath.Join(root, def.File)
	defContent, _ := s.sess.docContent(defAbsPath)
	loc := location{
		URI:   pathToURI(defAbsPath),
		Range: rangeForPosition(defContent, def.Pos),
	}
	_ = s.w.writeResponse(msg.ID, loc, nil)
}

// offsetForLineCol computes the byte offset within content of (line,
// byteCol), both 1-based, matching pos.Position's convention.
func offsetForLineCol(content string, line, byteCol int) int {
	start := 0
	cur := 1
	for cur < line {
		idx := strings.IndexByte(content[start:], '\n')
		if idx < 0 {
			return len(content)
		}
		start += idx + 1
		cur++
	}
	return start + (byteCol - 1)
}
