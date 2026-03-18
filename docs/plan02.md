# Project Build Plan: Omni-Code Phase 02 — Repo Architecture Summaries

## TL;DR

Generate and store a natural-language architecture summary for each indexed repository. Summaries are produced by an LLM (Ollama locally, OpenAI, or any OpenAI-compatible endpoint) or via a no-LLM structural fallback. Summaries are stored in ChromaDB and served as an MCP tool so Copilot can answer "what does this repo do?" questions instantly.

**Prerequisite**: plan01.md Phase 4 (Repo Metadata Collection) must be complete — specifically `RepoMeta` and the `repos` ChromaDB collection.

---

## 1. Goals

- Any indexed repo can have a human-readable architecture summary generated on demand or automatically post-index
- Summaries are cached in ChromaDB and never regenerated unless explicitly requested or the repo has changed significantly
- The feature works with **zero API keys** via the structural fallback (file tree + language stats)
- When an LLM is configured, the summary is genuinely useful: purpose, main components, entry points, key dependencies
- Copilot can retrieve the summary via a new `get_repo_summary` MCP tool

---

## 2. Summary Content Spec

A good repo summary contains:

| Section | Content | Source |
|---------|---------|--------|
| **Purpose** | 1–2 sentence description of what the repo does | LLM (from README + manifest) |
| **Languages** | Primary languages and their approximate % | Structural (file extension counts) |
| **Frameworks / Dependencies** | Key third-party libraries/frameworks | Manifest files (go.mod, package.json, etc.) |
| **Entry Points** | Files where execution starts (main.go, index.ts, app.py, etc.) | Structural (pattern matching) |
| **Key Components** | Top-level packages/directories with one-line descriptions | LLM (from directory structure + file names) |
| **Architecture Notes** | Any notable patterns: MCP server, CLI tool, REST API, library, etc. | LLM |

The no-LLM fallback produces all sections except "Purpose" and "Architecture Notes" (those require understanding, not just counting).

---

## 3. Seed Content Collection

Before calling an LLM, collect a compact "seed" document for the repo. Target: < 2000 tokens total.

**Seed document structure:**

```
=== REPO: <name> ===
ROOT: <path>

--- Directory Tree (depth 3, dirs only) ---
<tree output>

--- Dependency Manifest ---
<content of go.mod / package.json / pyproject.toml / Cargo.toml / pom.xml / etc.>
(truncated to 500 tokens if large)

--- README (first 800 tokens) ---
<content of README.md / README.rst / etc.>
(truncated if longer)
```

If no README exists, skip that section. If no manifest exists, skip that section. The directory tree is always present.

---

## 4. LLM Prompt Design

**System prompt:**

```
You are a software architecture analyst. Given a description of a code repository, produce a structured summary. Be concise and factual. Do not invent details not present in the provided content.
```

**User prompt:**

```
Analyze this repository and return a JSON object with the following fields:
- "purpose": string — one or two sentences describing what this repo does
- "languages": array of strings — primary programming languages used
- "frameworks": array of strings — key frameworks or libraries
- "entry_points": array of strings — main executable or entry point files (relative paths)
- "components": array of objects with "name" (package/dir name) and "description" (one sentence)
- "architecture_notes": string — notable patterns, e.g. "CLI tool", "MCP server", "REST API", "library"

Return ONLY valid JSON. No markdown fences. No explanation.

Repository content:
<seed document>
```

