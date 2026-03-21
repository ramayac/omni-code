# Project Omni-Code: Completed Features and Lessons

This document summarizes all the work that has been completed and established across the previous project plans. It serves as a historical record of implemented functionalities and stable project bases.

## Phase 00/01 Foundations

### 1. Core Platform & Scaffolding
- Set up Go 1.21+ module with project structure (`cmd/omni-code/`, `internal/db/`, `internal/indexer/`, `internal/chunker/`, `internal/mcp/`).
- Created standardized `Makefile` with targets for build, test, multi-repo tasks, and local db running (`docker-db`, `backup-db`, `restore-db`).
- Defined unified `internal/config` to safely load variables resolving across built-in defaults, `repos.yaml` config, Environment Variables, and CLI Flags.
- Developed primary CLI with subcommands: `index`, `search`, `mcp`, `repos`, and `watch`. Added `--dry-run` and environment scoping. Logging routed properly to `os.Stderr`.

### 2. Vector Storage & Database Layer
- Connected successfully to locally running ChromaDB via the Go Client.
- Ensured collection scaffolding correctly (`files`, `chunks`, `repos`).
- Implemented robust `Upsert`, `Delete`, `Query`, and Metadata tracking APIs.
- Included multi-repo status tracking and operations via `reset --all`, `reset --repo`, and lightweight per-run caching logic limiting re-processing.
- Enhanced query configurations enabling deduplication, `context-lines` injections, `min-score` exclusions, explicit format targeting (`--lang`, `--ext`), and initial RRF Hybrid Search bridging semantics with BM25 concepts.

### 3. Indexer & Semantic Chunker
- Built local `filepath.WalkDir` routines dynamically respecting default skip rules mappings alongside extensive `.gitignore` logic.
- Implemented Git-Aware fallback paths effectively matching `.git/` roots cleanly wrapping `git ls-files` internally checking diff statuses over standard paths successfully detecting branches explicitly.
- Established rigid change detection (Size -> MTime -> SHA-256 cascade).
- Integrated `Tree-sitter` bindings specifically isolating high-value bounds correctly chunking code logic inside `.go`, `.py`, `.js`, `.ts` and others cleanly correctly breaking out logic while keeping limits capped at optimal bounds.
- Hooked Pre-Scan estimation parameters accurately predicting run complexities through the `internal/estimator` package cleanly bounding pre-index sorts logically prior to sequential indexing routines.

### 4. MCP Agent Protocols & Extending Views (Phase 00 tools)
- Hosted active Context protocol standard stdio pipelines gracefully bridging system states for native agents correctly checking outputs gracefully without stepping on local STDOUT feeds.
- Registered core analysis commands resolving AI checks inherently natively enabling specific tools like:
  - `search_codebase`: Direct vector context retrievals formatting target arrays natively.
  - `list_repos`, `get_repo_files`, `get_file_content`: Tree navigation hooks allowing models specific checks.
  - `git_status`, `git_diff`, `git_log`, `index_status`: Advanced metadata status hooks securely granting models project-stage states openly.

### 5. Embedder Abstractions
- Extracted local logic constraints abstracting `Embedder` interface to effectively isolate target hosts.
- Implemented core local engines directly routing Chroma defaults vs `ollama` standard POST api endpoints seamlessly.
- Hooked external platforms cleanly bypassing manual API implementations securely through `OPENAI` implementations smoothly handling direct completions via generic formats smoothly.

---

## Phase 001 — Standalone MCP Web Server (HTTP / SSE Transport)

### What Was Built

**HTTP transport modes** added to `omni-code mcp`:

| Mode | Flag | SDK type | MCP spec |
|---|---|---|---|
| SSE (legacy) | `--transport sse` | `mcp.SSEHandler` | 2024-11-05 |
| Streamable HTTP | `--transport streamable` | `mcp.StreamableHTTPHandler` | 2025-03-26 |
| stdio (unchanged) | `--transport stdio` (default) | `mcp.StdioTransport` | any |

