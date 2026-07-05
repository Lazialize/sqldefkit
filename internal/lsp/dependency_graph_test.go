package lsp

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Lazialize/sqldefkit/internal/graphexport"
)

// TestIntegration_DependencyGraph exercises the custom
// sqldefkit/dependencyGraph request end to end: initialize advertises
// the experimental capability, a request on an open file returns the
// project's current nodes/edges (overlay-aware — an unsaved edit adding
// a REFERENCES shows up as a new edge before any save), and a request on
// a file outside any project returns null.
func TestIntegration_DependencyGraph(t *testing.T) {
	root, usersPath, ordersPath := setupIntegrationProject(t)

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()

	server := NewServer(serverConn, serverConn, os.Stderr)
	go server.Run()

	client := newTestClient(t, clientConn)

	// 1. initialize -> experimental.dependencyGraph = true.
	id := client.sendRequest("initialize", map[string]any{})
	resp := client.waitForResponse(id, 2*time.Second)
	if resp.Error != nil {
		t.Fatalf("initialize returned error: %s", resp.Error)
	}
	var initResult initializeResult
	if err := json.Unmarshal(resp.Result, &initResult); err != nil {
		t.Fatalf("unmarshal initialize result: %v", err)
	}
	if !initResult.Capabilities.Experimental.DependencyGraph {
		t.Errorf("expected capabilities.experimental.dependencyGraph = true")
	}
	client.sendNotification("initialized", map[string]any{})

	// 2. Open users.sql and orders.sql (orders.sql initially references
	// an undefined "missing_table").
	usersContent := readFileString(t, usersPath)
	client.sendNotification("textDocument/didOpen", didOpenParams{
		TextDocument: textDocumentItem{URI: pathToURI(usersPath), Text: usersContent},
	})
	client.waitForNotification("textDocument/publishDiagnostics", 2*time.Second)

	ordersContent := readFileString(t, ordersPath)
	client.sendNotification("textDocument/didOpen", didOpenParams{
		TextDocument: textDocumentItem{URI: pathToURI(ordersPath), Text: ordersContent},
	})
	drainDiagnostics(t, client, 2, 2*time.Second)

	// 3. dependencyGraph on orders.sql -> nodes for users/orders/the
	// external missing_table reference, plus the fk edge.
	graphID := client.sendRequest("sqldefkit/dependencyGraph", dependencyGraphParams{URI: pathToURI(ordersPath)})
	graphResp := client.waitForResponse(graphID, 2*time.Second)
	if graphResp.Error != nil {
		t.Fatalf("dependencyGraph returned error: %s", graphResp.Error)
	}
	var g graphexport.Graph
	if err := json.Unmarshal(graphResp.Result, &g); err != nil {
		t.Fatalf("unmarshal graph: %v (raw: %s)", err, graphResp.Result)
	}
	if !hasEdge(g, "orders", "missing_table", "fk") {
		t.Errorf("expected orders -> missing_table (fk) edge, got %+v", g.Edges)
	}
	if !hasNode(g, "missing_table") {
		t.Errorf("expected external missing_table node, got %+v", g.Nodes)
	}

	// 4. Unsaved edit fixing orders.sql to reference "users" instead ->
	// a subsequent dependencyGraph request reflects the new edge without
	// any save, and no longer shows missing_table.
	fixedContent := "CREATE TABLE orders (\n\tid int PRIMARY KEY,\n\tuser_id int REFERENCES users(id)\n);\n"
	client.sendNotification("textDocument/didChange", didChangeParams{
		TextDocument:   versionedTextDocumentIdentifier{URI: pathToURI(ordersPath)},
		ContentChanges: []contentChangeEvent{{Text: fixedContent}},
	})
	// Both users.sql and orders.sql are open in this project, so a
	// didChange republishes diagnostics for both; drain both or the
	// server (net.Pipe is unbuffered/synchronous) blocks writing the
	// second notification forever.
	drainDiagnostics(t, client, 2, 2*time.Second)

	graphID2 := client.sendRequest("sqldefkit/dependencyGraph", dependencyGraphParams{URI: pathToURI(ordersPath)})
	graphResp2 := client.waitForResponse(graphID2, 2*time.Second)
	if graphResp2.Error != nil {
		t.Fatalf("dependencyGraph (2) returned error: %s", graphResp2.Error)
	}
	var g2 graphexport.Graph
	if err := json.Unmarshal(graphResp2.Result, &g2); err != nil {
		t.Fatalf("unmarshal graph (2): %v", err)
	}
	if !hasEdge(g2, "orders", "users", "fk") {
		t.Errorf("expected orders -> users (fk) edge after unsaved edit, got %+v", g2.Edges)
	}
	if hasNode(g2, "missing_table") {
		t.Errorf("did not want missing_table node after the fix, got %+v", g2.Nodes)
	}

	// 5. dependencyGraph on a file outside any project -> null.
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "outside.sql")
	writeTestFile(t, outsidePath, "CREATE TABLE outside_table (id int PRIMARY KEY);\n")

	graphID3 := client.sendRequest("sqldefkit/dependencyGraph", dependencyGraphParams{URI: pathToURI(outsidePath)})
	graphResp3 := client.waitForResponse(graphID3, 2*time.Second)
	if graphResp3.Error != nil {
		t.Fatalf("dependencyGraph (outside project) returned error: %s", graphResp3.Error)
	}
	if string(graphResp3.Result) != "null" {
		t.Errorf("expected null result for a file outside any project, got %s", graphResp3.Result)
	}

	_ = root
}

func hasNode(g graphexport.Graph, id string) bool {
	for _, n := range g.Nodes {
		if n.ID == id {
			return true
		}
	}
	return false
}

func hasEdge(g graphexport.Graph, from, to, kind string) bool {
	for _, e := range g.Edges {
		if e.From == from && e.To == to && e.Kind == kind {
			return true
		}
	}
	return false
}
