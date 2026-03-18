# Project Build Plan: Omni-Code (Go + ChromaDB + MCP)

## TL;DR

Build **omni-code**, a local Go CLI and **MCP server** that indexes local Git repositories into ChromaDB. Features: intelligent change detection, global file deduplication, semantic code chunking via Tree-sitter, `.gitignore` support, and an MCP stdio interface so you can connect it to **GitHub Copilot** and ask natural-language questions about your code.

---

## 1. Tech Stack

| Component     | Choice                                                  |
| ------------- | ------------------------------------------------------- |
| Language      | **Go 1.21+**                                            |
| Vector DB     | **ChromaDB** (`github.com/amikos-tech/chroma-go/pkg/api/v2`) |
| Chunking      | **Tree-sitter** (`github.com/tree-sitter/go-tree-sitter`) |
| Gitignore     | `github.com/sabhiram/go-gitignore`                      |
| MCP Protocol  | `github.com/modelcontextprotocol/go-sdk`                |
| Build Tool    | **Make**                                                |

---

## 2. Makefile Specification

```makefile
.PHONY: build test dev docker-db clean

# Run ChromaDB locally for development
docker-db:
	docker run -d -p 8000:8000 --name chroma-db chromadb/chroma

# Build the binary
build:
	go build -o bin/omni-code ./cmd/omni-code

# Run tests
test:
	go test -v ./...

# Run in dev mode (index the test-data repo)
dev: build
	./bin/omni-code index --name test-repo ./test-data

# Clean build artifacts
clean:
	rm -rf bin/
```

---

## 3. Implementation Phases

### Phase 0 — Project Scaffolding

Set up the Go module, directory layout, Makefile, and a minimal `main.go` that compiles.

- [x] Run `go mod init` with the correct module path
- [x] Create directory structure: `cmd/omni-code/`, `internal/db/`, `internal/indexer/`, `internal/chunker/`, `internal/mcp/`, `docs/`, `test-data/`
- [x] Create `Makefile` with targets: `build`, `test`, `dev`, `docker-db`, `clean`
- [x] Create minimal `cmd/omni-code/main.go` that parses subcommands (`index`, `search`, `mcp`) and prints usage
- [x] Verify `make build` compiles and `make test` passes (no-op is fine)

---

### Phase 1 — Database Layer (`internal/db/chroma.go`)

Connect to ChromaDB, ensure required collections exist, and expose CRUD helpers the indexer and search layers need.

**Collections:**

| Collection | Purpose | Key metadata fields |
|---|---|---|
| `files` | One doc per indexed file; stores metadata for change detection | `repo`, `path`, `size`, `mtime`, `hash` |
| `chunks` | One doc per code chunk; stores the text + embedding | `repo`, `path`, `language`, `start_line`, `end_line` |

**Tasks:**

- [x] Implement `NewChromaClient(ctx, baseURL) (*ChromaClient, error)` — creates HTTP client, pings server, returns wrapper
- [x] Implement `EnsureCollections(ctx, ef)` — calls `GetOrCreateCollection` for `files` and `chunks`
- [x] Implement `GetFileMeta(ctx, repo, path) (*FileMeta, error)` — retrieves stored size/mtime/hash for a file (returns nil if not found)
- [x] Implement `UpsertFileMeta(ctx, repo, path, size, mtime, hash)` — upserts a record into `files`
- [x] Implement `UpsertChunks(ctx, chunks []Chunk)` — batch-upserts chunk documents with metadata into `chunks`
- [x] Implement `DeleteFileChunks(ctx, repo, path)` — removes all chunks for a file before re-indexing it (stale chunk cleanup)
- [x] Implement `QueryChunks(ctx, queryText string, nResults int, repoFilter string) ([]ChunkResult, error)` — semantic similarity search on `chunks`
- [x] Write tests with a running ChromaDB instance (skip if `CHROMA_URL` env not set)

---

### Phase 2 — Indexer & Change Detection (`internal/indexer/indexer.go`)

Walk a repository directory tree, skip ignored paths, detect which files changed since the last index, deduplicate globally, and hand off changed files for chunking + storage.

**Skip rules (hardcoded defaults):**

- **Directories**: `.git`, `node_modules`, `dist`, `build`, `vendor`, `.next`, `__pycache__`, `.venv`, `.tox`
- **Extensions**: `.pdf`, `.png`, `.jpg`, `.jpeg`, `.gif`, `.svg`, `.mp4`, `.mp3`, `.zip`, `.tar`, `.gz`, `.exe`, `.dll`, `.so`, `.dylib`, `.wasm`, `.bin`, `.dat`, `.db`, `.sqlite`
- **Filenames**: `.env`, `package-lock.json`, `yarn.lock`, `go.sum`, `.DS_Store`, `Thumbs.db`

