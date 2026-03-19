# omni-code

A local codebase indexer and **MCP server** written in Go. Connect it to GitHub Copilot to ask natural-language questions about your local repositories, backed by semantic search over indexed code.

## Features

- **Git-aware indexing** — scans only tracked/untracked-but-not-ignored files; uses commit-diffs for $O(\text{changed})$ incremental re-indexing
- **Multi-repo configuration** — configure multiple repositories in \`repos.yaml\` with custom branch and skip rules
- **Intelligent change detection** — Hash-based change detection with global across-repo deduplication
- **Semantic chunking** — Tree-sitter powered chunking for Go, Python, JS/TS, Rust, and Java
- **Flexible embedding backends** — local (Ollama/Chroma-built-in) or remote (OpenAI) embedding generation
- **Hybrid search** — combines vector similarity with BM25 keyword ranking using Reciprocal Rank Fusion (RRF)
- **Watch mode** — background daemon that polls for Git HEAD changes and re-indexes automatically
- **Comprehensive MCP tools** — includes \`search_codebase\`, \`list_repos\`, \`get_repo_files\`, and \`get_file_content\`

## Prerequisites

- Go 1.25+
- Docker (for ChromaDB)

## Setup

### 1. Start ChromaDB

\`\`\`bash
make docker-db-start
\`\`\`

### 2. Build the binary

\`\`\`bash
make build
\`\`\`

The binary is placed at \`bin/omni-code\`.

### 3. (Optional) Create a config file

Create \`repos.yaml\` to manage multiple repositories:

\`\`\`yaml
db: http://localhost:8000
embedding_backend: ollama
embedding_model: nomic-embed-text

repos:
  - name: omni-code
    path: /path/to/omni-code
    branch: main
\`\`\`

## Usage

### Indexing

\`\`\`bash
# Index a single repository
./bin/omni-code index --name my-project /path/to/project

# Index all repositories in the config
./bin/omni-code index --config repos.yaml
\`\`\`

### Search

\`\`\`bash
./bin/omni-code search --query "how does change detection work" --hybrid
\`\`\`

| Flag | Default | Description |
|---|---|---|
| \`--query\` | *(required)* | Natural-language search query |
| \`--hybrid\` | \`false\` | Enable BM25 + Vector hybrid re-ranking |
| \`--repo\` | *(empty)* | Filter results to a specific repository |
| \`--lang\` | *(empty)* | Filter by language (e.g. \`go\`) |
| \`--ext\` | *(empty)* | Filter by file extension (e.g. \`.ts\`) |
| \`--n\` | \`10\` | Number of results to return |

### Watch Mode

Start a background daemon that polls for changes every 5 minutes:

\`\`\`bash
./bin/omni-code watch --config repos.yaml --interval 5m
\`\`\`

### MCP Server

\`\`\`bash
./bin/omni-code mcp
\`\`\`

The server exposes tools: \`search_codebase\`, \`list_repos\`, \`get_repo_files\`, \`get_file_content\`.

## GitHub Copilot Integration

Add the following to your VS Code \`settings.json\` (or create \`.vscode/mcp.json\`):

\`\`\`json
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
\`\`\`

## Architecture

\`\`\`
cmd/omni-code/main.go       CLI entry point (index, search, mcp, watch, repos)
internal/config/            Config loading, resolution, and skip-list logic
internal/git/               Git-aware file listing, branch detection, diffing
internal/db/chroma.go       ChromaDB client — files, chunks, and repos collections
internal/indexer/indexer.go Change detection, deduplication, incremental logic
internal/chunker/chunker.go Tree-sitter & line-based chunking
internal/embedder/          Pluggable backends (Chroma, Ollama, OpenAI)
internal/mcp/server.go      MCP server providing codebase exploration tools
\`\`\`

## Data flow

\`\`\`
git ls-files / filepath.WalkDir
  → Branch check & Git diff (incremental)
  → Content hashing & Global dedup
  → Semantic Chunker
  → Embedder (local/remote)
  → ChromaDB (upsert chunks + repo/file meta)
\`\`\`
