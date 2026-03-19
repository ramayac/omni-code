# Project Build Plan: Omni-Code Phase 03 — Scan Complexity Estimation & Sorted Scheduling

## TL;DR

Before indexing begins, compute a lightweight complexity estimate for each repository and sort the scan queue so the smallest repos are indexed first. This gives faster feedback for simple repos, improves parallelism scheduling, and lays groundwork for progress reporting.

**Prerequisite**: plan01.md phases must be complete — specifically multi-repo config (`--config`) and `RunIndex`.

---

## 1. Goals

- Produce a cheap `ScanEstimate` for any repo without touching ChromaDB or the embedder
- Sort multi-repo index runs by estimated cost (ascending) so lightweight repos finish first
- Surface estimates in the CLI before scanning starts (optional `--dry-run` flag)
- Keep estimation logic fully reusable for watch mode scheduling

---

## 2. What Drives Scan Cost

| Factor | Phase where it matters | Cheap proxy |
|--------|----------------------|-------------|
| **Indexable file count** | Every run — stat + hash per file | `git ls-files \| wc -l` minus skip-listed extensions |
| **Total indexable bytes** | Full scan — I/O + chunking | `git ls-files` → `stat` each file |
| **Changed file count** | Incremental run — chunk + embed calls | `git diff --name-only <last> HEAD \| wc -l` |
| **Changed bytes** | Dominant cost — embedding calls | sum of sizes of changed files |

The embedding call per chunk is the single slowest operation. Changed bytes is therefore the best predictor for incremental runs; total indexable bytes for fresh/full scans.

---

## 3. New Package: `internal/estimator`

### 3.1 `ScanEstimate` struct

```go
// ScanEstimate holds pre-scan complexity metrics for a single repository.
type ScanEstimate struct {
    RepoName      string
    RootPath      string
    // Full-scan proxies
    TotalFiles    int
    TotalBytes    int64
    // Incremental proxies (-1 when no previous commit is known)
    ChangedFiles  int
    ChangedBytes  int64
    // Derived score used for sorting (lower = cheaper)
    Score         int64
}
```

`Score` is computed as:

$$\text{Score} = \begin{cases} \text{ChangedBytes} & \text{if ChangedFiles} \geq 0 \\ \text{TotalBytes} & \text{otherwise} \end{cases}$$

### 3.2 `Estimate` function

```go
// Estimate computes a ScanEstimate for a repo without touching ChromaDB.
// lastCommit is the SHA previously indexed; empty string forces full-scan estimate.
// skipExts and skipNames are the resolved skip maps for the repo.
func Estimate(
    rootPath, repoName, lastCommit string,
    skipExts, skipNames map[string]bool,
) (*ScanEstimate, error)
```

**Algorithm:**

1. Run `git ls-files` via `git.ListFiles`.
2. Filter the list through `skipExts` and `skipNames` (same logic as `shouldSkipFile` in `indexer`).
3. `stat` each remaining file — accumulate `TotalFiles` and `TotalBytes`.
4. If `lastCommit` is non-empty, run `git.DiffFiles(root, lastCommit, "HEAD")` to obtain the changed set. Intersect with the filtered list, `stat` changed files → `ChangedFiles`, `ChangedBytes`.
5. Compute `Score`.

Estimation must not read file contents — only `stat` calls are allowed.

### 3.3 `SortByScore`

```go
// SortByScore sorts estimates in-place, cheapest first.
func SortByScore(estimates []*ScanEstimate)
```

---

## 4. Integration Points

### 4.1 `cmd/omni-code/main.go` — `runIndex`

**Before the existing `runOne` loop**, when `--config` is set:

1. For each repo in `cfg.Repos` (filtered by `--repo` if set), call `estimator.Estimate`, passing:
   - the resolved `skipExts` / `skipNames` for that repo
   - the last known commit from ChromaDB (`client.GetRepoMeta` → `LastCommit`); empty string if not yet indexed
2. Call `estimator.SortByScore` on the result slice.
3. Reorder the repo list to match the sorted estimates.
4. Log each estimate (file count, bytes, score) at the `[index]` prefix.

The sort is a no-op when `--parallel` is set (all goroutines start at once anyway) but the estimates are still logged.

### 4.2 `--dry-run` flag for `index`

Add `--dry-run` to `runIndex`:

- Runs estimates for all repos.
- Prints a sorted table (repo name, total files, total bytes, changed files, changed bytes, score).
- Exits without indexing.

Example output:
```
[index] dry-run scan estimates (sorted by score, cheapest first):
REPO             FILES   TOTAL     CHANGED  CHANGED BYTES  SCORE
auth-service       312   1.2 MB         4         18 KB    18 KB
api-gateway        870   4.1 MB        22        340 KB   340 KB
frontend          2104  18.3 MB       --         --       18.3 MB  (full scan)
```

### 4.3 Watch mode (`cmd/omni-code/watch_test.go` + watch loop)

In the watch poll loop, before firing `RunIndex` for a batch of changed repos, call `Estimate` for each and sort. This ensures that when multiple repos change simultaneously, the cheapest re-index completes first and results are available to Copilot sooner.

---

## 5. Package Layout

```
internal/
  estimator/
    estimator.go        ← Estimate, SortByScore, ScanEstimate
    estimator_test.go   ← unit tests using test-data/ repos
```

No new external dependencies — uses only `internal/git` and stdlib `os.Stat`.

---

## 6. Testing Guidelines

- Use a temporary git repo (init + add files + commit) in `estimator_test.go` to test `TotalFiles` and `TotalBytes`.
- Create a second commit with one modified file to test `ChangedFiles` / `ChangedBytes`.
- Assert that `SortByScore` orders three estimates with known scores correctly.
- Test skip-list filtering: a `.png` file should not contribute to `TotalFiles` or `TotalBytes`.

---

## 7. Implementation Checklist

- [ ] `internal/estimator/estimator.go` — `ScanEstimate`, `Estimate`, `SortByScore`
- [ ] `internal/estimator/estimator_test.go` — unit tests
- [ ] `cmd/omni-code/main.go` — estimate + sort before `runOne` loop
- [ ] `cmd/omni-code/main.go` — `--dry-run` flag
- [ ] Watch loop — sort changed repos by estimate before re-indexing
- [ ] `AGENTS.md` — add `internal/estimator/` row to the package table
