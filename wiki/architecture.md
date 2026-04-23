# Architecture

## System Overview

omni-code is a Go monolith that indexes local Git repositories into a hybrid storage backend (ChromaDB for vector chunks, SQLite for metadata), then exposes the indexed data through three interfaces: CLI commands, an MCP protocol server, and an interactive AI chat REPL.

## Data Flow

```
git ls-files / filepath.WalkDir
  → Branch check & Git diff (incremental)
  → Content hashing & Global SHA256 dedup
  → Tree-sitter Semantic Chunker (9 languages + line fallback)
  → Embedder (chroma-default / ollama / openai)
  → ChromaDB (upsert chunks)
  → SQLite (upsert file + repo metadata)
```

## Layered Architecture

```
┌─────────────────────────────────────────────────────┐
│                   cmd/omni-code/                    │
│         CLI flags, subcommand routing, watch loop   │
├─────────────────────────────────────────────────────┤
│  internal/chat/   │  internal/mcp/                  │
│  REPL + OpenAI    │  MCP server (stdio/SSE/stream)  │
│  client + tools   │  Tool handlers + dispatch       │
├─────────────────────────────────────────────────────┤
│  internal/indexer/          │  internal/estimator/   │
│  Change detection, dedup,   │  Pre-scan cost sort    │
│  buffered flush, progress   │                        │
├─────────────────────────────────────────────────────┤
│  internal/chunker/  │  internal/embedder/            │
│  Tree-sitter AST    │  Ollama, OpenAI, Chroma-EF     │
├─────────────────────────────────────────────────────┤
│  internal/git/              │  internal/config/       │
│  ls-files, diff, branch,    │  YAML, env, CLI merge   │
│  log, status                │  + skip-list resolution  │
├─────────────────────────────────────────────────────┤
│  internal/db/                                        │
│  ChromaDB (chunks collection) + SQLite (files, repos)│
└─────────────────────────────────────────────────────┘
```

## Storage Model

### ChromaDB (`chunks` collection)

Stores embedded code/text chunks for semantic similarity search.

| Field | Type | Description |
|---|---|---|
| `id` | DocumentID | SHA256 of `repo\0path\0startLine` |
| `document` | string | Chunk text content |
| `embedding` | []float32 | Dense vector (computed by embedder or ChromaDB EF) |
| `repo` | string meta | Repository name |
| `path` | string meta | Absolute file path |
| `language` | string meta | Canonical language name |
| `start_line` | int meta | 1-based start line in source file |
| `end_line` | int meta | 1-based end line in source file |

### SQLite (`~/.omni-code/omni-code.db`)

Two tables for fast metadata queries without ChromaDB round-trips:

**`files` table** — change-detection metadata per indexed file:

| Column | Type | Description |
|---|---|---|
| `repo` | TEXT (PK) | Repository name |
| `path` | TEXT (PK) | Absolute file path |
| `hash` | TEXT | SHA256 content hash |
| `size` | INTEGER | File size in bytes |
| `mtime` | INTEGER | Unix modification timestamp |

**`repos` table** — per-repository indexing state:

| Column | Type | Description |
|---|---|---|
| `repo` | TEXT (PK) | Repository name |
| `root_path` | TEXT | Absolute path to repo root |
| `default_branch` | TEXT | Auto-detected default branch |
| `current_branch` | TEXT | Branch at last index time |
| `last_indexed_commit` | TEXT | HEAD SHA at last index |
| `last_indexed_at` | TEXT | RFC3339 timestamp |
| `index_mode` | TEXT | `"full"` or `"incremental"` |
| `file_count` | INTEGER | Files scanned in last run |
| `chunk_count` | INTEGER | Chunks upserted in last run |
| `duration_ms` | INTEGER | Indexing duration |

## Change Detection Cascade

The indexer uses a three-stage cascade to minimize I/O — **do not reorder**:

1. **Size** — if file size differs from stored metadata → changed
2. **MTime** — if modification time differs → compute SHA256 hash
3. **SHA256 Hash** — compare hash with stored value → changed if different

After change detection, a global `sync.Map` of hashes provides cross-repo deduplication.

## Chunking Strategy

- **Small files** (<1000 chars) → single chunk
- **Tree-sitter supported** (Go, JS, TS, Python, Java, PHP, Ruby, HTML, JSON) → one chunk per top-level declaration; oversized nodes split with 200-char overlap
- **Fallback** → line-based splitting at ~500-word boundaries with ~50-word overlap
- **Max chunk size**: ~3200 chars (~800 tokens)

## Search Pipeline

1. Vector similarity query against ChromaDB `chunks` collection
2. Optional post-filters: extension, language, min-score, dedup-by-file
3. Optional BM25 hybrid re-ranking via Reciprocal Rank Fusion (k=60)
4. Optional context-line expansion (reads source from disk)

## MCP Transport Modes

| Mode | Flag | SDK Type | Best For |
|---|---|---|---|
| stdio (default) | `--transport stdio` | `mcp.StdioTransport` | VS Code / Copilot direct spawn |
| SSE (legacy) | `--transport sse` | `mcp.SSEHandler` | Broad client compatibility |
| Streamable HTTP | `--transport streamable` | `mcp.StreamableHTTPHandler` | Modern MCP clients (2025-03-26 spec) |

All three modes share a single `buildServer(client)` helper for tool registration.

## Config Resolution Order

Settings are resolved with increasing priority:

1. Built-in defaults (`internal/config/defaults.go`)
2. `repos.yaml` file values
3. Environment variables (`OMNI_DB_URL`, `OMNI_EMBEDDING_BACKEND`, etc.)
4. CLI flags
