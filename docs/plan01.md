# Project Build Plan: Omni-Code Phase 01

## TL;DR

Extend `omni-code` with operational tooling, multi-repo configuration, git-aware indexing (branch filtering + commit-based change detection), a metadata/status layer, enhanced MCP tools, watch/daemon mode, configurable embedding backends, and search quality improvements.

**Prerequisite**: plan00.md phases must be complete before starting here.

---

## 1. Phase Overview

| Phase | Title | Priority |
|-------|-------|----------|
| 1 | Operational Makefile Targets | High — quick wins |
| 2 | Multi-Repo Config File + `repos` Command | High — foundation for everything |
| 3 | Git-Aware Indexing & Branch Filtering | High — core correctness |
| 4 | Repo Metadata Collection & Status Display | High — operational visibility |
| 5 | Enhanced MCP Tools | Medium — Copilot UX |
| 6 | Watch / Daemon Mode | Medium — automation |
| 7 | Embedding Model Configuration | Medium — quality |
| 8 | Search Quality Improvements | Low — polish |

---

## 2. Phase 1 — Operational Makefile Targets

The current Makefile is minimal. Add targets for day-to-day operations.

**New targets:**

| Target | What it does |
|--------|-------------|
| `reset-db` | Drop `files`, `chunks`, and `repos` collections from ChromaDB, then recreate them (full wipe) |
| `reset-repo` | Delete all docs where `metadata.repo == REPO` (targeted wipe); usage: `make reset-repo REPO=myapp` |
| `reindex` | Read `repos.yaml`, run `omni-code index --config repos.yaml` for all configured repos |
| `reindex-repo` | Reindex a single repo by name from config; usage: `make reindex-repo REPO=myapp` |
| `backup-db` | Copy ChromaDB data files out of the container to `./backups/<timestamp>/` |
| `restore-db` | Restore from a backup archive; usage: `make restore-db FILE=./backups/2026-03-17/chroma.tar.gz` |
| `status` | Run `omni-code repos` and print the repo index status table |
| `stop-db` | `docker stop chroma-db` |
| `rm-db` | `docker rm chroma-db` (use with caution; pair with `backup-db` first) |

**Tasks:**

- [ ] Add `reset-db` target: call `omni-code reset --all`
- [ ] Add `reset-repo` target: call `omni-code reset --repo $(REPO)` (error if `REPO` not set)
- [ ] Add `reindex` target: call `omni-code index --config repos.yaml`
- [ ] Add `reindex-repo` target: call `omni-code index --config repos.yaml --repo $(REPO)`
- [ ] Add `backup-db` target: `docker exec chroma-db tar -czf - /chroma/chroma` > `./backups/$(shell date +%Y-%m-%dT%H-%M-%S)/chroma.tar.gz`
- [ ] Add `restore-db` target: validate `FILE` var set, copy into container
- [ ] Add `status` target: call `omni-code repos`
- [ ] Add `stop-db` and `rm-db` targets
- [ ] Update `.PHONY` to include all new targets
- [ ] Add a `docker-db-start` alias that is idempotent (starts existing container if stopped, creates if missing)

---

## 3. Phase 2 — Multi-Repo Configuration File & `repos` Command

Instead of manually calling `omni-code index` for each repo, a YAML config file drives everything. Introduce a new `internal/config/` package and `repos` CLI subcommand.

**Config file format (`repos.yaml`):**

```yaml
db: http://localhost:8000          # ChromaDB URL (overridden by --db flag)
embedding_backend: chroma-default  # see Phase 7

# Global skip rules — applied to every repo unless overridden per-repo.
# These lists are merged with the built-in defaults defined in indexer.go.
# Use skip_*_extra to ADD to defaults, or skip_*_override to REPLACE them entirely.
skip_dirs_extra:       []           # e.g. ["fixtures", "mocks"]
skip_extensions_extra: []           # e.g. [".lock", ".sum"]
skip_filenames_extra:  []           # e.g. ["CHANGELOG.md"]

repos:
  - name: omni-code
    path: /mnt/plex_media/git/omni-code
    branch: main          # optional; auto-detect if omitted

  - name: some-project
    path: /mnt/plex_media/git/some-project
    # Per-repo overrides — merged with or replace global skip rules.
    skip_dirs_extra:       ["generated", "fixtures"]
    skip_extensions_extra: [".lock"]
    skip_filenames_extra:  []
    # Set to true to completely replace the global+default lists (not merge):
    # skip_dirs_override:       ["only_this_dir"]
    # skip_extensions_override: [".only_this_ext"]
    # skip_filenames_override:  ["only_this_file"]
```

