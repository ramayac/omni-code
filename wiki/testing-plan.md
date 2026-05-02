# MCP Testing Plan

## Current Test Coverage

### What's tested today (`internal/mcp/server_test.go`, `dispatch_test.go`)

| Layer | Coverage |
|---|---|
| Handler formatting logic | ✅ `handleSearchWithResults`, `formatListReposResult`, `formatGetRepoFilesResult`, `formatRepoSummary` |
| Handler input validation | ✅ Empty query, empty pattern, missing repo, missing params |
| Tool symbol extraction | ✅ Verifies `handleGetFileSymbols` detects Go functions from temp files |
| Tool definitions | ✅ `TestToolDefinitions` validates 14 tools exist, no duplicates, valid JSON schemas |
| Dispatch routing | ✅ Unknown tool returns error |
| HTTP transport wiring | ✅ `TestServeSSE_connect` hits SSE endpoint, `TestServeStreamable_health` hits /health |
| CORS middleware | ✅ Headers absent by default, present when enabled, OPTIONS returns 204 |
| Build server smoke test | ✅ `buildServer(nil)` returns non-nil server |
| Grep parameter validation | ✅ Empty pattern and invalid regex return errors |
| Grep output parsing | ✅ `TestHandleGetTopContributors_ParseOutput` exercises shortlog parsing |
| Search result formatting in chat | ✅ `TestSearchRepoSummaries_MultiRepo` |

**Gap: No tests exercise the full MCP protocol.** All handler tests call handler functions directly — they bypass the JSON-RPC layer, tool serialization, and the MCP transport entirely. There are no tests where a real MCP client connects to a test server and calls tools.

---

## Testing Layers (Bottom-Up)

### Layer 1: Unit Tests — Individual Handlers (existing, expand)

Test each handler function in isolation with fake/stub ChromaClient. Fast, deterministic, no Docker.

**Current:** `server_test.go`, `dispatch_test.go`  
**To add:**

| Test | File | What it covers |
|---|---|---|
| `TestHandleGrep_NoMatches` | `handlers_search_test.go` | Grep against files with no matching pattern returns empty |
| `TestHandleGrep_WithFileFilter` | `handlers_search_test.go` | Glob filter narrows results correctly |
| `TestHandleGetRepoSummary_WithGitLog` | `handlers_repo_test.go` | Verifies git log section rendering in a real git temp dir |
| `TestHandleGetRepoSummary_AllLanguages` | `handlers_repo_test.go` | One file per language (go, py, js, rs, c, cpp, java) produces correct distribution |
| `TestHandleGetRepoSummary_NestedDirs` | `handlers_repo_test.go` | Files in `internal/foo/bar/baz.go` show `internal` as top-level dir |
| `TestHandleIndexStatus_MultipleModes` | `handlers_index_test.go` | Mix of full+incremental repos renders correctly |
| `TestHandleReindex_ErrorNoMeta` | `handlers_index_test.go` | Repo not in DB returns clear error |
| `TestHandleGetFileContent_PathTraversal` | `handlers_search_test.go` | `../../etc/passwd` is rejected |
| `TestHandleGetFileContent_TooLarge` | `handlers_search_test.go` | Files >100 KB return limit message |
| `TestHandleGetFileSymbols_UnsupportedLang` | `handlers_index_test.go` | `.txt` file returns "No symbols found" message |
| `TestHandleGitStatus_Stale` | `handlers_git_test.go` | Indexed commit differs from HEAD → STALE |
| `TestHandleGitStatus_UpToDate` | `handlers_git_test.go` | Indexed commit matches HEAD → UP TO DATE |
| `TestHandleGetTopContributors_WithSince` | `handlers_git_test.go` | `--since` flag is passed to git shortlog |

### Layer 2: Protocol Tests — MCP Client ↔ Server (new)

Use `mcp.NewInMemoryTransports()` to create a client that connects to `buildServer()` over an in-memory pipe. This exercises the full JSON-RPC stack: tool discovery, parameter serialization/deserialization, and response formatting — without network or Docker.

