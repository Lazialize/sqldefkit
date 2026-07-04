package lsp

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setupHoverCompletionProject writes a temp sqldefkit project with:
//   - users.sql: defines table "users".
//   - orders.sql: defines table "orders", REFERENCES users.
//   - views.sql: a require-directive listing "users" plus a CREATE VIEW.
func setupHoverCompletionProject(t *testing.T) (root, usersPath, ordersPath, viewsPath string) {
	t.Helper()
	root = t.TempDir()
	writeTestFile(t, filepath.Join(root, "sqldefkit.yaml"), "dialect: postgres\nschema_dir: schema\n")
	usersPath = filepath.Join(root, "schema", "users.sql")
	ordersPath = filepath.Join(root, "schema", "orders.sql")
	viewsPath = filepath.Join(root, "schema", "views.sql")
	writeTestFile(t, usersPath, "CREATE TABLE users (id int PRIMARY KEY);\n")
	writeTestFile(t, ordersPath, "CREATE TABLE orders (\n\tid int PRIMARY KEY,\n\tuser_id int REFERENCES users(id)\n);\n")
	writeTestFile(t, viewsPath, "-- sqldefkit:require users\nCREATE VIEW user_view AS SELECT id FROM users;\n")
	return root, usersPath, ordersPath, viewsPath
}

func startTestServer(t *testing.T) *testClient {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { clientConn.Close() })

	server := NewServer(serverConn, serverConn, os.Stderr)
	go server.Run()

	client := newTestClient(t, clientConn)
	id := client.sendRequest("initialize", map[string]any{})
	client.waitForResponse(id, 2*time.Second)
	client.sendNotification("initialized", map[string]any{})
	return client
}

func openAndDrain(t *testing.T, client *testClient, path, content string, n int) {
	t.Helper()
	client.sendNotification("textDocument/didOpen", didOpenParams{
		TextDocument: textDocumentItem{URI: pathToURI(path), Text: content},
	})
	drainDiagnostics(t, client, n, 2*time.Second)
}

// TestIntegration_InitializeCapabilities checks hoverProvider and
// completionProvider are advertised alongside the existing capabilities.
func TestIntegration_InitializeCapabilities(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	server := NewServer(serverConn, serverConn, os.Stderr)
	go server.Run()

	client := newTestClient(t, clientConn)
	id := client.sendRequest("initialize", map[string]any{})
	resp := client.waitForResponse(id, 2*time.Second)
	if resp.Error != nil {
		t.Fatalf("initialize error: %s", resp.Error)
	}
	var initResult initializeResult
	if err := json.Unmarshal(resp.Result, &initResult); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !initResult.Capabilities.DefinitionProvider {
		t.Errorf("expected definitionProvider = true (must stay untouched)")
	}
	if !initResult.Capabilities.HoverProvider {
		t.Errorf("expected hoverProvider = true")
	}
	if initResult.Capabilities.TextDocumentSync.Change != textDocumentSyncKindFull {
		t.Errorf("expected TextDocumentSync.Change = Full")
	}

	// Verify completionProvider is present in the raw JSON (an empty
	// struct with no trigger characters), since completionOptions has no
	// fields to assert on via the typed struct.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(resp.Result, &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	var caps map[string]json.RawMessage
	if err := json.Unmarshal(raw["capabilities"], &caps); err != nil {
		t.Fatalf("unmarshal capabilities: %v", err)
	}
	if _, ok := caps["completionProvider"]; !ok {
		t.Errorf("expected capabilities.completionProvider to be present")
	}
}

