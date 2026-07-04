package bundle

import (
	"os"
	"strings"
	"testing"
)

// TestBuild_FKCycleTwoTablesInline exercises the base case: two tables
// referencing each other via inline column-level REFERENCES. bundle must
// succeed, neither CREATE TABLE may still reference the other, and two
// synthesized ALTER TABLE statements must appear after both tables.
func TestBuild_FKCycleTwoTablesInline(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "orders.sql", `CREATE TABLE orders (
	id int PRIMARY KEY,
	user_id int REFERENCES users(id)
);`)
	writeFile(t, dir, "users.sql", `CREATE TABLE users (
	id int PRIMARY KEY,
	order_id int REFERENCES orders(id)
);`)

	out, err := Build(dir, Postgres, os.ReadFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := string(out)

	createOrdersIdx := strings.Index(text, "CREATE TABLE orders")
	createUsersIdx := strings.Index(text, "CREATE TABLE users")
	alterOrdersIdx := strings.Index(text, "ALTER TABLE orders")
	alterUsersIdx := strings.Index(text, "ALTER TABLE users")

	if createOrdersIdx < 0 || createUsersIdx < 0 || alterOrdersIdx < 0 || alterUsersIdx < 0 {
		t.Fatalf("expected both CREATE TABLEs and both ALTER TABLEs, got:\n%s", text)
	}

	// Neither CREATE TABLE should still contain a REFERENCES clause.
	createOrdersEnd := strings.Index(text[createOrdersIdx:], ";") + createOrdersIdx
	createUsersEnd := strings.Index(text[createUsersIdx:], ";") + createUsersIdx
	if strings.Contains(text[createOrdersIdx:createOrdersEnd], "REFERENCES") {
		t.Errorf("CREATE TABLE orders still contains REFERENCES:\n%s", text[createOrdersIdx:createOrdersEnd])
	}
	if strings.Contains(text[createUsersIdx:createUsersEnd], "REFERENCES") {
		t.Errorf("CREATE TABLE users still contains REFERENCES:\n%s", text[createUsersIdx:createUsersEnd])
	}

	// Both ALTER TABLEs must come after both CREATE TABLEs.
	if alterOrdersIdx < createOrdersIdx || alterOrdersIdx < createUsersIdx {
		t.Errorf("ALTER TABLE orders must sort after both CREATE TABLEs")
	}
	if alterUsersIdx < createOrdersIdx || alterUsersIdx < createUsersIdx {
		t.Errorf("ALTER TABLE users must sort after both CREATE TABLEs")
	}

	if !strings.Contains(text, "ALTER TABLE orders ADD FOREIGN KEY (user_id) REFERENCES users(id);") {
		t.Errorf("expected synthesized ALTER TABLE for orders.user_id, got:\n%s", text)
	}
	if !strings.Contains(text, "ALTER TABLE users ADD FOREIGN KEY (order_id) REFERENCES orders(id);") {
		t.Errorf("expected synthesized ALTER TABLE for users.order_id, got:\n%s", text)
	}
}

// TestBuild_FKCycleDeterministic verifies repeated builds of the same
// breakable cycle produce byte-identical output.
func TestBuild_FKCycleDeterministic(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "orders.sql", `CREATE TABLE orders (id int PRIMARY KEY, user_id int REFERENCES users(id));`)
	writeFile(t, dir, "users.sql", `CREATE TABLE users (id int PRIMARY KEY, order_id int REFERENCES orders(id));`)

	var prev []byte
	for i := 0; i < 5; i++ {
		out, err := Build(dir, Postgres, os.ReadFile)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if prev != nil && string(prev) != string(out) {
			t.Fatalf("non-deterministic output across runs:\n%s\n---\n%s", prev, out)
		}
		prev = out
	}
}

