package fkrewrite

import (
	"testing"

	"github.com/Lazialize/sqldefkit/internal/lexer"
)

func extractColumns(t *testing.T, sql string) []Column {
	t.Helper()
	text, toks := stmtText(t, sql)
	return ExtractColumns(text, toks)
}

func findColumn(cols []Column, name string) (Column, bool) {
	for _, c := range cols {
		if c.Name == name {
			return c, true
		}
	}
	return Column{}, false
}

func TestExtractColumns_InlineConstraints(t *testing.T) {
	sql := `CREATE TABLE orders (
	id int PRIMARY KEY,
	user_id int NOT NULL REFERENCES users (id),
	code text UNIQUE,
	amount numeric(10, 2)
)`
	cols := extractColumns(t, sql)
	if len(cols) != 4 {
		t.Fatalf("expected 4 columns, got %d: %+v", len(cols), cols)
	}

	id, _ := findColumn(cols, "id")
	if !id.PK || !id.NotNull {
		t.Errorf("id = %+v, want PK+NotNull", id)
	}
	if id.Type != "int" {
		t.Errorf("id.Type = %q, want int", id.Type)
	}

	userID, _ := findColumn(cols, "user_id")
	if !userID.NotNull {
		t.Errorf("user_id.NotNull = false, want true")
	}
	if userID.FK == nil || userID.FK.Table != "users" || userID.FK.Column != "id" {
		t.Errorf("user_id.FK = %+v, want {users id}", userID.FK)
	}
	if userID.Type != "int" {
		t.Errorf("user_id.Type = %q, want int", userID.Type)
	}

	code, _ := findColumn(cols, "code")
	if !code.Unique {
		t.Errorf("code.Unique = false, want true")
	}

	amount, _ := findColumn(cols, "amount")
	if amount.Type != "numeric(10, 2)" {
		t.Errorf("amount.Type = %q, want numeric(10, 2)", amount.Type)
	}
}

func TestExtractColumns_TableLevelCompositePK(t *testing.T) {
	sql := `CREATE TABLE order_items (
	order_id int,
	product_id int,
	qty int,
	PRIMARY KEY (order_id, product_id)
)`
	cols := extractColumns(t, sql)
	orderID, _ := findColumn(cols, "order_id")
	productID, _ := findColumn(cols, "product_id")
	qty, _ := findColumn(cols, "qty")

	if !orderID.PK || !orderID.NotNull {
		t.Errorf("order_id = %+v, want PK+NotNull", orderID)
	}
	if !productID.PK || !productID.NotNull {
		t.Errorf("product_id = %+v, want PK+NotNull", productID)
	}
	if qty.PK {
		t.Errorf("qty.PK = true, want false")
	}
}

func TestExtractColumns_TableLevelSingleAndCompositeFK(t *testing.T) {
	sql := `CREATE TABLE shipments (
	id int PRIMARY KEY,
	order_id int,
	warehouse_id int,
	region_id int,
	FOREIGN KEY (order_id) REFERENCES orders (id),
	FOREIGN KEY (warehouse_id, region_id) REFERENCES warehouses (id, region)
)`
	cols := extractColumns(t, sql)

	orderID, _ := findColumn(cols, "order_id")
	if orderID.FK == nil || orderID.FK.Table != "orders" || orderID.FK.Column != "id" {
		t.Errorf("order_id.FK = %+v, want {orders id}", orderID.FK)
	}

	warehouseID, _ := findColumn(cols, "warehouse_id")
	if warehouseID.FK == nil || warehouseID.FK.Table != "warehouses" || warehouseID.FK.Column != "id" {
		t.Errorf("warehouse_id.FK = %+v, want {warehouses id}", warehouseID.FK)
	}
	regionID, _ := findColumn(cols, "region_id")
	if regionID.FK == nil || regionID.FK.Table != "warehouses" || regionID.FK.Column != "region" {
		t.Errorf("region_id.FK = %+v, want {warehouses region}", regionID.FK)
	}
}

