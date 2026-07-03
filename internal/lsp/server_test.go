package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// testClient is a minimal LSP client used to drive a Server over a
// net.Pipe for integration testing: it can send requests/notifications
// and read back responses/notifications with the same base-protocol
// framing the real server implements.
type testClient struct {
	t      *testing.T
	conn   net.Conn
	br     *bufio.Reader
	nextID int

	mu   sync.Mutex
	recv []json.RawMessage
}

func newTestClient(t *testing.T, conn net.Conn) *testClient {
	return &testClient{t: t, conn: conn, br: bufio.NewReader(conn)}
}

func (c *testClient) send(body []byte) {
	c.t.Helper()
	msg := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
	if _, err := c.conn.Write([]byte(msg)); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

func (c *testClient) sendRequest(method string, params any) int {
	c.t.Helper()
	c.nextID++
	id := c.nextID
	req := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{"2.0", id, method, params}
	body, err := json.Marshal(req)
	if err != nil {
		c.t.Fatalf("marshal: %v", err)
	}
	c.send(body)
	return id
}

func (c *testClient) sendNotification(method string, params any) {
	c.t.Helper()
	note := struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params,omitempty"`
	}{"2.0", method, params}
	body, err := json.Marshal(note)
	if err != nil {
		c.t.Fatalf("marshal: %v", err)
	}
	c.send(body)
}

// readMessage reads one framed message body from the connection.
func (c *testClient) readMessage() ([]byte, error) {
	var contentLength int
	haveLength := false
	for {
		line, err := c.br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, err
			}
			contentLength = n
			haveLength = true
		}
	}
	if !haveLength {
		return nil, fmt.Errorf("no Content-Length")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(c.br, body); err != nil {
		return nil, err
	}
	return body, nil
}

type wireMessage struct {
	Method string          `json:"method"`
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
	Params json.RawMessage `json:"params"`
}

// waitForNotification reads messages until it finds a notification with
// the given method, ignoring (but stashing) any other messages seen along
// the way. Fails the test if none arrives within the timeout.
func (c *testClient) waitForNotification(method string, timeout time.Duration) wireMessage {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c.conn.SetReadDeadline(time.Now().Add(timeout))
		body, err := c.readMessage()
		if err != nil {
			c.t.Fatalf("waiting for notification %s: %v", method, err)
		}
		var msg wireMessage
		if err := json.Unmarshal(body, &msg); err != nil {
			c.t.Fatalf("unmarshal: %v", err)
		}
		if msg.Method == method {
			return msg
		}
	}
	c.t.Fatalf("timed out waiting for notification %s", method)
	return wireMessage{}
}

// waitForResponse reads messages until it finds the response with the
// given request id.
func (c *testClient) waitForResponse(id int, timeout time.Duration) wireMessage {
	c.t.Helper()
	wantID, _ := json.Marshal(id)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c.conn.SetReadDeadline(time.Now().Add(timeout))
		body, err := c.readMessage()
		if err != nil {
			c.t.Fatalf("waiting for response id=%d: %v", id, err)
		}
		var msg wireMessage
		if err := json.Unmarshal(body, &msg); err != nil {
			c.t.Fatalf("unmarshal: %v", err)
		}
		if len(msg.ID) > 0 && string(msg.ID) == string(wantID) {
			return msg
		}
	}
	c.t.Fatalf("timed out waiting for response id=%d", id)
	return wireMessage{}
}

// setupIntegrationProject writes a temp sqldefkit project with two files:
// users.sql (defines "users") and orders.sql (references "users" and an
// unknown table "missing_table").
func setupIntegrationProject(t *testing.T) (root, usersPath, ordersPath string) {
	t.Helper()
	root = t.TempDir()
	writeTestFile(t, filepath.Join(root, "sqldefkit.yaml"), "dialect: postgres\nschema_dir: schema\n")
	usersPath = filepath.Join(root, "schema", "users.sql")
	ordersPath = filepath.Join(root, "schema", "orders.sql")
	writeTestFile(t, usersPath, "CREATE TABLE users (id int PRIMARY KEY);\n")
	writeTestFile(t, ordersPath, "CREATE TABLE orders (\n\tid int PRIMARY KEY,\n\tuser_id int REFERENCES missing_table(id)\n);\n")
	return root, usersPath, ordersPath
}

