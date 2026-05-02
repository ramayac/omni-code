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

---

## MCP-Focused Cleanup & Hardening (May 2026)

### What Was Done

A full codebase audit identified bugs, safety issues, and missing coverage. All fixes were applied on the `mcp-focused` branch.

**Chat mode removed.** The interactive chat REPL (`internal/chat/`) was deleted because MCP clients (VS Code Copilot, Claude Desktop, etc.) provide a better chat experience with full tool access. Chat-related config fields (`chat_api_url`, `chat_model`) and CLI subcommand were removed.

**Bug fixes:**
- `log.Fatal` in library code (`detectBranchAndCommit`) replaced with error return — prevents one repo on the wrong branch from crashing all concurrent index workers.
- Context cancellation wired through indexer feed loops and `processFile` — prevents goroutine leaks on Ctrl-C during long indexing runs.
- `hashCache.get` fixed — standard `v, ok := m[key]` pattern replaces always-false-for-empty-string logic.
- `ChromaClient.Close()` added and deferred in all 6 subcommands — SQLite WAL now properly checkpointed on exit.
- `OpenAIEmbedder` now sorts results by index and validates count — robust against out-of-order compatible backends.
- MCP HTTP servers now use `context.WithTimeout(5s)` instead of `context.Background()` for graceful shutdown.
- `handleGrep` changed from `os.ReadFile` (OOM risk) to streaming `bufio.Scanner` on `os.File`.

**New tree-sitter parsers:** Rust, C, and C++ added to `internal/chunker/chunker.go`. These languages were detected by `DetectLanguage` but fell through to line-based chunking. Now all 12 detected languages have tree-sitter parsing.

**MCP server refactored.** `server.go` (844 lines) split into:
- `server.go` — transports, CORS, `buildServer`, shared param types
- `handlers_search.go` — search_codebase, grep_codebase, get_file_content
- `handlers_repo.go` — list_repos, get_repo_files, get_repo_summary, search_repo_summaries
- `handlers_git.go` — git_status, git_diff, git_log, get_top_contributors
- `handlers_index.go` — index_status, reindex_repo, get_file_symbols

### Lessons Learned

10. **`log.Fatal` has no place below `main`.** Library code that calls `log.Fatal` kills the entire process, including concurrent workers doing unrelated work. Always return errors upward and let the caller decide on severity.

11. **Context cancellation must reach every goroutine.** The indexer spawns 8 workers + a feeder + a progress reporter. If any of them ignore `ctx.Done()`, the process hangs on Ctrl-C. Every loop and blocking channel send needs a `ctx.Err()` check.

12. **Ollama's single-text embed API is a bottleneck at scale.** `OllamaEmbedder.Embed` makes one HTTP call per text, sequentially. For 500 chunks that's 500 round-trips. Future work should parallelize with a semaphore or use a batch-compatible endpoint.

13. **Always stream large files — never `os.ReadFile` them.** The grep handler read every indexed file into memory before scanning. For a repo with 10,000 files averaging 50KB, that's 500MB of allocations. `bufio.Scanner` on `os.File` eliminates this.

14. **SQLite WAL needs explicit `Close`.** Without calling `sqlite3_close`, the WAL journal is left behind and may not be checkpointed. Always provide and call a `Close()` method on database wrappers.

15. **Out-of-order embedding response indices are a real problem.** While the OpenAI API returns embeddings in order, compatible backends (LiteLLM, local proxies) may not. Sorting by index and validating count is cheap insurance.

16. **Splitting `server.go` into handler files pays off immediately.** The 844-line monolith was hard to navigate. After the split, each handler file is 100–200 lines with a clear theme, making it obvious where to add new tools and reducing merge conflicts.

17. **Removing chat mode simplifies the entire codebase.** Chat had its own OpenAI client, tool bridge, and REPL — all of which duplicated MCP concepts. Deleting it removed ~480 lines of code, 3 config fields, an import, and a Makefile target with zero loss of functionality (any MCP client provides the same UX).
