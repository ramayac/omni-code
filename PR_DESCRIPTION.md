## Summary

Removes the built-in chat mode to focus omni-code on what it does best: indexing + MCP server. Any MCP-compatible client (VS Code Copilot, Claude Desktop, Cursor) provides a better chat experience with full tool access. This also fixes 7 bugs found during a full codebase audit and expands tree-sitter chunking to 12 languages.

## Changes

### Chat mode removed (~480 lines deleted)
- `internal/chat/` package deleted (4 files)
- `chat` CLI subcommand removed
- `chat_api_url` / `chat_model` config fields removed
- `chat` Makefile target removed
- Config simplified: chat env vars and yaml fields no longer needed

### Bug fixes
- **`log.Fatal` in library code** — `detectBranchAndCommit` now returns `(string, error)` instead of crashing the process. One repo on the wrong branch no longer kills parallel index workers.
- **Goroutine leak on Ctrl-C** — `ctx.Err()` checks added to indexer feed loops and `processFile`. Long indexing runs now cancel cleanly.
- **`hashCache.get` false negatives** — Fixed to standard `v, ok := m[key]` pattern.
- **SQLite resource leak** — `ChromaClient.Close()` added and deferred in all 6 subcommands. WAL now properly checkpointed.
- **OpenAIEmbedder index ordering** — Results sorted by index with count validation for robustness against out-of-order compatible backends.
- **MCP HTTP shutdown timeout** — `context.WithTimeout(5s)` replaces unbounded `context.Background()`. Servers shut down cleanly.
- **`handleGrep` OOM risk** — Changed from `os.ReadFile` (reads entire files into memory) to streaming `bufio.Scanner` on `os.File`.

### New tree-sitter parsers: Rust, C, C++
These languages were detected by `DetectLanguage` but fell through to line-based chunking. Now all 12 detected languages have full tree-sitter parsing:

| Language | Before | After |
|---|---|---|
| Go, JS/TS, Python, Java, PHP, Ruby, HTML, JSON | ✅ | ✅ |
| Rust, C, C++ | ❌ (line-chunk fallback) | ✅ (top-level declaration chunking) |

### MCP server refactored
`server.go` split from 844-line monolith into 5 focused files:

| File | Handlers |
|---|---|
| `server.go` | Transports (stdio/SSE/streamable), CORS, `buildServer`, shared param types |
| `handlers_search.go` | `search_codebase`, `grep_codebase`, `get_file_content` |
| `handlers_repo.go` | `list_repos`, `get_repo_files`, `get_repo_summary`, `search_repo_summaries` |
| `handlers_git.go` | `git_status`, `git_diff`, `git_log`, `get_top_contributors` |
| `handlers_index.go` | `index_status`, `reindex_repo`, `get_file_symbols` |

### Documentation
- README.md — removed chat mode, updated architecture, 12 languages
- AGENTS.md — removed chat references, updated handler file locations
- wiki/ updated: lessons (+8), mcp-tools (+3 languages), architecture, repo-map, log, new testing-plan.md

## Testing

All 9 packages pass `go test ./...` and `go vet ./...`. A [comprehensive MCP testing plan](wiki/testing-plan.md) is documented in the wiki covering 5 layers: handler unit tests, protocol tests (in-memory MCP client→server), transport tests, integration tests, and manual conformance.

## Breaking changes

- `omni-code chat` subcommand no longer exists — use an MCP client instead
- `chat_api_url` and `chat_model` fields removed from `repos.yaml` config
- `OMNI_CHAT_API_URL`, `OMNI_CHAT_MODEL`, `OMNI_CHAT_API_KEY` env vars no longer read
