package graphexport

import (
	"testing"
)

// fixtureGraph is a small, hand-built Graph (not derived from Build) used
// to pin exact formatter output: one plain table, one FK cycle between
// orders/users (both directions marked InCycle), and one external node,
// so DOT/Mermaid/JSON output for all three "interesting" shapes (cycle,
// external, plain) is locked down in one place. Table nodes carry a v2
// Columns list and the fk edges carry FromColumn/ToColumn, to pin the v2
// JSON shape; DOT/Mermaid stay object-level and must render identically to
// before despite this extra data (see FormatDOT/FormatMermaid tests).
func fixtureGraph() Graph {
	return Graph{
		Version: 2,
		Nodes: []Node{
			{ID: "ghost", Kind: "unknown", External: true},
			{ID: "orders", Kind: "table", File: "orders.sql", Line: 1, Col: 14, InCycle: true, Columns: []Column{
				{Name: "id", Type: "int", PK: true, NotNull: true},
				{Name: "user_id", Type: "int", FK: &ColumnFK{Table: "users", Column: "id"}},
			}},
			{ID: "users", Kind: "table", File: "users.sql", Line: 1, Col: 14, InCycle: true, Columns: []Column{
				{Name: "id", Type: "int", PK: true, NotNull: true},
				{Name: "order_id", Type: "int", FK: &ColumnFK{Table: "orders", Column: "id"}},
			}},
		},
		Edges: []Edge{
			{From: "orders", To: "ghost", Kind: "fk", InCycle: false},
			{From: "orders", To: "users", Kind: "fk", InCycle: true, FromColumn: "user_id", ToColumn: "id"},
			{From: "users", To: "orders", Kind: "fk", InCycle: true, FromColumn: "order_id", ToColumn: "id"},
		},
	}
}

func TestFormatJSON_ExactOutput(t *testing.T) {
	got, err := FormatJSON(fixtureGraph())
	if err != nil {
		t.Fatalf("FormatJSON: %v", err)
	}
	want := `{
  "version": 2,
  "nodes": [
    {
      "id": "ghost",
      "kind": "unknown",
      "external": true
    },
    {
      "id": "orders",
      "kind": "table",
      "file": "orders.sql",
      "line": 1,
      "col": 14,
      "inCycle": true,
      "columns": [
        {
          "name": "id",
          "type": "int",
          "pk": true,
          "notNull": true
        },
        {
          "name": "user_id",
          "type": "int",
          "fk": {
            "table": "users",
            "column": "id"
          }
        }
      ]
    },
    {
      "id": "users",
      "kind": "table",
      "file": "users.sql",
      "line": 1,
      "col": 14,
      "inCycle": true,
      "columns": [
        {
          "name": "id",
          "type": "int",
          "pk": true,
          "notNull": true
        },
        {
          "name": "order_id",
          "type": "int",
          "fk": {
            "table": "orders",
            "column": "id"
          }
        }
      ]
    }
  ],
  "edges": [
    {
      "from": "orders",
      "to": "ghost",
      "kind": "fk",
      "inCycle": false
    },
    {
      "from": "orders",
      "to": "users",
      "kind": "fk",
      "inCycle": true,
      "fromColumn": "user_id",
      "toColumn": "id"
    },
    {
      "from": "users",
      "to": "orders",
      "kind": "fk",
      "inCycle": true,
      "fromColumn": "order_id",
      "toColumn": "id"
    }
  ]
}
`
	if string(got) != want {
		t.Errorf("FormatJSON mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestFormatDOT_ExactOutput(t *testing.T) {
	got := string(FormatDOT(fixtureGraph()))
	want := `digraph dependencies {
  rankdir=LR;
  "ghost" [label="ghost", shape=plaintext, style="dashed"];
  "orders" [label="orders", shape=box, color="red"];
  "users" [label="users", shape=box, color="red"];
  "orders" -> "ghost" [label="fk", style=solid];
  "orders" -> "users" [label="fk", style=solid, color="red"];
  "users" -> "orders" [label="fk", style=solid, color="red"];
}
`
	if got != want {
		t.Errorf("FormatDOT mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestFormatDOT_QuotesSchemaQualifiedName(t *testing.T) {
	g := Graph{
		Version: 2,
		Nodes:   []Node{{ID: "schema.table", Kind: "table"}},
	}
	got := string(FormatDOT(g))
	if !containsLine(got, `  "schema.table" [label="schema.table", shape=box];`) {
		t.Errorf("FormatDOT = %q, want a quoted schema.table node line", got)
	}
}

func TestFormatMermaid_ExactOutput(t *testing.T) {
	got := string(FormatMermaid(fixtureGraph()))
	want := `graph TD
  n_ghost_c4745785(((ghost)))
  n_orders_96584038[orders]
  class n_orders_96584038 cycle
  n_users_5b7dcd14[users]
  class n_users_5b7dcd14 cycle
  n_orders_96584038 -->|fk| n_ghost_c4745785
  n_orders_96584038 -->|fk| n_users_5b7dcd14
  n_users_5b7dcd14 -->|fk| n_orders_96584038
  classDef cycle stroke:#ff0000,stroke-width:2px
  linkStyle 1 stroke:#ff0000,stroke-width:2px
  linkStyle 2 stroke:#ff0000,stroke-width:2px
`
	if got != want {
		t.Errorf("FormatMermaid mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestMermaidID_SanitizesDots(t *testing.T) {
	id := mermaidID("schema.table")
	if containsRune(id, '.') {
		t.Errorf("mermaidID(%q) = %q, contains a dot", "schema.table", id)
	}
	// Different names must not collide even if their sanitized prefixes
	// would otherwise match (e.g. "a.b" and "a_b" both naively -> "a_b").
	id2 := mermaidID("a_b")
	id3 := mermaidID("a.b")
	if id2 == id3 {
		t.Errorf("mermaidID collision: %q and %q both -> %q", "a_b", "a.b", id2)
	}
}

func containsLine(s, line string) bool {
	for _, l := range splitLines(s) {
		if l == line {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}
