# Omni-Code: AI Agent Instructions

Welcome, Agent. You are operating as an **Expert Go Developer** working on the `omni-code` repository.

Read these instructions carefully before writing or modifying any code.

---

## 1. Project Goal

Build an **MCP (Model Context Protocol) server** that connects to GitHub Copilot (or any MCP-compatible client). The server lets you ask natural-language questions about your local codebases and get answers backed by semantic search over indexed code.

The pipeline: **Scan repos → Detect changes → Deduplicate → Chunk with Tree-sitter → Store embeddings in ChromaDB → Serve queries via MCP over stdio.**

## 2. Project Context

`omni-code` is a high-performance CLI and MCP server written in Go. It scans thousands of local Git repositories, performs intelligent change detection and deduplication, chunks code semantically using Tree-sitter, and stores embeddings in a local ChromaDB instance.

## 3. Build Plan & Progress Tracking

**Reference the plan**: Always align architectural decisions with **`docs/plan00.md`**.

The plan contains checkbox-style `[ ]` / `[x]` items for every task. **After completing a task, open `docs/plan00.md` and mark the corresponding checkbox `[x]`**. This is how progress is tracked — do not skip this step.

## 4. Target Directory Structure

```
omni-code/
├── AGENTS.md                  # This file — agent instructions
├── Makefile                   # Build, test, run targets
├── go.mod / go.sum
├── cmd/
│   └── omni-code/
│       └── main.go            # CLI entrypoint (index, search, mcp commands)
├── internal/
│   ├── db/
│   │   └── chroma.go          # ChromaDB client wrapper
│   ├── indexer/
│   │   ├── indexer.go          # File walker, change detection, deduplication
│   │   └── indexer_test.go
│   ├── chunker/
│   │   ├── chunker.go          # Tree-sitter & text chunking
│   │   └── chunker_test.go
│   └── mcp/
│       ├── server.go           # MCP server setup & tool registration
│       └── server_test.go
├── docs/
│   └── plan00.md               # Build plan with tracked progress
└── test-data/                  # Sample repos for development testing
```

## 5. Core Directives

### Go Idioms
- Use `gofmt` formatting. Prefer standard library packages (e.g. `flag` for CLI) unless a third-party package is explicitly listed in the build plan.
- Module path: `github.com/<owner>/omni-code` (or as set in `go.mod`).

### Error Handling
- **Never swallow errors.** Wrap with context: `fmt.Errorf("failed to do X: %w", err)`.
- Return errors up the call stack; let `main.go` decide whether to `log.Fatal` or print usage.

### Concurrency
- Use `goroutines` + `sync.WaitGroup` for the file scanner and chunker to parallelize I/O.
- Protect shared state (e.g. `seenHashes` deduplication map) with `sync.Mutex`.
- Use buffered channels for work distribution where appropriate.

### Logging
- Use `log` (standard library) writing to `os.Stderr`.
- **Never use `fmt.Println` in any `internal/` package** — especially `internal/mcp/`, where stdout is the JSON-RPC stdio transport. Any stray stdout output corrupts the MCP protocol stream.
- Use `log.Printf("[component] message")` format for structured log prefixes.

### Keep it Lean
- No interfaces unless there are multiple implementations.
- No abstractions for one-time operations.
- No premature optimization — build correct first, fast second.

## 6. Workflow

### Testing
- Before committing or proposing changes, ensure `make test` passes.
- Every new feature gets a test in the same package (e.g. `indexer_test.go`).
- Use table-driven tests where appropriate.

### Dependencies
- Add modules with `go get <package>` then `go mod tidy`.
- Only add dependencies listed in the build plan unless you have explicit user permission.

### Database
- ChromaDB runs locally on port **8000** via Docker (`make docker-db`).
- The `internal/db` package must handle connection failures gracefully with retry or clear error messages.

## 7. Sacred Rules

1. **Change Detection Cascade is Sacred**: Do NOT alter the **Size → MTime → Hash** cascade without explicit user permission. This three-tier check is the core performance mechanism that avoids unnecessary disk reads.

2. **Paths**: Use `path/filepath` for ALL file system operations (cross-platform: Linux/macOS/Windows).

3. **MCP stdio**: The MCP server communicates via stdin/stdout JSON-RPC. Zero tolerance for stray stdout writes in `internal/mcp/`. Log to `os.Stderr` only.

4. **Plan is the source of truth**: When in doubt about what to build or how, re-read `docs/plan00.md`.