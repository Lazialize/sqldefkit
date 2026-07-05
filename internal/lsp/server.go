package lsp

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/Lazialize/sqldefkit/internal/bundle"
	"github.com/Lazialize/sqldefkit/internal/diag"
	"github.com/Lazialize/sqldefkit/internal/graphexport"
	"github.com/Lazialize/sqldefkit/internal/parse"
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
	case "textDocument/hover":
		s.handleHover(msg)
	case "textDocument/completion":
		s.handleCompletion(msg)
	case "sqldefkit/dependencyGraph":
		s.handleDependencyGraph(msg)
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
			HoverProvider:      true,
			CompletionProvider: completionOptions{},
			PositionEncoding:   positionEncodingUTF16,
			Experimental:       experimentalCapabilities{DependencyGraph: true},
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

	pc, ok := s.resolvePositionContext(params.TextDocument.URI, params.Position)
	if !ok {
		_ = s.w.writeResponse(msg.ID, json.RawMessage("null"), nil)
		return
	}

	def, ok := s.sess.findDefinition(pc.proj, pc.root, pc.path, pc.offset)
	if !ok {
		_ = s.w.writeResponse(msg.ID, json.RawMessage("null"), nil)
		return
	}

	defAbsPath := filepath.Join(pc.root, def.File)
	defContent, _ := s.sess.docContent(defAbsPath)
	loc := location{
		URI:   pathToURI(defAbsPath),
		Range: rangeForPosition(defContent, def.Pos),
	}
	_ = s.w.writeResponse(msg.ID, loc, nil)
}

// positionContext bundles everything resolvePositionContext computes so
// definition/hover/completion handlers don't each repeat the
// URI-to-path/project-resolution/content-fetch/offset-computation
// boilerplate.
type positionContext struct {
	path    string
	proj    *project
	root    string
	content string
	offset  int
}

// resolvePositionContext resolves uri to an absolute path, finds its
// project, fetches its (overlay-aware) content, and converts pos (an LSP
// Position, UTF-16) to a byte offset into that content. ok is false if any
// step fails (unrecognized URI, no owning project, or content
// unavailable), in which case callers should respond with a null/empty
// result per this server's "no target -> null" convention.
func (s *Server) resolvePositionContext(uri string, position lspPosition) (positionContext, bool) {
	path, ok := uriToPath(uri)
	if !ok {
		return positionContext{}, false
	}

	proj, ok := s.sess.resolveProject(path)
	if !ok {
		return positionContext{}, false
	}

	content, ok := s.sess.docContent(path)
	if !ok {
		return positionContext{}, false
	}

	root := proj.cfg.SchemaDir
	if root == "" {
		root = proj.cfg.Dir
	}

	lt := lineText(content, position.Line+1)
	byteCol := utf16ColToByteCol(lt, position.Character)
	offset := offsetForLineCol(content, position.Line+1, byteCol)

	return positionContext{path: path, proj: proj, root: root, content: content, offset: offset}, true
}

// handleHover answers textDocument/hover: cursor on a reference or
// definition (same span-hit logic as go-to-definition, via
// session.findDefinition) responds with the defining statement's verbatim
// text (including attached leading comments) as a Markdown code block,
// plus a "defined in <relative path>" line, and a range covering the
// identifier under the cursor. Null if the cursor isn't on a known,
// defined name.
func (s *Server) handleHover(msg requestMessage) {
	var params hoverParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		_ = s.w.writeResponse(msg.ID, nil, &responseError{Code: codeInvalidRequest, Message: "invalid params"})
		return
	}

	pc, ok := s.resolvePositionContext(params.TextDocument.URI, params.Position)
	if !ok {
		_ = s.w.writeResponse(msg.ID, json.RawMessage("null"), nil)
		return
	}

	def, ok := s.sess.findDefinition(pc.proj, pc.root, pc.path, pc.offset)
	if !ok {
		_ = s.w.writeResponse(msg.ID, json.RawMessage("null"), nil)
		return
	}

	text, ok := statementTextAt(pc.root, def, pc.proj.cfg.Dialect, s.sess.docContent)
	if !ok {
		_ = s.w.writeResponse(msg.ID, json.RawMessage("null"), nil)
		return
	}

	relPath := filepath.ToSlash(def.File)
	value := "```sql\n" + text + "\n```\ndefined in " + relPath

	result := hoverResult{
		Contents: markupContent{Kind: markupKindMarkdown, Value: value},
		Range:    rangeForPosition(pc.content, hoverIdentifierPos(pc.content, pc.offset)),
	}
	_ = s.w.writeResponse(msg.ID, result, nil)
}

// hoverIdentifierPos builds a pos.Position for the identifier span
// containing offset in content, for reuse with rangeForPosition (which
// expects a pos.Position and derives the range's end from
// identifierSpan(content, p.Offset) itself; only File/Offset/Line/Col need
// to be populated).
func hoverIdentifierPos(content string, offset int) pos.Position {
	lm := pos.NewLineMap(content)
	return lm.Pos("", offset)
}