// TestIntegration_HoverOnReference hovers over "users" inside orders.sql's
// REFERENCES clause and expects Markdown containing the CREATE TABLE text
// and a "defined in" line.
func TestIntegration_HoverOnReference(t *testing.T) {
	_, _, ordersPath, _ := setupHoverCompletionProject(t)
	client := startTestServer(t)

	ordersContent := readFileString(t, ordersPath)
	openAndDrain(t, client, ordersPath, ordersContent, 1)

	line := "\tuser_id int REFERENCES users(id)"
	byteCol := strings.Index(line, "users") + 1
	char := byteColToUTF16(line, byteCol)

	id := client.sendRequest("textDocument/hover", hoverParams{
		TextDocument: textDocumentIdentifier{URI: pathToURI(ordersPath)},
		Position:     lspPosition{Line: 2, Character: char},
	})
	resp := client.waitForResponse(id, 2*time.Second)
	if resp.Error != nil {
		t.Fatalf("hover error: %s", resp.Error)
	}
	var hover hoverResult
	if err := json.Unmarshal(resp.Result, &hover); err != nil {
		t.Fatalf("unmarshal hover: %v (raw: %s)", err, resp.Result)
	}
	if hover.Contents.Kind != markupKindMarkdown {
		t.Errorf("Contents.Kind = %q, want markdown", hover.Contents.Kind)
	}
	if !strings.Contains(hover.Contents.Value, "CREATE TABLE users") {
		t.Errorf("hover value missing CREATE TABLE users text: %q", hover.Contents.Value)
	}
	if !strings.Contains(hover.Contents.Value, "```sql") {
		t.Errorf("hover value missing sql fenced code block: %q", hover.Contents.Value)
	}
	wantDefinedIn := "defined in users.sql"
	if !strings.Contains(hover.Contents.Value, wantDefinedIn) {
		t.Errorf("hover value missing %q: %q", wantDefinedIn, hover.Contents.Value)
	}
}

// TestIntegration_HoverOnWhitespace expects null when hovering somewhere
// with no reference/definition span.
func TestIntegration_HoverOnWhitespace(t *testing.T) {
	_, _, ordersPath, _ := setupHoverCompletionProject(t)
	client := startTestServer(t)

	ordersContent := readFileString(t, ordersPath)
	openAndDrain(t, client, ordersPath, ordersContent, 1)

	id := client.sendRequest("textDocument/hover", hoverParams{
		TextDocument: textDocumentIdentifier{URI: pathToURI(ordersPath)},
		Position:     lspPosition{Line: 0, Character: 0},
	})
	resp := client.waitForResponse(id, 2*time.Second)
	if resp.Error != nil {
		t.Fatalf("hover error: %s", resp.Error)
	}
	if string(resp.Result) != "null" {
		t.Errorf("expected null, got %s", resp.Result)
	}
}

// TestIntegration_HoverOnUndefinedName expects null when hovering a
// reference to a name that isn't defined anywhere in the bundle.
func TestIntegration_HoverOnUndefinedName(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "sqldefkit.yaml"), "dialect: postgres\nschema_dir: schema\n")
	ordersPath := filepath.Join(root, "schema", "orders.sql")
	content := "CREATE TABLE orders (\n\tid int PRIMARY KEY,\n\tuser_id int REFERENCES missing_table(id)\n);\n"
	writeTestFile(t, ordersPath, content)

	client := startTestServer(t)
	openAndDrain(t, client, ordersPath, content, 1)

	line := "\tuser_id int REFERENCES missing_table(id)"
	byteCol := strings.Index(line, "missing_table") + 1
	char := byteColToUTF16(line, byteCol)

	id := client.sendRequest("textDocument/hover", hoverParams{
		TextDocument: textDocumentIdentifier{URI: pathToURI(ordersPath)},
		Position:     lspPosition{Line: 2, Character: char},
	})
	resp := client.waitForResponse(id, 2*time.Second)
	if resp.Error != nil {
		t.Fatalf("hover error: %s", resp.Error)
	}
	if string(resp.Result) != "null" {
		t.Errorf("expected null for undefined reference, got %s", resp.Result)
	}
}

