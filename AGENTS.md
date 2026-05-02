# omni-code: Agent Guidelines

Instructions for AI coding agents and LLM-assisted development.

## Core Principles

- **Single Source of Truth** — `internal/git` is the source of truth for file discovery. It handles `git ls-files` and honors ignore rules and branch detection. Do not use custom `WalkDir` logic unless specifically asked.
- **Config-First** — Prefer `internal/config.LoadConfig` and `internal/config.ResolveConfig` (aliased `config.RepoConfig`) for all flags and repo settings.
- **Incremental Indexing** — Indexing is $O(\text{changed})$. The `internal/indexer` uses `git diff` for performance.
- **Deduplication** — Changes are tracked by SHA256 hashes in ChromaDB.

## Repository Layout

| Package | Purpose | Use when... |
|---|---|---|
| `cmd/omni-code/` | CLI & Watch Mode | Modifying CLI flags or poll loop. |
| `internal/config/` | Skip lists & Defaults | Adding file exclusions or new globals. |
| `internal/git/` | File Listing / Diffs | Improving Git-aware discovery. |
| `internal/estimator/` | Scan Cost Estimation | Adjusting pre-scan scheduling and score logic. |
| `internal/db/` | ChromaDB / BM25 | SQL-like access to records. |
| `internal/chunker/` | Tree-sitter | Adding support for new languages. |
| `internal/indexer/` | Logic & Stats | Improving indexing speed or reliability. |
| `internal/mcp/` | MCP Tools + Dispatch | Adding new tools. Handlers are split: `server.go` (transports/types), `handlers_search.go`, `handlers_repo.go`, `handlers_git.go`, `handlers_index.go`. |
| `internal/db/sqlite.go` | SQLite Metadata | Modifying file-meta or repo-meta schema. |
| `internal/embedder/` | Embedding Backends | Adding new embedding providers. |

## Common Tasks

- **Adding a New Language** — Update `internal/chunker/chunker.go` mapping. Add tree-sitter library if needed.
- **Refining Search Quality** — Tune RRF weights or hybrid flags in `internal/db/chroma.go`.
- **Extending MCP Tools** — Add handler to the appropriate `internal/mcp/handlers_*.go` file
  - `handlers_search.go` — search_codebase, grep_codebase, get_file_content
  - `handlers_repo.go` — list_repos, get_repo_files, get_repo_summary, search_repo_summaries
  - `handlers_git.go` — git_status, git_diff, git_log, get_top_contributors
  - `handlers_index.go` — index_status, reindex_repo, get_file_symbols
  Then register in `server.go:buildServer()`, add to `dispatch.go:ToolDefinitions()` + `DispatchTool()`.

## Phase History

See `docs/` for full plan and lessons documents (`plan000.md`, `plan001.md`, `lessons001.md`, `todo001.md`).

## Testing Guidelines

- Data for tests should go in `test-data/` or be mocked (see `internal/embedder/embed_mock.go`).
- End-to-end (E2E) logic is in `cmd/omni-code/watch_test.go` and `internal/indexer/indexer_test.go`.