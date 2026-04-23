# Agents Guide

Migrated from `AGENTS.md` (root) — this is the canonical location.

## Core Principles

- **Single Source of Truth** — `internal/git` is the source of truth for file discovery. It handles `git ls-files` and honors ignore rules and branch detection. Do not use custom `WalkDir` logic unless specifically asked.
- **Config-First** — Prefer `internal/config.LoadConfig` and `internal/config.ResolveConfig` for all flags and repo settings.
- **Incremental Indexing** — Indexing is O(changed). The `internal/indexer` uses `git diff` for performance.
- **Deduplication** — Changes are tracked by SHA256 hashes in ChromaDB. A global `sync.Map` prevents re-indexing identical content across repos.
- **Change Detection Order** — Size → MTime → SHA256. This cascade is sacred — do not reorder.

## Repository Layout

| Package | Purpose | Use when... |
|---|---|---|
| `cmd/omni-code/` | CLI & Watch Mode | Modifying CLI flags or poll loop |
| `internal/config/` | Skip lists & Defaults | Adding file exclusions or new globals |
| `internal/git/` | File Listing / Diffs | Improving Git-aware discovery |
| `internal/estimator/` | Scan Cost Estimation | Adjusting pre-scan scheduling and score logic |
| `internal/db/` | ChromaDB / SQLite / BM25 | Modifying storage, query, or metadata |
| `internal/chunker/` | Tree-sitter | Adding support for new languages |
| `internal/indexer/` | Logic & Stats | Improving indexing speed or reliability |
| `internal/embedder/` | Embedding backends | Adding new embedding providers |
| `internal/mcp/` | MCP Tools + Dispatch | Adding new tools or exposing them to chat |
| `internal/chat/` | Interactive Chat REPL | Modifying chat UX, OpenAI client, or tool bridge |

## Common Tasks

- **Adding a New Language** — Update `internal/chunker/chunker.go` mapping (add language import + `topKinds` map + switch case). Add the tree-sitter library to `go.mod` if needed.
- **Refining Search Quality** — Tune BM25 constants (`bm25K1`, `bm25B`) or RRF `k` in `internal/db/chroma.go`.
- **Extending MCP Tools** — Add handler to `internal/mcp/server.go`, register in `buildServer()`, add `SimpleTool` to `ToolDefinitions()` and case to `DispatchTool()` in `internal/mcp/dispatch.go`.
- **Chat Mode** — The REPL is in `internal/chat/chat.go`. The OpenAI-compatible client is `internal/chat/openai.go`. Tools are bridged via `internal/chat/tools.go` which calls `internal/mcp/dispatch.go`.

## Testing Guidelines

- Data for tests should go in `test-data/` or be mocked (see `internal/embedder/embed_mock.go`).
- End-to-end (E2E) logic is in `cmd/omni-code/watch_test.go` and `internal/indexer/indexer_test.go`.
- Use `httptest.NewServer` for HTTP handler tests (MCP server tests).

## Phase History

See `docs/` for full plan and lessons documents (`plan000.md`, `plan001.md`, `lessons001.md`, `todo001.md`).