// TestBuild_FKCycleThreeTables exercises a longer cycle A -> B -> C -> A,
// all via table-level FOREIGN KEY constraints.
func TestBuild_FKCycleThreeTables(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.sql", `CREATE TABLE a (
	id int PRIMARY KEY,
	b_id int,
	FOREIGN KEY (b_id) REFERENCES b (id)
);`)
	writeFile(t, dir, "b.sql", `CREATE TABLE b (
	id int PRIMARY KEY,
	c_id int,
	FOREIGN KEY (c_id) REFERENCES c (id)
);`)
	writeFile(t, dir, "c.sql", `CREATE TABLE c (
	id int PRIMARY KEY,
	a_id int,
	FOREIGN KEY (a_id) REFERENCES a (id)
);`)

	out, err := Build(dir, Postgres, os.ReadFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := string(out)

	for _, table := range []string{"a", "b", "c"} {
		createIdx := strings.Index(text, "CREATE TABLE "+table)
		if createIdx < 0 {
			t.Fatalf("missing CREATE TABLE %s in:\n%s", table, text)
		}
		endIdx := strings.Index(text[createIdx:], ");") + createIdx
		if strings.Contains(text[createIdx:endIdx], "FOREIGN KEY") {
			t.Errorf("CREATE TABLE %s still contains FOREIGN KEY:\n%s", table, text[createIdx:endIdx])
		}
	}

	// Each ALTER TABLE must sort after both the table it modifies and its
	// FK target's CREATE TABLE (the guarantee the spec actually makes —
	// not necessarily after every table in the cycle, since a 3-node
	// cycle's feedback edges each only need their own two endpoints
	// available).
	alters := []struct {
		text, source, target string
	}{
		{"ALTER TABLE a ADD FOREIGN KEY (b_id) REFERENCES b (id);", "a", "b"},
		{"ALTER TABLE b ADD FOREIGN KEY (c_id) REFERENCES c (id);", "b", "c"},
		{"ALTER TABLE c ADD FOREIGN KEY (a_id) REFERENCES a (id);", "c", "a"},
	}
	for _, a := range alters {
		alterIdx := strings.Index(text, a.text)
		if alterIdx < 0 {
			t.Errorf("expected %q in output:\n%s", a.text, text)
			continue
		}
		sourceIdx := strings.Index(text, "CREATE TABLE "+a.source)
		targetIdx := strings.Index(text, "CREATE TABLE "+a.target)
		if alterIdx < sourceIdx {
			t.Errorf("%q sorted before CREATE TABLE %s", a.text, a.source)
		}
		if alterIdx < targetIdx {
			t.Errorf("%q sorted before CREATE TABLE %s", a.text, a.target)
		}
	}
}

// TestBuild_CycleThroughDirectiveStillErrors verifies a cycle closed by a
// non-FK edge (a require directive) remains a hard error, unchanged
// message, even though one of the two edges is an FK.
func TestBuild_CycleThroughDirectiveStillErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.sql", `-- sqldefkit:require b
CREATE TABLE a (id int);`)
	writeFile(t, dir, "b.sql", `CREATE TABLE b (id int, a_id int REFERENCES a(id));`)

	_, err := Build(dir, Postgres, os.ReadFile)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	if !strings.Contains(err.Error(), "dependency cycle detected") {
		t.Errorf("error = %v, want dependency cycle message", err)
	}
	if strings.Contains(err.Error(), "foreign key") {
		t.Errorf("error = %v, should not mention foreign keys for a non-FK cycle", err)
	}
}

// TestBuild_FKCyclePlusUnsplittableConstructAugmentedError verifies that
// when a cycle is entirely FK-based but one edge can't be extracted with
// certainty, the error is the same cycle error with one extra sentence
// added.
func TestBuild_FKCyclePlusUnsplittableConstructAugmentedError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.sql", `CREATE TABLE a (
	id int PRIMARY KEY,
	b_id int REFERENCES b(id) SOME_UNRECOGNIZED_OPTION
);`)
	writeFile(t, dir, "b.sql", `CREATE TABLE b (id int PRIMARY KEY, a_id int REFERENCES a(id));`)

	_, err := Build(dir, Postgres, os.ReadFile)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "dependency cycle detected") {
		t.Errorf("error = %q, want dependency cycle message", msg)
	}
	if !strings.Contains(msg, "could not be split automatically") {
		t.Errorf("error = %q, want augmented sentence about unsplittable FK", msg)
	}
}

// TestBuild_FKCycleTwoColumnsSameTarget verifies that when a table has
// two separate FK columns both referencing the same in-SCC table,
// internal/parse's DepRefs deduplication by name (see parse.dedupeRefs)
// doesn't cause the second FK to be silently left in place: both must be
// extracted into their own ALTER TABLE statements.
func TestBuild_FKCycleTwoColumnsSameTarget(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.sql", `CREATE TABLE a (
	id int PRIMARY KEY,
	b_id1 int REFERENCES b(id),
	b_id2 int REFERENCES b(id)
);`)
	writeFile(t, dir, "b.sql", `CREATE TABLE b (id int PRIMARY KEY, a_id int REFERENCES a(id));`)

	out, err := Build(dir, Postgres, os.ReadFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := string(out)

	createAIdx := strings.Index(text, "CREATE TABLE a")
	createAEnd := strings.Index(text[createAIdx:], ");") + createAIdx
	if strings.Contains(text[createAIdx:createAEnd], "REFERENCES") {
		t.Errorf("CREATE TABLE a still contains a REFERENCES clause:\n%s", text[createAIdx:createAEnd])
	}
	if !strings.Contains(text, "ALTER TABLE a ADD FOREIGN KEY (b_id1) REFERENCES b(id);") {
		t.Errorf("expected synthesized ALTER TABLE for a.b_id1, got:\n%s", text)
	}
	if !strings.Contains(text, "ALTER TABLE a ADD FOREIGN KEY (b_id2) REFERENCES b(id);") {
		t.Errorf("expected synthesized ALTER TABLE for a.b_id2, got:\n%s", text)
	}
}

// TestBuild_FKCycleQuotedTableNamesPreserveCaseInAlter verifies that a
// synthesized ALTER TABLE uses the table's verbatim (quoted, case-
// preserved) name — not internal/parse's normalized name — since writing
// an unquoted, lowercased name for a quoted, mixed-case identifier would
// silently change which table the statement targets.
func TestBuild_FKCycleQuotedTableNamesPreserveCaseInAlter(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "orders.sql", `CREATE TABLE "Orders" (
	id int PRIMARY KEY,
	user_id int REFERENCES "Users"(id)
);`)
	writeFile(t, dir, "users.sql", `CREATE TABLE "Users" (
	id int PRIMARY KEY,
	order_id int REFERENCES "Orders"(id)
);`)

	out, err := Build(dir, Postgres, os.ReadFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := string(out)

	if !strings.Contains(text, `ALTER TABLE "Orders" ADD FOREIGN KEY (user_id) REFERENCES "Users"(id);`) {
		t.Errorf("expected quoted ALTER TABLE \"Orders\", got:\n%s", text)
	}
	if !strings.Contains(text, `ALTER TABLE "Users" ADD FOREIGN KEY (order_id) REFERENCES "Orders"(id);`) {
		t.Errorf("expected quoted ALTER TABLE \"Users\", got:\n%s", text)
	}
}

// TestBuild_FKCycleMySQLBackticksTableLevel verifies the MySQL dialect
// (backtick-quoted identifiers) works through the same rewriter, using
// table-level FOREIGN KEY constraints — the form that actually creates an
// enforced constraint under InnoDB (see the MySQL inline-REFERENCES note
// in the README: unlike this table-level form, inline column REFERENCES
// is accepted-but-ignored by InnoDB, though sqldefkit still extracts it
// uniformly).
func TestBuild_FKCycleMySQLBackticksTableLevel(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "orders.sql", "CREATE TABLE `orders` (\n\t`id` int PRIMARY KEY,\n\t`user_id` int, FOREIGN KEY (`user_id`) REFERENCES `users`(`id`)\n);")
	writeFile(t, dir, "users.sql", "CREATE TABLE `users` (\n\t`id` int PRIMARY KEY,\n\t`order_id` int, FOREIGN KEY (`order_id`) REFERENCES `orders`(`id`)\n);")

	out, err := Build(dir, MySQL, os.ReadFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := string(out)
	if !strings.Contains(text, "ALTER TABLE `orders` ADD FOREIGN KEY (`user_id`) REFERENCES `users`(`id`);") {
		t.Errorf("expected backtick-quoted ALTER TABLE for orders, got:\n%s", text)
	}
	if !strings.Contains(text, "ALTER TABLE `users` ADD FOREIGN KEY (`order_id`) REFERENCES `orders`(`id`);") {
		t.Errorf("expected backtick-quoted ALTER TABLE for users, got:\n%s", text)
	}
	if strings.Contains(text, "FOREIGN KEY") && strings.Count(text, "FOREIGN KEY") != 2 {
		t.Errorf("expected exactly 2 FOREIGN KEY occurrences (both in synthesized ALTER TABLEs), got %d:\n%s", strings.Count(text, "FOREIGN KEY"), text)
	}
}

// TestBuild_FKCycleSQLiteLeftInline verifies the sqlite dialect's cycle
// strategy: SQLite has no ALTER TABLE ... ADD FOREIGN KEY (the
// postgres/mysql split would emit a syntax error), and doesn't need one
// since it resolves FKs lazily at DML time — so an all-FK cycle must
// bundle successfully with every CREATE TABLE emitted verbatim
// (REFERENCES still inline), no ALTER TABLE, and no "moved by sqldefkit"
// comment, in deterministic tie-break order.
func TestBuild_FKCycleSQLiteLeftInline(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "orders.sql", `CREATE TABLE orders (
	id integer PRIMARY KEY,
	user_id integer REFERENCES users(id)
);`)
	writeFile(t, dir, "users.sql", `CREATE TABLE users (
	id integer PRIMARY KEY,
	order_id integer REFERENCES orders(id)
);`)

	out, err := Build(dir, SQLite, os.ReadFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := `-- Code generated by sqldefkit; DO NOT EDIT.