**Change Detection Algorithm (sacred — do not alter order):**

```
1. SIZE   — if current size ≠ stored size → changed
2. MTIME  — if current mtime ≠ stored mtime → changed
3. HASH   — compute SHA-256 (use mtime+size cache key to avoid re-reads)
             if hash ≠ stored hash → changed
             else → unchanged
```

**Global deduplication:** Maintain `map[string]bool` of SHA-256 hashes across the run. If `seenHashes[hash]` is true, skip indexing (avoids indexing `jquery.min.js` 50 times across repos).

**Tasks:**

- [x] Define `IndexerConfig` struct: root path, repo name, ChromaDB client, skip lists
- [x] Implement `filepath.WalkDir` walker that respects skip rules (dirs, extensions, filenames)
- [x] Load and apply `.gitignore` rules using `go-gitignore` library at each repo root
- [x] Implement `HasChanged(ctx, filePath, stat) (bool, error)` using the Size → MTime → Hash cascade
- [x] Implement SHA-256 file hasher with mtime+size cache key (avoid redundant reads)
- [x] Implement global deduplication with `sync.Mutex`-protected `seenHashes` map
- [x] Implement `RunIndex(ctx, config) (*IndexStats, error)` — orchestrates the full walk+detect+chunk+store pipeline
- [x] Use `goroutines` + `sync.WaitGroup` + buffered channel for parallel file processing
- [x] Track and return `IndexStats`: files scanned, changed, skipped (dedup), skipped (unchanged), chunks upserted, errors
- [x] Write table-driven unit tests for skip rules, change detection logic, and deduplication

---

### Phase 3 — Smart Chunking (`internal/chunker/chunker.go`)

Split source files into semantically meaningful chunks suitable for embedding. Use Tree-sitter for code files, line-based splitting for text.

**Strategy table:**

| Condition | Strategy | Target size |
|---|---|---|
| File < 1000 chars | Return entire file as 1 chunk | — |
| Code file (`.go`, `.ts`, `.js`, `.py`, `.rs`, `.java`, `.c`, `.cpp`, `.rb`) | Tree-sitter AST split at top-level declarations (`function_declaration`, `method_declaration`, `class_declaration`, `type_declaration`) | ~800 tokens per chunk, 50-token overlap |
| Text / Markdown / Docker / Other | Line-based split | ~500 words per chunk, 50-word overlap |

**Chunk struct:**

```go
type Chunk struct {
    ID        string            // deterministic: SHA-256(repo + path + startLine)
    Repo      string
    Path      string
    Language  string
    Content   string
    StartLine int
    EndLine   int
    Metadata  map[string]string // extra: e.g. function name, class name
}
```

**Tasks:**

- [x] Define `Chunk` struct and `ChunkFile(repo, path, content string, lang string) ([]Chunk, error)` entry point
- [x] Implement small-file shortcut (< 1000 chars → single chunk)
- [x] Implement Tree-sitter parsing for Go (install `tree_sitter_go` grammar)
- [x] Implement Tree-sitter parsing for JavaScript/TypeScript
- [x] Implement Tree-sitter parsing for Python
- [x] Implement Tree-sitter parsing for Dockerfiles (if grammar available)
- [x] Implement Tree-sitter parsing for CircleCI (if grammar available)
- [x] Implement fallback line-based splitter for text/markdown/unknown files
- [x] Handle overlap: when a Tree-sitter node exceeds ~800 tokens, split it with overlap
- [x] Generate deterministic chunk IDs (SHA-256 of repo+path+startLine)
- [x] Write tests: small file, Go code, JS code, plain text, large single function

---

### Phase 4 — CLI Interface (`cmd/omni-code/main.go`)

Wire everything together behind a clean CLI with three subcommands.

**Subcommands:**

| Command | Description | Key flags |
|---|---|---|
| `omni-code index` | Scan and index a repository | `--name` (repo name), `--db` (ChromaDB URL, default `http://localhost:8000`) |
| `omni-code search` | Query indexed code from the terminal | `--query`, `--repo` (optional filter), `--n` (results, default 10), `--db` |
| `omni-code mcp` | Start the MCP stdio server for Copilot | `--db` |

**Tasks:**