**New package `internal/config/config.go`:**

```go
type Config struct {
    DB               string      `yaml:"db"`
    EmbeddingBackend string      `yaml:"embedding_backend"`
    // Global skip-list additions (merged with built-in defaults for every repo).
    SkipDirsExtra       []string    `yaml:"skip_dirs_extra"`
    SkipExtensionsExtra []string    `yaml:"skip_extensions_extra"`
    SkipFilenamesExtra  []string    `yaml:"skip_filenames_extra"`
    Repos               []RepoEntry `yaml:"repos"`
}

type RepoEntry struct {
    Name   string `yaml:"name"`
    Path   string `yaml:"path"`
    Branch string `yaml:"branch"` // optional; empty = auto-detect
    // Per-repo skip-list additions (merged with global + built-in defaults).
    SkipDirsExtra       []string `yaml:"skip_dirs_extra"`
    SkipExtensionsExtra []string `yaml:"skip_extensions_extra"`
    SkipFilenamesExtra  []string `yaml:"skip_filenames_extra"`
    // Per-repo full overrides — when set, completely replaces the merged default+global list.
    SkipDirsOverride       []string `yaml:"skip_dirs_override"`
    SkipExtensionsOverride []string `yaml:"skip_extensions_override"`
    SkipFilenamesOverride  []string `yaml:"skip_filenames_override"`
}
```

**Skip list resolution order (lowest → highest priority):**

```
1. Built-in defaults (currently hardcoded in indexer.go — moved to config package)
2. Global  skip_*_extra  (merged in from top-level repos.yaml fields)
3. Per-repo skip_*_extra (merged in from the repo's entry)
4. Per-repo skip_*_override (if set, replaces the entire merged list for that repo)
```

**New CLI subcommand `omni-code repos`:**

```
$ omni-code repos
REPO              BRANCH  LAST COMMIT  LAST INDEXED          FILES   CHUNKS
omni-code         main    a3f92b1      2026-03-17 14:32:00   47      312
some-project      master  d91bc44      2026-03-16 09:11:00   1204    8943

$ omni-code repos remove <name>
$ omni-code repos add --name <name> --path <path> [--branch <branch>]
```

**Tasks:**

- [ ] Create `internal/config/` package with `Config` and `RepoEntry` structs
- [ ] Implement `Load(path string) (*Config, error)` — reads and validates `repos.yaml`
- [ ] Implement `Save(cfg *Config, path string) error` — writes back (used by `repos add/remove`)
- [ ] Add `--config` flag to `index` subcommand; when set, iterates all repos in config (serial by default, `--parallel` flag for concurrent)
- [ ] Add `--repo` flag to `index --config` to target a single named repo from the config
- [ ] Add `repos` subcommand with `list` (default), `add`, `remove` sub-subcommands
- [ ] Add `repos list` table output (reads from ChromaDB `repos` metadata collection)
- [ ] Add `repos add` — appends to `repos.yaml`, creates file if missing
- [ ] Add `repos remove` — removes from `repos.yaml` AND calls `omni-code reset --repo <name>`
- [ ] Add `reset` subcommand with `--all` and `--repo <name>` flags (calls `db.DeleteRepo` or `db.ResetAll`)
- [ ] Move hardcoded skip lists out of `indexer.go` into `internal/config/defaults.go` as exported `Default*` vars
- [ ] Implement `ResolveSkipLists(global Config, entry RepoEntry) (dirs, exts, filenames map[string]bool)` in config package — applies the 4-level merge/override logic
- [ ] Pass resolved skip lists into `IndexerConfig` instead of reading package-level vars
- [ ] Remove the three `var skip*` package-level maps from `indexer.go`; replace with fields on `IndexerConfig`
- [ ] Write tests for `ResolveSkipLists`: defaults only, global merge, per-repo merge, per-repo override
- [ ] Write tests for config load/save, validation of required fields (name, path)