// TestIntegration_HoverShowsUnsavedBufferEdit checks that hovering a
// reference whose target's definition was modified in an unsaved open
// buffer reflects the buffer's current (not on-disk) text.
func TestIntegration_HoverShowsUnsavedBufferEdit(t *testing.T) {
	_, usersPath, ordersPath, _ := setupHoverCompletionProject(t)
	client := startTestServer(t)

	ordersContent := readFileString(t, ordersPath)
	openAndDrain(t, client, ordersPath, ordersContent, 1)

	// Open users.sql then edit it in the overlay (not saved to disk) to
	// add a new column, changing the CREATE TABLE text.
	usersContent := readFileString(t, usersPath)
	openAndDrain(t, client, usersPath, usersContent, 2)

	editedUsers := "CREATE TABLE users (id int PRIMARY KEY, email text);\n"
	client.sendNotification("textDocument/didChange", didChangeParams{
		TextDocument:   versionedTextDocumentIdentifier{URI: pathToURI(usersPath)},
		ContentChanges: []contentChangeEvent{{Text: editedUsers}},
	})
	drainDiagnostics(t, client, 2, 2*time.Second)

	line := "\tuser_id int REFERENCES users(id)"
	byteCol := strings.Index(line, "users") + 1
	char := byteColToUTF16(line, byteCol)

	id := client.sendRequest("textDocument/hover", hoverParams{
		TextDocument: textDocumentIdentifier{URI: pathToURI(ordersPath)},
		Position:     lspPosition{Line: 2, Character: char},
	})
	resp := client.waitForResponse(id, 2*time.Second)
	if resp.Error != nil {
		t.Fatalf("hover error: %s", resp.Error)
	}
	var hover hoverResult
	if err := json.Unmarshal(resp.Result, &hover); err != nil {
		t.Fatalf("unmarshal hover: %v (raw: %s)", err, resp.Result)
	}
	if !strings.Contains(hover.Contents.Value, "email text") {
		t.Errorf("expected hover to reflect unsaved buffer edit (email text), got: %q", hover.Contents.Value)
	}
}

// TestIntegration_CompletionAfterReferences checks completion after
// "REFERENCES u" offers "users" (a table) but not "user_view" (a view).
func TestIntegration_CompletionAfterReferences(t *testing.T) {
	_, _, _, viewsPath := setupHoverCompletionProject(t)
	client := startTestServer(t)

	viewsContent := readFileString(t, viewsPath)
	openAndDrain(t, client, viewsPath, viewsContent, 1)

	// Simulate typing "REFERENCES u" at the end of a new statement in
	// views.sql's buffer via didChange, then request completion right
	// after the "u".
	newContent := viewsContent + "\nCREATE TABLE t2 (id int REFERENCES u"
	client.sendNotification("textDocument/didChange", didChangeParams{
		TextDocument:   versionedTextDocumentIdentifier{URI: pathToURI(viewsPath)},
		ContentChanges: []contentChangeEvent{{Text: newContent}},
	})
	drainDiagnostics(t, client, 1, 2*time.Second)

	lines := strings.Split(newContent, "\n")
	lastLine := lines[len(lines)-1]
	char := byteColToUTF16(lastLine, len(lastLine)+1)

	id := client.sendRequest("textDocument/completion", completionParams{
		TextDocument: textDocumentIdentifier{URI: pathToURI(viewsPath)},
		Position:     lspPosition{Line: len(lines) - 1, Character: char},
	})
	resp := client.waitForResponse(id, 2*time.Second)
	if resp.Error != nil {
		t.Fatalf("completion error: %s", resp.Error)
	}
	var list completionList
	if err := json.Unmarshal(resp.Result, &list); err != nil {
		t.Fatalf("unmarshal completion list: %v (raw: %s)", err, resp.Result)
	}
	foundUsers := false
	for _, item := range list.Items {
		if item.Label == "users" {
			foundUsers = true
		}
		if item.Label == "user_view" {
			t.Errorf("expected view %q to NOT appear in REFERENCES completion", item.Label)
		}
	}
	if !foundUsers {
		t.Errorf("expected %q in completion items, got %+v", "users", list.Items)
	}
}