func TestIntegration_FullLifecycle(t *testing.T) {
	root, usersPath, ordersPath := setupIntegrationProject(t)

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()

	server := NewServer(serverConn, serverConn, os.Stderr)
	done := make(chan int, 1)
	go func() {
		done <- server.Run()
	}()

	client := newTestClient(t, clientConn)

	// 1. initialize -> correct capabilities.
	id := client.sendRequest("initialize", map[string]any{})
	resp := client.waitForResponse(id, 2*time.Second)
	if resp.Error != nil {
		t.Fatalf("initialize returned error: %s", resp.Error)
	}
	var initResult initializeResult
	if err := json.Unmarshal(resp.Result, &initResult); err != nil {
		t.Fatalf("unmarshal initialize result: %v", err)
	}
	if !initResult.Capabilities.DefinitionProvider {
		t.Errorf("expected definitionProvider = true")
	}
	if initResult.Capabilities.TextDocumentSync.Change != textDocumentSyncKindFull {
		t.Errorf("expected TextDocumentSync.Change = Full (1), got %d", initResult.Capabilities.TextDocumentSync.Change)
	}
	if !initResult.Capabilities.TextDocumentSync.OpenClose {
		t.Errorf("expected TextDocumentSync.OpenClose = true")
	}
	if initResult.ServerInfo.Name != "sqldefkit" {
		t.Errorf("ServerInfo.Name = %q, want sqldefkit", initResult.ServerInfo.Name)
	}

	client.sendNotification("initialized", map[string]any{})

	// 2. didOpen orders.sql (has an unknown REFERENCES target) -> expect
	// a warning diagnostic at the right position.
	ordersContent := readFileString(t, ordersPath)
	client.sendNotification("textDocument/didOpen", didOpenParams{
		TextDocument: textDocumentItem{URI: pathToURI(ordersPath), Text: ordersContent},
	})

	diagMsg := client.waitForNotification("textDocument/publishDiagnostics", 2*time.Second)
	var diagParams publishDiagnosticsParams
	if err := json.Unmarshal(diagMsg.Params, &diagParams); err != nil {
		t.Fatalf("unmarshal diagnostics: %v", err)
	}
	if diagParams.URI != pathToURI(ordersPath) {
		t.Fatalf("diagnostics URI = %s, want %s", diagParams.URI, pathToURI(ordersPath))
	}
	if len(diagParams.Diagnostics) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %+v", len(diagParams.Diagnostics), diagParams.Diagnostics)
	}
	d := diagParams.Diagnostics[0]
	if d.Severity != 2 {
		t.Errorf("expected warning severity (2), got %d", d.Severity)
	}
	// Line 2 (0-based) is "\tuser_id int REFERENCES missing_table(id)".
	if d.Range.Start.Line != 2 {
		t.Errorf("diagnostic line = %d, want 2", d.Range.Start.Line)
	}
	wantLine := "\tuser_id int REFERENCES missing_table(id)"
	wantChar := byteColToUTF16(wantLine, strings.Index(wantLine, "missing_table")+1)
	if d.Range.Start.Character != wantChar {
		t.Errorf("diagnostic character = %d, want %d", d.Range.Start.Character, wantChar)
	}

	// 3. didChange fixing the file -> expect empty diagnostics.
	fixedContent := "CREATE TABLE orders (\n\tid int PRIMARY KEY,\n\tuser_id int REFERENCES users(id)\n);\n"
	client.sendNotification("textDocument/didChange", didChangeParams{
		TextDocument:   versionedTextDocumentIdentifier{URI: pathToURI(ordersPath)},
		ContentChanges: []contentChangeEvent{{Text: fixedContent}},
	})
	diagMsg2 := client.waitForNotification("textDocument/publishDiagnostics", 2*time.Second)
	var diagParams2 publishDiagnosticsParams
	if err := json.Unmarshal(diagMsg2.Params, &diagParams2); err != nil {
		t.Fatalf("unmarshal diagnostics: %v", err)
	}
	if len(diagParams2.Diagnostics) != 0 {
		t.Fatalf("expected 0 diagnostics after fix, got %+v", diagParams2.Diagnostics)
	}

	// 4. didOpen users.sql, then definition request on "users" reference
	// in orders.sql -> Location pointing at users.sql's CREATE TABLE name.
	usersContent := readFileString(t, usersPath)
	client.sendNotification("textDocument/didOpen", didOpenParams{
		TextDocument: textDocumentItem{URI: pathToURI(usersPath), Text: usersContent},
	})
	// Consume the diagnostics republish triggered by this didOpen (project
	// has 2 open files now: orders.sql and users.sql). Drain both.
	drainDiagnostics(t, client, 2, 2*time.Second)

	// Position of "users" in the fixed orders.sql content, line 2 (0-based).
	refLine := "\tuser_id int REFERENCES users(id)"
	byteCol := strings.Index(refLine, "users") + 1
	char := byteColToUTF16(refLine, byteCol)

	defID := client.sendRequest("textDocument/definition", definitionParams{
		TextDocument: textDocumentIdentifier{URI: pathToURI(ordersPath)},
		Position:     lspPosition{Line: 2, Character: char},
	})
	defResp := client.waitForResponse(defID, 2*time.Second)
	if defResp.Error != nil {
		t.Fatalf("definition returned error: %s", defResp.Error)
	}
	var loc location
	if err := json.Unmarshal(defResp.Result, &loc); err != nil {
		t.Fatalf("unmarshal location: %v (raw: %s)", err, defResp.Result)
	}
	if loc.URI != pathToURI(usersPath) {
		t.Errorf("definition URI = %s, want %s", loc.URI, pathToURI(usersPath))
	}
	if loc.Range.Start.Line != 0 {
		t.Errorf("definition line = %d, want 0", loc.Range.Start.Line)
	}

	// 5. definition on a name with no definition -> null. Point at
	// "orders" in "CREATE TABLE orders (" (line 0), which IS defined, so
	// instead test a position that hits nothing: whitespace at the very
	// start of the file, which has no reference/definition span.
	defID2 := client.sendRequest("textDocument/definition", definitionParams{
		TextDocument: textDocumentIdentifier{URI: pathToURI(ordersPath)},
		Position:     lspPosition{Line: 0, Character: 0},
	})
	defResp2 := client.waitForResponse(defID2, 2*time.Second)
	if defResp2.Error != nil {
		t.Fatalf("definition (no target) returned error: %s", defResp2.Error)
	}
	if string(defResp2.Result) != "null" {
		t.Errorf("expected null result for no-target position, got %s", defResp2.Result)
	}

	// 6. shutdown -> exit; loop terminates cleanly.
	shutdownID := client.sendRequest("shutdown", nil)
	shutdownResp := client.waitForResponse(shutdownID, 2*time.Second)
	if shutdownResp.Error != nil {
		t.Fatalf("shutdown returned error: %s", shutdownResp.Error)
	}
	client.sendNotification("exit", nil)

	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit code = %d, want 0 after shutdown", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not terminate after exit")
	}
	_ = root
}

