package graphexport

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Lazialize/sqldefkit/internal/bundle"
)

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustLoad(t *testing.T, dir string) bundle.Loaded {
	t.Helper()
	loaded, err := bundle.Load(dir, bundle.Postgres, os.ReadFile)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return loaded
}

func findNode(g Graph, id string) (Node, bool) {
	for _, n := range g.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return Node{}, false
}

func findEdge(g Graph, from, to, kind string) (Edge, bool) {
	for _, e := range g.Edges {
		if e.From == from && e.To == to && e.Kind == kind {
			return e, true
		}
	}
	return Edge{}, false
}

// TestBuild_NodesAndEdges exercises tables, a view, an index, a
// directive, and a high-confidence external reference all in one schema.
func TestBuild_NodesAndEdges(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "users.sql", `CREATE TABLE users (id int PRIMARY KEY);`)
	writeFile(t, dir, "orders.sql", `CREATE TABLE orders (
	id int PRIMARY KEY,
	user_id int REFERENCES users(id),
	ghost_id int REFERENCES ghost(id)
);
CREATE INDEX idx_orders_user ON orders (user_id);`)
	writeFile(t, dir, "views.sql", `-- sqldefkit:require some_type
CREATE VIEW v_orders AS SELECT * FROM orders o JOIN users u ON u.id = o.user_id;`)

	g := Build(mustLoad(t, dir))

	if g.Version != Version {
		t.Errorf("Version = %d, want %d", g.Version, Version)
	}

	usersNode, ok := findNode(g, "users")
	if !ok {
		t.Fatal("missing users node")
	}
	if usersNode.Kind != "table" || usersNode.File != "users.sql" || usersNode.Line != 1 {
		t.Errorf("users node = %+v", usersNode)
	}

	viewNode, ok := findNode(g, "v_orders")
	if !ok || viewNode.Kind != "view" {
		t.Errorf("v_orders node = %+v, ok=%v", viewNode, ok)
	}

	idxNode, ok := findNode(g, "idx_orders_user")
	if !ok || idxNode.Kind != "index" {
		t.Errorf("idx_orders_user node = %+v, ok=%v", idxNode, ok)
	}

	// External node: ghost is referenced via REFERENCES (high confidence)
	// but never defined.
	ghostNode, ok := findNode(g, "ghost")
	if !ok {
		t.Fatal("missing external ghost node")
	}
	if !ghostNode.External || ghostNode.Kind != "unknown" || ghostNode.File != "" {
		t.Errorf("ghost node = %+v, want external unknown with no file", ghostNode)
	}

	// External node: some_type is referenced via directive but never
	// defined.
	typeNode, ok := findNode(g, "some_type")
	if !ok || !typeNode.External {
		t.Errorf("some_type node = %+v, ok=%v, want external", typeNode, ok)
	}

	if _, ok := findEdge(g, "orders", "users", "fk"); !ok {
		t.Error("missing orders -> users (fk) edge")
	}
	if _, ok := findEdge(g, "orders", "ghost", "fk"); !ok {
		t.Error("missing orders -> ghost (fk) edge")
	}
	if _, ok := findEdge(g, "idx_orders_user", "orders", "on"); !ok {
		t.Error("missing idx_orders_user -> orders (on) edge")
	}
	if _, ok := findEdge(g, "v_orders", "some_type", "directive"); !ok {
		t.Error("missing v_orders -> some_type (directive) edge")
	}
	if _, ok := findEdge(g, "v_orders", "orders", "view"); !ok {
		t.Error("missing v_orders -> orders (view) edge")
	}
	if _, ok := findEdge(g, "v_orders", "users", "view"); !ok {
		t.Error("missing v_orders -> users (view) edge")
	}
}

// TestBuild_ViewScanUndefinedRefDropped verifies that a view's best-effort
// FROM/JOIN scan to an undefined name produces neither a node nor an edge
// (alias false positives are common and this is deliberately never
// warned about, so it shouldn't show up in the graph either).
func TestBuild_ViewScanUndefinedRefDropped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "orders.sql", `CREATE TABLE orders (id int PRIMARY KEY);`)
	writeFile(t, dir, "views.sql", `CREATE VIEW v AS SELECT * FROM orders o JOIN not_a_real_table x ON x.id = o.id;`)

	g := Build(mustLoad(t, dir))

	if _, ok := findNode(g, "not_a_real_table"); ok {
		t.Error("did not want a node for an undefined view-scan reference")
	}
	if _, ok := findEdge(g, "v", "not_a_real_table", "view"); ok {
		t.Error("did not want an edge for an undefined view-scan reference")
	}
}