// TestIntegration_CompletionInDirective checks completion inside a
// "-- sqldefkit:require " directive includes views (unlike REFERENCES
// completion, which is tables-only).
func TestIntegration_CompletionInDirective(t *testing.T) {
	_, _, _, viewsPath := setupHoverCompletionProject(t)
	client := startTestServer(t)

	// Rewrite views.sql's directive line to add a second, partial name
	// being completed.
	newContent := "-- sqldefkit:require users user\nCREATE VIEW user_view AS SELECT id FROM users;\n"
	openAndDrain(t, client, viewsPath, newContent, 1)

	line := "-- sqldefkit:require users user"
	char := byteColToUTF16(line, len(line)+1)

	id := client.sendRequest("textDocument/completion", completionParams{
		TextDocument: textDocumentIdentifier{URI: pathToURI(viewsPath)},
		Position:     lspPosition{Line: 0, Character: char},
	})
	resp := client.waitForResponse(id, 2*time.Second)
	if resp.Error != nil {
		t.Fatalf("completion error: %s", resp.Error)
	}
	var list completionList
	if err := json.Unmarshal(resp.Result, &list); err != nil {
		t.Fatalf("unmarshal completion list: %v (raw: %s)", err, resp.Result)
	}
	foundView := false
	foundUsers := false
	for _, item := range list.Items {
		if item.Label == "user_view" {
			foundView = true
		}
		if item.Label == "users" {
			foundUsers = true
		}
	}
	if !foundView {
		t.Errorf("expected view %q in directive completion items, got %+v", "user_view", list.Items)
	}
	if !foundUsers {
		t.Errorf("expected table %q in directive completion items, got %+v", "users", list.Items)
	}
}

// TestIntegration_CompletionPlainColumnPosition checks completion in an
// ordinary column-definition position (no recognized context) returns an
// empty (not null) item list.
func TestIntegration_CompletionPlainColumnPosition(t *testing.T) {
	_, _, ordersPath, _ := setupHoverCompletionProject(t)
	client := startTestServer(t)

	ordersContent := readFileString(t, ordersPath)
	openAndDrain(t, client, ordersPath, ordersContent, 1)

	// Position at the end of "\tid int PRIMARY KEY," (line 1, 0-based) —
	// plain column-definition text, not REFERENCES/directive/ON.
	line := "\tid int PRIMARY KEY,"
	char := byteColToUTF16(line, len(line)+1)

	id := client.sendRequest("textDocument/completion", completionParams{
		TextDocument: textDocumentIdentifier{URI: pathToURI(ordersPath)},
		Position:     lspPosition{Line: 1, Character: char},
	})
	resp := client.waitForResponse(id, 2*time.Second)
	if resp.Error != nil {
		t.Fatalf("completion error: %s", resp.Error)
	}
	var list completionList
	if err := json.Unmarshal(resp.Result, &list); err != nil {
		t.Fatalf("unmarshal completion list: %v (raw: %s)", err, resp.Result)
	}
	if list.Items == nil {
		t.Errorf("expected non-nil empty items slice")
	}
	if len(list.Items) != 0 {
		t.Errorf("expected 0 items, got %+v", list.Items)
	}
	if list.IsIncomplete {
		t.Errorf("expected IsIncomplete = false")
	}
}

// TestIntegration_CompletionReflectsOverlay checks that a table defined
// only in an unsaved open buffer (never written to disk) appears in
// REFERENCES completion items.
func TestIntegration_CompletionReflectsOverlay(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "sqldefkit.yaml"), "dialect: postgres\nschema_dir: schema\n")
	// DiscoverFiles walks the schema_dir on disk to find candidate .sql
	// files, so new_table.sql must exist there (this is a pre-existing
	// limitation unrelated to hover/completion: a schema file must be
	// saved at least once for the file list to notice it). Its *content*
	// is then entirely overridden by the overlay: on disk it doesn't
	// define "overlay_only" at all, only the unsaved buffer does.
	newTablePath := filepath.Join(root, "schema", "new_table.sql")
	writeTestFile(t, newTablePath, "-- placeholder, not yet saved with real content\n")
	overlayContent := "CREATE TABLE overlay_only (id int PRIMARY KEY);\n"

	client := startTestServer(t)
	client.sendNotification("textDocument/didOpen", didOpenParams{
		TextDocument: textDocumentItem{URI: pathToURI(newTablePath), Text: overlayContent},
	})
	drainDiagnostics(t, client, 1, 2*time.Second)

	otherPath := filepath.Join(root, "schema", "other.sql")
	otherContent := "CREATE TABLE t (id int REFERENCES overlay_"
	client.sendNotification("textDocument/didOpen", didOpenParams{
		TextDocument: textDocumentItem{URI: pathToURI(otherPath), Text: otherContent},
	})
	drainDiagnostics(t, client, 2, 2*time.Second)

	char := byteColToUTF16(otherContent, len(otherContent)+1)
	id := client.sendRequest("textDocument/completion", completionParams{
		TextDocument: textDocumentIdentifier{URI: pathToURI(otherPath)},
		Position:     lspPosition{Line: 0, Character: char},
	})
	resp := client.waitForResponse(id, 2*time.Second)
	if resp.Error != nil {
		t.Fatalf("completion error: %s", resp.Error)
	}
	var list completionList
	if err := json.Unmarshal(resp.Result, &list); err != nil {
		t.Fatalf("unmarshal completion list: %v (raw: %s)", err, resp.Result)
	}
	found := false
	for _, item := range list.Items {
		if item.Label == "overlay_only" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected overlay-only table %q in completion items, got %+v", "overlay_only", list.Items)
	}
}

