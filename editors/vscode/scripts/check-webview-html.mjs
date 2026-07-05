// Sanity-checks buildGraphHtml (src/graph/panel.ts) without a running VS
// Code host: verifies the CSP contains the nonce, the script tag's src
// uses the placeholder webview URI, and the nonce'd style tag is present.
// Run with: node scripts/check-webview-html.mjs
import { build } from "esbuild";
import { fileURLToPath } from "node:url";
import path from "node:path";
import fs from "node:fs/promises";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const entry = path.join(__dirname, "..", "src", "graph", "panel.ts");

// panel.ts imports "vscode", which doesn't exist outside the extension
// host; stub it out since buildGraphHtml itself never touches the
// vscode module (only the DependencyGraphPanel class does).
const result = await build({
  entryPoints: [entry],
  bundle: true,
  write: false,
  format: "cjs",
  platform: "node",
  target: "es2020",
  external: ["vscode", "vscode-languageclient/node"],
});

const tmpModule = path.join(__dirname, "_panel.check.cjs");
await fs.writeFile(tmpModule, result.outputFiles[0].text);

// panel.ts's top-level `import * as vscode from "vscode"` only needs to
// resolve, not do anything real, since buildGraphHtml itself never
// touches it (only the DependencyGraphPanel class does). Stub both
// externals via Module._load so `require("vscode")` succeeds here.
const Module = (await import("node:module")).default;
const originalLoad = Module._load;
Module._load = function (request, parent, isMain) {
  if (request === "vscode" || request === "vscode-languageclient/node") {
    return {};
  }
  return originalLoad.call(this, request, parent, isMain);
};

let ok = true;
function assert(cond, msg) {
  if (!cond) {
    ok = false;
    console.error("FAIL:", msg);
  } else {
    console.log("OK:", msg);
  }
}

try {
  const { buildGraphHtml } = await import(tmpModule);

  const scriptUri = "vscode-webview://abc123/dist/webview/graph.js";
  const cspSource = "vscode-webview://abc123";
  const testNonce = "abcdefghij0123456789ABCDEFGHIJKL";

  const html = buildGraphHtml(scriptUri, cspSource, testNonce);

  assert(typeof html === "string" && html.includes("<!DOCTYPE html>"), "returns an HTML document string");
  assert(
    html.includes(`script-src 'nonce-${testNonce}'`),
    "CSP meta tag's script-src includes the nonce"
  );
  assert(
    html.includes(`style-src 'nonce-${testNonce}'`),
    "CSP meta tag's style-src includes the nonce"
  );
  assert(html.includes("default-src 'none'"), "CSP defaults to none");
  assert(
    html.includes(`<script nonce="${testNonce}" src="${scriptUri}">`),
    "script tag uses the nonce and the placeholder webview URI"
  );
  assert(
    html.includes(`<style nonce="${testNonce}">`),
    "style tag carries the same nonce"
  );
  assert(
    !/<script(?![^>]*nonce)[^>]*>/.test(html.replace(`<script nonce="${testNonce}" src="${scriptUri}"></script>`, "")),
    "no un-nonced script tags"
  );
} finally {
  await fs.unlink(tmpModule);
}

if (!ok) {
  console.error("\ncheck-webview-html: FAILED");
  process.exit(1);
}
console.log("\ncheck-webview-html: all assertions passed");
