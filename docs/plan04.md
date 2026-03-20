# Plan 04: Performance, Monitoring, and Automation

Plan for evolving `omni-code` into a high-performance, event-driven development companion with deep Git integration and global configuration.

## Phase 1: Global Configuration Consolidation
*Goal: Centralize flag/config/env resolution to eliminate duplication and hardcoded defaults.*

- [x] Refactor `internal/config` to support a merged resolution strategy (Defaults -> `repos.yaml` -> Env -> Flags).
- [x] Add support for `OMNI_DB_URL`, `OMNI_EMO_BACKEND`, etc., as environment variables.
- [x] Update all subcommands (`search`, `mcp`, `watch`, `repos`) to accept `--config` and honor global settings.
- [x] Eliminate hardcoded `http://localhost:8000` from `runSearch` and `runMCP`.

## Phase 2: Indexing Performance & Filtering
*Goal: Reduce network round-trips and add strict branch enforcement.*

- [x] Implement `GetBatchFileMeta` in `internal/db` to pre-fetch all file metadata for a repo at scan start.
- [ ] Modify `indexer.processFile` to buffer chunk and metadata upserts.
- [ ] Implement batch flush logic in `internal/db.ChromaClient` (e.g., flush every 50 files or 500 chunks).
- [x] Add `skip_if_wrong_branch: boolean` to `RepoEntry` in `internal/config`.
- [x] Update `indexer.RunIndex` to skip scanning entirely if `skip_if_wrong_branch` is true and the current branch doesn't match.

## Phase 3: Embedding Optimization
*Goal: Speed up vector generation through parallelism and smarter batching.*

- [ ] Parallelize `OllamaEmbedder` using `errgroup` (currently sequential).
- [ ] Add batching logic to `internal/db.UpsertChunks` to split massive file chunk lists into optimal API batch sizes.
- [x] Implement a lightweight per-run hash cache to avoid re-reading files for global deduplication checks.

## Phase 4: Watch Mode: Instant Monitoring (fsnotify)
*Goal: Replace polling with event-based change detection.*

- [ ] Integrate `fsnotify/fsnotify` into `runWatch`.
- [ ] Implement watchers for `.git/refs/heads/` to detect `git commit`/`merge` events instantly.
- [ ] Add a debounce timer (e.g., 5s) to prevent multiple re-indexes during rebases or rapid commits.
- [ ] Keep polling as a fallback for non-local filesystems.

## Phase 5: Automation: Master Branch Auto-Pull
*Goal: Keep local indexes in sync with remote repositories automatically.*

- [ ] Add `auto_pull: boolean` property to `RepoEntry` in `internal/config`.
- [ ] Implement `git.FetchAndFastForward` in `internal/git` (safe `--ff-only` merges).
- [ ] Integrate auto-pull logic into the `watch` loop before trigger checks.
- [ ] Add safety checks to skip auto-pull if the working tree is dirty.

## Phase 6: MCP Tool Expansion (Git & Diagnostics)
*Goal: Empower AI agents with deeper repository context.*

- [x] Implement `git_status`: Show branch, uncommitted changes, and index staleness.
- [x] Implement `git_diff`: Show diff between current state and last indexed commit.
- [x] Implement `git_log`: Show recent commit history.
- [x] Implement `index_status`: Detailed breakdown of when/how each repo was last indexed.

## Phase 7: Advanced MCP Tools & Quality of Life
*Goal: Enhanced discovery and management via protocol tools.*

- [ ] Implement `get_file_symbols`: Use tree-sitter to return function/class definitions without reading the full file.
- [ ] Implement `grep_codebase`: Literal/regex search utility using `git grep`.
- [ ] Implement `reindex_repo`: Allow an MCP client to trigger a refresh of a specific repository.
- [ ] Add progress bars/percentage reporting to the CLI indexer.
