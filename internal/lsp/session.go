package lsp

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Lazialize/sqldefkit/internal/bundle"
	"github.com/Lazialize/sqldefkit/internal/config"
	"github.com/Lazialize/sqldefkit/internal/diag"
	"github.com/Lazialize/sqldefkit/internal/lexer"
)

// document is one open (or previously open) text document tracked by the
// session: its absolute path and current in-memory content.
type document struct {
	path    string
	content string
}

// project is a resolved sqldefkit project (one sqldefkit.yaml directory)
// the session is tracking diagnostics/symbols for. Keyed by config.Dir.
type project struct {
	cfg config.Config

	// openFiles is the set of currently-open absolute paths belonging to
	// this project (used to know which docs need diagnostics
	// republished, and which overlay entries apply when loading).
	openFiles map[string]bool

	// symbols is the most recently computed symbol index for this
	// project, used to answer textDocument/definition. Nil until the
	// first successful load.
	symbols *bundle.Symbols
}

// session holds all server-wide state: open documents (overlay) and
// resolved projects. All access is guarded by mu since the message loop
// could in principle be extended to run handlers concurrently later, and
// it costs nothing to be safe now.
type session struct {
	mu sync.Mutex

	// docs is the overlay: absolute path -> currently open document
	// content, for every document that has been opened and not yet
	// closed.
	docs map[string]*document

	// projects is keyed by the project's config directory
	// (config.Config.Dir).
	projects map[string]*project

	// warnedNoProject dedupes the "no project for this file" stderr log
	// so a client that reopens the same file repeatedly doesn't spam
	// stderr.
	warnedNoProject map[string]bool

	logger *log
}

func newSession(logger *log) *session {
	return &session{
		docs:            make(map[string]*document),
		projects:        make(map[string]*project),
		warnedNoProject: make(map[string]bool),
		logger:          logger,
	}
}

// readFile implements the ReadFile-style func bundle.Load expects,
// consulting the overlay first (by absolute path) and falling back to
// disk. bundle.Load joins root and each discovered relative path with
// filepath.Join, producing an OS-native absolute path when root itself is
// absolute (config.Config.SchemaDir always is), so overlay keys (also
// absolute, from uriToPath) match directly.
func (s *session) readFile(path string) ([]byte, error) {
	s.mu.Lock()
	if doc, ok := s.docs[path]; ok {
		content := doc.content
		s.mu.Unlock()
		return []byte(content), nil
	}
	s.mu.Unlock()
	return os.ReadFile(path)
}

