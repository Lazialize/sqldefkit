import * as vscode from "vscode";
import type { LanguageClient } from "vscode-languageclient/node";
import type {
  DependencyGraphPayload,
  HostToWebviewMessage,
  WebviewToHostMessage,
} from "./protocol";
import { findProjectRoot, resolveGraphFile } from "./resolveTarget";

const DEPENDENCY_GRAPH_REQUEST = "sqldefkit/dependencyGraph";

// requestDependencyGraph sends the sqldefkit/dependencyGraph request for
// targetUri through client. Exported so extension.ts can do an upfront
// check (surfacing "not part of a project" / errors as native info
// messages before opening the panel) while the panel itself reuses the
// same call for its Refresh button.
export async function requestDependencyGraph(
  client: LanguageClient,
  targetUri: vscode.Uri
): Promise<DependencyGraphPayload | null> {
  const result = await client.sendRequest<DependencyGraphPayload | null>(
    DEPENDENCY_GRAPH_REQUEST,
    { uri: targetUri.toString() }
  );
  return result ?? null;
}

function nonce(): string {
  let text = "";
  const possible =
    "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789";
  for (let i = 0; i < 32; i++) {
    text += possible.charAt(Math.floor(Math.random() * possible.length));
  }
  return text;
}

// buildGraphHtml renders the webview's HTML shell: a strict CSP (no
// inline anything except the nonce'd script and a nonce'd stylesheet),
// the bundled webview script referenced via asWebviewUri, and minimal
// markup (canvas host + refresh button) that graph.ts populates/controls.
// Exported standalone (no vscode.Webview dependency beyond the two URIs)
// so it can be sanity-checked from a plain node script without a running
// VS Code host.
export function buildGraphHtml(
  scriptUri: string,
  cspSource: string,
  nonceValue: string
): string {
  return `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src ${cspSource}; style-src 'nonce-${nonceValue}'; script-src 'nonce-${nonceValue}';" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>sqldefkit dependency graph</title>
  <style nonce="${nonceValue}">
    html, body {
      margin: 0;
      padding: 0;
      width: 100%;
      height: 100%;
      overflow: hidden;
      background: var(--vscode-editor-background);
      color: var(--vscode-editor-foreground);
      font-family: var(--vscode-font-family, sans-serif);
    }
    #toolbar {
      position: absolute;
      top: 8px;
      right: 8px;
      z-index: 10;
    }
    #toolbar button {
      background: var(--vscode-button-background);
      color: var(--vscode-button-foreground);
      border: none;
      padding: 4px 10px;
      cursor: pointer;
      border-radius: 2px;
      font-size: 12px;
    }
    #toolbar button:hover {
      background: var(--vscode-button-hoverBackground);
    }
    #status {
      position: absolute;
      top: 8px;
      left: 8px;
      z-index: 10;
      font-size: 12px;
      opacity: 0.8;
    }
    svg {
      display: block;
      width: 100%;
      height: 100%;
      cursor: grab;
    }
    svg.panning {
      cursor: grabbing;
    }
  </style>
</head>
<body>
  <div id="toolbar">
    <button id="refresh-button" type="button">Refresh</button>
  </div>
  <div id="status"></div>
  <div id="graph-root"></div>
  <script nonce="${nonceValue}" src="${scriptUri}"></script>
</body>
</html>`;
}

export class DependencyGraphPanel {
  private static current: DependencyGraphPanel | undefined;

  private readonly panel: vscode.WebviewPanel;
  private readonly extensionUri: vscode.Uri;
  private readonly getClient: () => LanguageClient | undefined;
  private disposed = false;
  private targetUri: vscode.Uri;

  private constructor(
    panel: vscode.WebviewPanel,
    extensionUri: vscode.Uri,
    targetUri: vscode.Uri,
    getClient: () => LanguageClient | undefined
  ) {
    this.panel = panel;
    this.extensionUri = extensionUri;
    this.targetUri = targetUri;
    this.getClient = getClient;

    this.panel.onDidDispose(() => this.dispose());
    this.panel.webview.onDidReceiveMessage((message: WebviewToHostMessage) =>
      this.handleMessage(message)
    );

    this.panel.webview.html = this.renderHtml();
  }

