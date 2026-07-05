import * as vscode from "vscode";

// findProjectRoot walks up from startUri's directory looking for a
// sqldefkit.yaml/sqldefkit.yml file, returning the directory that
// contains it. Returns undefined if none is found before the workspace
// folder root (or filesystem root, for files outside any workspace
// folder).
export async function findProjectRoot(
  startUri: vscode.Uri
): Promise<vscode.Uri | undefined> {
  let dir = vscode.Uri.joinPath(startUri, "..");
  const workspaceFolder = vscode.workspace.getWorkspaceFolder(startUri);
  const stopAt = workspaceFolder ? workspaceFolder.uri.fsPath : undefined;

  // Bound the walk so a file outside any workspace folder (or a
  // pathological filesystem) can't loop forever: cap at 200 levels.
  for (let i = 0; i < 200; i++) {
    const yaml = vscode.Uri.joinPath(dir, "sqldefkit.yaml");
    const yml = vscode.Uri.joinPath(dir, "sqldefkit.yml");
    if (await fileExists(yaml)) {
      return dir;
    }
    if (await fileExists(yml)) {
      return dir;
    }

    const parent = vscode.Uri.joinPath(dir, "..");
    if (parent.fsPath === dir.fsPath) {
      // Reached the filesystem root.
      return undefined;
    }
    if (stopAt && dir.fsPath === stopAt) {
      // Reached the workspace folder root without finding a config;
      // don't walk above the workspace folder.
      return undefined;
    }
    dir = parent;
  }
  return undefined;
}

async function fileExists(uri: vscode.Uri): Promise<boolean> {
  try {
    await vscode.workspace.fs.stat(uri);
    return true;
  } catch {
    return false;
  }
}

// findFirstConfigFile scans workspace folders for the first
// sqldefkit.yaml/.yml, deterministically: workspace folders in their
// declared order, and within a folder, whichever findFiles returns first
// (sorted by path) for "sqldefkit.yaml" then "sqldefkit.yml".
export async function findFirstConfigFile(): Promise<vscode.Uri | undefined> {
  const patterns = ["**/sqldefkit.yaml", "**/sqldefkit.yml"];
  for (const pattern of patterns) {
    const matches = await vscode.workspace.findFiles(pattern, "**/node_modules/**");
    if (matches.length > 0) {
      const sorted = [...matches].sort((a, b) => a.fsPath.localeCompare(b.fsPath));
      return sorted[0];
    }
  }
  return undefined;
}

export interface ResolveFileResult {
  uri?: vscode.Uri;
  warning?: string;
}

// resolveGraphFile turns a schema-root-relative "file" field from a
// dependencyGraph node into an openable vscode.Uri.
//
// Design choice: rather than parsing the project's sqldefkit.yaml to find
// its `schema_dir` (which requires a real YAML parser to do correctly —
// the config file's schema_dir can point anywhere, and hand-rolling that
// with regex is exactly the kind of fragile parsing this project's own
// Go config loader avoids), this resolves the click target by searching
// the workspace for a file whose path ends with the payload's relative
// path, scoped under the project root directory (the directory containing
// the sqldefkit.yaml/.yml discovered by findProjectRoot). This is robust
// because schema file basenames (and typically their relative paths) are
// unique within one project by construction (sqldefkit itself requires
// unique object names across the schema tree, and files are one-per-path);
// if more than one file matches, we can't disambiguate blindly, so we warn
// and open the first (deterministic, sorted) candidate rather than
// silently guessing which one the user meant.
export async function resolveGraphFile(
  projectRoot: vscode.Uri,
  relativeFile: string
): Promise<ResolveFileResult> {
  const normalized = relativeFile.replace(/\\/g, "/");
  const direct = vscode.Uri.joinPath(projectRoot, normalized);
  if (await fileExists(direct)) {
    return { uri: direct };
  }

  // Fall back to a workspace-wide search by relative-path suffix, in case
  // projectRoot (sqldefkit.yaml's directory) differs from the project's
  // schema_dir. Glob on the basename and disambiguate by suffix match.
  const basename = normalized.split("/").pop() ?? normalized;
  const matches = await vscode.workspace.findFiles(`**/${basename}`, "**/node_modules/**");
  if (matches.length === 0) {
    return { warning: `Could not find "${relativeFile}" in the workspace.` };
  }

  const suffixMatches = matches.filter((m) =>
    m.fsPath.replace(/\\/g, "/").endsWith(normalized)
  );
  const candidates = suffixMatches.length > 0 ? suffixMatches : matches;

  if (candidates.length === 1) {
    return { uri: candidates[0] };
  }

  const sorted = [...candidates].sort((a, b) => a.fsPath.localeCompare(b.fsPath));
  return {
    uri: sorted[0],
    warning: `Multiple files named "${basename}" found in the workspace; opening "${sorted[0].fsPath}". Consider a more specific schema layout to avoid ambiguity.`,
  };
}
