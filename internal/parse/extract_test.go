package parse

import (
	"reflect"
	"sort"
	"testing"

	"github.com/Lazialize/sqldefkit/internal/lexer"
)

func parseOne(t *testing.T, sql string, dialect lexer.Dialect) Statement {
	t.Helper()
	stmts, err := lexer.Split(sql, dialect)
	if err != nil {
		t.Fatalf("lexer.Split error: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("expected exactly 1 statement, got %d", len(stmts))
	}
	return Parse(stmts[0], "", nil)
}

func sorted(ss []string) []string {
	out := append([]string(nil), ss...)
	sort.Strings(out)
	return out
}

func TestExtract_CreateTableBasic(t *testing.T) {
	s := parseOne(t, `CREATE TABLE users (id int primary key);`, lexer.Postgres)
	if s.Kind != KindCreateTable {
		t.Errorf("kind = %v, want KindCreateTable", s.Kind)
	}
	if s.Name != "users" {
		t.Errorf("name = %q, want %q", s.Name, "users")
	}
}

func TestExtract_CreateTableSchemaQualifiedIfNotExistsQuoted(t *testing.T) {
	s := parseOne(t, `CREATE TABLE IF NOT EXISTS public."Users" (id int);`, lexer.Postgres)
	if s.Kind != KindCreateTable {
		t.Errorf("kind = %v", s.Kind)
	}
	if s.Name != "public.Users" {
		t.Errorf("name = %q, want %q", s.Name, "public.Users")
	}
}

func TestExtract_CreateTableReferences(t *testing.T) {
	s := parseOne(t, `CREATE TABLE orders (
		id int PRIMARY KEY,
		user_id int REFERENCES users(id),
		other_id int REFERENCES public.other (id)
	);`, lexer.Postgres)
	if s.Name != "orders" {
		t.Errorf("name = %q", s.Name)
	}
	want := []string{"public.other", "users"}
	got := sorted(s.Deps)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deps = %+v, want %+v", got, want)
	}
}

func TestExtract_CreateTableSelfReferenceIgnored(t *testing.T) {
	s := parseOne(t, `CREATE TABLE nodes (
		id int PRIMARY KEY,
		parent_id int REFERENCES nodes(id)
	);`, lexer.Postgres)
	if len(s.Deps) != 0 {
		t.Errorf("expected no deps (self-reference ignored), got %+v", s.Deps)
	}
}

func TestExtract_AlterTableDependsOnTableAndReferences(t *testing.T) {
	s := parseOne(t, `ALTER TABLE orders ADD CONSTRAINT fk_user FOREIGN KEY (user_id) REFERENCES users(id);`, lexer.Postgres)
	if s.Kind != KindAlterTable {
		t.Errorf("kind = %v", s.Kind)
	}
	if s.Name != "orders" {
		t.Errorf("name = %q", s.Name)
	}
	want := []string{"orders", "users"}
	got := sorted(s.Deps)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deps = %+v, want %+v", got, want)
	}
}

func TestExtract_CreateView(t *testing.T) {
	s := parseOne(t, `CREATE VIEW active_users AS SELECT * FROM users u JOIN accounts a ON u.account_id = a.id WHERE u.active;`, lexer.Postgres)
	if s.Kind != KindCreateView {
		t.Errorf("kind = %v", s.Kind)
	}
	if s.Name != "active_users" {
		t.Errorf("name = %q", s.Name)
	}
	want := []string{"accounts", "users"}
	got := sorted(s.Deps)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deps = %+v, want %+v", got, want)
	}
}

func TestExtract_CreateMaterializedView(t *testing.T) {
	s := parseOne(t, `CREATE MATERIALIZED VIEW mv_report AS SELECT * FROM orders;`, lexer.Postgres)
	if s.Kind != KindCreateView {
		t.Errorf("kind = %v", s.Kind)
	}
	if s.Name != "mv_report" {
		t.Errorf("name = %q", s.Name)
	}
	if got := sorted(s.Deps); !reflect.DeepEqual(got, []string{"orders"}) {
		t.Errorf("deps = %+v", got)
	}
}