  static async showOrReveal(
    extensionUri: vscode.Uri,
    targetUri: vscode.Uri,
    getClient: () => LanguageClient | undefined
  ): Promise<void> {
    if (DependencyGraphPanel.current) {
      DependencyGraphPanel.current.targetUri = targetUri;
      DependencyGraphPanel.current.panel.reveal(vscode.ViewColumn.Active);
      await DependencyGraphPanel.current.requestAndSendGraph();
      return;
    }

    const panel = vscode.window.createWebviewPanel(
      "sqldefkitDependencyGraph",
      "sqldefkit: Dependency Graph",
      vscode.ViewColumn.Active,
      {
        enableScripts: true,
        retainContextWhenHidden: false,
        localResourceRoots: [vscode.Uri.joinPath(extensionUri, "dist", "webview")],
      }
    );

    DependencyGraphPanel.current = new DependencyGraphPanel(
      panel,
      extensionUri,
      targetUri,
      getClient
    );
    await DependencyGraphPanel.current.requestAndSendGraph();
  }

  private dispose(): void {
    this.disposed = true;
    if (DependencyGraphPanel.current === this) {
      DependencyGraphPanel.current = undefined;
    }
  }

  private renderHtml(): string {
    const scriptUri = this.panel.webview.asWebviewUri(
      vscode.Uri.joinPath(this.extensionUri, "dist", "webview", "graph.js")
    );
    return buildGraphHtml(
      scriptUri.toString(),
      this.panel.webview.cspSource,
      nonce()
    );
  }

  private post(message: HostToWebviewMessage): void {
    if (!this.disposed) {
      void this.panel.webview.postMessage(message);
    }
  }

  private async handleMessage(message: WebviewToHostMessage): Promise<void> {
    switch (message.type) {
      case "ready":
        await this.requestAndSendGraph();
        return;
      case "refresh":
        await this.requestAndSendGraph();
        return;
      case "openNode":
        await this.openNode(message.id);
        return;
    }
  }

  private async requestAndSendGraph(): Promise<void> {
    const client = this.getClient();
    if (!client) {
      this.post({
        type: "error",
        message: "The sqldefkit language server is not running.",
      });
      return;
    }

    try {
      const result = await requestDependencyGraph(client, this.targetUri);
      if (result === null) {
        this.post({
          type: "error",
          message: "This file/workspace is not part of a sqldefkit project.",
        });
        return;
      }
      this.post({ type: "graph", graph: result });
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      this.post({
        type: "error",
        message: `Failed to load the dependency graph: ${message}`,
      });
    }
  }

  private async openNode(id: string): Promise<void> {
    const client = this.getClient();
    if (!client) {
      return;
    }
    const result = await requestDependencyGraph(client, this.targetUri);
    if (!result) {
      return;
    }
    const node = result.nodes.find((n) => n.id === id);
    if (!node || node.external || !node.file) {
      return;
    }

    const projectRoot = (await findProjectRoot(this.targetUri)) ?? this.targetUri;
    const { uri, warning } = await resolveGraphFile(projectRoot, node.file);
    if (warning) {
      void vscode.window.showWarningMessage(`sqldefkit: ${warning}`);
    }
    if (!uri) {
      void vscode.window.showErrorMessage(
        `sqldefkit: could not locate file "${node.file}" for "${id}".`
      );
      return;
    }

    const document = await vscode.workspace.openTextDocument(uri);
    const editor = await vscode.window.showTextDocument(document, {
      viewColumn: vscode.ViewColumn.Beside,
      preserveFocus: false,
    });
    const line = Math.max(0, (node.line ?? 1) - 1);
    const position = new vscode.Position(line, 0);
    editor.selection = new vscode.Selection(position, position);
    editor.revealRange(
      new vscode.Range(position, position),
      vscode.TextEditorRevealType.InCenter
    );
  }
}