**Pattern:**
```go
func TestMCPClient_ListTools(t *testing.T) {
    t1, t2 := mcp.NewInMemoryTransports()
    s := buildServer(nil) // or a fake ChromaClient

    // Start server in background
    go s.Connect(ctx, t1, nil)

    // Connect client
    c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
    session, err := c.Connect(ctx, t2, nil)
    require.NoError(t, err)
    defer session.Close()

    // Initialize
    _, err = session.Initialize(ctx, &mcp.InitializeParams{...})
    require.NoError(t, err)

    // List tools
    tools, err := session.ListTools(ctx, nil)
    require.NoError(t, err)
    require.Len(t, tools.Tools, 14)
}
```

**Tests to add in `internal/mcp/protocol_test.go`:**

| Test | What it validates |
|---|---|
| `TestMCP_Initialize` | Server handshake returns capabilities |
| `TestMCP_ListTools` | All 14 tools present with valid schemas |
| `TestMCP_CallTool_SearchCodebase` | Tool call with valid args returns formatted markdown |
| `TestMCP_CallTool_SearchCodebase_EmptyQuery` | Missing required param returns JSON-RPC error |
| `TestMCP_CallTool_ListRepos` | Returns table with expected columns |
| `TestMCP_CallTool_GetFileContent` | Returns file contents in code fence |
| `TestMCP_CallTool_GrepCodebase_ValidRegex` | Pattern matches return grep-style output |
| `TestMCP_CallTool_GrepCodebase_InvalidRegex` | Returns structured error |
| `TestMCP_CallTool_GetFileSymbols` | Returns symbol table for Go file |
| `TestMCP_CallTool_GetRepoSummary` | Returns full markdown summary |
| `TestMCP_CallTool_GetTopContributors` | Returns leaderboard table |

### Layer 3: Transport Tests (existing, expand)

Test each transport mode with `httptest.NewServer` — already done for SSE and streamable. Add stdio transport test.

**Tests to add in `server_test.go`:**

| Test | What it validates |
|---|---|
| `TestServeStdio_Connect` | Client connects over in-memory stdio transport |
| `TestServeSSE_PostEndpoint` | SSE POST `/message` endpoint accepts JSON-RPC |
| `TestServeStreamable_PostMcp` | Streamable POST `/mcp` returns session ID header |
| `TestCORS_AllowedMethods` | OPTIONS returns expected Allow headers |
| `TestGracefulShutdown` | Context cancel triggers clean server shutdown within 5s timeout |

### Layer 4: Integration Tests — Real ChromaDB (new, opt-in with build tag)

Tests that require a running ChromaDB instance and a small test git repo. Gated behind `//go:build integration` so they don't block `go test ./...`.

**Test data:** Create `test-data/integration-repo/` with a small multi-file git repo:
```
test-data/integration-repo/
  .git/
  main.go          # func Add, func main
  utils/helper.go  # func helper
  README.md        # markdown content
  .gitignore
```

**Tests in `internal/mcp/integration_test.go`:**

| Test | What it validates |
|---|---|
| `TestIntegration_IndexAndSearch` | Index test repo → search returns results |
| `TestIntegration_IncrementalReindex` | Modify file → reindex → only changed files processed |
| `TestIntegration_ListReposAfterIndex` | Repo appears in list after indexing |
| `TestIntegration_GetRepoFiles` | All indexed files returned, glob filter works |
| `TestIntegration_GetFileContent` | Can read file content through tool |
| `TestIntegration_GitStatus` | Shows branch and staleness |
| `TestIntegration_GitLog` | Returns commit history |
| `TestIntegration_GrepAfterIndex` | Pattern matches found in indexed files |
| `TestIntegration_GetFileSymbols` | Symbols extracted from indexed files |
| `TestIntegration_ReindexFull` | Full reindex wipes and rebuilds |
| `TestIntegration_GetRepoSummary` | Language distribution matches actual files |
| `TestIntegration_EndToEnd_MCPClient` | Full client→server→tool→response cycle with real data |

