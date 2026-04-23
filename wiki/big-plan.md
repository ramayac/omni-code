# Big Plan

Consolidated planning document migrated from `docs/plan000.md`, `docs/plan001.md`, `docs/lessons001.md`, and `docs/todo001.md`.

## Completed Phases

### Phase 00/01 — Foundations

- Go module scaffolding with standard layout (`cmd/`, `internal/`)
- Makefile with build, test, multi-repo, DB management targets
- Config resolution: defaults → YAML → env → CLI
- CLI subcommands: `index`, `search`, `mcp`, `repos`, `watch`, `reset`
- ChromaDB integration: `chunks` collection with upsert/delete/query
- SQLite for file/repo metadata (replacing ChromaDB metadata collections)
- Indexer with change detection cascade (Size → MTime → SHA256)
- Tree-sitter chunking for Go, JS/TS, Python, Java, PHP, Ruby, HTML, JSON
- Embedder interface with Ollama, OpenAI, and chroma-default backends
- MCP stdio server with core tools

### Phase 001 — HTTP/SSE MCP Server

- Three MCP transport modes: stdio, SSE, streamable HTTP
- CLI flags: `--transport`, `--addr`, `--stateless`, `--cors`
- `buildServer()` shared tool registration (all transports share identical tools)
- Signal handling with `signal.NotifyContext` for graceful shutdown
- CORS middleware (opt-in via `--cors`)
- New tools: `grep_codebase`, `get_file_symbols`, `reindex_repo`

### Phase 001b — Repo Summaries

- `get_repo_summary` tool — rich Markdown with metadata, language distribution, directories, git log
- `search_repo_summaries` tool — compact cards for all indexed repos
- `get_top_contributors` tool — ranked contributor leaderboard

### Chat Mode

- Interactive terminal REPL (`internal/chat/chat.go`)
- OpenAI-compatible API client (`internal/chat/openai.go`)
- Tool bridge from chat to MCP dispatch (`internal/chat/tools.go`)

## Pending Work

### High Priority

- **HT-1 — E2E Smoke Test**: Full end-to-end validation from `make docker-db` through Copilot query
- **HT-2 — fsnotify Watch Mode**: Replace polling with `fsnotify` for `.git/refs/heads/` + 5s debounce + `auto_pull` support

### Medium Priority

- **MT-2 — Structured Progress Events**: Expose progress reporting as structured events for watch mode

### Lower Priority

- **LP-1 — LLM Structural Summaries**: Optional LLM-generated prose architecture descriptions via `get_repo_summary`
- **LP-2 — `generate_agents_md`**: Auto-generate AGENTS.md for any indexed repo
- **LP-4 — Verify Graceful Drain**: Manual verification of `Ctrl-C` behavior under SSE connections

## Key Lessons Learned

1. `buildServer` extraction eliminates tool-registration duplication across transports
2. `http.ErrServerClosed` must be swallowed during graceful shutdown
3. CORS must always be opt-in (never default)
4. Tree-sitter `ExtractSymbols` reuses chunker infra — no separate AST bindings needed
5. Grep must cap results (default 50 lines) to prevent unbounded output
6. `reindex_repo` must respect `full` flag — only delete data when `full=true`
7. ChromaDB metadata aggregation is fast enough for language distribution stats
8. Change detection cascade order (Size → MTime → SHA256) is sacred
