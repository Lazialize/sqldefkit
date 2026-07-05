package lsp

// This file defines the small subset of LSP JSON structures actually used
// by this server: initialize/initialized, didOpen/didChange/didSave/
// didClose, publishDiagnostics, textDocument/definition,
// textDocument/hover, and textDocument/completion. Field sets are
// intentionally partial (only what we read or write), per the "no
// over-implementing" goal.

// position is an LSP Position: 0-based line, UTF-16-code-unit character.
type lspPosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// lspRange is an LSP Range.
type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

// textDocumentIdentifier identifies a document by URI.
type textDocumentIdentifier struct {
	URI string `json:"uri"`
}

// versionedTextDocumentIdentifier is used by didChange.
type versionedTextDocumentIdentifier struct {
	URI string `json:"uri"`
}

// textDocumentItem is the full document sent on didOpen.
type textDocumentItem struct {
	URI  string `json:"uri"`
	Text string `json:"text"`
}

// didOpenParams is textDocument/didOpen's params.
type didOpenParams struct {
	TextDocument textDocumentItem `json:"textDocument"`
}

// contentChangeEvent is one entry of didChange's contentChanges. Only
// full-document sync is supported, so Text is the entire new document
// content (Range/RangeLength are omitted/ignored).
type contentChangeEvent struct {
	Text string `json:"text"`
}

// didChangeParams is textDocument/didChange's params.
type didChangeParams struct {
	TextDocument   versionedTextDocumentIdentifier `json:"textDocument"`
	ContentChanges []contentChangeEvent            `json:"contentChanges"`
}

// didSaveParams is textDocument/didSave's params. Text is optional
// (included only if the client's syncKind/includeText requests it); we
// never need it since full sync means didChange already has the current
// content.
type didSaveParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Text         *string                `json:"text,omitempty"`
}

// didCloseParams is textDocument/didClose's params.
type didCloseParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
}

// definitionParams is textDocument/definition's params
// (TextDocumentPositionParams).
type definitionParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     lspPosition            `json:"position"`
}

// hoverParams is textDocument/hover's params (TextDocumentPositionParams).
type hoverParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     lspPosition            `json:"position"`
}

// markupContent is an LSP MarkupContent value (used for Hover.Contents).
type markupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// hoverResult is the result of textDocument/hover.
type hoverResult struct {
	Contents markupContent `json:"contents"`
	Range    lspRange      `json:"range"`
}

const markupKindMarkdown = "markdown"

// completionParams is textDocument/completion's params
// (TextDocumentPositionParams; we don't need the optional CompletionContext).
type completionParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     lspPosition            `json:"position"`
}

// completionItem is an LSP CompletionItem (the partial field set this
// server populates).
type completionItem struct {
	Label      string `json:"label"`
	Kind       int    `json:"kind,omitempty"`
	Detail     string `json:"detail,omitempty"`
	InsertText string `json:"insertText,omitempty"`
	FilterText string `json:"filterText,omitempty"`
}

// completionList is the result of textDocument/completion.
type completionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []completionItem `json:"items"`
}

// LSP CompletionItemKind values actually used here.
const (
	completionItemKindClass  = 7
	completionItemKindStruct = 22
)

// completionOptions advertises completion support with no trigger
// characters (clients request completion on manual invoke or their own
// word-boundary heuristics; a space trigger would fire constantly).
type completionOptions struct{}

// location is an LSP Location (URI + Range).
type location struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

// diagnostic is an LSP Diagnostic.
type diagnostic struct {
	Range    lspRange `json:"range"`
	Severity int      `json:"severity,omitempty"`
	Message  string   `json:"message"`
	Source   string   `json:"source,omitempty"`
}

// publishDiagnosticsParams is textDocument/publishDiagnostics' params.
type publishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Diagnostics []diagnostic `json:"diagnostics"`
}

// initializeParams is the (partial) params of the initialize request. We
// don't need anything from it (no workspace-folder-based root resolution
// — project roots are resolved per-file via config.Discover on didOpen),
// but keep the type for documentation/clarity.
type initializeParams struct{}

// serverInfo is returned in InitializeResult.
type serverInfo struct {
	Name string `json:"name"`
}

// textDocumentSyncOptions advertises full-document sync with open/close
// notifications.
type textDocumentSyncOptions struct {
	OpenClose bool `json:"openClose"`
	Change    int  `json:"change"` // TextDocumentSyncKind.Full = 1
}

const textDocumentSyncKindFull = 1

// serverCapabilities is the (partial) ServerCapabilities we advertise:
// full-document sync, go-to-definition, hover, and completion.
type serverCapabilities struct {
	TextDocumentSync   textDocumentSyncOptions `json:"textDocumentSync"`
	DefinitionProvider bool                    `json:"definitionProvider"`
	HoverProvider      bool                    `json:"hoverProvider"`
	CompletionProvider completionOptions       `json:"completionProvider"`
	// PositionEncoding pins the encoding explicitly to utf-16, which is
	// the LSP default; stated here for clarity even though omitting it
	// would mean the same thing.
	PositionEncoding string `json:"positionEncoding"`
	// Experimental advertises non-standard capabilities so clients can
	// feature-detect them (e.g. sqldefkit/dependencyGraph) rather than
	// unconditionally sending a request the server might not support.
	Experimental experimentalCapabilities `json:"experimental"`
}

// experimentalCapabilities is the (partial) contents of
// ServerCapabilities.experimental this server advertises.
type experimentalCapabilities struct {
	DependencyGraph bool `json:"dependencyGraph"`
}

// dependencyGraphParams is sqldefkit/dependencyGraph's params: any file
// belonging to a project, resolved the same way other handlers resolve
// their textDocument URI.
type dependencyGraphParams struct {
	URI string `json:"uri"`
}

// initializeResult is the result of the initialize request.
type initializeResult struct {
	Capabilities serverCapabilities `json:"capabilities"`
	ServerInfo   serverInfo         `json:"serverInfo"`
}

const positionEncodingUTF16 = "utf-16"
