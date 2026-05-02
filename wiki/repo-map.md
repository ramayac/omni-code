# Repo Map

## Purpose

omni-code is a local codebase indexer, MCP server, and interactive AI chat written in Go. It indexes multiple local Git repositories into ChromaDB and SQLite, providing semantic search, hybrid BM25+vector retrieval, and a full suite of MCP tools for AI coding assistants. Users connect it to GitHub Copilot via MCP, or use the built-in chat mode from the terminal.

## High-Signal Areas

| Path | Role |
|---|---|
| `cmd/omni-code/main.go` | CLI entry point — subcommands: `index`, `search`, `chat`, `mcp`, `watch`, `repos`, `reset` |
| `internal/config/config.go` | YAML config loading, env/CLI resolution, skip-list merging |
| `internal/config/defaults.go` | Built-in skip directories, extensions, and filenames |
| `internal/git/` | Git-aware file listing (`ls-files`), branch detection, `diff`, `log`, `status` |
| `internal/indexer/indexer.go` | Core indexing loop — change detection (Size→MTime→SHA256), deduplication, buffered flush, incremental via `git diff` |
| `internal/chunker/chunker.go` | Tree-sitter semantic chunking for 9 languages + line-based fallback |
| `internal/db/chroma.go` | ChromaDB client — chunks collection, semantic query, BM25 hybrid re-ranking (RRF) |
| `internal/db/sqlite.go` | SQLite storage for file metadata and repo metadata (replaces ChromaDB metadata collections) |
| `internal/embedder/embedder.go` | Pluggable embedding backends: `chroma-default`, `ollama`, `openai`, `openai-compatible` |
| `internal/estimator/` | Pre-scan complexity estimation and cost-sorted scheduling |
| `internal/mcp/server.go` | MCP server — `buildServer()` shared tool registration, stdio/SSE/streamable transports |
| `internal/mcp/dispatch.go` | Tool definitions and dispatch bridge for chat mode |
| `Makefile` | Build, test, index, MCP, DB management targets |

## Generated Artifacts

| Path | Description |
|---|---|
| `bin/omni-code` | Compiled binary |
| `*.log` | Build and tidy logs |
| `build_error.txt`, `error.txt` | Build error capture files |
| `~/.omni-code/omni-code.db` | SQLite database (file + repo metadata) |
| ChromaDB container | `chunks` collection (vector storage, managed via Docker) |

## Build and Run Path

```bash
# Prerequisites
# Go 1.25+, Docker

# Start ChromaDB
make docker-db-start

# Build
make build

# Run tests
make test

# Index a single repo
./bin/omni-code index --name my-project /path/to/project

# Index all repos from config
./bin/omni-code index --config repos.yaml

# Dry-run estimate
make estimate

# Search
./bin/omni-code search --query "how does change detection work" --hybrid

# MCP server (SSE)
make mcp

# Watch mode
./bin/omni-code watch --config repos.yaml --interval 5m
```

## Ignored Paths (from .wikirc)

- `wiki/`
- `bin/`
- `*.log`
- `*.tmp`