---

## 4. Phase 3 — Git-Aware Indexing & Branch Filtering

The current indexer walks the filesystem blindly. Make it git-aware: detect the default branch, warn or skip if the wrong branch is checked out, use `git ls-files` to enumerate tracked files, and use commit-diff to do O(changed) incremental re-indexing.

**Default branch detection algorithm:**

```
1. git -C <root> symbolic-ref refs/remotes/origin/HEAD 2>/dev/null
   → e.g. refs/remotes/origin/main → strip prefix → "main"
2. If that fails, check existence of local branches in order:
   main, master, th-main, develop, trunk
3. If all fail → log a warning and fall back to filesystem walk
```

**Branch enforcement:**

- Compare detected default branch with `git rev-parse --abbrev-ref HEAD`
- If current branch ≠ default branch:
  - If `repos.yaml` entry has `branch` set and it matches current → OK
  - Otherwise: log a warning `[indexer] WARNING: repo <name> is on branch <current>, expected <default>`
  - `--strict-branch` flag makes this a fatal error
  - Add a `skip_branch_check: true` field in `RepoEntry` for repos where you intentionally index non-default

**Git-based file enumeration (replaces `filepath.WalkDir` for git repos):**

```go
// git ls-files --cached --others --exclude-standard <root>
// Returns all tracked + untracked-but-not-ignored files
// Respects all .gitignore files automatically (no go-gitignore needed for git repos)
```

- Fall back to `filepath.WalkDir` + go-gitignore for non-git directories (detected by absence of `.git/`)
- Remove `go-gitignore` dependency from git-repo path (keep as fallback only)

**Commit-hash-based incremental update:**

```
On first index:
  - Run full file enumeration
  - Store HEAD commit hash in repos metadata collection as last_indexed_commit

On re-index:
  - Fetch stored last_indexed_commit from repos collection
  - Run: git diff --name-only <last_indexed_commit> HEAD
  - Also run: git diff --name-only --diff-filter=D <last_indexed_commit> HEAD  (deleted files)
  - Process only changed/added files through chunker
  - For deleted files: call db.DeleteFileChunks + db.DeleteFileMeta
  - Update last_indexed_commit to current HEAD
  - Fall back to full scan if last_indexed_commit not found or git command fails
```

This makes re-indexing O(changed files) instead of O(all files) — critical for large repos.

**`IndexerConfig` gains skip-list fields (set by `config.ResolveSkipLists`):**

```go
type IndexerConfig struct {
    RootPath   string
    RepoName   string
    DB         *db.ChromaClient
    ChunkFn    ChunkFunc
    SeenHashes *sync.Map
    // Resolved skip lists — populated from config, not hardcoded in this package.
    SkipDirs       map[string]bool
    SkipExtensions map[string]bool
    SkipFilenames  map[string]bool
}
```

**New package `internal/git/`:**

```go
// DetectDefaultBranch(root string) (string, error)
// CurrentBranch(root string) (string, error)
// ListFiles(root string) ([]string, error)          // git ls-files
// DiffFiles(root, fromCommit, toCommit string) (changed []string, deleted []string, error)
// HeadCommit(root string) (string, error)
// IsGitRepo(root string) bool
```

**Tasks:**

