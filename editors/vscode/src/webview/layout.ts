// Pure dagre-based layout step: turns a dependency graph (nodes/edges) into
// positioned nodes and routed edge points. Deliberately free of any DOM
// access so it can be unit-tested with a plain node script (see
// scripts/check-layout.mjs) as well as bundled into the webview script.
import dagre, { type GraphLabel, type NodeLabel, type EdgeLabel } from "@dagrejs/dagre";

export interface ColumnFK {
  table: string;
  column?: string;
}

export interface Column {
  name: string;
  type: string;
  pk?: boolean;
  notNull?: boolean;
  unique?: boolean;
  fk?: ColumnFK;
}

export interface GraphNode {
  id: string;
  kind: string;
  file?: string;
  line?: number;
  col?: number;
  external?: boolean;
  inCycle?: boolean;
  // Ordered column list; present only for kind=table nodes on version 2+
  // payloads. Its presence (not the payload's top-level version) is what
  // decides whether this node renders as an ER row box or a plain box, so
  // v1-shaped nodes (columns undefined) fall back to today's rendering
  // automatically.
  columns?: Column[];
}

export interface GraphEdge {
  from: string;
  to: string;
  kind: string;
  inCycle?: boolean;
  // Column anchors for "fk" edges, when the payload knows them.
  fromColumn?: string;
  toColumn?: string;
}

export interface DependencyGraph {
  version: number;
  nodes: GraphNode[];
  edges: GraphEdge[];
}

// RowAnchor is one column row's geometry inside its owning node's box, in
// the node's local coordinate space (y measured from the box's top edge,
// as returned alongside the node's own x/y/width/height — see
// PositionedNode.rows). Renderers add the node's box top-left to get
// screen coordinates.
export interface RowAnchor {
  name: string;
  // Vertical center of this row, relative to the box's top edge.
  y: number;
  // Row height, so callers can draw a divider/hit-area.
  height: number;
}

export interface PositionedNode extends GraphNode {
  x: number;
  y: number;
  width: number;
  height: number;
  // Row geometry for table nodes with columns (mirrors node.columns
  // 1:1, in the same order); undefined for nodes without columns (v1
  // payloads, or non-table nodes) so those keep rendering as a single box.
  rows?: RowAnchor[];
  // Header row height (the band occupied by the table name), present
  // whenever `rows` is present. Rows start immediately below it.
  headerHeight?: number;
}

export interface PositionedEdgeEndpoint {
  x: number;
  y: number;
}

export interface PositionedEdge extends GraphEdge {
  points: Array<{ x: number; y: number }>;
  // Row-anchored endpoint overrides, present only when the edge has
  // fromColumn/toColumn AND the corresponding node rendered rows for that
  // column. Renderers should use these in place of points[0]/points[last]
  // when present; `points` itself keeps dagre's original routing for the
  // interior of the path (see layoutGraph's edge-building comment for how
  // the endpoint segments get spliced in).
  fromAnchor?: PositionedEdgeEndpoint;
  toAnchor?: PositionedEdgeEndpoint;
}

export interface LayoutResult {
  nodes: PositionedNode[];
  edges: PositionedEdge[];
  width: number;
  height: number;
}

// --- Text width estimation ---------------------------------------------
//
// We have no canvas/DOM available in layout.ts (it must stay pure so
// scripts/check-layout.mjs can exercise it headlessly), so column/label
// widths are estimated from character count rather than measured. VS
// Code's default UI font is proportional, not monospace, but a flat
// per-character width with generous padding is good enough here: worst
// case we reserve a bit more horizontal space than a row strictly needs,
// which just means slightly wider boxes — never clipped text. The factor
// below (7.5px/char) matches the pre-existing single-line node sizing in
// estimateNodeSize, tuned for VS Code's ~12-13px default editor font.
const CHAR_WIDTH = 7.5;
const NODE_PADDING_X = 24;
const MIN_NODE_WIDTH = 80;

const HEADER_HEIGHT = 36;
const ROW_HEIGHT = 22;
const ROW_PADDING_X = 10;
// Horizontal gap reserved between a column's name and its type/badges so
// they don't visually collide even in the narrowest estimate.
const ROW_NAME_TYPE_GAP = 12;
// Width of a single badge ("PK" or "U"), plus the gap before the type.
const BADGE_WIDTH = 20;

// estimateNodeSize sizes a plain (no columns) node box from its label
// length so labels don't get clipped; height is fixed since labels are
// single-line. Used for v1-shaped nodes and non-table nodes.
function estimateNodeSize(label: string): { width: number; height: number } {
  const width = Math.max(MIN_NODE_WIDTH, Math.round(label.length * CHAR_WIDTH) + NODE_PADDING_X);
  const height = HEADER_HEIGHT;
  return { width, height };
}

// estimateRowWidth estimates the width a column row needs: name + badges
// (PK/U, ~20px each) + gap + type text, plus left/right padding.
function estimateRowWidth(column: Column): number {
  const nameWidth = column.name.length * CHAR_WIDTH;
  const typeWidth = column.type.length * CHAR_WIDTH;
  const badgeCount = (column.pk ? 1 : 0) + (column.unique ? 1 : 0);
  const badgesWidth = badgeCount * BADGE_WIDTH;
  return Math.round(
    nameWidth + ROW_NAME_TYPE_GAP + badgesWidth + typeWidth + ROW_PADDING_X * 2
  );
}

