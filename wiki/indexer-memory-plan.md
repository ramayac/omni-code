# Indexer Memory Optimization Plan

## Problem
The current indexer reads the entire content of every file into memory using `os.ReadFile` and returns a complete slice of `[]db.Chunk` for the file. For very large files, or when multiple large files are processed by the 8 concurrent workers, this causes severe memory bloat (up to 20+ GB RAM), ultimately killing the process or the machine. 

## Solution
We will re-architect the file processing pipeline to be **stream-based** for both file reading and chunk emitting. We will also introduce configurable memory limits.

### 1. Streaming Chunk Emitting
Currently, `processFile` returns `([]db.Chunk, *db.FileMeta)`. This means all chunks for a file are held in memory simultaneously.
- **Change**: Pass a callback `emit func(db.Chunk) error` to the chunker and `processFile`.
- **Benefit**: Chunks can be flushed to the database continuously in batches of 500 without ever holding the full file's chunks in memory.

### 2. Sequential Reads for Large Files
Currently, `ChunkFunc` takes `content string`. 
- **Change**: Modify `ChunkFunc` to take an `io.Reader` instead. 
- **Logic**: 
  - For files smaller than a threshold (e.g. 1 MB), we can still read fully to use `tree-sitter` for high-quality AST chunking.
  - For files larger than the threshold, we will fall back to **sequential line-by-line reading** (`chunker.chunkSequential`), never loading the whole file into memory.
  - This strictly caps the RAM used per file to a tiny buffer size regardless of whether the file is 10 MB or 10 GB.

### 3. Configurable Limits
- Add `MaxFileSizeBytes` to `config.Config` and `repos.yaml` (default: 10 GB). If a file exceeds this size, it is skipped entirely to prevent indefinite CPU hanging.
- Optionally add a `runtime/debug.SetMemoryLimit` call if a `MemoryLimit` config is set, though sequential reads will primarily solve the root cause.

## Implementation Steps
1. **Config Update**: 
   - Add `MaxFileSizeBytes` to `internal/config/config.go` with a default of 10 GB (10 * 1024 * 1024 * 1024).
2. **Chunker Update (`internal/chunker`)**:
   - Change `ChunkFunc` signature: `type ChunkFunc func(repo, path string, r io.Reader, size int64, lang string, emit func(db.Chunk)) error`.
   - Implement `chunkSequential(repo, path string, r io.Reader, lang string, emit func(db.Chunk))` that uses `bufio.Scanner` to read sequentially and emit word-bounded or line-bounded chunks with overlap, identical to the logic in `chunkByLines`.
   - Update `ChunkFile` to use `io.ReadAll` only if `size < 1MB`, otherwise route to `chunkSequential`.
3. **Indexer Update (`internal/indexer`)**:
   - In `processFile`, replace `os.ReadFile` with `os.Open`.
   - Check `info.Size() > MaxFileSizeBytes` and skip if too large.
   - Pass the `io.Reader` to `cfg.ChunkFn`.
   - Implement the `emit` callback in the worker pool to append to `chunkBuffer` and lock-flush when full.
4. **Unit Tests**:
   - Create a test in `indexer_test.go` or `chunker_test.go` that simulates a large file limit (e.g. 10 MB) and verifies it streams properly without loading it all into memory.

## Expected Outcome
Memory usage will become strictly proportional to `worker_count * buffer_size`, flattening the memory curve entirely regardless of file size, allowing even huge log or JSON files to be indexed safely.