- [ ] Create `internal/git/` package
- [ ] Implement `IsGitRepo(root string) bool` — checks for `.git/` directory or `git rev-parse` success
- [ ] Implement `DetectDefaultBranch(root string) (string, error)` using the algorithm above
- [ ] Implement `CurrentBranch(root string) (string, error)`
- [ ] Implement `HeadCommit(root string) (string, error)`
- [ ] Implement `ListFiles(root string) ([]string, error)` — wraps `git ls-files`
- [ ] Implement `DiffFiles(root, from, to string) (changed, deleted []string, error)` — wraps `git diff --name-only`
- [ ] Integrate `IsGitRepo` check at start of `RunIndex`; route to git path or fs-walk path accordingly
- [ ] Integrate `DetectDefaultBranch` + `CurrentBranch` into `RunIndex`; emit warning if mismatch
- [ ] Implement `--strict-branch` flag on `index` subcommand
- [ ] Replace `filepath.WalkDir` + go-gitignore with `git ls-files` for git repos
- [ ] Implement commit-hash incremental: load `last_indexed_commit` from repos collection, run diff, process only changed files
- [ ] On initial index (no stored commit), run full scan then save HEAD commit
- [ ] For deleted files detected via `git diff --diff-filter=D`: call `db.DeleteFileChunks` and `db.DeleteFileMeta`
- [ ] Update `IndexStats` to include `deleted_files`, `last_commit`, `branch`, `index_mode` (full/incremental)
- [ ] Write tests for `DetectDefaultBranch` (mock git output), `DiffFiles`, `IsGitRepo`
- [ ] Write integration test: index a small test git repo, make a file change, re-index, verify only changed file was processed

---

## 5. Phase 4 — Repo Metadata Collection & Status Display

Add a `repos` ChromaDB collection to track per-repo indexing state. Used by `omni-code repos`, `make status`, and the incremental indexing logic in Phase 3.

**New ChromaDB collection `repos`:**

| Field | Type | Description |
|-------|------|-------------|
| `repo` | string | Repo name (also the document ID) |
| `root_path` | string | Absolute filesystem path |
| `default_branch` | string | Auto-detected or configured branch |
| `current_branch` | string | Branch at last index time |
| `last_indexed_commit` | string | Git SHA of HEAD at last index |
| `last_indexed_at` | string | RFC3339 timestamp |
| `file_count` | int | Files indexed |
| `chunk_count` | int | Chunks stored |
| `index_mode` | string | `full` or `incremental` |
| `duration_ms` | int | How long the last index took |

**New `db` methods:**

```go
UpsertRepoMeta(ctx, meta RepoMeta) error
GetRepoMeta(ctx, repo string) (*RepoMeta, error)
ListRepoMeta(ctx) ([]RepoMeta, error)
DeleteRepoMeta(ctx, repo string) error
DeleteAllRepoMeta(ctx) error
```

**Tasks:**

- [ ] Add `repos` collection to `EnsureCollections`
- [ ] Define `RepoMeta` struct in `internal/db/`
- [ ] Implement `UpsertRepoMeta`, `GetRepoMeta`, `ListRepoMeta`, `DeleteRepoMeta`, `DeleteAllRepoMeta`
- [ ] Call `UpsertRepoMeta` at end of `RunIndex` with final stats
- [ ] Implement `repos list` subcommand output: tabwriter-formatted table to stdout
- [ ] Implement `reset --all`: calls `ResetAllCollections` which drops+recreates `files`, `chunks`, `repos`
- [ ] Implement `reset --repo <name>`: calls `DeleteFileChunks` (all for repo) + `DeleteFileMeta` (all for repo) + `DeleteRepoMeta`
- [ ] Extend `db.DeleteFileChunks` to accept a repo-wide delete (no specific path filter)
- [ ] Write tests for `UpsertRepoMeta` / `ListRepoMeta` round-trip (skip if `CHROMA_URL` not set)

---

## 6. Phase 5 — Enhanced MCP Tools

The current MCP server exposes only `search_codebase`. Add tools that let Copilot explore the index directly.

**New tools:**

| Tool name | Description | Parameters |
|-----------|-------------|------------|
| `list_repos` | List all indexed repos with stats | none |
| `get_repo_files` | List all indexed file paths for a repo | `repo` (required), `filter` (glob, optional) |
| `get_file_content` | Return full content of a specific indexed file from disk | `repo` (required), `path` (required) |

**Notes:**

- `get_file_content` reads from **disk** (not ChromaDB) using the `root_path` stored in repo metadata — avoids the chunking boundary problem when Copilot needs a full file
- `get_repo_files` uses `db.QueryAllFileMeta(ctx, repo)` to list indexed paths without doing a vector search
- These three tools are in addition to the existing `search_codebase`