func TestExtract_CreateIndex(t *testing.T) {
	s := parseOne(t, `CREATE INDEX idx_orders_user ON orders (user_id);`, lexer.Postgres)
	if s.Kind != KindCreateIndex {
		t.Errorf("kind = %v", s.Kind)
	}
	if s.Name != "idx_orders_user" {
		t.Errorf("name = %q", s.Name)
	}
	if got := sorted(s.Deps); !reflect.DeepEqual(got, []string{"orders"}) {
		t.Errorf("deps = %+v", got)
	}
}

func TestExtract_CreateUniqueIndexIfNotExists(t *testing.T) {
	s := parseOne(t, `CREATE UNIQUE INDEX IF NOT EXISTS idx_u ON users (email);`, lexer.Postgres)
	if s.Kind != KindCreateIndex {
		t.Errorf("kind = %v", s.Kind)
	}
	if s.Name != "idx_u" {
		t.Errorf("name = %q", s.Name)
	}
	if got := sorted(s.Deps); !reflect.DeepEqual(got, []string{"users"}) {
		t.Errorf("deps = %+v", got)
	}
}

func TestExtract_CreateTrigger(t *testing.T) {
	s := parseOne(t, `CREATE TRIGGER trg_orders_upd BEFORE UPDATE ON orders FOR EACH ROW EXECUTE FUNCTION touch_updated_at();`, lexer.Postgres)
	if s.Kind != KindCreateTrigger {
		t.Errorf("kind = %v", s.Kind)
	}
	if s.Name != "trg_orders_upd" {
		t.Errorf("name = %q", s.Name)
	}
	if got := sorted(s.Deps); !reflect.DeepEqual(got, []string{"orders"}) {
		t.Errorf("deps = %+v", got)
	}
}

func TestExtract_Directive(t *testing.T) {
	sql := `-- sqldefkit:require users accounts
CREATE VIEW v AS SELECT 1;`
	s := parseOne(t, sql, lexer.Postgres)
	want := []string{"accounts", "users"}
	got := sorted(s.Deps)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deps = %+v, want %+v", got, want)
	}
}

func TestExtract_DirectiveQuotedName(t *testing.T) {
	sql := `-- sqldefkit:require "Users"
CREATE VIEW v AS SELECT 1;`
	s := parseOne(t, sql, lexer.Postgres)
	if got := sorted(s.Deps); !reflect.DeepEqual(got, []string{"Users"}) {
		t.Errorf("deps = %+v", got)
	}
}

func TestExtract_MySQLBackticks(t *testing.T) {
	s := parseOne(t, "CREATE TABLE `orders` (`user_id` int, FOREIGN KEY (`user_id`) REFERENCES `users`(`id`));", lexer.MySQL)
	if s.Name != "orders" {
		t.Errorf("name = %q", s.Name)
	}
	if got := sorted(s.Deps); !reflect.DeepEqual(got, []string{"users"}) {
		t.Errorf("deps = %+v", got)
	}
}

func TestExtract_KindOtherPassThrough(t *testing.T) {
	s := parseOne(t, `INSERT INTO users (id) VALUES (1);`, lexer.Postgres)
	if s.Kind != KindOther {
		t.Errorf("kind = %v, want KindOther", s.Kind)
	}
	if s.Name != "" {
		t.Errorf("name = %q, want empty", s.Name)
	}
}

func TestExtract_CreateSequenceAndType(t *testing.T) {
	s1 := parseOne(t, `CREATE SEQUENCE seq_orders_id;`, lexer.Postgres)
	if s1.Kind != KindCreateSequence || s1.Name != "seq_orders_id" {
		t.Errorf("seq: kind=%v name=%q", s1.Kind, s1.Name)
	}
	s2 := parseOne(t, `CREATE TYPE mood AS ENUM ('sad', 'ok', 'happy');`, lexer.Postgres)
	if s2.Kind != KindCreateType || s2.Name != "mood" {
		t.Errorf("type: kind=%v name=%q", s2.Kind, s2.Name)
	}
}

func TestExtract_CreateExtension(t *testing.T) {
	s := parseOne(t, `CREATE EXTENSION IF NOT EXISTS pgcrypto;`, lexer.Postgres)
	if s.Kind != KindCreateExtension || s.Name != "pgcrypto" {
		t.Errorf("kind=%v name=%q", s.Kind, s.Name)
	}
}
