import { layoutGraph, type DependencyGraph, type LayoutResult } from "./layout";

const SVG_NS = "http://www.w3.org/2000/svg";

// Fill colors keyed by node kind, drawn from VS Code's chart palette CSS
// variables so both dark and light themes stay legible. Falls back to the
// editor foreground for unrecognized kinds.
const KIND_COLOR_VAR: Record<string, string> = {
  table: "--vscode-charts-blue",
  view: "--vscode-charts-green",
  materialized_view: "--vscode-charts-green",
  index: "--vscode-charts-yellow",
  function: "--vscode-charts-purple",
  procedure: "--vscode-charts-purple",
  trigger: "--vscode-charts-orange",
  type: "--vscode-charts-red",
  sequence: "--vscode-charts-yellow",
  extension: "--vscode-charts-foreground",
  other: "--vscode-charts-foreground",
  unknown: "--vscode-charts-foreground",
};

const EDGE_DASH: Record<string, string> = {
  fk: "",
  alter: "",
  view: "6,4",
  directive: "6,4",
  on: "1,4",
};

function kindColorVar(kind: string): string {
  return KIND_COLOR_VAR[kind] ?? "--vscode-charts-foreground";
}

function edgeDash(kind: string): string {
  return EDGE_DASH[kind] ?? "";
}

export interface RenderCallbacks {
  onNodeClick: (id: string) => void;
}

export interface GraphView {
  render(graph: DependencyGraph): void;
  destroy(): void;
}

