# Todo 001 — Pending Items

This file lists all pending work items derived from `plan000.md` and `plan001.md` that have **not yet been implemented**. Items are grouped by theme and ordered by estimated value/complexity.

---

## High Priority

### HT-1 — E2E Smoke Test (plan000 Phase 1)

Run a full end-to-end sequence to validate the system works from a fresh clone:

```
make docker-db          # start ChromaDB
make dev                # index test-data
./bin/omni-code search --query "how does change detection work"
./bin/omni-code mcp --transport sse --addr :8090 &
# Connect VS Code + Copilot, trigger a real query
```

- [ ] Verify VS Code discovers and uses the `search_codebase` tool
- [ ] Trigger explicit Copilot query: "How does the indexer detect file changes?"
- [ ] Connect at least 2 real local repos and document query success

---

### HT-2 — fsnotify Watch Mode Refinement (plan000 Phase 3)

Replace or augment the polling loop in `watch` mode with filesystem event hooks:

- [ ] Integrate `github.com/fsnotify/fsnotify` to watch `.git/refs/heads/` for branch HEAD changes
- [ ] Retain 5-minute polling as fallback for remote-only changes (e.g. `git pull`)
- [ ] Add 5-second debounce to prevent rapid-save loops from triggering repeated indexing
- [ ] Wire `auto_pull` / `FetchAndFastForward` (already stubbed in config) into the watch hook
- [ ] Apply `estimator` complexity check before deciding whether to re-index watched repos

---

## Medium Priority

### MT-1 — Batch Flush for Indexer (plan000 Phase 2) ✅ already implemented

The indexer already buffers upserts (`fileMetaFlushSize=50`, `chunkFlushSize=500`, `flushLocked()`). No further work needed.

---

### MT-2 — Progress Reporting during Indexing (plan000 Phase 2) ✅

- [x] Emit progress lines to stderr every 5 s: `[indexer] <repo>: 42/210 files (20%)` in git-list mode or `[indexer] <repo>: N files scanned` in WalkDir mode
- [ ] Expose a structured progress event that watch mode can surface in logs

---

### MT-3 — `search_repo_summaries` MCP Tool (plan000 Phase 4) ✅

- [x] Implement `search_repo_summaries` tool — returns a compact Markdown card (branch, commit, file/chunk counts, top-3 languages) for every indexed repo

---

## Lower Priority

### LP-1 — LLM-backed Structural Summaries (plan000 Phase 4)

Extend `get_repo_summary` to optionally call an LLM (Ollama / OpenAI) to generate a prose description of the repo's architecture:

- [ ] Build `BuildSeedDocument` — gather top-level README + directory tree + package names into a ≤800-token context
- [ ] Wire `LLMClient` interface (Ollama `/api/generate` or OpenAI chat) to summarize the seed document
- [ ] Cache the generated summary in ChromaDB repo-meta so subsequent calls are instant
- [ ] Expose via `get_repo_summary?include_llm_summary=true` parameter

---

### LP-2 — `generate_agents_md` MCP Tool

New tool to produce a tailored `AGENTS.md` file for any indexed repository:

- [ ] Detect language(s) and top-level package structure from ChromaDB file-meta
- [ ] Identify key architectural packages (highest chunk density dirs)
- [ ] Generate AGENTS.md following omni-code conventions (core principles, layout table, common tasks)
- [ ] Output as Markdown text content so the AI can write it to disk

---

### LP-3 — `get_top_contributors` MCP Tool ✅

- [x] Run `git shortlog -sn --no-merges HEAD` via `git.RunGit`
- [x] Parse and return formatted Markdown table (Rank | Author | Commits)
- [x] Support optional `since` parameter to scope to a recent time window

---

### LP-4 — Verify Graceful Drain on Ctrl-C (plan001 Phase 4)

The `signal.NotifyContext` wiring is in place. This item is a manual verification task:

- [ ] Start `omni-code mcp --transport sse --addr :8090`
- [ ] Open a long-lived SSE connection from a client
- [ ] Send `Ctrl-C` and confirm the process exits cleanly (no `bind: address already in use` on restart)
