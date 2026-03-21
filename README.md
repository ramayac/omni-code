# omni-code

A local codebase indexer and **MCP server** written in Go. Connect it to GitHub Copilot to ask natural-language questions about your local repositories, backed by semantic search over indexed code.

## Features

- **Git-aware indexing** — scans only tracked/untracked-but-not-ignored files; uses commit-diffs for $O(\text{changed})$ incremental re-indexing
- **Pre-scan complexity estimation** — computes cheap repo scan estimates and sorts scheduling by estimated cost
- **Multi-repo configuration** — configure multiple repositories in `repos.yaml` with custom branch and skip rules
- **Intelligent change detection** — Hash-based change detection with global across-repo deduplication
- **Semantic chunking** — Tree-sitter powered chunking for Go, Python, JS/TS, Rust, and Java
- **Flexible embedding backends** — local (Ollama/Chroma-built-in) or remote (OpenAI) embedding generation
- **Hybrid search** — combines vector similarity with BM25 keyword ranking using Reciprocal Rank Fusion (RRF)
- **Watch mode** — background daemon that polls for Git HEAD changes and re-indexes automatically
- **Comprehensive MCP tools** — `search_codebase`, `list_repos`, `get_repo_files`, `get_file_content`, `git_status`, `git_diff`, `git_log`, `index_status`, `grep_codebase`, `get_file_symbols`, `reindex_repo`, `get_repo_summary`, `search_repo_summaries`, `get_top_contributors`

## Prerequisites

- Go 1.25+
- Docker (for ChromaDB)

## Setup

### 1. Start ChromaDB

```bash
make docker-db-start
```

### 2. Build the binary

```bash
make build
```

The binary is placed at `bin/omni-code`.

### 3. (Optional) Create a config file

Create `repos.yaml` to manage multiple repositories:

```yaml
db: http://localhost:8000
embedding_backend: ollama
embedding_model: nomic-embed-text

repos:
  - name: omni-code
    path: /path/to/omni-code
    branch: main
```

## Usage

### Indexing

```bash
# Index a single repository
./bin/omni-code index --name my-project /path/to/project

# Index all repositories in the config
./bin/omni-code index --config repos.yaml

# Estimate scan complexity and show sorted order (no indexing)
./bin/omni-code index --config repos.yaml --dry-run
```

You can also run the dry-run estimate through Make:

```bash
make estimate
```

### Search

```bash
./bin/omni-code search --query "how does change detection work" --hybrid
```

| Flag | Default | Description |
|---|---|---|
| `--query` | *(required)* | Natural-language search query |
| `--hybrid` | `false` | Enable BM25 + Vector hybrid re-ranking |
| `--repo` | *(empty)* | Filter results to a specific repository |
| `--lang` | *(empty)* | Filter by language (e.g. `go`) |
| `--ext` | *(empty)* | Filter by file extension (e.g. `.ts`) |
| `--n` | `10` | Number of results to return |

### Watch Mode

Start a background daemon that polls for changes every 5 minutes:

```bash
./bin/omni-code watch --config repos.yaml --interval 5m
```

### MCP Server

#### stdio (default — for VS Code / Copilot CLI direct spawn)

```bash
./bin/omni-code mcp
```

#### SSE HTTP server (legacy transport, broadest client compatibility)

```bash
./bin/omni-code mcp --transport sse --addr :8090
```

#### Streamable HTTP server (modern MCP spec 2025-03-26)

```bash
./bin/omni-code mcp --transport streamable --addr :8090
# Health probe
curl http://localhost:8090/health
```

Add `--cors` to either HTTP mode when connecting a browser-based GUI (e.g. MCP Inspector). Never use `--cors` by default.

The server exposes tools: `search_codebase`, `list_repos`, `get_repo_files`, `get_file_content`, `git_status`, `git_diff`, `git_log`, `index_status`, `grep_codebase`, `get_file_symbols`, `reindex_repo`, `get_repo_summary`, `search_repo_summaries`, `get_top_contributors`.

## GitHub Copilot Integration

### Option A — SSE server (recommended: no hardcoded paths)

Start the daemon once:
```bash
./bin/omni-code mcp --transport sse --addr :8090
```

Add to `.vscode/mcp.json`:
```json
{
  "servers": {
    "omni-code": {
      "type": "sse",
      "url": "http://localhost:8090"
    }
  }
}
```

### Option B — stdio (spawns binary per session)

```json
{
  "mcp": {
    "servers": {
      "omni-code": {
        "command": "/absolute/path/to/bin/omni-code",
        "args": ["mcp"]
      }
    }
  }
}
```

## Architecture

```
cmd/omni-code/main.go       CLI entry point (index, search, mcp, watch, repos)
internal/config/            Config loading, resolution, and skip-list logic
internal/git/               Git-aware file listing, branch detection, diffing
internal/estimator/         Pre-scan complexity estimation & score sorting
internal/db/chroma.go       ChromaDB client — files, chunks, and repos collections
internal/indexer/indexer.go Change detection, deduplication, incremental logic
internal/chunker/chunker.go Tree-sitter & line-based chunking
internal/embedder/          Pluggable backends (Chroma, Ollama, OpenAI)
internal/mcp/server.go      MCP server providing codebase exploration tools
```

## Data flow

```
git ls-files / filepath.WalkDir
  → Branch check & Git diff (incremental)
  → Content hashing & Global dedup
  → Semantic Chunker
  → Embedder (local/remote)
  → ChromaDB (upsert chunks + repo/file meta)
```