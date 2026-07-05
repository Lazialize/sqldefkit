import type {
  DependencyGraphPayload,
  HostToWebviewMessage,
  WebviewToHostMessage,
} from "../graph/protocol";
import { createGraphView } from "./render";

declare function acquireVsCodeApi(): {
  postMessage(message: WebviewToHostMessage): void;
};

const vscode = acquireVsCodeApi();

function post(message: WebviewToHostMessage): void {
  vscode.postMessage(message);
}

function setStatus(text: string): void {
  const el = document.getElementById("status");
  if (el) {
    el.textContent = text;
  }
}

window.addEventListener("DOMContentLoaded", () => {
  const root = document.getElementById("graph-root");
  const refreshButton = document.getElementById("refresh-button");

  if (!root) {
    return;
  }

  const view = createGraphView(root, {
    onNodeClick: (id: string) => post({ type: "openNode", id }),
  });

  refreshButton?.addEventListener("click", () => {
    setStatus("Refreshing…");
    post({ type: "refresh" });
  });

  window.addEventListener("message", (event: MessageEvent<HostToWebviewMessage>) => {
    const message = event.data;
    switch (message.type) {
      case "graph":
        setStatus("");
        renderGraph(message.graph);
        return;
      case "error":
        setStatus(message.message);
        return;
    }
  });

  function renderGraph(graph: DependencyGraphPayload): void {
    view.render(graph);
  }

  post({ type: "ready" });
});
