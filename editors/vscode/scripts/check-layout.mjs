// Headless sanity check for the pure dagre layout step (src/webview/layout.ts).
// Run with: node scripts/check-layout.mjs
// Requires a built dist/webview/graph.js OR ts-node — instead we do a
// lightweight esbuild-based transpile of layout.ts directly in memory
// avoiding the DOM-touching parts of graph.ts/render.ts.
import { build } from "esbuild";
import { fileURLToPath } from "node:url";
import path from "node:path";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const entry = path.join(__dirname, "..", "src", "webview", "layout.ts");

const result = await build({
  entryPoints: [entry],
  bundle: true,
  write: false,
  format: "esm",
  platform: "node",
  target: "es2020",
});

const code = result.outputFiles[0].text;
const tmpModule = path.join(__dirname, "_layout.check.mjs");
await import("node:fs/promises").then((fs) => fs.writeFile(tmpModule, code));

const { layoutGraph } = await import(tmpModule);

// 5 nodes including a cycle: a -> b -> c -> a (cycle), plus d (isolated),
// e (external, referenced by a).
const graph = {
  version: 1,
  nodes: [
    { id: "a", kind: "table", file: "a.sql", line: 1, col: 1, inCycle: true },
    { id: "b", kind: "table", file: "b.sql", line: 1, col: 1, inCycle: true },
    { id: "c", kind: "table", file: "c.sql", line: 1, col: 1, inCycle: true },
    { id: "d", kind: "view", file: "d.sql", line: 1, col: 1 },
    { id: "e", kind: "unknown", external: true },
  ],
  edges: [
    { from: "a", to: "b", kind: "fk", inCycle: true },
    { from: "b", to: "c", kind: "fk", inCycle: true },
    { from: "c", to: "a", kind: "fk", inCycle: true },
    { from: "d", to: "a", kind: "view", inCycle: false },
    { from: "a", to: "e", kind: "fk", inCycle: false },
  ],
};

const layout = layoutGraph(graph);

let ok = true;
function assert(cond, msg) {
  if (!cond) {
    ok = false;
    console.error("FAIL:", msg);
  }
}

assert(layout.nodes.length === 5, `expected 5 nodes, got ${layout.nodes.length}`);
for (const n of layout.nodes) {
  assert(Number.isFinite(n.x), `node ${n.id} x is not finite (${n.x})`);
  assert(Number.isFinite(n.y), `node ${n.id} y is not finite (${n.y})`);
  assert(n.width > 0 && n.height > 0, `node ${n.id} has non-positive size`);
}

assert(layout.edges.length === 5, `expected 5 edges, got ${layout.edges.length}`);
for (const e of layout.edges) {
  assert(Array.isArray(e.points), `edge ${e.from}->${e.to} missing points array`);
  assert(e.points.length >= 2, `edge ${e.from}->${e.to} has fewer than 2 points (${e.points.length})`);
  for (const p of e.points) {
    assert(Number.isFinite(p.x) && Number.isFinite(p.y), `edge ${e.from}->${e.to} has a non-finite point`);
  }
}

assert(Number.isFinite(layout.width) && layout.width > 0, "overall layout width not finite/positive");
assert(Number.isFinite(layout.height) && layout.height > 0, "overall layout height not finite/positive");

console.log(JSON.stringify(layout, null, 2));

// --- v2 payload: two tables, columns + a column-anchored fk edge -------
// Mirrors the shape documented in the main README's "Visualizing the
// dependency graph" section: orders.user_id -> users.id. "users" also
// gets a v1-shaped node (no columns key) mixed into the same graph, plus
// a wider third table (more columns) to assert height grows with column
// count.
const v2Graph = {
  version: 2,
  nodes: [
    {
      id: "orders",
      kind: "table",
      file: "orders.sql",
      line: 1,
      col: 14,
      columns: [
        { name: "id", type: "int", pk: true, notNull: true },
        {
          name: "user_id",
          type: "int",
          notNull: true,
          fk: { table: "users", column: "id" },
        },
      ],
    },
    {
      id: "users",
      kind: "table",
      file: "users.sql",
      line: 1,
      col: 14,
      columns: [
        { name: "id", type: "int", pk: true, notNull: true },
        { name: "email", type: "text", unique: true },
        { name: "name", type: "varchar(255)" },
        { name: "created_at", type: "timestamp", notNull: true },
      ],
    },
    // v1-shaped node: no `columns` key at all (older server). Must still
    // lay out fine, sized like a plain single-row box.
    { id: "legacy_view", kind: "view", file: "legacy_view.sql", line: 1, col: 1 },
  ],
  edges: [
    {
      from: "orders",
      to: "users",
      kind: "fk",
      inCycle: false,
      fromColumn: "user_id",
      toColumn: "id",
    },
    { from: "legacy_view", to: "users", kind: "view", inCycle: false },
  ],
};

const v2Layout = layoutGraph(v2Graph);