// TestIntegration_MultibyteHoverAndCompletion checks that hover/completion
// positions after Japanese text on the same line resolve correctly
// (UTF-16 vs byte offset must differ and still hit the right target).
func TestIntegration_MultibyteHoverAndCompletion(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "sqldefkit.yaml"), "dialect: postgres\nschema_dir: schema\n")
	usersPath := filepath.Join(root, "schema", "users.sql")
	writeTestFile(t, usersPath, "CREATE TABLE users (id int PRIMARY KEY); -- ユーザー\n")

	ordersPath := filepath.Join(root, "schema", "orders.sql")
	// Japanese comment ("ユーザーへの参照" = "reference to user") precedes
	// REFERENCES on the same line as the partial word being completed.
	ordersLine := "\tuser_id int /* ユーザーへの参照 */ REFERENCES use"
	ordersContent := "CREATE TABLE orders (\n\tid int PRIMARY KEY,\n" + ordersLine + "rs(id)\n);\n"
	writeTestFile(t, ordersPath, ordersContent)

	client := startTestServer(t)
	openAndDrain(t, client, usersPath, readFileString(t, usersPath), 1)
	openAndDrain(t, client, ordersPath, ordersContent, 2)

	// Hover: position right on "users" in the REFERENCES clause.
	hoverByteCol := strings.Index(ordersLine, "REFERENCES use") + len("REFERENCES ") + 1
	hoverChar := byteColToUTF16(ordersLine, hoverByteCol)
	hoverID := client.sendRequest("textDocument/hover", hoverParams{
		TextDocument: textDocumentIdentifier{URI: pathToURI(ordersPath)},
		Position:     lspPosition{Line: 2, Character: hoverChar},
	})
	hoverResp := client.waitForResponse(hoverID, 2*time.Second)
	if hoverResp.Error != nil {
		t.Fatalf("hover error: %s", hoverResp.Error)
	}
	var hover hoverResult
	if err := json.Unmarshal(hoverResp.Result, &hover); err != nil {
		t.Fatalf("unmarshal hover: %v (raw: %s)", err, hoverResp.Result)
	}
	if !strings.Contains(hover.Contents.Value, "CREATE TABLE users") {
		t.Errorf("hover value missing CREATE TABLE users: %q", hover.Contents.Value)
	}

	// Completion: position right after "use" (mid-word, end of the typed
	// prefix "use" before "rs(id)").
	compByteCol := strings.Index(ordersLine, "REFERENCES use") + len("REFERENCES use") + 1
	compChar := byteColToUTF16(ordersLine, compByteCol)
	compID := client.sendRequest("textDocument/completion", completionParams{
		TextDocument: textDocumentIdentifier{URI: pathToURI(ordersPath)},
		Position:     lspPosition{Line: 2, Character: compChar},
	})
	compResp := client.waitForResponse(compID, 2*time.Second)
	if compResp.Error != nil {
		t.Fatalf("completion error: %s", compResp.Error)
	}
	var list completionList
	if err := json.Unmarshal(compResp.Result, &list); err != nil {
		t.Fatalf("unmarshal completion list: %v (raw: %s)", err, compResp.Result)
	}
	found := false
	for _, item := range list.Items {
		if item.Label == "users" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected %q in completion items after multibyte-line prefix, got %+v", "users", list.Items)
	}
}