// createGraphView mounts an SVG-based, pan/zoomable rendering of a
// dependency graph into container. Layout is computed via layoutGraph
// (dagre); this module owns only DOM/SVG construction and interaction.
export function createGraphView(
  container: HTMLElement,
  callbacks: RenderCallbacks
): GraphView {
  container.innerHTML = "";
  const svg = document.createElementNS(SVG_NS, "svg");
  svg.setAttribute("xmlns", SVG_NS);
  container.appendChild(svg);

  const rootGroup = document.createElementNS(SVG_NS, "g");
  svg.appendChild(rootGroup);

  ensureMarkerDefs(svg);

  let viewBox = { x: 0, y: 0, w: 800, h: 600 };
  let panning = false;
  let panStart = { x: 0, y: 0 };
  let viewBoxStart = { x: 0, y: 0 };

  function applyViewBox(): void {
    svg.setAttribute(
      "viewBox",
      `${viewBox.x} ${viewBox.y} ${viewBox.w} ${viewBox.h}`
    );
  }

  svg.addEventListener("wheel", (event: WheelEvent) => {
    event.preventDefault();
    const zoomFactor = event.deltaY > 0 ? 1.1 : 1 / 1.1;
    const rect = svg.getBoundingClientRect();
    const mx = viewBox.x + ((event.clientX - rect.left) / rect.width) * viewBox.w;
    const my = viewBox.y + ((event.clientY - rect.top) / rect.height) * viewBox.h;

    const newW = clamp(viewBox.w * zoomFactor, 50, 20000);
    const newH = clamp(viewBox.h * zoomFactor, 50, 20000);
    viewBox = {
      x: mx - ((mx - viewBox.x) * newW) / viewBox.w,
      y: my - ((my - viewBox.y) * newH) / viewBox.h,
      w: newW,
      h: newH,
    };
    applyViewBox();
  });

  svg.addEventListener("mousedown", (event: MouseEvent) => {
    if (event.button !== 0) {
      return;
    }
    panning = true;
    svg.classList.add("panning");
    panStart = { x: event.clientX, y: event.clientY };
    viewBoxStart = { x: viewBox.x, y: viewBox.y };
  });

  window.addEventListener("mousemove", (event: MouseEvent) => {
    if (!panning) {
      return;
    }
    const rect = svg.getBoundingClientRect();
    const dx = ((event.clientX - panStart.x) / rect.width) * viewBox.w;
    const dy = ((event.clientY - panStart.y) / rect.height) * viewBox.h;
    viewBox = { ...viewBox, x: viewBoxStart.x - dx, y: viewBoxStart.y - dy };
    applyViewBox();
  });

  window.addEventListener("mouseup", () => {
    panning = false;
    svg.classList.remove("panning");
  });

  function clamp(v: number, min: number, max: number): number {
    return Math.max(min, Math.min(max, v));
  }

  function render(graph: DependencyGraph): void {
    const layout = layoutGraph(graph);
    rootGroup.innerHTML = "";

    // Track which SVG elements belong to which "row key" (nodeId + "::" +
    // columnName) and which fk edges touch that row, so hover on a row
    // (or an edge) can toggle highlighting classes across both — pure
    // CSS-class driven, no framework, per the dbmlx-inspired hover UX.
    const rowElementsByKey = new Map<string, SVGElement[]>();
    const edgesByRowKey = new Map<string, SVGElement[]>();
    const edgeElements: SVGElement[] = [];

    function rowKey(nodeId: string, columnName: string): string {
      return `${nodeId}::${columnName}`;
    }
    function addRowElement(key: string, el: SVGElement): void {
      const list = rowElementsByKey.get(key);
      if (list) {
        list.push(el);
      } else {
        rowElementsByKey.set(key, [el]);
      }
    }
    function addEdgeForRowKey(key: string, el: SVGElement): void {
      const list = edgesByRowKey.get(key);
      if (list) {
        list.push(el);
      } else {
        edgesByRowKey.set(key, [el]);
      }
    }

    function setEdgesDimmed(dimmed: boolean): void {
      for (const el of edgeElements) {
        el.classList.toggle("dimmed", dimmed);
      }
    }

    function highlightRowKey(key: string, on: boolean): void {
      for (const el of rowElementsByKey.get(key) ?? []) {
        el.classList.toggle("row-highlight", on);
      }
      for (const el of edgesByRowKey.get(key) ?? []) {
        el.classList.toggle("edge-highlight", on);
      }
    }

    for (const edge of layout.edges) {
      const edgeEl = renderEdge(edge);
      edgeElements.push(edgeEl);
      rootGroup.appendChild(edgeEl);

      if (edge.kind === "fk") {
        if (edge.fromColumn) {
          addEdgeForRowKey(rowKey(edge.from, edge.fromColumn), edgeEl);
        }
        if (edge.toColumn) {
          addEdgeForRowKey(rowKey(edge.to, edge.toColumn), edgeEl);
        }
        edgeEl.addEventListener("mouseover", () => {
          setEdgesDimmed(true);
          edgeEl.classList.remove("dimmed");
          edgeEl.classList.add("edge-highlight");
          if (edge.fromColumn) {
            highlightRowKey(rowKey(edge.from, edge.fromColumn), true);
          }
          if (edge.toColumn) {
            highlightRowKey(rowKey(edge.to, edge.toColumn), true);
          }
        });
        edgeEl.addEventListener("mouseout", () => {
          setEdgesDimmed(false);
          edgeEl.classList.remove("edge-highlight");
          if (edge.fromColumn) {
            highlightRowKey(rowKey(edge.from, edge.fromColumn), false);
          }
          if (edge.toColumn) {
            highlightRowKey(rowKey(edge.to, edge.toColumn), false);
          }
        });
      }
    }
    for (const node of layout.nodes) {
      rootGroup.appendChild(
        renderNode(node, callbacks, {
          onRowHover: (columnName, hovering) => {
            const key = rowKey(node.id, columnName);
            if (hovering) {
              setEdgesDimmed(true);
              for (const el of edgesByRowKey.get(key) ?? []) {
                el.classList.remove("dimmed");
              }
            } else {
              setEdgesDimmed(false);
            }
            highlightRowKey(key, hovering);
          },
          registerRowElement: (columnName, el) => addRowElement(rowKey(node.id, columnName), el),
        })
      );
    }
    rootGroup.appendChild(renderLegend(layout));

    const padding = 40;
    viewBox = {
      x: -padding,
      y: -padding,
      w: layout.width + padding * 2,
      h: layout.height + padding * 2,
    };
    applyViewBox();
  }

  function destroy(): void {
    container.innerHTML = "";
  }

  return { render, destroy };
}

