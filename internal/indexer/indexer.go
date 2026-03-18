package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	gitignore "github.com/sabhiram/go-gitignore"

	"github.com/ramayac/omni-code/internal/db"
)

// Hardcoded skip lists per plan specification.
var skipDirNames = map[string]bool{
	".git": true, "node_modules": true, "dist": true, "build": true,
	"vendor": true, ".next": true, "__pycache__": true, ".venv": true, ".tox": true,
}

var skipExtensions = map[string]bool{
	".pdf": true, ".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".svg": true, ".mp4": true, ".mp3": true, ".zip": true, ".tar": true,
	".gz": true, ".exe": true, ".dll": true, ".so": true, ".dylib": true,
	".wasm": true, ".bin": true, ".dat": true, ".db": true, ".sqlite": true,
}

var skipFilenames = map[string]bool{
	".env": true, "package-lock.json": true, "yarn.lock": true,
	"go.sum": true, ".DS_Store": true, "Thumbs.db": true,
}

// ShouldSkipDir reports whether a directory name should be skipped entirely during walking.
func ShouldSkipDir(name string) bool {
	return skipDirNames[name]
}

// shouldSkipFile reports whether a file should be skipped based on its name, extension, or
// .gitignore rules. gi may be nil if no .gitignore exists at the repo root.
func shouldSkipFile(path string, repoRoot string, gi *gitignore.GitIgnore) bool {
	base := filepath.Base(path)
	if skipFilenames[base] {
		return true
	}
	if skipExtensions[strings.ToLower(filepath.Ext(base))] {
		return true
	}
	if gi != nil {
		rel, err := filepath.Rel(repoRoot, path)
		if err == nil && gi.MatchesPath(rel) {
			return true
		}
	}
	return false
}

// DetectLanguage returns a canonical language name from a file's extension.
func DetectLanguage(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx":
		return "javascript"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".c":
		return "c"
	case ".cpp", ".cc", ".cxx":
		return "cpp"
	case ".rb":
		return "ruby"
	case ".md", ".markdown":
		return "markdown"
	default:
		return "text"
	}
}

// ChunkFunc is the signature used to chunk a file's content into indexable segments.
// The chunker package (Phase 3) satisfies this signature.
type ChunkFunc func(repo, path, content, lang string) ([]db.Chunk, error)

// IndexerConfig holds all parameters for a single indexing run.
type IndexerConfig struct {
	RootPath string // Absolute path to the repository root.
	RepoName string // Logical repository name stored in ChromaDB.
	DB       *db.ChromaClient
	ChunkFn  ChunkFunc
	// SeenHashes enables global deduplication across multiple RunIndex calls.
	// Pass nil to create a fresh map scoped to this run only.
	SeenHashes *sync.Map
}

// IndexStats reports the outcome of a RunIndex call.
type IndexStats struct {
	FilesScanned   int
	FilesChanged   int
	FilesUnchanged int
	FilesDedupSkip int
	ChunksUpserted int
	Errors         int
}

// hashCache avoids reading the same file twice within a single run.
// Cache key format: "<size>:<mtime>:<absolutePath>".
type hashCache struct {
	mu    sync.Mutex
	cache map[string]string
}

func newHashCache() *hashCache {
	return &hashCache{cache: make(map[string]string)}
}

func (h *hashCache) get(key string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.cache[key]
	return v, ok
}

func (h *hashCache) set(key, val string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cache[key] = val
}

// HashFile computes the SHA-256 digest of a file, using a mtime+size cache key to skip
// redundant disk reads when the same file is encountered more than once in a run.
func HashFile(path string, size, mtime int64, cache *hashCache) (string, error) {
	cacheKey := fmt.Sprintf("%d:%d:%s", size, mtime, path)
	if h, ok := cache.get(cacheKey); ok {
		return h, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open file for hashing: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash file: %w", err)
	}
	hash := hex.EncodeToString(h.Sum(nil))
	cache.set(cacheKey, hash)
	return hash, nil
}

// HasChanged determines whether a file has changed since it was last indexed.
// Returns (changed bool, currentHash string, error).
//
// The Size → MTime → Hash cascade is sacred — do not reorder.
// Steps:
//  1. SIZE   — fast O(1); if different, the file changed.
//  2. MTIME  — fast O(1); if different, compute hash to confirm.
//  3. Both match — trust the file is unchanged (no hash needed).
func HasChanged(ctx context.Context, client *db.ChromaClient, repo, filePath string, info os.FileInfo, cache *hashCache) (bool, string, error) {
	size := info.Size()
	mtime := info.ModTime().Unix()

	meta, err := client.GetFileMeta(ctx, repo, filePath)
	if err != nil {
		return false, "", fmt.Errorf("get file meta: %w", err)
	}

	// Never indexed before — definitely changed.
	if meta == nil {
		hash, err := HashFile(filePath, size, mtime, cache)
		if err != nil {
			return false, "", err
		}
		return true, hash, nil
	}

	// 1. SIZE check.
	if size != meta.Size {
		hash, err := HashFile(filePath, size, mtime, cache)
		if err != nil {
			return false, "", err
		}
		return true, hash, nil
	}

	// 2. MTIME check — sizes match; verify content via hash.
	if mtime != meta.MTime {
		hash, err := HashFile(filePath, size, mtime, cache)
		if err != nil {
			return false, "", err
		}
		if hash != meta.Hash {
			return true, hash, nil
		}
		return false, hash, nil
	}

	// 3. Size and mtime both match — trust the file is unchanged.
	return false, meta.Hash, nil
}

