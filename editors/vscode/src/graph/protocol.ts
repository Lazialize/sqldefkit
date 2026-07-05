// Shared message shapes between the extension host (src/panel.ts) and the
// webview script (src/webview/graph.ts). Kept dependency-free (no vscode,
// no DOM) so both tsconfigs can include it.

export interface GraphNodePayload {
  id: string;
  kind: string;
  file?: string;
  line?: number;
  col?: number;
  external?: boolean;
  inCycle?: boolean;
}

export interface GraphEdgePayload {
  from: string;
  to: string;
  kind: string;
  inCycle?: boolean;
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