const ordersNode = v2Layout.nodes.find((n) => n.id === "orders");
const usersNode = v2Layout.nodes.find((n) => n.id === "users");
const legacyNode = v2Layout.nodes.find((n) => n.id === "legacy_view");

assert(!!ordersNode && !!usersNode && !!legacyNode, "expected orders/users/legacy_view nodes present");

// Node height grows with column count: users has 4 columns, orders has 2;
// both should exceed a header-only box, and users (more rows) should be
// taller than orders.
assert(ordersNode.rows?.length === 2, `orders should have 2 rows, got ${ordersNode.rows?.length}`);
assert(usersNode.rows?.length === 4, `users should have 4 rows, got ${usersNode.rows?.length}`);
assert(
  usersNode.height > ordersNode.height,
  `users (4 cols) should be taller than orders (2 cols): ${usersNode.height} vs ${ordersNode.height}`
);
assert(
  ordersNode.height > (ordersNode.headerHeight ?? 0),
  "orders node height should exceed the header band alone"
);

// legacy_view (v1-shaped, no columns) should render like a plain node:
// no rows, and the same fixed single-row height as before.
assert(legacyNode.rows === undefined, "legacy_view (no columns) should not get row geometry");
assert(legacyNode.headerHeight === undefined, "legacy_view (no columns) should not get a headerHeight");
assert(legacyNode.height > 0, "legacy_view should still get a positive height");

// Row anchor y-coordinates fall inside the node's box and are ordered
// top-to-bottom, per node.
for (const n of [ordersNode, usersNode]) {
  const boxTop = 0;
  const boxBottom = n.height;
  let lastY = -Infinity;
  for (const row of n.rows) {
    assert(row.y > boxTop && row.y < boxBottom, `row ${row.name} on ${n.id} falls outside its box (y=${row.y}, height=${n.height})`);
    assert(row.y > lastY, `row ${row.name} on ${n.id} is not below the previous row (y=${row.y}, lastY=${lastY})`);
    lastY = row.y;
  }
}

// fk edge endpoints coincide with the expected row anchors: orders.user_id
// (fromAnchor) and users.id (toAnchor).
const fkEdge = v2Layout.edges.find((e) => e.from === "orders" && e.to === "users" && e.kind === "fk");
assert(!!fkEdge, "expected the orders->users fk edge");
assert(!!fkEdge.fromAnchor, "fk edge should have a fromAnchor (orders.user_id has a row)");
assert(!!fkEdge.toAnchor, "fk edge should have a toAnchor (users.id has a row)");

function expectedRowAnchor(node, columnName) {
  const row = node.rows.find((r) => r.name === columnName);
  const boxLeft = node.x - node.width / 2;
  const boxRight = node.x + node.width / 2;
  // Whichever side faces the other node's center (mirrors layout.ts's
  // rowAnchorPoint logic).
  return { row, boxLeft, boxRight };
}

const { row: userIdRow } = expectedRowAnchor(ordersNode, "user_id");
const expectedFromY = ordersNode.y - ordersNode.height / 2 + userIdRow.y;
assert(
  Math.abs(fkEdge.fromAnchor.y - expectedFromY) < 0.001,
  `fromAnchor.y should equal orders.user_id row center (${expectedFromY}), got ${fkEdge.fromAnchor.y}`
);
assert(
  fkEdge.fromAnchor.x === ordersNode.x - ordersNode.width / 2 ||
    fkEdge.fromAnchor.x === ordersNode.x + ordersNode.width / 2,
  "fromAnchor.x should sit exactly on orders' left or right box edge"
);

const { row: idRow } = expectedRowAnchor(usersNode, "id");
const expectedToY = usersNode.y - usersNode.height / 2 + idRow.y;
assert(
  Math.abs(fkEdge.toAnchor.y - expectedToY) < 0.001,
  `toAnchor.y should equal users.id row center (${expectedToY}), got ${fkEdge.toAnchor.y}`
);
assert(
  fkEdge.toAnchor.x === usersNode.x - usersNode.width / 2 ||
    fkEdge.toAnchor.x === usersNode.x + usersNode.width / 2,
  "toAnchor.x should sit exactly on users' left or right box edge"
);

// The non-fk view edge (legacy_view -> users) should have no row anchors
// at all, since it carries no fromColumn/toColumn.
const viewEdge = v2Layout.edges.find((e) => e.from === "legacy_view" && e.to === "users");
assert(!!viewEdge, "expected the legacy_view->users view edge");
assert(viewEdge.fromAnchor === undefined, "non-fk edge should not get a fromAnchor");
assert(viewEdge.toAnchor === undefined, "non-fk edge should not get a toAnchor");

console.log(JSON.stringify(v2Layout, null, 2));

await import("node:fs/promises").then((fs) => fs.unlink(tmpModule));

if (!ok) {
  console.error("\ncheck-layout: FAILED");
  process.exit(1);
}
console.log("\ncheck-layout: all assertions passed");
