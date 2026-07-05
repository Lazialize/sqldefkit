package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Lazialize/sqldefkit/internal/graphexport"
)

func TestRun_Graph_JSONEndToEnd(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "users.sql"), `CREATE TABLE users (id int PRIMARY KEY);`)
	writeFile(t, filepath.Join(dir, "orders.sql"), `CREATE TABLE orders (
	id int PRIMARY KEY,
	user_id int REFERENCES users(id)
);`)

	var stdout, stderr bytes.Buffer
	err := run([]string{"graph", "--dir", dir, "--dialect", "postgres", "--format", "json"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v (stderr=%s)", err, stderr.String())
	}

	var g graphexport.Graph
	if err := json.Unmarshal(stdout.Bytes(), &g); err != nil {
		t.Fatalf("output not valid JSON: %v\noutput: %s", err, stdout.String())
	}
	if g.Version != graphexport.Version {
		t.Errorf("Version = %d, want %d", g.Version, graphexport.Version)
	}
	if len(g.Nodes) != 2 {
		t.Errorf("len(Nodes) = %d, want 2: %+v", len(g.Nodes), g.Nodes)
	}
	foundEdge := false
	for _, e := range g.Edges {
		if e.From == "orders" && e.To == "users" && e.Kind == "fk" {
			foundEdge = true
		}
	}
	if !foundEdge {
		t.Errorf("missing orders -> users (fk) edge: %+v", g.Edges)
	}
	if !strings.HasSuffix(stdout.String(), "\n") {
		t.Error("expected trailing newline")
	}
}

// TestRun_Graph_CyclicSchemaSucceeds verifies that `graph` succeeds (no
// cycle error) on a schema with an FK cycle, unlike `bundle`/`check`
// which either split it or diagnose it — graph must show cycles, not
// reject them.
func TestRun_Graph_CyclicSchemaSucceeds(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.sql"), `CREATE TABLE a (id int PRIMARY KEY, b_id int REFERENCES b(id));`)
	writeFile(t, filepath.Join(dir, "b.sql"), `CREATE TABLE b (id int PRIMARY KEY, a_id int REFERENCES a(id));`)

	var stdout, stderr bytes.Buffer
	err := run([]string{"graph", "--dir", dir, "--dialect", "postgres", "--format", "json"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error on cyclic schema: %v (stderr=%s)", err, stderr.String())
	}

	var g graphexport.Graph
	if err := json.Unmarshal(stdout.Bytes(), &g); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	for _, n := range g.Nodes {
		if !n.InCycle {
			t.Errorf("node %q not marked InCycle", n.ID)
		}
	}
	for _, e := range g.Edges {
		if !e.InCycle {
			t.Errorf("edge %+v not marked InCycle", e)
		}
	}
}

func TestRun_Graph_DefaultFormatIsDOT(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "users.sql"), `CREATE TABLE users (id int PRIMARY KEY);`)

	var stdout, stderr bytes.Buffer
	err := run([]string{"graph", "--dir", dir, "--dialect", "postgres"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v (stderr=%s)", err, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "digraph dependencies {") {
		t.Errorf("stdout = %q, want a DOT digraph by default", stdout.String())
	}
}

func TestRun_Graph_MermaidFormat(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "users.sql"), `CREATE TABLE users (id int PRIMARY KEY);`)

	var stdout, stderr bytes.Buffer
	err := run([]string{"graph", "--dir", dir, "--dialect", "postgres", "--format", "mermaid"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v (stderr=%s)", err, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "graph TD") {
		t.Errorf("stdout = %q, want a mermaid flowchart", stdout.String())
	}
}

func TestRun_Graph_UnknownFormat(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "users.sql"), `CREATE TABLE users (id int PRIMARY KEY);`)

	var stdout, stderr bytes.Buffer
	err := run([]string{"graph", "--dir", dir, "--dialect", "postgres", "--format", "yaml"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown --format")
	}
	if !strings.Contains(err.Error(), "yaml") {
		t.Errorf("error = %v, want mention of the bad format value", err)
	}
}

func TestRun_Graph_OutputToFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "users.sql"), `CREATE TABLE users (id int PRIMARY KEY);`)
	outPath := filepath.Join(dir, "out.json")

	var stdout, stderr bytes.Buffer
	err := run([]string{"graph", "--dir", dir, "--dialect", "postgres", "--format", "json", "-o", outPath}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v (stderr=%s)", err, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want empty when -o is set", stdout.String())
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}
	if !strings.Contains(string(data), `"version": 1`) {
		t.Errorf("output file content = %q, want version 1 JSON", data)
	}
}