function ensureMarkerDefs(svg: SVGSVGElement): void {
  const defs = document.createElementNS(SVG_NS, "defs");

  const marker = document.createElementNS(SVG_NS, "marker");
  marker.setAttribute("id", "arrow");
  marker.setAttribute("viewBox", "0 0 10 10");
  marker.setAttribute("refX", "9");
  marker.setAttribute("refY", "5");
  marker.setAttribute("markerWidth", "7");
  marker.setAttribute("markerHeight", "7");
  marker.setAttribute("orient", "auto-start-reverse");
  const path = document.createElementNS(SVG_NS, "path");
  path.setAttribute("d", "M 0 0 L 10 5 L 0 10 z");
  path.setAttribute("fill", "var(--vscode-editor-foreground)");
  marker.appendChild(path);
  defs.appendChild(marker);

  const cycleMarker = document.createElementNS(SVG_NS, "marker");
  cycleMarker.setAttribute("id", "arrow-cycle");
  cycleMarker.setAttribute("viewBox", "0 0 10 10");
  cycleMarker.setAttribute("refX", "9");
  cycleMarker.setAttribute("refY", "5");
  cycleMarker.setAttribute("markerWidth", "7");
  cycleMarker.setAttribute("markerHeight", "7");
  cycleMarker.setAttribute("orient", "auto-start-reverse");
  const cyclePath = document.createElementNS(SVG_NS, "path");
  cyclePath.setAttribute("d", "M 0 0 L 10 5 L 0 10 z");
  cyclePath.setAttribute("fill", "#e51400");
  cycleMarker.appendChild(cyclePath);
  defs.appendChild(cycleMarker);

  svg.appendChild(defs);
}

// buildEdgePoints returns the point list to actually draw, splicing in
// fromAnchor/toAnchor (row-level endpoints) in place of dagre's own first/
// last point when present. Rather than just prepending/appending the
// anchor (which can produce a visually odd kink right at the box edge),
// the adjacent dagre point is *replaced*: the path runs as a straight
// segment from the row anchor directly to dagre's second (or
// second-to-last) point, then continues along dagre's original interior
// routing unchanged. This keeps the bulk of the path (which dagre already
// routed to avoid other nodes) intact, and only the segment nearest the
// box — which dagre had anchored to the box's old center/edge — gets
// redrawn to instead meet the specific row.
function buildEdgePoints(
  edge: LayoutResult["edges"][number]
): Array<{ x: number; y: number }> {
  if (edge.points.length < 2) {
    return edge.points;
  }
  const points = [...edge.points];
  if (edge.fromAnchor) {
    points[0] = edge.fromAnchor;
  }
  if (edge.toAnchor) {
    points[points.length - 1] = edge.toAnchor;
  }
  return points;
}

function renderEdge(edge: LayoutResult["edges"][number]): SVGElement {
  const group = document.createElementNS(SVG_NS, "g");
  group.classList.add("edge");
  const points = buildEdgePoints(edge);
  if (points.length >= 2) {
    const d = points.map((p, i) => `${i === 0 ? "M" : "L"} ${p.x} ${p.y}`).join(" ");

    // A wide, invisible stroke widens the hover hit-area beyond the thin
    // visible line, without affecting the drawn appearance.
    const hitArea = document.createElementNS(SVG_NS, "path");
    hitArea.setAttribute("d", d);
    hitArea.setAttribute("fill", "none");
    hitArea.setAttribute("stroke", "transparent");
    hitArea.setAttribute("stroke-width", "12");
    group.appendChild(hitArea);

    const path = document.createElementNS(SVG_NS, "path");
    path.classList.add("edge-path");
    path.setAttribute("d", d);
    path.setAttribute("fill", "none");
    const stroke = edge.inCycle ? "#e51400" : "var(--vscode-editor-foreground)";
    path.setAttribute("stroke", stroke);
    path.setAttribute("stroke-width", edge.inCycle ? "2" : "1.5");
    path.setAttribute("opacity", edge.inCycle ? "0.9" : "0.6");
    const dash = edgeDash(edge.kind);
    if (dash) {
      path.setAttribute("stroke-dasharray", dash);
    }
    path.setAttribute(
      "marker-end",
      edge.inCycle ? "url(#arrow-cycle)" : "url(#arrow)"
    );
    const title = document.createElementNS(SVG_NS, "title");
    const columnSuffix =
      edge.fromColumn && edge.toColumn ? ` [${edge.fromColumn} → ${edge.toColumn}]` : "";
    title.textContent = `${edge.from} → ${edge.to} (${edge.kind})${columnSuffix}`;
    path.appendChild(title);
    group.appendChild(path);
  }
  return group;
}

