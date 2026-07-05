# sqldefkit for VS Code

A thin [Language Server Protocol](https://microsoft.github.io/language-server-protocol/)
client that starts `sqldefkit lsp` and wires it up to SQL files in workspaces
that use [sqldefkit](https://github.com/Lazialize/sqldefkit).

This extension does not implement any language features itself — all
diagnostics, go-to-definition, hover, and completion come from the
`sqldefkit lsp` server. See the
[Editor integration (LSP) section](https://github.com/Lazialize/sqldefkit#editor-integration-lsp)
of the main README for what the server provides and how a project is
recognized.

## Commands

- **`sqldefkit: Show Dependency Graph`** (`sqldefkit.showDependencyGraph`) —
  opens a WebView panel rendering the current project's object dependency
  graph (tables, views, indexes, etc., laid out with dagre). Click a node
  (or a table's header) to jump to its definition; drag the background to
  pan, scroll to zoom, and use the panel's **Refresh** button to re-fetch
  the graph after editing. Requires a `sqldefkit` server new enough to
  advertise the `sqldefkit/dependencyGraph` LSP request (v0.5.0+); older
  servers show an info message instead of opening the panel.

  Table nodes render dbmlx-style entity-relationship boxes when the
  server's payload includes column data (dependency graph payload
  **version 2**): a header with the table name, followed by one row per
  column showing its name and type. `PK` and `U` badges mark primary-key
  and unique columns; a **bold** column name means `NOT NULL`.
  Foreign-key edges anchor to the exact source/target column rows instead
  of the table box as a whole, and hovering a column row (or an edge)
  highlights every FK edge touching that column while dimming the rest.
  Clicking a column row jumps to the table's definition, same as clicking
  the header — the payload doesn't carry per-column source positions.
  Against an older server that only emits payload version 1 (no column
  data), tables fall back to the plain single-box rendering from previous
  versions; nothing breaks, you just don't get row-level detail.

## Requirements

- The `sqldefkit` binary, **v0.3.0 or newer**, available either on your
  `PATH` or at the location configured via `sqldefkit.serverPath`.

  ```sh
  go install github.com/Lazialize/sqldefkit/cmd/sqldefkit@latest
  ```

  or download a prebuilt binary from the
  [releases page](https://github.com/Lazialize/sqldefkit/releases).

- A `sqldefkit.yaml` (or `sqldefkit.yml`) at the root of your schema, as
  described in the main README. The extension (and the server) do nothing
  for files outside a recognized project.

## Why no `onLanguage:sql` activation

This extension activates only when a workspace contains a
`sqldefkit.yaml`/`sqldefkit.yml` file
(`workspaceContains:**/sqldefkit.yaml` / `**/sqldefkit.yml`), not on every
opened SQL file. SQL files are extremely common outside of sqldefkit
projects, and activating on `onLanguage:sql` unconditionally would start
loading this extension (and attempt to spawn the language server) in
unrelated SQL projects that have no use for it. The language client's
`documentSelector` (`{ scheme: "file", language: "sql" }`) still filters
which documents are attached to the server *after* activation — the
`workspaceContains` activation event is just the gate that decides whether
the extension loads at all.

## Settings

| Setting                    | Type   | Default        | Description                                                        |
| --------------------------- | ------ | -------------- | -------------------------------------------------------------------- |
| `sqldefkit.serverPath`       | string | `"sqldefkit"`  | Path to the sqldefkit binary. Change this if it isn't on your `PATH`. |
| `sqldefkit.trace.server`     | string | `"off"`        | LSP trace verbosity: `off`, `messages`, or `verbose`.                 |

Changing `sqldefkit.serverPath` restarts the language client automatically.

## Installing locally

This extension is not published to the marketplace. Build and install it
from source:

```sh
cd editors/vscode
npm install
npm run build
npm run package        # produces sqldefkit-vscode-<version>.vsix
code --install-extension sqldefkit-vscode-0.3.0.vsix
```

Alternatively, for development: open `editors/vscode` in VS Code, run
`npm install && npm run build`, and press F5 to launch an Extension
Development Host with the extension loaded.

## Troubleshooting

If you see an error notification that the language server failed to start,
it almost always means the `sqldefkit` binary could not be found or
executed. Confirm `sqldefkit lsp` runs from a terminal, or point
`sqldefkit.serverPath` at the correct binary path.
