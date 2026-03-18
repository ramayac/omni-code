# omni-code

A local codebase indexer and **MCP server** written in Go. Connect it to GitHub Copilot to ask natural-language questions about your local repositories, backed by semantic search over indexed code.

## Features

- **Intelligent change detection** — Size → MTime → SHA-256 hash cascade avoids redundant re-indexing
- **Global deduplication** — identical files (e.g. vendored libraries) are indexed only once
- **Tree-sitter chunking** — Go, JavaScript, TypeScript, and Python files are split at function/class boundaries for precise retrieval
- **Line-based fallback** — markdown, Dockerfiles, and other text files are chunked by word count with overlap
- **MCP stdio server** — plug directly into GitHub Copilot or any MCP-compatible AI assistant

## Prerequisites

- Go 1.25+
- Docker (for ChromaDB)

## Setup

### 1. Start ChromaDB

```bash
make docker-db
```

### 2. Build the binary

```bash
make build
```

The binary is placed at `bin/omni-code`.

## Usage

### Index a repository

```bash
./bin/omni-code index --name my-project /path/to/project
```

| Flag | Default | Description |
|---|---|---|
| `--name` | *(required)* | Logical repository name stored in ChromaDB |
| `--db` | `http://localhost:8000` | ChromaDB base URL |

### Search indexed code

```bash
./bin/omni-code search --query "how does change detection work"
```

| Flag | Default | Description |
|---|---|---|
| `--query` | *(required)* | Natural-language search query |
| `--repo` | *(empty — all)* | Filter results to a specific repository |
| `--n` | `10` | Number of results to return |
| `--db` | `http://localhost:8000` | ChromaDB base URL |

### Start the MCP server

```bash
./bin/omni-code mcp
```

The server communicates over stdin/stdout using the MCP JSON-RPC protocol.  
It exposes a single tool: **`search_codebase`**.

## GitHub Copilot Integration

Add the following to your VS Code `settings.json` (or create `.vscode/mcp.json`):

```json
{
  "servers": {
    "omni-code": {
      "command": "/absolute/path/to/bin/omni-code",
      "args": ["mcp"]
    }
  }
}
```

Then in Copilot Chat, ask questions like:

> "How does the indexer detect file changes?"  
> "Show me the ChromaDB upsert logic."  
> "What does the chunker do with large functions?"

## Development

```bash
# Run tests
make test

# Index the test-data directory
make dev

# Remove build artifacts
make clean
```

## Architecture

```
cmd/omni-code/main.go       CLI entry point (index, search, mcp subcommands)
internal/db/chroma.go       ChromaDB client — collections, upsert, query
internal/indexer/indexer.go File walker, change detection, deduplication
internal/chunker/chunker.go Tree-sitter & line-based chunking
internal/mcp/server.go      MCP stdio server — search_codebase tool
```

## Data flow

```
filepath.WalkDir
  → Change detection (Size → MTime → Hash)
  → Global dedup (seenHashes map)
  → Chunker (Tree-sitter / line-based)
  → ChromaDB (upsert chunks + file meta)
```