// TestIntegration_ExitWithoutShutdown checks the exit-code-1 path.
func TestIntegration_ExitWithoutShutdown(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()

	server := NewServer(serverConn, serverConn, os.Stderr)
	done := make(chan int, 1)
	go func() {
		done <- server.Run()
	}()

	client := newTestClient(t, clientConn)
	client.sendNotification("exit", nil)

	select {
	case code := <-done:
		if code != 1 {
			t.Errorf("exit code = %d, want 1 without shutdown", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not terminate after exit")
	}
}

// TestIntegration_UnknownRequest checks MethodNotFound handling.
func TestIntegration_UnknownRequest(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()

	server := NewServer(serverConn, serverConn, os.Stderr)
	go server.Run()

	client := newTestClient(t, clientConn)
	id := client.sendRequest("initialize", map[string]any{})
	client.waitForResponse(id, 2*time.Second)
	client.sendNotification("initialized", map[string]any{})

	badID := client.sendRequest("workspace/nonexistentMethod", map[string]any{})
	resp := client.waitForResponse(badID, 2*time.Second)
	if resp.Error == nil {
		t.Fatalf("expected error for unknown method")
	}
	var errObj responseError
	if err := json.Unmarshal(resp.Error, &errObj); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if errObj.Code != codeMethodNotFound {
		t.Errorf("error code = %d, want %d", errObj.Code, codeMethodNotFound)
	}
}

// TestIntegration_JapaneseCommentBeforeName checks that a multibyte
// (Japanese) comment before a name on the same line yields a UTF-16
// character offset that differs from the byte offset, and is correct.
func TestIntegration_JapaneseCommentBeforeName(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "sqldefkit.yaml"), "dialect: postgres\nschema_dir: schema\n")
	sqlPath := filepath.Join(root, "schema", "t.sql")
	// "あいう" is a 3-rune, 9-byte Japanese comment preceding the table
	// name on the very same line via an inline block comment.
	content := "CREATE TABLE /* あいう */ users (id int PRIMARY KEY);\n"
	writeTestFile(t, sqlPath, content)

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	server := NewServer(serverConn, serverConn, os.Stderr)
	go server.Run()

	client := newTestClient(t, clientConn)
	id := client.sendRequest("initialize", map[string]any{})
	client.waitForResponse(id, 2*time.Second)
	client.sendNotification("initialized", map[string]any{})

	client.sendNotification("textDocument/didOpen", didOpenParams{
		TextDocument: textDocumentItem{URI: pathToURI(sqlPath), Text: content},
	})
	// Clean file: expect an (empty) diagnostics publish.
	msg := client.waitForNotification("textDocument/publishDiagnostics", 2*time.Second)
	var params publishDiagnosticsParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(params.Diagnostics) != 0 {
		t.Fatalf("expected no diagnostics, got %+v", params.Diagnostics)
	}

	// Byte offset of "users" in the line.
	line := strings.TrimSuffix(content, "\n")
	byteIdx := strings.Index(line, "users")
	byteCol := byteIdx + 1
	utf16Char := byteColToUTF16(line, byteCol)

	if utf16Char == byteIdx {
		t.Fatalf("expected UTF-16 offset (%d) to differ from byte offset (%d) due to multibyte comment", utf16Char, byteIdx)
	}
	// あいう is 3 runes -> 3 UTF-16 units, but 9 bytes. Byte offset should
	// be 6 bytes ahead of the UTF-16 offset (9 bytes - 3 units = 6).
	if byteIdx-utf16Char != 6 {
		t.Errorf("byte/utf16 offset difference = %d, want 6", byteIdx-utf16Char)
	}

	// Definition request right on "users" should resolve to itself
	// (a CREATE TABLE name is both definition and, trivially, its own
	// jump target).
	defID := client.sendRequest("textDocument/definition", definitionParams{
		TextDocument: textDocumentIdentifier{URI: pathToURI(sqlPath)},
		Position:     lspPosition{Line: 0, Character: utf16Char},
	})
	resp := client.waitForResponse(defID, 2*time.Second)
	if resp.Error != nil {
		t.Fatalf("definition error: %s", resp.Error)
	}
	var loc location
	if err := json.Unmarshal(resp.Result, &loc); err != nil {
		t.Fatalf("unmarshal location: %v", err)
	}
	if loc.URI != pathToURI(sqlPath) {
		t.Errorf("definition URI = %s, want %s", loc.URI, pathToURI(sqlPath))
	}
	if loc.Range.Start.Character != utf16Char {
		t.Errorf("definition range start character = %d, want %d", loc.Range.Start.Character, utf16Char)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// drainDiagnostics reads exactly n publishDiagnostics notifications
// (order not asserted), for cases where multiple files get republished
// together and we just need to get them off the wire before continuing.
func drainDiagnostics(t *testing.T, client *testClient, n int, timeout time.Duration) {
	t.Helper()
	for i := 0; i < n; i++ {
		client.waitForNotification("textDocument/publishDiagnostics", timeout)
	}
}