export interface NodeRenderCallbacks {
  onRowHover: (columnName: string, hovering: boolean) => void;
  registerRowElement: (columnName: string, el: SVGElement) => void;
}

function renderNode(
  node: LayoutResult["nodes"][number],
  callbacks: RenderCallbacks,
  rowCallbacks: NodeRenderCallbacks
): SVGElement {
  const group = document.createElementNS(SVG_NS, "g");
  group.setAttribute("data-node-id", node.id);

  const x = node.x - node.width / 2;
  const y = node.y - node.height / 2;
  const strokeColor = node.inCycle ? "#e51400" : `var(${kindColorVar(node.kind)})`;

  const outline = document.createElementNS(SVG_NS, "rect");
  outline.setAttribute("x", String(x));
  outline.setAttribute("y", String(y));
  outline.setAttribute("width", String(node.width));
  outline.setAttribute("height", String(node.height));
  outline.setAttribute("rx", "6");
  outline.setAttribute("ry", "6");
  outline.setAttribute("fill", "var(--vscode-editor-background)");
  outline.setAttribute("stroke", strokeColor);
  outline.setAttribute("stroke-width", node.inCycle ? "2.5" : "1.5");
  if (node.external) {
    outline.setAttribute("stroke-dasharray", "4,3");
  }
  group.appendChild(outline);

  if (node.rows && node.headerHeight !== undefined) {
    renderTableRows(group, node, x, y, strokeColor, callbacks, rowCallbacks);
  } else {
    renderPlainBody(group, node, x, y);
    if (!node.external) {
      group.style.cursor = "pointer";
      group.addEventListener("click", () => callbacks.onNodeClick(node.id));
    }
  }

  const title = document.createElementNS(SVG_NS, "title");
  title.textContent = `${node.id} (${node.kind}${node.external ? ", external" : ""})`;
  group.appendChild(title);

  return group;
}

// renderPlainBody draws today's single-row box body (fill + centered
// label) for nodes without column data — v1 payloads, or non-table kinds.
function renderPlainBody(
  group: SVGElement,
  node: LayoutResult["nodes"][number],
  x: number,
  y: number
): void {
  const fillRect = document.createElementNS(SVG_NS, "rect");
  fillRect.setAttribute("x", String(x));
  fillRect.setAttribute("y", String(y));
  fillRect.setAttribute("width", String(node.width));
  fillRect.setAttribute("height", String(node.height));
  fillRect.setAttribute("rx", "6");
  fillRect.setAttribute("ry", "6");
  fillRect.setAttribute("fill", `var(${kindColorVar(node.kind)})`);
  fillRect.setAttribute("fill-opacity", node.external ? "0.15" : "0.35");
  fillRect.setAttribute("pointer-events", "none");
  group.appendChild(fillRect);

  const text = document.createElementNS(SVG_NS, "text");
  text.setAttribute("x", String(node.x));
  text.setAttribute("y", String(node.y));
  text.setAttribute("text-anchor", "middle");
  text.setAttribute("dominant-baseline", "middle");
  text.setAttribute("fill", "var(--vscode-editor-foreground)");
  text.setAttribute("font-size", "12");
  text.setAttribute("pointer-events", "none");
  text.textContent = node.id;
  group.appendChild(text);
}