const indexWorkers = 8

// processFile runs the full detect → deduplicate → chunk → store pipeline for one file.
// It returns a per-file IndexStats which the caller merges into the aggregate.
func processFile(ctx context.Context, cfg IndexerConfig, path string, info os.FileInfo,
	cache *hashCache, seenHashes *sync.Map) (ls IndexStats) {

	changed, hash, err := HasChanged(ctx, cfg.DB, cfg.RepoName, path, info, cache)
	if err != nil {
		log.Printf("[indexer] change check %s: %v", path, err)
		ls.Errors++
		return
	}

	if !changed {
		ls.FilesUnchanged++
		return
	}

	// Global deduplication — skip if another repo already indexed identical content.
	if _, loaded := seenHashes.LoadOrStore(hash, true); loaded {
		ls.FilesDedupSkip++
		return
	}

	ls.FilesChanged++

	content, err := os.ReadFile(path)
	if err != nil {
		log.Printf("[indexer] read %s: %v", path, err)
		ls.Errors++
		return
	}

	lang := DetectLanguage(path)
	chunks, err := cfg.ChunkFn(cfg.RepoName, path, string(content), lang)
	if err != nil {
		log.Printf("[indexer] chunk %s: %v", path, err)
		ls.Errors++
		return
	}

	if err := cfg.DB.DeleteFileChunks(ctx, cfg.RepoName, path); err != nil {
		log.Printf("[indexer] delete old chunks %s: %v", path, err)
		ls.Errors++
		return
	}
	if len(chunks) > 0 {
		if err := cfg.DB.UpsertChunks(ctx, chunks); err != nil {
			log.Printf("[indexer] upsert chunks %s: %v", path, err)
			ls.Errors++
			return
		}
	}
	if err := cfg.DB.UpsertFileMeta(ctx, cfg.RepoName, path, info.Size(), info.ModTime().Unix(), hash); err != nil {
		log.Printf("[indexer] upsert meta %s: %v", path, err)
		ls.Errors++
		return
	}

	ls.ChunksUpserted = len(chunks)
	return
}

// RunIndex walks the repository at cfg.RootPath, detects changed files, chunks them, and stores
// them in ChromaDB. Worker goroutines process files concurrently via a buffered channel.
// Returns aggregate IndexStats for the entire run.
func RunIndex(ctx context.Context, cfg IndexerConfig) (*IndexStats, error) {
	stats := &IndexStats{}
	cache := newHashCache()

	seenHashes := cfg.SeenHashes
	if seenHashes == nil {
		seenHashes = &sync.Map{}
	}

	// Load .gitignore from the repo root if present.
	var gi *gitignore.GitIgnore
	giPath := filepath.Join(cfg.RootPath, ".gitignore")
	if _, err := os.Stat(giPath); err == nil {
		if compiled, err := gitignore.CompileIgnoreFile(giPath); err == nil {
			gi = compiled
		} else {
			log.Printf("[indexer] could not parse .gitignore at %s: %v", giPath, err)
		}
	}

	type workItem struct {
		path string
		info os.FileInfo
	}
	workCh := make(chan workItem, indexWorkers*4)

	var wg sync.WaitGroup
	var mu sync.Mutex

	for i := 0; i < indexWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range workCh {
				ls := processFile(ctx, cfg, item.path, item.info, cache, seenHashes)
				mu.Lock()
				stats.FilesScanned++
				stats.FilesChanged += ls.FilesChanged
				stats.FilesUnchanged += ls.FilesUnchanged
				stats.FilesDedupSkip += ls.FilesDedupSkip
				stats.ChunksUpserted += ls.ChunksUpserted
				stats.Errors += ls.Errors
				mu.Unlock()
			}
		}()
	}

	walkErr := filepath.WalkDir(cfg.RootPath, func(path string, d os.DirEntry, walkEntryErr error) error {
		if walkEntryErr != nil {
			log.Printf("[indexer] walk error at %s: %v", path, walkEntryErr)
			mu.Lock()
			stats.Errors++
			mu.Unlock()
			return nil // skip unreadable entry, continue walk
		}
		if d.IsDir() {
			if ShouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if shouldSkipFile(path, cfg.RootPath, gi) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			log.Printf("[indexer] stat %s: %v", path, err)
			mu.Lock()
			stats.Errors++
			mu.Unlock()
			return nil
		}
		workCh <- workItem{path: path, info: info}
		return nil
	})

	close(workCh)
	wg.Wait()

	if walkErr != nil {
		return stats, fmt.Errorf("directory walk failed: %w", walkErr)
	}
	return stats, nil
}