// estimateTableNodeSize sizes a table node that has columns: width is
// driven by the widest row (or the header label), height is the header
// band plus one row per column. Also returns the row anchors (local,
// relative to the box top) so both layoutGraph and renderers can share
// the same geometry.
function estimateTableNodeSize(
  label: string,
  columns: Column[]
): { width: number; height: number; rows: RowAnchor[] } {
  const headerWidth = Math.max(MIN_NODE_WIDTH, Math.round(label.length * CHAR_WIDTH) + NODE_PADDING_X);
  const widestRow = columns.reduce(
    (max, col) => Math.max(max, estimateRowWidth(col)),
    0
  );
  const width = Math.max(headerWidth, widestRow, MIN_NODE_WIDTH);

  const rows: RowAnchor[] = [];
  let y = HEADER_HEIGHT;
  for (const col of columns) {
    rows.push({ name: col.name, y: y + ROW_HEIGHT / 2, height: ROW_HEIGHT });
    y += ROW_HEIGHT;
  }
  const height = HEADER_HEIGHT + columns.length * ROW_HEIGHT;
  return { width, height, rows };
}

// layoutGraph lays out g with dagre (rankdir LR) and returns positioned
// nodes (top-left-independent center x/y, per dagre's convention) and
// edges with their routed point lists. Isolated/unknown-reference nodes
// dagre can't place still get a node entry via setNode, so every input
// node is guaranteed a finite x/y in the result.
export function layoutGraph(g: DependencyGraph): LayoutResult {
  const dg = new dagre.graphlib.Graph<GraphLabel, NodeLabel, EdgeLabel>({
    multigraph: true,
  });
  dg.setGraph({ rankdir: "LR", nodesep: 40, ranksep: 60, marginx: 20, marginy: 20 });
  dg.setDefaultEdgeLabel(() => ({}));

  // Rows computed up front (per node id) so both dagre's sizing pass and
  // the final PositionedNode construction share the exact same geometry.
  const rowsById = new Map<string, { width: number; height: number; rows: RowAnchor[] }>();

  for (const n of g.nodes) {
    if (n.columns) {
      const sized = estimateTableNodeSize(n.id, n.columns);
      rowsById.set(n.id, sized);
      dg.setNode(n.id, { width: sized.width, height: sized.height, label: n.id });
    } else {
      const { width, height } = estimateNodeSize(n.id);
      dg.setNode(n.id, { width, height, label: n.id });
    }
  }

  for (const e of g.edges) {
    if (!dg.hasNode(e.from) || !dg.hasNode(e.to)) {
      continue;
    }
    // dagre keys edges by (v, w, name); include kind in the name so
    // parallel edges of different kinds between the same pair don't
    // collapse into one.
    dg.setEdge(e.from, e.to, {}, e.kind);
  }

  dagre.layout(dg);

  const nodes: PositionedNode[] = g.nodes.map((n) => {
    const label = dg.node(n.id);
    const x = label?.x ?? 0;
    const y = label?.y ?? 0;
    const width = label?.width ?? 0;
    const height = label?.height ?? 0;
    const sized = rowsById.get(n.id);
    return {
      ...n,
      x,
      y,
      width,
      height,
      rows: sized?.rows,
      headerHeight: sized ? HEADER_HEIGHT : undefined,
    };
  });
  const nodesById = new Map(nodes.map((n) => [n.id, n]));

  // rowAnchorPoint returns the screen-space anchor point for a named
  // column row on node n: the vertical center of its row, on whichever
  // side of the box (left or right) faces `towardX` — so an edge coming
  // from the right lands on the box's left edge, and vice versa. Returns
  // undefined if the node has no rows or the column name isn't found,
  // so callers can fall back to the node-box anchoring used today.
  function rowAnchorPoint(
    n: PositionedNode,
    columnName: string | undefined,
    towardX: number
  ): PositionedEdgeEndpoint | undefined {
    if (!n.rows || !columnName) {
      return undefined;
    }
    const row = n.rows.find((r) => r.name === columnName);
    if (!row) {
      return undefined;
    }
    const boxLeft = n.x - n.width / 2;
    const boxRight = n.x + n.width / 2;
    const side = towardX >= n.x ? boxRight : boxLeft;
    return { x: side, y: n.y - n.height / 2 + row.y };
  }

  const edges: PositionedEdge[] = [];
  for (const e of g.edges) {
    if (!dg.hasNode(e.from) || !dg.hasNode(e.to)) {
      // Defensive: graphexport should never emit an edge referencing an
      // undeclared node, but don't let a malformed payload crash layout.
      edges.push({ ...e, points: [] });
      continue;
    }
    const edgeLabel = dg.edge(e.from, e.to, e.kind);
    const points = (edgeLabel?.points ?? []).map((p) => ({ x: p.x, y: p.y }));

    const fromNode = nodesById.get(e.from);
    const toNode = nodesById.get(e.to);

    // Row-anchor overrides: only for fk edges carrying column names, and
    // only when the owning node actually rendered rows (v1 nodes / rowless
    // nodes fall back to box anchoring, handled entirely by the
    // renderer using `points` as before). "Toward" the other node's
    // center approximates which side of the box the edge should exit/
    // enter from once dagre has placed both nodes.
    const fromAnchor =
      fromNode && toNode ? rowAnchorPoint(fromNode, e.fromColumn, toNode.x) : undefined;
    const toAnchor =
      fromNode && toNode ? rowAnchorPoint(toNode, e.toColumn, fromNode.x) : undefined;

    edges.push({ ...e, points, fromAnchor, toAnchor });
  }

  const graphLabel = dg.graph();
  return {
    nodes,
    edges,
    width: graphLabel?.width ?? 0,
    height: graphLabel?.height ?? 0,
  };
}
