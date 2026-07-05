// Pure dagre-based layout step: turns a dependency graph (nodes/edges) into
// positioned nodes and routed edge points. Deliberately free of any DOM
// access so it can be unit-tested with a plain node script (see
// scripts/check-layout.mjs) as well as bundled into the webview script.
import dagre, { type GraphLabel, type NodeLabel, type EdgeLabel } from "@dagrejs/dagre";

export interface GraphNode {
  id: string;
  kind: string;
  file?: string;
  line?: number;
  col?: number;
  external?: boolean;
  inCycle?: boolean;
}

export interface GraphEdge {
  from: string;
  to: string;
  kind: string;
  inCycle?: boolean;
}

export interface DependencyGraph {
  version: number;
  nodes: GraphNode[];
  edges: GraphEdge[];
}

export interface PositionedNode extends GraphNode {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface PositionedEdge extends GraphEdge {
  points: Array<{ x: number; y: number }>;
}

export interface LayoutResult {
  nodes: PositionedNode[];
  edges: PositionedEdge[];
  width: number;
  height: number;
}

// estimateNodeSize sizes a node box from its label length so labels don't
// get clipped; height is fixed since labels are single-line.
function estimateNodeSize(label: string): { width: number; height: number } {
  const charWidth = 7.5;
  const paddingX = 24;
  const minWidth = 80;
  const width = Math.max(minWidth, Math.round(label.length * charWidth) + paddingX);
  const height = 36;
  return { width, height };
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

  for (const n of g.nodes) {
    const { width, height } = estimateNodeSize(n.id);
    dg.setNode(n.id, { width, height, label: n.id });
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
    return {
      ...n,
      x: label?.x ?? 0,
      y: label?.y ?? 0,
      width: label?.width ?? 0,
      height: label?.height ?? 0,
    };
  });

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
    edges.push({ ...e, points });
  }

  const graphLabel = dg.graph();
  return {
    nodes,
    edges,
    width: graphLabel?.width ?? 0,
    height: graphLabel?.height ?? 0,
  };
}