**New `db` methods needed:**

```go
QueryAllFileMeta(ctx, repo string) ([]FileMeta, error)  // list all files for a repo
```

**Tasks:**

- [ ] Add `QueryAllFileMeta` to `internal/db/chroma.go`
- [ ] Register `list_repos` tool: calls `db.ListRepoMeta`, formats as markdown table
- [ ] Register `get_repo_files` tool: calls `db.QueryAllFileMeta`, applies optional glob filter via `path.Match`, returns newline-separated list
- [ ] Register `get_file_content` tool: resolves absolute path from `RepoMeta.root_path + path`, reads file from disk, returns content with language fence
- [ ] Add file size guard on `get_file_content`: if file > 100KB, return first 100KB with a truncation notice
- [ ] Ensure all new tool handlers log to `os.Stderr` only — zero stdout except JSON-RPC
- [ ] Write tests for each new tool handler (mock db, assert output format)

---

## 7. Phase 6 — Watch / Daemon Mode

Add an `omni-code watch` subcommand that polls for git HEAD changes and triggers incremental re-indexing automatically.

**Command:**

```
omni-code watch --config repos.yaml [--interval 5m] [--once]
```

- `--interval`: how often to check for HEAD changes (default `5m`)
- `--once`: run one check-and-reindex pass then exit (useful for cron/git hooks)

**Algorithm per repo per tick:**

```
1. HeadCommit(root) → current_commit
2. GetRepoMeta(repo) → last_indexed_commit
3. If current_commit == last_indexed_commit → skip (no changes)
4. Else → run incremental RunIndex (Phase 3 diff-based)
```

**Git post-commit hook installer:**

```
omni-code watch --install-hook --config repos.yaml
```

Writes a `post-commit` script to `<repo>/.git/hooks/post-commit`:

```sh
#!/bin/sh
omni-code index --config /path/to/repos.yaml --repo <name> --once 2>/dev/null &
```

**Tasks:**

- [ ] Add `watch` subcommand to CLI
- [ ] Implement poll loop with configurable interval (use `time.Ticker`)
- [ ] Implement per-repo HEAD-change check using `HeadCommit` from `internal/git`
- [ ] Trigger `RunIndex` (incremental) for repos with changed HEAD
- [ ] Add `--once` flag for single-pass mode (useful in git hooks)
- [ ] Implement `--install-hook` flag: write `post-commit` hook script to each repo in config
- [ ] Ensure `watch` handles `SIGINT`/`SIGTERM` gracefully (finish current index, then exit)
- [ ] Log each check cycle and reindex trigger to `os.Stderr`
- [ ] Write test for poll loop logic (mock HeadCommit to return changing values)

---

## 8. Phase 7 — Embedding Model Configuration

Decouple the embedding backend from ChromaDB's default. Allow configuring which model generates embeddings for chunks.

**Supported backends:**

| Backend | Config value | Notes |
|---------|-------------|-------|
| ChromaDB built-in | `chroma-default` | Current behavior; no extra setup |
| Ollama | `ollama` | `POST http://localhost:11434/api/embeddings`; best local option |
| OpenAI | `openai` | Requires `OPENAI_API_KEY`; `text-embedding-3-small` default |
| OpenAI-compatible | `openai-compatible` | Any endpoint accepting OpenAI embedding API; `EMBEDDING_API_URL` + `EMBEDDING_API_KEY` env vars |

**Config fields (in `repos.yaml` top level and per-repo override):**

```yaml
embedding_backend: ollama
embedding_model: nomic-embed-text   # default per backend if omitted
embedding_url: http://localhost:11434  # for ollama or openai-compatible
```

**New package `internal/embedder/`:**

```go
type Embedder interface {
    Embed(ctx context.Context, texts []string) ([][]float32, error)
}

func NewEmbedder(backend, model, url, apiKey string) (Embedder, error)
```

**Tasks:**

