// Shared message shapes between the extension host (src/panel.ts) and the
// webview script (src/webview/graph.ts). Kept dependency-free (no vscode,
// no DOM) so both tsconfigs can include it.

export interface ColumnFKPayload {
  table: string;
  column?: string;
}

export interface ColumnPayload {
  name: string;
  type: string;
  pk?: boolean;
  notNull?: boolean;
  unique?: boolean;
  fk?: ColumnFKPayload;
}

export interface GraphNodePayload {
  id: string;
  kind: string;
  file?: string;
  line?: number;
  col?: number;
  external?: boolean;
  inCycle?: boolean;
  // Ordered column list (kind=table only, version 2+ payloads). Absent on
  // version 1 payloads and on non-table nodes; renderers must
  // feature-detect per node (columns undefined) rather than trust the
  // payload's top-level version, since that's the whole point of the
  // "columns present" check being per-node.
  columns?: ColumnPayload[];
}

export interface GraphEdgePayload {
  from: string;
  to: string;
  kind: string;
  inCycle?: boolean;
  // Column anchors for "fk" edges (version 2+ payloads); absent otherwise.
  fromColumn?: string;
  toColumn?: string;
}

export interface DependencyGraphPayload {
  version: number;
  nodes: GraphNodePayload[];
  edges: GraphEdgePayload[];
}

// Messages sent from the extension host to the webview.
export type HostToWebviewMessage =
  | { type: "graph"; graph: DependencyGraphPayload }
  | { type: "error"; message: string };

// Messages sent from the webview to the extension host.
export type WebviewToHostMessage =
  | { type: "ready" }
  | { type: "refresh" }
  | { type: "openNode"; id: string };