// renderTableRows draws the dbmlx-style ER box: a colored header band
// with the bold table name, followed by one row per column (name left,
// PK/U badges + dimmed type right; NOT NULL columns render their name
// bold). Row click/hover targets are transparent rects layered over the
// text so hit-testing doesn't depend on exact glyph metrics.
function renderTableRows(
  group: SVGElement,
  node: LayoutResult["nodes"][number],
  x: number,
  y: number,
  strokeColor: string,
  callbacks: RenderCallbacks,
  rowCallbacks: NodeRenderCallbacks
): void {
  const headerHeight = node.headerHeight ?? 36;
  const rows = node.rows ?? [];

  const header = document.createElementNS(SVG_NS, "rect");
  header.setAttribute("x", String(x));
  header.setAttribute("y", String(y));
  header.setAttribute("width", String(node.width));
  header.setAttribute("height", String(headerHeight));
  header.setAttribute("fill", `var(${kindColorVar(node.kind)})`);
  header.setAttribute("fill-opacity", node.external ? "0.15" : "0.35");
  if (!node.external) {
    header.style.cursor = "pointer";
    header.addEventListener("click", () => callbacks.onNodeClick(node.id));
  }
  group.appendChild(header);

  const headerText = document.createElementNS(SVG_NS, "text");
  headerText.setAttribute("x", String(node.x));
  headerText.setAttribute("y", String(y + headerHeight / 2));
  headerText.setAttribute("text-anchor", "middle");
  headerText.setAttribute("dominant-baseline", "middle");
  headerText.setAttribute("fill", "var(--vscode-editor-foreground)");
  headerText.setAttribute("font-size", "12");
  headerText.setAttribute("font-weight", "bold");
  headerText.setAttribute("pointer-events", "none");
  headerText.textContent = node.id;
  group.appendChild(headerText);

  const nameX = x + 10;
  const typeRightX = x + node.width - 10;

  for (const [i, col] of rows.entries()) {
    const rowTop = y + headerHeight + i * col.height;
    const rowCenterY = y + col.y;

    if (i > 0) {
      const divider = document.createElementNS(SVG_NS, "line");
      divider.setAttribute("x1", String(x));
      divider.setAttribute("x2", String(x + node.width));
      divider.setAttribute("y1", String(rowTop));
      divider.setAttribute("y2", String(rowTop));
      divider.setAttribute("stroke", strokeColor);
      divider.setAttribute("stroke-opacity", "0.25");
      divider.setAttribute("stroke-width", "1");
      divider.setAttribute("pointer-events", "none");
      group.appendChild(divider);
    }

    const rowGroup = document.createElementNS(SVG_NS, "g");
    rowGroup.classList.add("er-row");
    rowGroup.style.cursor = node.external ? "default" : "pointer";

    const hitRect = document.createElementNS(SVG_NS, "rect");
    hitRect.setAttribute("x", String(x));
    hitRect.setAttribute("y", String(rowTop));
    hitRect.setAttribute("width", String(node.width));
    hitRect.setAttribute("height", String(col.height));
    hitRect.setAttribute("fill", "transparent");
    rowGroup.appendChild(hitRect);

    const nameText = document.createElementNS(SVG_NS, "text");
    nameText.setAttribute("x", String(nameX));
    nameText.setAttribute("y", String(rowCenterY));
    nameText.setAttribute("dominant-baseline", "middle");
    nameText.setAttribute("fill", "var(--vscode-editor-foreground)");
    nameText.setAttribute("font-size", "11");
    nameText.setAttribute("pointer-events", "none");
    if (findColumn(node, col.name)?.notNull) {
      nameText.setAttribute("font-weight", "bold");
    }
    nameText.textContent = col.name;
    rowGroup.appendChild(nameText);

    const column = findColumn(node, col.name);
    const badges: string[] = [];
    if (column?.pk) {
      badges.push("PK");
    }
    if (column?.unique) {
      badges.push("U");
    }

    const typeText = document.createElementNS(SVG_NS, "text");
    typeText.setAttribute("x", String(typeRightX));
    typeText.setAttribute("y", String(rowCenterY));
    typeText.setAttribute("text-anchor", "end");
    typeText.setAttribute("dominant-baseline", "middle");
    typeText.setAttribute("fill", "var(--vscode-editor-foreground)");
    typeText.setAttribute("fill-opacity", "0.6");
    typeText.setAttribute("font-size", "10");
    typeText.setAttribute("pointer-events", "none");
    typeText.textContent = column?.type ?? "";
    rowGroup.appendChild(typeText);

    if (badges.length > 0) {
      const badgeText = document.createElementNS(SVG_NS, "text");
      // Position badges immediately left of the type text; a rough
      // character-width estimate (matches layout.ts's) keeps them from
      // overlapping without needing real text measurement.
      const typeWidthEstimate = (column?.type.length ?? 0) * 6 + 6;
      badgeText.setAttribute("x", String(typeRightX - typeWidthEstimate));
      badgeText.setAttribute("y", String(rowCenterY));
      badgeText.setAttribute("text-anchor", "end");
      badgeText.setAttribute("dominant-baseline", "middle");
      badgeText.setAttribute("fill", `var(${kindColorVar(node.kind)})`);
      badgeText.setAttribute("font-size", "9");
      badgeText.setAttribute("font-weight", "bold");
      badgeText.setAttribute("pointer-events", "none");
      badgeText.textContent = badges.join(" ");
      rowGroup.appendChild(badgeText);
    }

    if (!node.external) {
      rowGroup.addEventListener("click", (event) => {
        event.stopPropagation();
        callbacks.onNodeClick(node.id);
      });
    }
    rowGroup.addEventListener("mouseover", () => rowCallbacks.onRowHover(col.name, true));
    rowGroup.addEventListener("mouseout", () => rowCallbacks.onRowHover(col.name, false));

    group.appendChild(rowGroup);
    rowCallbacks.registerRowElement(col.name, rowGroup);
  }
}

