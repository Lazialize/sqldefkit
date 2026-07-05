import * as vscode from "vscode";
import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
} from "vscode-languageclient/node";
import { DependencyGraphPanel, requestDependencyGraph } from "./graph/panel";
import { findFirstConfigFile } from "./graph/resolveTarget";

let client: LanguageClient | undefined;
let hasShownStartupError = false;

interface ExperimentalCapabilities {
  dependencyGraph?: boolean;
}

function getServerPath(): string {
  const config = vscode.workspace.getConfiguration("sqldefkit");
  return config.get<string>("serverPath", "sqldefkit");
}

async function startClient(): Promise<void> {
  const serverPath = getServerPath();

  // No transport option: for an Executable the default is plain stdio.
  // Specifying TransportKind.stdio makes vscode-languageclient append a
  // --stdio argument, which the server rejects as an unknown flag.
  const run: import("vscode-languageclient/node").Executable = {
    command: serverPath,
    args: ["lsp"],
  };

  const serverOptions: ServerOptions = {
    run,
    debug: run,
  };

  const clientOptions: LanguageClientOptions = {
    documentSelector: [{ scheme: "file", language: "sql" }],
  };

  client = new LanguageClient(
    "sqldefkit",
    "sqldefkit",
    serverOptions,
    clientOptions
  );

  try {
    await client.start();
    hasShownStartupError = false;
  } catch (err) {
    client = undefined;
    if (!hasShownStartupError) {
      hasShownStartupError = true;
      const message =
        `Failed to start the sqldefkit language server ("${serverPath} lsp"). ` +
        `Install sqldefkit (\`go install github.com/Lazialize/sqldefkit/cmd/sqldefkit@latest\` ` +
        `or download a release binary) and/or set "sqldefkit.serverPath" to its location.`;
      void vscode.window.showErrorMessage(message);
    }
  }
}

async function stopClient(): Promise<void> {
  if (client) {
    const toStop = client;
    client = undefined;
    await toStop.stop();
  }
}

async function restartClient(): Promise<void> {
  await stopClient();
  await startClient();
}

// serverSupportsDependencyGraph reports whether the running language
// server advertised capabilities.experimental.dependencyGraph in its
// initialize response, so the command can feature-detect old server
// binaries instead of sending a request they don't understand.
function serverSupportsDependencyGraph(): boolean {
  const result = client?.initializeResult;
  const experimental = result?.capabilities?.experimental as
    | ExperimentalCapabilities
    | undefined;
  return experimental?.dependencyGraph === true;
}

// resolveGraphTarget determines which project/file to request the graph
// for: the active editor's document if it's a .sql file, else the first
// sqldefkit.yaml/.yml found in the workspace (deterministic: sorted by
// path, "yaml" before "yml").
async function resolveGraphTarget(): Promise<vscode.Uri | undefined> {
  const activeDocument = vscode.window.activeTextEditor?.document;
  if (activeDocument && activeDocument.languageId === "sql" && activeDocument.uri.scheme === "file") {
    return activeDocument.uri;
  }
  return findFirstConfigFile();
}

async function showDependencyGraph(context: vscode.ExtensionContext): Promise<void> {
  if (!client) {
    void vscode.window.showInformationMessage(
      "sqldefkit: the language server is not running. Check \"sqldefkit.serverPath\" and try again."
    );
    return;
  }

  if (!serverSupportsDependencyGraph()) {
    void vscode.window.showInformationMessage(
      "sqldefkit: the dependency graph command requires a newer sqldefkit server (sqldefkit v0.5+). Update your sqldefkit binary."
    );
    return;
  }

  const target = await resolveGraphTarget();
  if (!target) {
    void vscode.window.showInformationMessage(
      "sqldefkit: no sqldefkit.yaml/sqldefkit.yml project found in this workspace."
    );
    return;
  }

  try {
    const result = await requestDependencyGraph(client, target);
    if (result === null) {
      void vscode.window.showInformationMessage(
        "sqldefkit: this file/workspace is not part of a sqldefkit project."
      );
      return;
    }
  } catch (err) {
    const message = err instanceof Error ? err.message : String(err);
    void vscode.window.showErrorMessage(
      `sqldefkit: failed to request the dependency graph: ${message}`
    );
    return;
  }

  await DependencyGraphPanel.showOrReveal(context.extensionUri, target, () => client);
}

export async function activate(context: vscode.ExtensionContext): Promise<void> {
  context.subscriptions.push(
    vscode.workspace.onDidChangeConfiguration((event) => {
      if (event.affectsConfiguration("sqldefkit.serverPath")) {
        void restartClient();
      }
    })
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("sqldefkit.showDependencyGraph", () =>
      showDependencyGraph(context)
    )
  );

  await startClient();
}

export async function deactivate(): Promise<void> {
  await stopClient();
}