- [x] Implement subcommand routing using `os.Args` + `flag.NewFlagSet` per subcommand
- [x] Implement `index` subcommand: parse flags → create ChromaClient → run `indexer.RunIndex` → print `IndexStats` summary
- [x] Implement `search` subcommand: parse flags → create ChromaClient → call `QueryChunks` → format and print results (path, lines, snippet)
- [x] Implement `mcp` subcommand: parse flags → create ChromaClient → start MCP server (Phase 5)
- [x] Print usage/help when no subcommand or `--help` is given
- [x] Ensure all logging goes to `os.Stderr`; only the `mcp` subcommand writes to `os.Stdout` (the JSON-RPC stream)
- [ ] End-to-end manual smoke test: `make docker-db`, populate `test-data/`, `make dev`, `omni-code search --query "main function"`

---

### Phase 5 — MCP Server (`internal/mcp/server.go`)

Expose the search functionality as an MCP tool so Copilot can call it over stdio.

**Tool definition:**

| Tool name | Description | Parameters |
|---|---|---|
| `search_codebase` | Semantic search across all indexed local codebases | `query` (string, required), `repo` (string, optional — filter to one repo), `n_results` (int, optional, default 10) |

**Response format per result:**

```
## <repo>:<path> (lines <start>–<end>)
```<language>
<chunk content>
```
```

**Tasks:**

- [x] Create MCP server using `server.NewServer("omni-code", "1.0.0")` from `go-sdk`
- [x] Register `search_codebase` tool with input schema (query, repo, n_results)
- [x] Implement tool handler: validate input → call `db.QueryChunks` → format results as markdown text
- [x] Call `server.ServeStdio(s)` in the `mcp` subcommand — this blocks and handles JSON-RPC over stdin/stdout
- [x] Ensure **zero** `fmt.Print*` calls in `internal/mcp/` — all logs to `os.Stderr` via `log.Printf`
- [x] Write test: mock or local ChromaDB, invoke tool handler directly, assert markdown output format

---

### Phase 6 — Copilot Integration & Polish

Connect the built binary to GitHub Copilot as an MCP server and validate the full loop.

**Tasks:**

- [x] Add MCP server config to VS Code `settings.json` (or `.vscode/mcp.json`): `{ "servers": { "omni-code": { "command": "/path/to/bin/omni-code", "args": ["mcp"] } } }`
- [ ] Verify Copilot discovers the `search_codebase` tool
- [ ] Test a real query from Copilot Chat: "How does the indexer detect file changes?"
- [ ] Index at least 2 real local repos and verify search returns relevant results
- [x] Add `--verbose` / `--quiet` flags to control log output level
- [x] Write a `README.md` with: project description, setup instructions, usage examples, Copilot configuration
- [x] Final pass: `make test` passes, `go vet ./...` clean, no TODO/FIXME left in code

---

## 4. Data Flow Summary

```
User runs: omni-code index --name my-repo ./src
              │
              ▼
   ┌─────────────────────┐
   │  filepath.WalkDir    │  Skip .git, node_modules, binaries, etc.
   │  + .gitignore rules  │  Apply go-gitignore at repo root
   └──────────┬──────────┘
              │ for each file
              ▼
   ┌─────────────────────┐
   │  Change Detection    │  Size → MTime → SHA-256 hash
   │  (compare vs DB)     │  Skip if unchanged
   └──────────┬──────────┘
              │ if changed
              ▼
   ┌─────────────────────┐
   │  Global Dedup        │  Skip if seenHashes[hash] == true
   └──────────┬──────────┘
              │ if unique
              ▼
   ┌─────────────────────┐
   │  Chunker             │  Tree-sitter (code) or line-split (text)
   └──────────┬──────────┘
              │ []Chunk
              ▼
   ┌─────────────────────┐
   │  ChromaDB            │  Upsert file meta + chunks (with embeddings)
   └─────────────────────┘

User asks Copilot: "How does the indexer work?"
              │
              ▼
   ┌─────────────────────┐
   │  MCP stdio server    │  Copilot sends search_codebase tool call
   └──────────┬──────────┘
              │
              ▼
   ┌─────────────────────┐
   │  db.QueryChunks()    │  Semantic similarity search in ChromaDB
   └──────────┬──────────┘
              │ top-N results
              ▼
   ┌─────────────────────┐
   │  Format as Markdown  │  repo:path (lines X–Y) + code snippet
   └──────────┬──────────┘
              │
              ▼
   Copilot shows answer with relevant code context
```