// TestBuild_TwoTableFKCycle verifies both nodes and both edges of a
// two-table FK cycle are marked InCycle.
func TestBuild_TwoTableFKCycle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.sql", `CREATE TABLE a (id int PRIMARY KEY, b_id int REFERENCES b(id));`)
	writeFile(t, dir, "b.sql", `CREATE TABLE b (id int PRIMARY KEY, a_id int REFERENCES a(id));`)

	g := Build(mustLoad(t, dir))

	aNode, _ := findNode(g, "a")
	bNode, _ := findNode(g, "b")
	if !aNode.InCycle || !bNode.InCycle {
		t.Errorf("expected both nodes InCycle: a=%+v b=%+v", aNode, bNode)
	}

	aToB, ok := findEdge(g, "a", "b", "fk")
	if !ok || !aToB.InCycle {
		t.Errorf("a -> b edge = %+v, ok=%v, want InCycle", aToB, ok)
	}
	bToA, ok := findEdge(g, "b", "a", "fk")
	if !ok || !bToA.InCycle {
		t.Errorf("b -> a edge = %+v, ok=%v, want InCycle", bToA, ok)
	}
}

// TestBuild_NoCycleErrorEvenWhenUnbreakable verifies Build doesn't fail
// (and doesn't mark anything InCycle) for an acyclic schema, and that a
// cycle bundle couldn't break automatically (closed by a directive, not
// an FK) still succeeds and is flagged, since Build never runs
// FK-cycle-breaking or a topological sort at all.
func TestBuild_UnbreakableCycleStillSucceeds(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.sql", "-- sqldefkit:require b\nCREATE TABLE a (id int);")
	writeFile(t, dir, "b.sql", "CREATE TABLE b (id int, a_id int REFERENCES a(id));")

	g := Build(mustLoad(t, dir))

	aNode, _ := findNode(g, "a")
	bNode, _ := findNode(g, "b")
	if !aNode.InCycle || !bNode.InCycle {
		t.Errorf("expected both nodes InCycle: a=%+v b=%+v", aNode, bNode)
	}
	if _, ok := findEdge(g, "a", "b", "directive"); !ok {
		t.Error("missing a -> b (directive) edge")
	}
	if _, ok := findEdge(g, "b", "a", "fk"); !ok {
		t.Error("missing b -> a (fk) edge")
	}
}

// TestBuild_Dedup verifies that two occurrences of the same
// (from, to, kind) triple collapse to a single edge.
func TestBuild_Dedup(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "users.sql", `CREATE TABLE users (id int PRIMARY KEY);`)
	writeFile(t, dir, "orders.sql", `CREATE TABLE orders (
	id int PRIMARY KEY,
	buyer_id int REFERENCES users(id),
	seller_id int REFERENCES users(id)
);`)

	g := Build(mustLoad(t, dir))

	count := 0
	for _, e := range g.Edges {
		if e.From == "orders" && e.To == "users" && e.Kind == "fk" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("orders -> users (fk) edge count = %d, want 1", count)
	}
}

// TestBuild_Deterministic builds the same schema twice and checks the
// results are identical (ordering included).
func TestBuild_Deterministic(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "z.sql", `CREATE TABLE z (id int PRIMARY KEY);`)
	writeFile(t, dir, "a.sql", `CREATE TABLE a (id int PRIMARY KEY, z_id int REFERENCES z(id));`)
	writeFile(t, dir, "m.sql", `CREATE INDEX idx_a ON a (z_id);`)

	g1 := Build(mustLoad(t, dir))
	g2 := Build(mustLoad(t, dir))

	if !reflect.DeepEqual(g1, g2) {
		t.Errorf("Build not deterministic:\n g1=%+v\n g2=%+v", g1, g2)
	}

	// Nodes sorted by id.
	for i := 1; i < len(g1.Nodes); i++ {
		if g1.Nodes[i-1].ID >= g1.Nodes[i].ID {
			t.Errorf("nodes not sorted by id: %v", g1.Nodes)
			break
		}
	}
	// Edges sorted by (from, to, kind).
	for i := 1; i < len(g1.Edges); i++ {
		a, b := g1.Edges[i-1], g1.Edges[i]
		if a.From > b.From || (a.From == b.From && (a.To > b.To || (a.To == b.To && a.Kind > b.Kind))) {
			t.Errorf("edges not sorted by (from,to,kind): %v", g1.Edges)
			break
		}
	}
}