**Parsing:**
- Expect JSON; if parsing fails, retry once with a "fix the JSON" follow-up
- If second attempt fails, fall back to structural summary and log a warning
- Strip any markdown fences (` ```json `) before parsing

---

## 5. LLM Backend Configuration

**Supported backends (same as plan01 Phase 7 embedding backends, but for generation not embedding):**

| Backend | Config value | Default model | Notes |
|---------|-------------|---------------|-------|
| None (structural only) | `none` | — | Always works; no API call |
| Ollama | `ollama` | `llama3.2` | Local; recommended default |
| OpenAI | `openai` | `gpt-4o-mini` | Requires `OPENAI_API_KEY` |
| OpenAI-compatible | `openai-compatible` | configurable | `LLM_API_URL` + `LLM_API_KEY` env vars |

**Config fields (in `repos.yaml` top level):**

```yaml
llm_backend: ollama             # none | ollama | openai | openai-compatible
llm_model: llama3.2             # model name; default varies by backend
llm_url: http://localhost:11434 # for ollama or openai-compatible
```

**Environment variables (for API keys — never in config file):**

```
OPENAI_API_KEY       # for openai backend
LLM_API_KEY          # for openai-compatible backend
LLM_API_URL          # base URL for openai-compatible backend
```

---

## 6. New Package: `internal/summarizer/`

```
internal/summarizer/
├── summarizer.go      # orchestration: seed collection → LLM call → storage
├── seed.go            # BuildSeedDocument(root string) (string, error)
├── structural.go      # BuildStructuralSummary(root string) (*Summary, error)
├── llm.go             # LLMClient interface + backends
├── ollama.go          # OllamaClient implementation
├── openai.go          # OpenAIClient implementation (handles openai + openai-compatible)
└── summarizer_test.go
```

**Core types:**

```go
type Summary struct {
    Repo              string      `json:"repo"`
    Purpose           string      `json:"purpose"`
    Languages         []string    `json:"languages"`
    Frameworks        []string    `json:"frameworks"`
    EntryPoints       []string    `json:"entry_points"`
    Components        []Component `json:"components"`
    ArchitectureNotes string      `json:"architecture_notes"`
    GeneratedAt       time.Time   `json:"generated_at"`
    LLMBackend        string      `json:"llm_backend"`
    LLMModel          string      `json:"llm_model"`
}

type Component struct {
    Name        string `json:"name"`
    Description string `json:"description"`
}

type LLMClient interface {
    Generate(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}
```

---

## 7. ChromaDB Storage

Store summaries in the existing `repos` collection (from plan01 Phase 4) as an additional field, OR in a dedicated `summaries` collection. **Use a dedicated `summaries` collection** to keep the repos metadata lean and allow semantic search over summary content.

**Collection `summaries`:**

| Field | Type | Description |
|-------|------|-------------|
| document ID | string | `<repo>_summary` |
| content | string | Full formatted summary text (for vector search) |
| `repo` | metadata | Repo name |
| `generated_at` | metadata | RFC3339 timestamp |
| `llm_backend` | metadata | Which backend generated it |
| `llm_model` | metadata | Which model |
| `summary_json` | metadata | Full JSON-encoded `Summary` struct |

Storing the summary as a searchable document in ChromaDB means Copilot can semantically search across all summaries (e.g. "which repo handles authentication?").

**New `db` methods:**

```go
UpsertSummary(ctx context.Context, s Summary) error
GetSummary(ctx context.Context, repo string) (*Summary, error)
DeleteSummary(ctx context.Context, repo string) error
SearchSummaries(ctx context.Context, query string, n int) ([]Summary, error)
```

---

## 8. New CLI Subcommand: `omni-code summarize`

```
omni-code summarize --repo <name> [--config repos.yaml] [--backend ollama] [--model llama3.2] [--force]
omni-code summarize --all [--config repos.yaml]
```

| Flag | Description |
|------|-------------|
| `--repo` | Summarize a single repo by name |
| `--all` | Summarize all repos in config (skips repos with fresh summaries unless `--force`) |
| `--config` | Path to `repos.yaml` (default: `repos.yaml`) |
| `--backend` | LLM backend override (`none`, `ollama`, `openai`, `openai-compatible`) |
| `--model` | Model name override |
| `--force` | Regenerate even if a summary already exists |

**Output (stdout):**

```
=== omni-code ===
Purpose: An MCP server and CLI tool for indexing local Git repositories into
         ChromaDB and exposing semantic search to GitHub Copilot.
Languages: Go
Frameworks: ChromaDB, Tree-sitter, MCP go-sdk
Entry Points: cmd/omni-code/main.go
Components:
  - internal/indexer  File walker, change detection, deduplication
  - internal/chunker  Tree-sitter and line-based code chunking
  - internal/db       ChromaDB client wrapper
  - internal/mcp      MCP stdio server with tool registration
Architecture Notes: CLI tool and MCP stdio server
Generated: 2026-03-17T14:32:00Z (ollama / llama3.2)
```

---

## 9. New MCP Tools

**`get_repo_summary`** — retrieve a stored summary for a specific repo:

```json
{
  "name": "get_repo_summary",
  "description": "Return the architecture summary for a specific indexed repository",
  "parameters": {
    "repo": { "type": "string", "description": "Repository name", "required": true }
  }
}
```

Response: formatted markdown with all summary sections.

**`search_repo_summaries`** — semantic search across all summaries:

```json
{
  "name": "search_repo_summaries",
  "description": "Find indexed repositories matching a description (e.g. 'which repo handles OAuth?')",
  "parameters": {
    "query": { "type": "string", "description": "Natural language description to search for", "required": true },
    "n_results": { "type": "integer", "description": "Number of repos to return (default 5)" }
  }
}
```

Response: list of matching repos with their purpose and architecture notes.

---

## 10. Makefile Targets

Add to the existing Makefile:

| Target | What it does |
|--------|-------------|
| `summarize` | `omni-code summarize --all --config repos.yaml` |
| `summarize-repo` | `omni-code summarize --repo $(REPO) --config repos.yaml`; error if `REPO` not set |

---

## 11. Auto-Summary on Index

Add an optional `auto_summarize: true` field in `repos.yaml`. When set, `RunIndex` calls the summarizer after a successful index if:
- No summary exists yet, OR
- The last indexed commit differs from when the summary was last generated (significant change)

This makes the workflow fully automatic: `make reindex` → indexes + updates summaries.

---

## 12. Task Checklist

### Seed Collection
- [ ] Implement `BuildSeedDocument(root string) (string, error)` in `seed.go`
- [ ] Detect and read README (`.md`, `.rst`, `.txt` variants); truncate to 800 tokens
- [ ] Detect and read dependency manifest (`go.mod`, `package.json`, `pyproject.toml`, `Cargo.toml`, `pom.xml`, `*.gemspec`); truncate to 500 tokens
- [ ] Generate directory tree (depth 3, dirs only) using `filepath.WalkDir` with depth counter
- [ ] Assemble seed document string

### Structural Fallback
- [ ] Implement `BuildStructuralSummary(root string) (*Summary, error)` in `structural.go`
- [ ] Count files by extension to determine primary languages
- [ ] Detect entry points by filename patterns (`main.go`, `index.*`, `app.*`, `server.*`, `cmd/*/main.go`)
- [ ] List top-level directories as components (name only, no LLM description)
- [ ] Parse dependency manifest for frameworks list (go.mod `require` block, package.json `dependencies`, etc.)

### LLM Clients
- [ ] Define `LLMClient` interface in `llm.go`
- [ ] Implement `NewLLMClient(backend, model, url, apiKey string) (LLMClient, error)` factory
- [ ] Implement `OllamaClient.Generate` — POST to `/api/generate` with `stream: false`
- [ ] Implement `OpenAIClient.Generate` — POST to `/v1/chat/completions` (handles both openai and openai-compatible)
- [ ] Handle HTTP errors, timeouts (30s default), and JSON parse failures
- [ ] Implement JSON extraction: strip markdown fences, attempt parse, retry once on failure

### Summarizer Orchestration
- [ ] Implement `Summarize(ctx, repo string, root string, client LLMClient) (*Summary, error)` in `summarizer.go`
- [ ] Call `BuildSeedDocument`; if LLM client is nil or `--backend none`, call `BuildStructuralSummary` directly
- [ ] Build system + user prompts; call `client.Generate`
- [ ] Parse LLM JSON response into `Summary` struct
- [ ] Merge structural fallback data (languages, entry points) into LLM result to catch anything the LLM missed
- [ ] Return complete `Summary`

### ChromaDB Integration
- [ ] Add `summaries` collection to `EnsureCollections`
- [ ] Implement `UpsertSummary`, `GetSummary`, `DeleteSummary`, `SearchSummaries` in `internal/db/chroma.go`
- [ ] Serialize `Summary` struct to JSON for `summary_json` metadata field
- [ ] Format summary as readable text for the document content (used for vector search)

### CLI
- [ ] Add `summarize` subcommand to `cmd/omni-code/main.go`
- [ ] Implement `--repo`, `--all`, `--config`, `--backend`, `--model`, `--force` flags
- [ ] Add `auto_summarize` field to `internal/config/Config` and `RepoEntry`
- [ ] Call summarizer at end of `RunIndex` when `auto_summarize` is enabled
- [ ] Add `summarize` and `summarize-repo` targets to Makefile

### MCP Tools
- [ ] Register `get_repo_summary` tool in `internal/mcp/server.go`
- [ ] Register `search_repo_summaries` tool
- [ ] Implement `get_repo_summary` handler: calls `db.GetSummary`, formats as markdown
- [ ] Implement `search_repo_summaries` handler: calls `db.SearchSummaries`, formats results
- [ ] Zero stdout writes in handlers — log to `os.Stderr` only

### Tests
- [ ] Test `BuildSeedDocument` with a real directory (use `test-data/`)
- [ ] Test `BuildStructuralSummary` with known file tree
- [ ] Test `OllamaClient.Generate` with a mock HTTP server
- [ ] Test `OpenAIClient.Generate` with a mock HTTP server (both OpenAI format and compatible)
- [ ] Test JSON extraction and retry logic (malformed JSON → retry → parsed)
- [ ] Test `Summarize` with `--backend none` (structural only path)
- [ ] Test `UpsertSummary` / `GetSummary` round-trip (skip if `CHROMA_URL` not set)
- [ ] Test `get_repo_summary` MCP handler (mock db)
- [ ] Test `search_repo_summaries` MCP handler (mock db)

---

## 13. Data Flow

```
omni-code summarize --repo omni-code
              │
              ▼
   ┌─────────────────────┐
   │  BuildSeedDocument   │  dir tree + README + manifest → <2000 tokens
   └──────────┬──────────┘
              │ seed text
              ▼
   ┌─────────────────────┐
   │  LLMClient.Generate  │  ollama / openai / openai-compatible
   │  (or structural      │  Prompt → JSON response
   │   fallback)          │
   └──────────┬──────────┘
              │ JSON string
              ▼
   ┌─────────────────────┐
   │  Parse + Merge       │  JSON → Summary struct
   │                      │  Merge structural fallback data
   └──────────┬──────────┘
              │ *Summary
              ▼
   ┌─────────────────────┐
   │  db.UpsertSummary    │  Store in ChromaDB `summaries` collection
   └──────────┬──────────┘
              │
              ▼
   Print formatted summary to stdout


Copilot → MCP: get_repo_summary("omni-code")
              │
              ▼
   ┌─────────────────────┐
   │  db.GetSummary       │  Fetch from `summaries` collection (no LLM call)
   └──────────┬──────────┘
              │ formatted markdown
              ▼
   Copilot displays repo architecture summary
```