### Layer 5: Manual Conformance — Real MCP Clients (manual checklist)

Manual verification against actual MCP clients to catch edge cases that programmatic tests miss.

| Client | Config | Test |
|---|---|---|
| VS Code + Copilot | `type: stdio`, command: `omni-code mcp --config repos.yaml` | Trigger @workspace search, verify results contain correct file paths |
| VS Code + Copilot | `type: sse`, url: `http://localhost:8090` | Same as above, over HTTP |
| Claude Desktop | `type: stdio`, command: `omni-code mcp --config repos.yaml` | Ask "what repos are indexed" — verify list_repos response |
| Claude Desktop | `type: streamable`, url: `http://localhost:8090/mcp` | Same as above, over streamable HTTP |
| MCP Inspector | `npx @modelcontextprotocol/inspector` | Connect to stdio/SSE/streamable, list tools, call each one |
| Cursor | `type: stdio` | Verify tool calls work from Cursor's agent mode |

---

## Test File Organization

After implementation, the MCP test files would be:

```
internal/mcp/
  server.go               # transports, buildServer, shared types
  server_test.go           # transport + middleware tests (existing, expanded)
  protocol_test.go         # NEW: in-memory MCP client ↔ server protocol tests
  handlers_search.go       # search_codebase, grep_codebase, get_file_content
  handlers_search_test.go  # NEW: handler unit tests for search tools
  handlers_repo.go         # list_repos, get_repo_files, get_repo_summary, search_repo_summaries
  handlers_repo_test.go    # NEW: handler unit tests for repo tools
  handlers_git.go          # git_status, git_diff, git_log, get_top_contributors
  handlers_git_test.go     # NEW: handler unit tests for git tools
  handlers_index.go        # index_status, reindex_repo, get_file_symbols
  handlers_index_test.go   # NEW: handler unit tests for index tools
  dispatch.go              # tool definitions + dispatch
  dispatch_test.go         # tool definition validation (existing)
  integration_test.go      # NEW: integration tests (build tag: integration)
```

---

## Test Data Strategy

### For unit tests (no Docker required):
- **Fake ChromaClient** — create a `ChromaClient` interface (also documented as a future improvement) or pass nil client to handlers that only format data.
- **Temp files** — `os.CreateTemp` for file content/symbol tests.
- **Temp git repos** — `git init` + `git add` + `git commit` in `t.TempDir()` for git tool tests.

### For integration tests (requires Docker + ChromaDB):
- **Build tag**: `//go:build integration` — skipped by default.
- **Pre-built test repo**: `test-data/integration-repo/` committed to the repo with a known git history.
- **ChromaDB lifecycle**: Tests start ChromaDB via `make docker-db-start` or use `testcontainers-go` for programmatic container management.
- **Run**: `go test -tags=integration ./internal/mcp/`

---

## Running Tests

```bash
# Fast unit + protocol tests (no Docker, no network)
go test ./internal/mcp/ -count=1

# With verbose output
go test ./internal/mcp/ -v -count=1

# Integration tests (requires ChromaDB running on localhost:8000)
make docker-db-start
go test -tags=integration ./internal/mcp/ -v -count=1

# All tests including E2E
go test -tags=integration ./... -count=1
```

---

## Priority Order

| Priority | Layer | Why |
|---|---|---|
| 🔴 P0 | Protocol tests (Layer 2) | Closes the biggest gap — handlers are tested in isolation but nobody tests if they actually work through MCP |
| 🟡 P1 | Handler unit tests (Layer 1 additions) | Catches regressions in formatting and input validation |
| 🟡 P1 | Handler test file split (one per handler group) | Matches the refactored handler file layout |
| 🟢 P2 | Integration tests (Layer 4) | End-to-end confidence, but requires ChromaDB |
| 🟢 P2 | Transport tests (Layer 3 additions) | Stdio transport has zero test coverage today |
| ⚪ P3 | Manual conformance (Layer 5) | One-time verification against real clients |