-- source: orders.sql
CREATE TABLE orders (
	id integer PRIMARY KEY,
	user_id integer REFERENCES users(id)
);

-- source: users.sql
CREATE TABLE users (
	id integer PRIMARY KEY,
	order_id integer REFERENCES orders(id)
);
`
	if string(out) != want {
		t.Errorf("output mismatch\n--- got ---\n%s\n--- want ---\n%s", out, want)
	}
	if strings.Contains(string(out), "ALTER TABLE") {
		t.Errorf("sqlite output must not contain ALTER TABLE:\n%s", out)
	}
	if strings.Contains(string(out), "moved by sqldefkit") {
		t.Errorf("sqlite output must not contain a moved-constraint comment:\n%s", out)
	}

	// Deterministic across runs.
	for i := 0; i < 5; i++ {
		again, err := Build(dir, SQLite, os.ReadFile)
		if err != nil {
			t.Fatalf("unexpected error on run %d: %v", i, err)
		}
		if string(again) != string(out) {
			t.Fatalf("non-deterministic sqlite output:\n%s\n---\n%s", out, again)
		}
	}
}

// TestBuild_FKCycleSQLiteVsPostgresSameFixture pins the dialect contrast
// on one fixture: sqlite leaves the cycle's REFERENCES inline; postgres
// splits them into ALTER TABLE statements.
func TestBuild_FKCycleSQLiteVsPostgresSameFixture(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.sql", `CREATE TABLE a (id integer PRIMARY KEY, b_id integer REFERENCES b(id));`)
	writeFile(t, dir, "b.sql", `CREATE TABLE b (id integer PRIMARY KEY, a_id integer REFERENCES a(id));`)

	sqliteOut, err := Build(dir, SQLite, os.ReadFile)
	if err != nil {
		t.Fatalf("sqlite: unexpected error: %v", err)
	}
	if strings.Contains(string(sqliteOut), "ALTER TABLE") {
		t.Errorf("sqlite output must not contain ALTER TABLE:\n%s", sqliteOut)
	}
	if !strings.Contains(string(sqliteOut), "b_id integer REFERENCES b(id)") ||
		!strings.Contains(string(sqliteOut), "a_id integer REFERENCES a(id)") {
		t.Errorf("sqlite output must keep both inline REFERENCES verbatim:\n%s", sqliteOut)
	}

	pgOut, err := Build(dir, Postgres, os.ReadFile)
	if err != nil {
		t.Fatalf("postgres: unexpected error: %v", err)
	}
	if !strings.Contains(string(pgOut), "ALTER TABLE a ADD FOREIGN KEY (b_id) REFERENCES b(id);") ||
		!strings.Contains(string(pgOut), "ALTER TABLE b ADD FOREIGN KEY (a_id) REFERENCES a(id);") {
		t.Errorf("postgres output must still split the cycle into ALTER TABLEs:\n%s", pgOut)
	}
}

// TestBuild_FKCycleSQLiteDirectiveCycleStillErrors verifies a sqlite
// cycle closed by a non-FK edge (a require directive) remains a hard
// error exactly like the other dialects.
func TestBuild_FKCycleSQLiteDirectiveCycleStillErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.sql", `-- sqldefkit:require b
CREATE TABLE a (id integer);`)
	writeFile(t, dir, "b.sql", `CREATE TABLE b (id integer, a_id integer REFERENCES a(id));`)

	_, err := Build(dir, SQLite, os.ReadFile)
	if err == nil {
		t.Fatal("expected cycle error")
	}
	if !strings.Contains(err.Error(), "dependency cycle detected") {
		t.Errorf("error = %v, want dependency cycle message", err)
	}
	if strings.Contains(err.Error(), "foreign key") {
		t.Errorf("error = %v, should not mention foreign keys for a non-FK cycle", err)
	}
}

// TestBuild_NonCyclicOutputUnaffected re-confirms (independent of the
// golden file test) that schemas without any cycle produce output
// completely untouched by the cycle-breaking pass.
func TestBuild_NonCyclicOutputUnaffected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "orders.sql", `CREATE TABLE orders (
	id int PRIMARY KEY,
	user_id int REFERENCES users(id)
);`)
	writeFile(t, dir, "users.sql", `CREATE TABLE users (
	id int PRIMARY KEY,
	name text
);`)

	out, err := Build(dir, Postgres, os.ReadFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := `-- Code generated by sqldefkit; DO NOT EDIT.

-- source: users.sql
CREATE TABLE users (
	id int PRIMARY KEY,
	name text
);

-- source: orders.sql
CREATE TABLE orders (
	id int PRIMARY KEY,
	user_id int REFERENCES users(id)
);
`
	if string(out) != want {
		t.Errorf("output mismatch\n--- got ---\n%s\n--- want ---\n%s", out, want)
	}
}
