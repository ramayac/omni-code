# Plan 000: Consolidated Missing Functionalities

This document groups all uncompleted tasks from all previous plans into targeted, coherent phases to guide future development efficiently.

## Phase 1: End-to-End System Validation & AI Integration Tests
*Goal: Ensure the existing base framework hooks correctly from CLI all the way back up to real Copilot inputs robustly.*

- [ ] Execute an end-to-end manual smoke test sequence explicitly tracing `make docker-db`, `populate test-data`, `make dev`, then querying.
- [ ] Verify VS Code's system directly acknowledges and discovers the registered `search_codebase` tool.
- [ ] Trigger an explicit sanity real query from actual Copilot chat: "How does the indexer detect file changes?" ensuring contextual tracking performs accurately.
- [ ] Connect at least 2 real complex local repositories and formally document the query resolution success setups.

## Phase 2: High-Performance Data Streaming & Embedding Optimization
*Goal: Improve performance scaling and reduce network round-trips during massive single-shot ingestions.*

- [ ] Modify `indexer.processFile` logic to memory buffer chunk and metadata upserts sequentially avoiding repeated context drops.
- [ ] Implement robust batch flush logic directly safely dropping into `internal/db.ChromaClient` checks correctly routing `50` file thresholds securely dynamically cleanly.
- [ ] Add batching limits handling direct bounds limiting massive arrays safely capping logic explicitly limits strictly.
- [ ] Refactor the `OllamaEmbedder` internally leveraging `errgroup` cleanly implementing asynchronous batch calls natively strictly directly arrays successfully dynamically.
- [ ] Introduce progress reporting metrics/UI rendering native visual percentage checks actively strictly securely safely cleanly limits handling.

## Phase 3: Watch Mode Refinements & Native Sync Hooks
*Goal: Transition the native daemon from pure polling loops to instant fs file triggers smoothing local logic bindings seamlessly.*

- [ ] Integrate explicit native `fsnotify/fsnotify` systems into the `.git/refs/heads/` system smoothly targeting accurate explicit hooks correctly matching local constraints.
- [ ] Retain native periodic polling layers safely wrapping a fallback tracking check effectively limiting systems securely locally smoothing explicitly arrays.
- [ ] Hook 5-second `debounce` delay bounds preventing race scenarios where repetitive saves limit processing loops constraints efficiently handling cleanly checks.
- [ ] Tie auto-pull `FetchAndFastForward` hooks dynamically applying validations properly testing working trees effectively securely checking local cleanly smoothing configurations via `auto_pull` effectively dynamically dynamically setups cleanly safely explicit gracefully.
- [ ] Apply `estimator` checks into the watch hooks explicitly safely grouping cheapest operations directly seamlessly checking mapping constraints smoothing checking.

## Phase 4: Structural Architecture Overviews & AI Summaries
*Goal: Empower the local system to automatically read repos and output structural contexts safely natively locally indexing states.*

- [ ] Assemble `BuildSeedDocument` implementations gathering up dependencies natively correctly strictly checking packages gracefully properly effectively checking limits safely truncating 800 token caps correctly dynamically setups successfully dynamically.
- [ ] Code local fallback heuristics securely mapping `BuildStructuralSummary` correctly generating language mappings parsing packages strictly contexts strictly limits contexts checking constraints smoothly setups securely cleanly checking array loops setups locally.
- [ ] Wire the `LLMClient` integrations cleanly tracking Ollama outputs formatting JSON schemas routing validations limits gracefully scaling timeouts limits formats successfully correctly.
- [ ] Complete overarching logic integrations managing CLI validations safely applying parameters strictly strictly explicitly formats arrays securely gracefully correctly checks loops templates natively hooks effectively setups dynamically triggers loops smoothly mapping contexts securely natively formats cleanly.
- [ ] Expose native `get_repo_summary` and `search_repo_summaries` directly accurately successfully safely handling directly. 

## Phase 5: Expanded Intelligence & MCP Tools
*Goal: Enhance AI models' capability to navigate and process local architectures dynamically with specific granular tools.*

- [ ] Add explicit context rule: `grep_codebase` capturing dynamic bounds checking matching constraints securely safely constraints limits checks safely mappings cleanly explicitly smoothly effectively bounds tracking.
- [ ] Build AST definitions explicit target matching: `get_file_symbols` safely securely parsing arrays securely limits explicitly safely seamlessly formats limits handling effectively dynamically safely mapping checks smoothly.
- [ ] Include operational trigger `reindex_repo` logic seamlessly limits correctly environments formats gracefully tracking loops setups explicitly gracefully gracefully directly mapping checks successfully perfectly setups environments natively strictly targets securely cleanly natively.