**New CLI flags on `omni-code mcp`:**

| Flag | Default | Purpose |
|---|---|---|
| `--transport` | `stdio` | Select transport: `stdio`, `sse`, `streamable` |
| `--addr` | `:8090` | `host:port` for HTTP modes |
| `--stateless` | `false` | Stateless mode for `streamable` |
| `--cors` | `false` | Opt-in CORS headers for browser GUIs |

**New functions in `internal/mcp/server.go`:**
- `buildServer(client)` — shared tool-registration helper (all transports reuse it)
- `ServeSSE(ctx, client, addr, cors)` — SSE HTTP server
- `ServeStreamable(ctx, client, addr, stateless, cors)` — modern streamable server with `/health` endpoint
- `corsMiddleware(handler)` — thin CORS wrapper (only applied when `--cors` is set)

**Phase 5 tools** (also landed in this phase):
- `grep_codebase` — regex grep across all indexed files, with optional repo/file filter
- `get_file_symbols` — tree-sitter AST symbol extraction (functions, classes, types)
- `reindex_repo` — trigger incremental or full re-index of a registered repository

**Signal handling:** `signal.NotifyContext` in `runMCP` ensures `Ctrl-C` / SIGTERM drain active sessions cleanly.

**VS Code integration updated:** `.vscode/mcp.json` example updated to use `type: sse` pointing at `http://localhost:8090`.

### Lessons Learned

1. **`buildServer` extraction pays off.** Having all transport modes call a single shared `buildServer(client)` helper eliminated code duplication and made it trivial to add new tools — they appear in stdio, SSE, and streamable modes without any extra wiring.

2. **`http.ErrServerClosed` is not a real error.** When graceful shutdown calls `srv.Shutdown(ctx)`, `ListenAndServe` returns `http.ErrServerClosed`. This must be swallowed; otherwise the process logs a spurious error on clean `Ctrl-C`.

3. **CORS must be opt-in.** Never default CORS headers on. Binding to a specific address (even localhost) without CORS is the secure default; add `--cors` only when a browser-based GUI is needed.

4. **`httptest.NewServer` is the right isolation tool.** Unit tests for ServeSSE and ServeStreamable use `httptest.NewServer` with hand-constructed mux/handlers, bypassing real port allocation and keeping tests deterministic and fast.

5. **Tree-sitter symbol extraction via `chunker.ExtractSymbols`.** The `get_file_symbols` tool reuses the existing chunker infra to walk the AST and return a symbol table, avoiding duplicating tree-sitter bindings.

6. **Grep must cap results.** Without a `maxResults` cap the grep tool would read every file in every repo synchronously, which could be slow and produce enormous output. Default cap of 50 lines with a user-visible truncation message is the right UX.

7. **`reindex_repo` needs to respect the `Full` flag carefully.** The tool deletes chunks + file-meta before reindexing only when `full=true`; incremental mode skips deletion and lets the indexer's SHA-256 dedup handle unchanged files.

---

## Phase 001 — `get_repo_summary` MCP Tool

### What Was Built

Added `get_repo_summary` MCP tool to `internal/mcp/server.go`. It returns a rich Markdown summary of a repository without requiring a live LLM:

- **Metadata block** — branch, last commit (short), last indexed timestamp, file count, chunk count
- **Language distribution** — counts and percentage bars derived from ChromaDB file-meta language tags
- **Directory overview** — top-level directories with per-directory file counts
- **Recent git log** — last 10 commits via `git log --oneline`

The tool is registered in `buildServer()` alongside all other tools so it is available across stdio, SSE, and streamable transports.

### Lessons Learned

8. **Aggregation from ChromaDB metadata is fast.** Language distribution and directory counts can be derived entirely from the `FileMeta` records already stored in ChromaDB, with no extra embeddings or LLM calls needed.

9. **Percentage bars with ASCII blocks improve readability.** A simple `strings.Repeat("█", n)` bar communicates proportions at a glance inside Markdown text content.