func TestExtractColumns_CompositeFKMismatchedLengths(t *testing.T) {
	sql := `CREATE TABLE shipments (
	warehouse_id int,
	region_id int,
	FOREIGN KEY (warehouse_id, region_id) REFERENCES warehouses (id)
)`
	cols := extractColumns(t, sql)
	warehouseID, _ := findColumn(cols, "warehouse_id")
	regionID, _ := findColumn(cols, "region_id")
	if warehouseID.FK == nil || warehouseID.FK.Table != "warehouses" || warehouseID.FK.Column != "" {
		t.Errorf("warehouse_id.FK = %+v, want {warehouses \"\"}", warehouseID.FK)
	}
	if regionID.FK == nil || regionID.FK.Table != "warehouses" || regionID.FK.Column != "" {
		t.Errorf("region_id.FK = %+v, want {warehouses \"\"}", regionID.FK)
	}
}

func TestExtractColumns_MultiWordTypes(t *testing.T) {
	cases := []struct {
		name    string
		colDef  string
		colName string
		want    string
	}{
		{"double_precision", "price double precision", "price", "double precision"},
		{"timestamp_tz", "created_at timestamp with time zone", "created_at", "timestamp with time zone"},
		{"varchar", "name character varying(50)", "name", "character varying(50)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sql := "CREATE TABLE t (\n\t" + tc.colDef + "\n)"
			cols := extractColumns(t, sql)
			col, ok := findColumn(cols, tc.colName)
			if !ok {
				t.Fatalf("column %q not found: %+v", tc.colName, cols)
			}
			if col.Type != tc.want {
				t.Errorf("Type = %q, want %q", col.Type, tc.want)
			}
		})
	}
}

func TestExtractColumns_QuotedNames(t *testing.T) {
	// Postgres double-quoted identifier.
	sql := `CREATE TABLE "Orders" (
	"Id" int PRIMARY KEY,
	user_id int
)`
	cols := extractColumns(t, sql)
	if len(cols) != 2 {
		t.Fatalf("expected 2 columns, got %d: %+v", len(cols), cols)
	}
	id, ok := findColumn(cols, "Id")
	if !ok || !id.PK {
		t.Errorf("Id column = %+v, ok=%v, want PK", id, ok)
	}

	// MySQL backtick-quoted identifier.
	mysqlSQL := "CREATE TABLE `Orders` (\n\t`Id` int PRIMARY KEY,\n\t`user_id` int\n)"
	stmts, err := lexer.Split(mysqlSQL, lexer.MySQL)
	if err != nil {
		t.Fatalf("lexer.Split: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(stmts))
	}
	mysqlCols := ExtractColumns(stmts[0].Text, stmts[0].Tokens)
	if len(mysqlCols) != 2 {
		t.Fatalf("expected 2 mysql columns, got %d: %+v", len(mysqlCols), mysqlCols)
	}
	mid, ok := findColumn(mysqlCols, "Id")
	if !ok || !mid.PK {
		t.Errorf("mysql Id column = %+v, ok=%v, want PK", mid, ok)
	}
}

func TestExtractColumns_DefaultWithParenExpression(t *testing.T) {
	sql := `CREATE TABLE t (
	id int PRIMARY KEY,
	created_at timestamp DEFAULT (now())
)`
	cols := extractColumns(t, sql)
	col, ok := findColumn(cols, "created_at")
	if !ok {
		t.Fatalf("created_at not found: %+v", cols)
	}
	if col.Type != "timestamp" {
		t.Errorf("Type = %q, want timestamp (DEFAULT expression must not be swallowed)", col.Type)
	}
}

func TestExtractColumns_WeirdEntryFailSoft(t *testing.T) {
	// A deliberately malformed/unusual list item (a bare CHECK-like
	// expression with no name in a position ExtractColumns treats as a
	// column def) must not panic or abort the whole extraction — it may be
	// skipped or degraded, but the rest of the table's columns still come
	// through.
	sql := `CREATE TABLE t (
	id int PRIMARY KEY,
	***weird*** garbage,
	name text
)`
	var cols []Column
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("ExtractColumns panicked: %v", r)
			}
		}()
		cols = extractColumns(t, sql)
	}()

	if _, ok := findColumn(cols, "id"); !ok {
		t.Errorf("expected id column to still be present: %+v", cols)
	}
	if _, ok := findColumn(cols, "name"); !ok {
		t.Errorf("expected name column to still be present: %+v", cols)
	}
}

func TestExtractColumns_NoTableShape(t *testing.T) {
	text, toks := stmtText(t, `CREATE TABLE t AS SELECT 1`)
	cols := ExtractColumns(text, toks)
	if cols != nil {
		t.Errorf("expected nil columns for a non-column-list CREATE TABLE, got %+v", cols)
	}
}
