import * as vscode from "vscode";
import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
  TransportKind,
} from "vscode-languageclient/node";

let client: LanguageClient | undefined;
let hasShownStartupError = false;

function getServerPath(): string {
  const config = vscode.workspace.getConfiguration("sqldefkit");
  return config.get<string>("serverPath", "sqldefkit");
}

async function startClient(): Promise<void> {
  const serverPath = getServerPath();

  const run: import("vscode-languageclient/node").Executable = {
    command: serverPath,
    args: ["lsp"],
    transport: TransportKind.stdio,
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

export async function activate(context: vscode.ExtensionContext): Promise<void> {
  context.subscriptions.push(
    vscode.workspace.onDidChangeConfiguration((event) => {
      if (event.affectsConfiguration("sqldefkit.serverPath")) {
        void restartClient();
      }
    })
  );

  await startClient();
}

export async function deactivate(): Promise<void> {
  await stopClient();
}
