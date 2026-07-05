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

await import("node:fs/promises").then((fs) => fs.unlink(tmpModule));

if (!ok) {
  console.error("\ncheck-layout: FAILED");
  process.exit(1);
}
console.log("\ncheck-layout: all assertions passed");
