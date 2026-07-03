# CLAUDE.md

sqldefkit bundles a directory tree of `.sql` schema files into one dependency-ordered `.sql` file for sqldef. See @README.md for user-facing behavior.

## Commands

- Test: `go test ./...` (prefer a single package, e.g. `go test ./internal/bundle`, while iterating)
- Lint: `go vet ./...` and `gofmt -l .` — CI fails on both
- Build: `go build ./...`
- Regenerate the golden file after an intentional output-format change:
  `go run ./cmd/sqldefkit bundle --dir internal/bundle/testdata/golden/input --dialect postgres -o internal/bundle/testdata/golden/expected.sql`

## Architecture (data flow)

`cmd/sqldefkit` (CLI) → `internal/config` (sqldefkit.yaml discovery/resolution) → `internal/bundle` (load tree, symbol index, emit) → `internal/lexer` (dialect-aware statement splitting) → `internal/parse` (extract defines/depends-on edges) → `internal/graph` (stable topological sort). `internal/diag` and `internal/pos` carry diagnostics and source positions shared by `check` and `internal/lsp`.

## Constraints

- Pure Go by design: the only dependency is `gopkg.in/yaml.v3`. Do not add SQL parser libraries — the lexer/parser are deliberately hand-rolled.
- Output must be byte-for-byte deterministic across platforms and runs: input line endings are normalized to LF on load, and topo-sort ties break by file path, then statement position. The golden test in `internal/bundle` compares byte-for-byte.
- Tests are plain stdlib `testing`, mostly table-driven; no assertion frameworks.
- Commit messages: conventional commits (`feat:`, `fix:`, `refactor:`, …), in English.
