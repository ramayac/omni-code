# omni-code: Agent Guidelines

Instructions for AI coding agents and LLM-assisted development.

## Core Principles

- **Single Source of Truth** — \`internal/git\` is the source of truth for file discovery. It handles \`git ls-files\` and honors ignore rules and branch detection. Do not use custom \`WalkDir\` logic unless specifically asked.
- **Config-First** — Prefer \`internal/config.LoadConfig\` and \`internal/config.ResolveConfig\` (aliased \`config.RepoConfig\`) for all flags and repo settings.
- **Incremental Indexing** — Indexing is $O(\text{changed})$. The \`internal/indexer\` uses \`git diff\` for performance.
- **Deduplication** — Changes are tracked by SHA256 hashes in ChromaDB.

## Repository Layout

| Package | Purpose | Use when... |
|---|---|---|
| \`cmd/omni-code/\` | CLI & Watch Mode | Modifying CLI flags or poll loop. |
| \`internal/config/\` | Skip lists & Defaults | Adding file exclusions or new globals. |
| \`internal/git/\` | File Listing / Diffs | Improving Git-aware discovery. |
| \`internal/db/\` | ChromaDB / BM25 | SQL-like access to records. |
| \`internal/chunker/\` | Tree-sitter | Adding support for new languages. |
| \`internal/indexer/\` | Logic & Stats | Improving indexing speed or reliability. |
| \`internal/mcp/\` | MCP Tools | Adding new tools like \`git_status\` or \`file_search\`. |

## Common Tasks

- **Adding a New Language** — Update \`internal/chunker/chunker.go\` mapping. Add tree-sitter library if needed.
- **Refining Search Quality** — Tune RRF weights or hybrid flags in \`internal/db/chroma.go\`.
- **Extending MCP Tools** — Add handler to \`internal/mcp/server.go\` and update registration in \`main.go\`.

## Phase History

- **Phase 00** — Barebones indexing and search.
- **Phase 01** — Technical core: Git-integrated, multi-repo config, watch mode daemon, hybrid search + RRF.

## Testing Guidelines

- Data for tests should go in \`test-data/\` or be mocked (see \`internal/embedder/embed_mock.go\`).
- End-to-end (E2E) logic is in \`cmd/omni-code/watch_test.go\` and \`internal/indexer/indexer_test.go\`.
