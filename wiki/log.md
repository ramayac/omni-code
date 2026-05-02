# Wiki Log

Append-only timeline of wiki maintenance activity.

## [2026-04-23] ingest | initial full-repo wiki build

### What was added

- **repo-map.md** — fully populated with purpose, high-signal areas (16 entries), generated artifacts, build/run commands, and `.wikirc` ignore list.
- **architecture.md** — system overview, ASCII layer diagram, storage model (ChromaDB chunks + SQLite tables), change detection cascade, chunking strategy (9 tree-sitter languages), search pipeline, MCP transport modes, config resolution order.
- **mcp-tools.md** — complete inventory of all 14 MCP tools with parameters, plus "adding a new tool" procedure and supported tree-sitter language table.
- **agents-guide.md** — AI agent SOPs, repo layout table, common tasks, testing guidelines.
- **big-plan.md** — consolidated from `docs/plan000.md`, `docs/plan001.md`, `docs/lessons001.md`, `docs/todo001.md`. Covers completed phases, pending work items, and 8 key lessons learned.

### What was migrated

- `AGENTS.md` content → `wiki/agents-guide.md` (root file retained as redirect since it's used as a user rule).
- `docs/plan000.md`, `docs/plan001.md`, `docs/lessons001.md`, `docs/todo001.md` → consolidated into `wiki/big-plan.md`.

### Needs human review

- `docs/` directory: original files retained — consider deleting once wiki content is validated.
- `AGENTS.md`: retained in root because it serves as the user rule file for AI agents. Content is duplicated in `wiki/agents-guide.md`.

## [2026-05-01] maintenance | MCP-focused cleanup and hardening

### What was changed

- **Chat mode removed** — `internal/chat/` deleted, CLI subcommand deleted, config fields removed.
- **Bug fixes applied** — 7 bugs fixed (log.Fatal in library, goroutine leak on ctx cancel, hashCache.get, SQLite close, OpenAIEmbedder index ordering, MCP shutdown timeout, handleGrep OOM).
- **New tree-sitter parsers** — Rust, C, C++ added to `internal/chunker/chunker.go` (12 languages total).
- **MCP server refactored** — `server.go` split into 5 files with handler groupings.

### Wiki files updated

- `wiki/repo-map.md` — removed chat entries, added new handler files, updated language count.
- `wiki/architecture.md` — removed chat layer from diagram, updated chunking language count.
- `wiki/mcp-tools.md` — added Rust/C/C++ to language table, updated "adding a tool" section.
- `wiki/lessons.md` — added MCP-focused cleanup section with 8 new lessons (lessons 10–17).

## [2026-04-23] ingest | indexer memory optimization

### What was added

- **indexer-memory-plan.md** — documented the architectural shift from reading entire files into memory strings towards a stream-based chunk emitting pipeline, solving the 20+ GB RAM usage problem during massive indexing runs. Included limits (`MaxFileSizeBytes`) and `bufio.Scanner` sequential scanning fallback.
- Updated `wiki/index.md` to link `indexer-memory-plan.md` under Topic Pages.

## [2026-04-23] maintenance | consolidate historical plans

### What was changed

- Merged `wiki/plan000.md` and `wiki/plan001.md` into a single `wiki/plans.md` file.
- Renamed `wiki/lessons001.md` to `wiki/lessons.md` to drop versioning.
- Renamed `wiki/todo001.md` to `wiki/todo.md` to drop versioning.
- Updated `wiki/index.md` to point to the simplified structures.