// handleCompletion answers textDocument/completion: detects one of the
// three supported contexts (REFERENCES target, require-directive name, or
// CREATE INDEX/TRIGGER ... ON target) by scanning the current buffer
// backward from the cursor, then returns matching defined-object names as
// completion items (empty list, not null, outside those contexts).
func (s *Server) handleCompletion(msg requestMessage) {
	var params completionParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		_ = s.w.writeResponse(msg.ID, nil, &responseError{Code: codeInvalidRequest, Message: "invalid params"})
		return
	}

	empty := completionList{IsIncomplete: false, Items: []completionItem{}}

	pc, ok := s.resolvePositionContext(params.TextDocument.URI, params.Position)
	if !ok {
		_ = s.w.writeResponse(msg.ID, empty, nil)
		return
	}

	symbols := s.sess.symbolsFor(pc.proj)
	if symbols == nil {
		_ = s.w.writeResponse(msg.ID, empty, nil)
		return
	}

	ctx := detectCompletionContext(pc.content, pc.offset)
	if ctx.Kind == completionContextNone {
		_ = s.w.writeResponse(msg.ID, empty, nil)
		return
	}

	tablesOnly := ctx.Kind == completionContextReferences || ctx.Kind == completionContextOn
	items := completionItemsForNames(symbols, ctx.Prefix, tablesOnly)
	_ = s.w.writeResponse(msg.ID, completionList{IsIncomplete: false, Items: items}, nil)
}

// handleDependencyGraph answers the custom sqldefkit/dependencyGraph
// request: params name a file belonging to a project (same resolution
// rule as every other handler here — config.Discover from the file's
// directory, file must be under schema_dir, config must declare a
// dialect); the response is the internal/graphexport.Graph JSON payload
// for that project, built overlay-aware (unsaved buffer content is
// reflected via s.sess.readFile). A file outside any project responds
// with a null result, not an error, matching this server's established
// convention for "no target" (see resolvePositionContext's callers).
func (s *Server) handleDependencyGraph(msg requestMessage) {
	var params dependencyGraphParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		_ = s.w.writeResponse(msg.ID, nil, &responseError{Code: codeInvalidRequest, Message: "invalid params"})
		return
	}

	path, ok := uriToPath(params.URI)
	if !ok {
		_ = s.w.writeResponse(msg.ID, json.RawMessage("null"), nil)
		return
	}

	proj, ok := s.sess.resolveProject(path)
	if !ok {
		_ = s.w.writeResponse(msg.ID, json.RawMessage("null"), nil)
		return
	}

	root := proj.cfg.SchemaDir
	if root == "" {
		root = proj.cfg.Dir
	}

	loaded, err := bundle.Load(root, proj.cfg.Dialect, s.sess.readFile)
	if err != nil {
		s.log.Printf("dependencyGraph: load failed for project %s: %v", proj.cfg.Dir, err)
		_ = s.w.writeResponse(msg.ID, json.RawMessage("null"), nil)
		return
	}

	g := graphexport.Build(loaded)
	_ = s.w.writeResponse(msg.ID, g, nil)
}

// completionItemsForNames builds the sorted, prefix-filtered completion
// item list for a completion request: one item per defined name in
// symbols matching prefix case-insensitively (prefix match only, no
// fuzzy), restricted to KindCreateTable definitions if tablesOnly.
func completionItemsForNames(symbols *bundle.Symbols, prefix string, tablesOnly bool) []completionItem {
	lowerPrefix := strings.ToLower(prefix)
	items := make([]completionItem, 0, len(symbols.Definitions))
	for _, name := range symbols.DefinitionNames() {
		def, ok := symbols.FirstDefinition(name)
		if !ok {
			continue
		}
		if tablesOnly && def.Kind != parse.KindCreateTable {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(name), lowerPrefix) {
			continue
		}
		items = append(items, completionItem{
			Label:      name,
			Kind:       completionKindFor(def.Kind),
			Detail:     kindLabel(def.Kind) + " — " + filepath.Base(def.File),
			InsertText: name,
			FilterText: name,
		})
	}
	return items
}

// completionKindFor maps a parse.Kind to an LSP CompletionItemKind: tables
// (and anything else) use Class (7); views specifically use Struct (22) to
// visually distinguish them in client UIs that render kind icons.
func completionKindFor(k parse.Kind) int {
	if k == parse.KindCreateView {
		return completionItemKindStruct
	}
	return completionItemKindClass
}

// kindLabel renders a parse.Kind as the short lowercase word used in a
// completion item's Detail field (e.g. "table — users.sql").
func kindLabel(k parse.Kind) string {
	switch k {
	case parse.KindCreateTable:
		return "table"
	case parse.KindCreateView:
		return "view"
	case parse.KindCreateIndex:
		return "index"
	case parse.KindCreateFunction:
		return "function"
	case parse.KindCreateTrigger:
		return "trigger"
	case parse.KindCreateType:
		return "type"
	case parse.KindCreateSequence:
		return "sequence"
	case parse.KindCreateExtension:
		return "extension"
	case parse.KindAlterTable:
		return "table"
	default:
		return "object"
	}
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