function findColumn(
  node: LayoutResult["nodes"][number],
  name: string
): { name: string; type: string; pk?: boolean; notNull?: boolean; unique?: boolean } | undefined {
  return node.columns?.find((c) => c.name === name);
}

function renderLegend(layout: LayoutResult): SVGElement {
  const group = document.createElementNS(SVG_NS, "g");
  const entries: Array<[string, string]> = [
    ["table", "table"],
    ["view", "view"],
    ["index", "index"],
    ["unknown", "external ref"],
  ];
  // Text-only lines appended below the kind swatches, explaining the ER
  // row markers: badges for PK/unique, and bold for NOT NULL.
  const textLines = ["PK / U badge = primary key / unique", "bold column name = NOT NULL"];

  const x = -20;
  let y = layout.height + 10;
  const totalLines = entries.length + textLines.length;
  const box = document.createElementNS(SVG_NS, "rect");
  box.setAttribute("x", String(x - 10));
  box.setAttribute("y", String(y - 16));
  box.setAttribute("width", "210");
  box.setAttribute("height", String(totalLines * 18 + 10));
  box.setAttribute("fill", "var(--vscode-editor-background)");
  box.setAttribute("stroke", "var(--vscode-editor-foreground)");
  box.setAttribute("stroke-opacity", "0.2");
  group.appendChild(box);

  for (const [kind, label] of entries) {
    const swatch = document.createElementNS(SVG_NS, "rect");
    swatch.setAttribute("x", String(x));
    swatch.setAttribute("y", String(y - 10));
    swatch.setAttribute("width", "12");
    swatch.setAttribute("height", "12");
    swatch.setAttribute("rx", "2");
    swatch.setAttribute("fill", `var(${kindColorVar(kind)})`);
    swatch.setAttribute("fill-opacity", "0.35");
    swatch.setAttribute("stroke", `var(${kindColorVar(kind)})`);
    group.appendChild(swatch);

    const text = document.createElementNS(SVG_NS, "text");
    text.setAttribute("x", String(x + 18));
    text.setAttribute("y", String(y));
    text.setAttribute("font-size", "11");
    text.setAttribute("fill", "var(--vscode-editor-foreground)");
    text.textContent = label;
    group.appendChild(text);

    y += 18;
  }

  for (const line of textLines) {
    const text = document.createElementNS(SVG_NS, "text");
    text.setAttribute("x", String(x));
    text.setAttribute("y", String(y));
    text.setAttribute("font-size", "10");
    text.setAttribute("fill", "var(--vscode-editor-foreground)");
    text.setAttribute("fill-opacity", "0.7");
    text.textContent = line;
    group.appendChild(text);

    y += 18;
  }

  return group;
}
