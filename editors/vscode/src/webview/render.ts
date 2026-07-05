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

    for (const edge of layout.edges) {
      rootGroup.appendChild(renderEdge(edge));
    }
    for (const node of layout.nodes) {
      rootGroup.appendChild(renderNode(node, callbacks));
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

function renderEdge(edge: LayoutResult["edges"][number]): SVGElement {
  const group = document.createElementNS(SVG_NS, "g");
  if (edge.points.length >= 2) {
    const d = edge.points
      .map((p, i) => `${i === 0 ? "M" : "L"} ${p.x} ${p.y}`)
      .join(" ");
    const path = document.createElementNS(SVG_NS, "path");
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
    title.textContent = `${edge.from} → ${edge.to} (${edge.kind})`;
    path.appendChild(title);
    group.appendChild(path);
  }
  return group;
}

function renderNode(
  node: LayoutResult["nodes"][number],
  callbacks: RenderCallbacks
): SVGElement {
  const group = document.createElementNS(SVG_NS, "g");
  group.setAttribute("data-node-id", node.id);

  const x = node.x - node.width / 2;
  const y = node.y - node.height / 2;

  const rect = document.createElementNS(SVG_NS, "rect");
  rect.setAttribute("x", String(x));
  rect.setAttribute("y", String(y));
  rect.setAttribute("width", String(node.width));
  rect.setAttribute("height", String(node.height));
  rect.setAttribute("rx", "6");
  rect.setAttribute("ry", "6");
  rect.setAttribute("fill", `var(${kindColorVar(node.kind)})`);
  rect.setAttribute("fill-opacity", node.external ? "0.15" : "0.35");
  const strokeColor = node.inCycle ? "#e51400" : `var(${kindColorVar(node.kind)})`;
  rect.setAttribute("stroke", strokeColor);
  rect.setAttribute("stroke-width", node.inCycle ? "2.5" : "1.5");
  if (node.external) {
    rect.setAttribute("stroke-dasharray", "4,3");
  }
  if (!node.external) {
    group.style.cursor = "pointer";
    group.addEventListener("click", () => callbacks.onNodeClick(node.id));
  }
  group.appendChild(rect);

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

  const title = document.createElementNS(SVG_NS, "title");
  title.textContent = `${node.id} (${node.kind}${node.external ? ", external" : ""})`;
  group.appendChild(title);

  return group;
}

function renderLegend(layout: LayoutResult): SVGElement {
  const group = document.createElementNS(SVG_NS, "g");
  const entries: Array<[string, string]> = [
    ["table", "table"],
    ["view", "view"],
    ["index", "index"],
    ["unknown", "external ref"],
  ];

  const x = -20;
  let y = layout.height + 10;
  const box = document.createElementNS(SVG_NS, "rect");
  box.setAttribute("x", String(x - 10));
  box.setAttribute("y", String(y - 16));
  box.setAttribute("width", "150");
  box.setAttribute("height", String(entries.length * 18 + 10));
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

  return group;
}