- [ ] Create `internal/embedder/` package with `Embedder` interface
- [ ] Implement `ChromaDefaultEmbedder` (pass nil embedder to chroma-go; let it handle internally)
- [ ] Implement `OllamaEmbedder`: HTTP POST to Ollama `/api/embeddings`
- [ ] Implement `OpenAIEmbedder`: HTTP POST to OpenAI `/v1/embeddings` (or compatible URL)
- [ ] Wire embedder selection into `UpsertChunks` via the ChromaDB collection's embedding function
- [ ] Add `--embedding-backend`, `--embedding-model`, `--embedding-url` flags to `index` subcommand
- [ ] Read embedding config from `repos.yaml` as defaults; flags override
- [ ] Add `OPENAI_API_KEY` and `EMBEDDING_API_KEY` env var lookup (never accept API keys as CLI flags)
- [ ] Write tests for `OllamaEmbedder` and `OpenAIEmbedder` with a mock HTTP server

---

## 9. Phase 8 — Search Quality Improvements

Improve the quality and usability of search results.

**Improvements:**

| Feature | Description |
|---------|-------------|
| Language filter | `--lang go` filters results to chunks where `metadata.language == go` |
| Extension filter | `--ext .go,.ts` filters by file extension |
| Result deduplication | Suppress near-duplicate chunks from the same file (e.g. top 3 chunks from same file → keep 1) |
| Context lines | `--context-lines N`: when returning a chunk, read N extra lines above/below from disk |
| Score threshold | `--min-score 0.7`: suppress results below a similarity score floor |
| Hybrid search | Combine BM25 keyword ranking + vector similarity; re-rank by Reciprocal Rank Fusion (RRF) |

**Tasks:**

- [ ] Add `--lang` and `--ext` filter flags to `search` subcommand; pass as ChromaDB `where` metadata filter
- [ ] Implement result deduplication in `QueryChunks`: if >2 chunks from same file in top-N, keep only highest-score one
- [ ] Add `--context-lines` flag: after fetching chunk, read file from disk and extend `StartLine`/`EndLine` by N
- [ ] Add `--min-score` flag: filter `QueryChunks` results below threshold
- [ ] Expose `--lang`, `--ext`, `--min-score` as optional parameters on `search_codebase` MCP tool
- [ ] Implement BM25 keyword index over chunk content (in-memory using `github.com/blevesearch/bleve` or a simple TF-IDF) for hybrid search
- [ ] Implement RRF score fusion: `combined_score = 1/(k + bm25_rank) + 1/(k + vector_rank)` (k=60)
- [ ] Add `--hybrid` flag to enable BM25+vector hybrid mode (off by default)
- [ ] Write tests for deduplication logic, score threshold filtering, RRF fusion

---

## 10. Data Flow Summary (Updated)

```
repos.yaml
    │
    ▼
omni-code index --config repos.yaml
    │
    ▼  for each repo:
┌──────────────────────────┐
│  internal/git            │  IsGitRepo? → git path or fs-walk path
│  DetectDefaultBranch     │  Warn if wrong branch checked out
│  HeadCommit              │  Compare to last_indexed_commit
│  DiffFiles (incremental) │  Only changed files on re-index
└──────────┬───────────────┘
           │ file list (full or diff)
           ▼
┌──────────────────────────┐
│  Indexer                 │  Size→MTime→Hash fallback (non-git / initial)
│  Skip rules              │  Skip binaries, node_modules, etc.
│  Global dedup (seenHash) │  Skip files identical across repos
└──────────┬───────────────┘
           │ changed unique files
           ▼
┌──────────────────────────┐
│  Chunker                 │  Tree-sitter (code) or line-split (text)
└──────────┬───────────────┘
           │ []Chunk
           ▼
┌──────────────────────────┐
│  Embedder                │  chroma-default / ollama / openai
└──────────┬───────────────┘
           │ []Chunk with embeddings
           ▼
┌──────────────────────────┐
│  ChromaDB                │  Upsert files, chunks, repos collections
└──────────────────────────┘


omni-code watch --config repos.yaml
    │  every --interval (default 5m)
    ▼
    Check HeadCommit per repo → if changed → RunIndex (incremental)


GitHub Copilot → MCP stdio
    │
    ▼
  search_codebase(query, repo?, n_results?)
  list_repos()
  get_repo_files(repo, filter?)
  get_file_content(repo, path)
```