// resolveProject finds (or creates and caches) the project owning the
// .sql file at absPath, per the rule: config.Discover from the file's
// directory; the file must resolve under the config's schema_dir and the
// config must declare a dialect. Returns ok=false (and logs once to
// stderr) if no project owns the file.
func (s *session) resolveProject(absPath string) (*project, bool) {
	dir := filepath.Dir(absPath)
	cfg, err := config.Discover(dir)
	if err != nil {
		s.warnNoProject(absPath, "no sqldefkit.yaml found: "+err.Error())
		return nil, false
	}
	if !cfg.HasDialect {
		s.warnNoProject(absPath, "sqldefkit.yaml at "+cfg.Path+" has no dialect set")
		return nil, false
	}
	schemaDir := cfg.SchemaDir
	if schemaDir == "" {
		schemaDir = cfg.Dir
	}
	if !pathUnder(schemaDir, absPath) {
		s.warnNoProject(absPath, "file is outside schema_dir "+schemaDir)
		return nil, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[cfg.Dir]
	if !ok {
		p = &project{cfg: cfg, openFiles: make(map[string]bool)}
		s.projects[cfg.Dir] = p
	} else {
		// Config may have changed on disk since last resolved; refresh it.
		p.cfg = cfg
	}
	return p, true
}

// pathUnder reports whether target is base itself or lies under base,
// comparing cleaned absolute paths.
func pathUnder(base, target string) bool {
	base = filepath.Clean(base)
	target = filepath.Clean(target)
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !strings.HasPrefix(rel, "..") && rel != ".."
}

func (s *session) warnNoProject(absPath, reason string) {
	s.mu.Lock()
	already := s.warnedNoProject[absPath]
	s.warnedNoProject[absPath] = true
	s.mu.Unlock()
	if !already {
		s.logger.Printf("no project for %s: %s", absPath, reason)
	}
}

// normalizeContent applies the same CRLF->LF normalization bundle.Load
// applies, so overlay content and on-disk content are treated identically
// and positions line up with what Load/CheckDiagnostics compute.
func normalizeContent(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// openDocument records/updates the overlay entry for path and returns the
// project it belongs to, if any.
func (s *session) openDocument(path, content string) (*project, bool) {
	content = normalizeContent(content)
	s.mu.Lock()
	s.docs[path] = &document{path: path, content: content}
	s.mu.Unlock()

	proj, ok := s.resolveProject(path)
	if !ok {
		return nil, false
	}
	s.mu.Lock()
	proj.openFiles[path] = true
	s.mu.Unlock()
	return proj, true
}

// changeDocument updates the overlay content for an already-open path.
func (s *session) changeDocument(path, content string) {
	content = normalizeContent(content)
	s.mu.Lock()
	if doc, ok := s.docs[path]; ok {
		doc.content = content
	} else {
		s.docs[path] = &document{path: path, content: content}
	}
	s.mu.Unlock()
}

// closeDocument drops the overlay entry for path and returns the project
// it belonged to (if any), so the caller can re-run diagnostics for the
// remaining open documents in that project.
func (s *session) closeDocument(path string) (*project, bool) {
	s.mu.Lock()
	delete(s.docs, path)
	s.mu.Unlock()

	// Look up the project without re-discovering from disk necessarily
	// failing (the file itself may have been deleted); prefer the
	// already-associated project if we have one.
	s.mu.Lock()
	for _, proj := range s.projects {
		if proj.openFiles[path] {
			delete(proj.openFiles, path)
			s.mu.Unlock()
			return proj, true
		}
	}
	s.mu.Unlock()
	return nil, false
}

// docContent returns the current content for path (overlay if open, else
// disk), normalized, and whether it could be obtained at all.
func (s *session) docContent(path string) (string, bool) {
	s.mu.Lock()
	if doc, ok := s.docs[path]; ok {
		content := doc.content
		s.mu.Unlock()
		return content, true
	}
	s.mu.Unlock()
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return normalizeContent(string(data)), true
}

// diagnosticsResult is the outcome of running the check engine for one
// project: per-open-file diagnostics (always present, possibly empty, for
// every currently open file in the project) plus the freshly computed
// symbol index.
type diagnosticsResult struct {
	perFile map[string][]diag.Diagnostic // absolute path -> diagnostics
	symbols *bundle.Symbols
}

// runProject loads and checks proj's schema tree (overlay-aware) and
// returns diagnostics bucketed per open file, publishing an empty slice
// for open files that have none (required to clear stale squiggles). It
// also updates proj.symbols for definition lookups.
func (s *session) runProject(proj *project) diagnosticsResult {
	root := proj.cfg.SchemaDir
	if root == "" {
		root = proj.cfg.Dir
	}

	diags, err := bundle.CheckDiagnostics(root, proj.cfg.Dialect, s.readFile)

	result := diagnosticsResult{perFile: make(map[string][]diag.Diagnostic)}

	s.mu.Lock()
	for f := range proj.openFiles {
		result.perFile[f] = nil
	}
	s.mu.Unlock()

	if err != nil {
		// Load itself failed (e.g. an I/O error reading a file, or no
		// .sql files at all under root). There's no per-symbol
		// information to report; leave diagnostics empty for open files
		// rather than guessing which file was at fault.
		s.logger.Printf("check failed for project %s: %v", proj.cfg.Dir, err)
		s.mu.Lock()
		proj.symbols = nil
		s.mu.Unlock()
		return result
	}

	for _, d := range diags {
		abs := filepath.Join(root, d.Pos.File)
		if _, tracked := result.perFile[abs]; tracked {
			result.perFile[abs] = append(result.perFile[abs], d)
		}
	}

	// Recompute symbols via a second Load call is wasteful; instead reuse
	// CheckDiagnostics's diags but we still need Symbols for definition
	// lookups. Load again through bundle.Load directly (cheap: schemas
	// are small, per the spec's stated single-threaded-is-fine model).
	loaded, loadErr := bundle.Load(root, proj.cfg.Dialect, s.readFile)
	s.mu.Lock()
	if loadErr == nil {
		proj.symbols = loaded.Symbols
	} else {
		proj.symbols = nil
	}
	s.mu.Unlock()

	return result
}

// findDefinition resolves the definition for the symbol at (absPath,
// byteOffset) within proj, if any. It looks for a Reference or Definition
// in proj.symbols whose name-span (computed via identifierSpan against
// that occurrence's own file content) contains byteOffset, then returns
// the first Definition recorded for that name.
//
// This is the shared lookup behind both textDocument/definition and
// textDocument/hover: both need "what name is under the cursor, and where
// is it defined" — hover additionally wants the defining statement's text,
// fetched separately via statementTextAt.
func (s *session) findDefinition(proj *project, root, absPath string, byteOffset int) (bundle.Definition, bool) {
	_, def, ok := s.resolveSymbolAt(proj, root, absPath, byteOffset)
	return def, ok
}

// symbolsFor returns proj's most recently computed symbol index (nil if
// none yet), guarded by the session lock like every other access to
// project fields the session mutates concurrently with the message loop.
func (s *session) symbolsFor(proj *project) *bundle.Symbols {
	s.mu.Lock()
	defer s.mu.Unlock()
	return proj.symbols
}

// resolveSymbolAt is like findDefinition but also returns the resolved
// name itself (hover needs nothing beyond the Definition today, but
// returning the name keeps this the single place that implements
// "what's under the cursor" span-hit logic for both features).
func (s *session) resolveSymbolAt(proj *project, root, absPath string, byteOffset int) (name string, def bundle.Definition, ok bool) {
	s.mu.Lock()
	symbols := proj.symbols
	s.mu.Unlock()
	if symbols == nil {
		return "", bundle.Definition{}, false
	}

	rel, err := filepath.Rel(root, absPath)
	if err != nil {
		return "", bundle.Definition{}, false
	}
	rel = filepath.ToSlash(rel)

	name, ok = symbolNameAt(s, symbols, root, rel, byteOffset)
	if !ok {
		return "", bundle.Definition{}, false
	}
	def, ok = symbols.FirstDefinition(name)
	if !ok {
		return name, bundle.Definition{}, false
	}
	return name, def, true
}

// symbolNameAt scans every Definition and Reference in symbols that is
// positioned in file rel, and returns the Name of the one whose
// identifier span (recomputed against that file's current content)
// contains byteOffset.
func symbolNameAt(s *session, symbols *bundle.Symbols, root, rel string, byteOffset int) (string, bool) {
	content, ok := fileContentForRoot(s, root, rel)
	if !ok {
		return "", false
	}

	for _, defs := range symbols.Definitions {
		for _, d := range defs {
			if d.File != rel {
				continue
			}
			if spanContains(content, d.Pos.Offset, byteOffset) {
				return d.Name, true
			}
		}
	}
	for _, r := range symbols.References {
		// RefViewScan references (best-effort FROM/JOIN scans) are still
		// valid go-to-definition targets when they happen to resolve;
		// RefViewScan only means "don't warn if unresolved" for
		// diagnostics purposes, not "don't allow jumping to it".
		if r.File != rel {
			continue
		}
		if spanContains(content, r.Pos.Offset, byteOffset) {
			return r.Name, true
		}
	}
	return "", false
}

// fileContentForRoot returns the current (overlay-aware) content for the
// file at root/rel.
func fileContentForRoot(s *session, root, rel string) (string, bool) {
	abs := filepath.Join(root, rel)
	return s.docContent(abs)
}

// statementTextAt returns the verbatim source text of the statement
// defining def (its attached leading comments, if any, through its own
// body), re-lexing def's file's current (overlay-aware) content and
// locating the statement whose body contains def.Pos.Offset. Trailing
// whitespace is trimmed; leading comments (if present) are included
// exactly as written, along with whatever separates them from the
// statement body, since lexer.Statement doesn't otherwise expose a single
// "full text including comments" field.
//
// dialect must match the project's configured dialect so comment/string
// syntax is lexed the same way Load did when producing def.
func statementTextAt(root string, def bundle.Definition, dialect lexer.Dialect, docContent func(string) (string, bool)) (string, bool) {
	abs := filepath.Join(root, def.File)
	content, ok := docContent(abs)
	if !ok {
		return "", false
	}

	stmts, err := lexer.Split(content, dialect)
	if err != nil {
		return "", false
	}

	offset := def.Pos.Offset
	for _, st := range stmts {
		if offset < st.Start || offset >= st.End {
			continue
		}
		start := st.Start
		if len(st.LeadingCommentStarts) > 0 {
			start = st.LeadingCommentStarts[0]
		}
		text := content[start:st.End]
		return strings.TrimRight(text, " \t\r\n\v\f"), true
	}
	return "", false
}

// spanContains reports whether byteOffset falls within the identifier
// token starting at startOffset in content.
func spanContains(content string, startOffset, byteOffset int) bool {
	if startOffset < 0 || startOffset > len(content) {
		return false
	}
	end := identifierSpan(content, startOffset)
	if end == startOffset {
		// Zero-length fallback span: only match an exact hit.
		return byteOffset == startOffset
	}
	return byteOffset >= startOffset && byteOffset < end
}

// log is a tiny stderr logger, kept as its own type so it's trivially
// mockable/no-op-able in tests without pulling in log.Logger's global
// state.
type log struct {
	mu sync.Mutex
	w  io.Writer
}

func newLog(w io.Writer) *log {
	return &log{w: w}
}

func (l *log) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.w == nil {
		return
	}
	fmt.Fprintf(l.w, "sqldefkit lsp: "+format+"\n", args...)
}
