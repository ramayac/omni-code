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
	"time"

	gitignore "github.com/sabhiram/go-gitignore"

	"github.com/ramayac/omni-code/internal/config"
	"github.com/ramayac/omni-code/internal/db"
	"github.com/ramayac/omni-code/internal/git"
)

// ShouldSkipDir reports whether a directory name should be skipped.
// Uses the built-in defaults; RunIndex uses cfg.SkipDirs for per-run overrides.
func ShouldSkipDir(name string) bool {
	return config.DefaultSkipDirsMap[name]
}

// shouldSkipFile reports whether a file should be skipped based on its name,
// extension, or .gitignore rules. gi may be nil.
func shouldSkipFile(path string, repoRoot string, skipExts, skipNames map[string]bool, gi *gitignore.GitIgnore) bool {
	base := filepath.Base(path)
	if skipNames[base] {
		return true
	}
	if skipExts[strings.ToLower(filepath.Ext(base))] {
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

// DetectLanguage returns a canonical language name from a file extension.
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
type ChunkFunc func(repo, path, content, lang string) ([]db.Chunk, error)

// IndexerConfig holds all parameters for a single indexing run.
type IndexerConfig struct {
	RootPath string
	RepoName string
	DB       *db.ChromaClient
	ChunkFn  ChunkFunc
	// SeenHashes enables global deduplication across multiple RunIndex calls.
	SeenHashes *sync.Map
	// Resolved skip lists. If nil, config package defaults are used.
	SkipDirs       map[string]bool
	SkipExtensions map[string]bool
	SkipFilenames  map[string]bool
	// Branch is the expected branch; empty = auto-detect.
	Branch          string
	StrictBranch    bool
	SkipBranchCheck bool
	// SkipIfWrongBranch stops scanning if the current branch doesn't match Branch.
	SkipIfWrongBranch bool
}

// IndexStats reports the outcome of a RunIndex call.
type IndexStats struct {
	FilesScanned   int
	FilesChanged   int
	FilesUnchanged int
	FilesDedupSkip int
	DeletedFiles   int
	ChunksUpserted int
	Errors         int
	Branch         string
	LastCommit     string
	IndexMode      string // "full" or "incremental"
}

// hashCache avoids reading the same file twice within a single run.
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
	return h.cache[key], h.cache[key] != ""
}

func (h *hashCache) set(key, val string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cache[key] = val
}

// HashFile computes SHA-256 of a file, caching by size+mtime.
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

// HasChanged determines whether a file has changed since last index.
// Size → MTime → Hash cascade is sacred — do not reorder.
func HasChanged(ctx context.Context, client *db.ChromaClient, repo, filePath string, info os.FileInfo, cache *hashCache) (bool, string, error) {
	size := info.Size()
	mtime := info.ModTime().Unix()

	meta, err := client.GetFileMeta(ctx, repo, filePath)
	if err != nil {
		return false, "", fmt.Errorf("get file meta: %w", err)
	}
	if meta == nil {
		hash, err := HashFile(filePath, size, mtime, cache)
		if err != nil {
			return false, "", err
		}
		return true, hash, nil
	}
	if size != meta.Size {
		hash, err := HashFile(filePath, size, mtime, cache)
		if err != nil {
			return false, "", err
		}
		return true, hash, nil
	}
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
	return false, meta.Hash, nil
}

const indexWorkers = 8

// processFile runs detect → deduplicate → chunk → store for one file.
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

// RunIndex walks/lists cfg.RootPath, detects changes, chunks, and stores.
// For git repos it uses git ls-files and commit-based incremental indexing.
func RunIndex(ctx context.Context, cfg IndexerConfig) (*IndexStats, error) {
	startTime := time.Now()

	if cfg.SkipDirs == nil {
		cfg.SkipDirs = config.DefaultSkipDirsMap
	}
	if cfg.SkipExtensions == nil {
		cfg.SkipExtensions = config.DefaultSkipExtensionsMap
	}
	if cfg.SkipFilenames == nil {
		cfg.SkipFilenames = config.DefaultSkipFilenamesMap
	}

	stats := &IndexStats{IndexMode: "full"}
	cache := newHashCache()

	seenHashes := cfg.SeenHashes
	if seenHashes == nil {
		seenHashes = &sync.Map{}
	}

	isGitRepo := git.IsGitRepo(cfg.RootPath)
	var headCommit string
	var fileList []string // nil → WalkDir fallback

	if isGitRepo {
		headCommit = detectBranchAndCommit(ctx, cfg, stats)
		if headCommit == "" && cfg.SkipIfWrongBranch {
			log.Printf("[indexer] skipping repo %s: not on expected branch %s", cfg.RepoName, cfg.Branch)
			return stats, nil
		}
		fileList, stats.IndexMode = buildFileList(ctx, cfg, stats, headCommit)
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

	var feedErr error

	if fileList != nil {
		for _, path := range fileList {
			if shouldSkipFile(path, cfg.RootPath, cfg.SkipExtensions, cfg.SkipFilenames, nil) {
				continue
			}
			info, err := os.Stat(path)
			if err != nil {
				log.Printf("[indexer] stat %s: %v", path, err)
				mu.Lock()
				stats.Errors++
				mu.Unlock()
				continue
			}
			workCh <- workItem{path: path, info: info}
		}
	} else {
		var gi *gitignore.GitIgnore
		giPath := filepath.Join(cfg.RootPath, ".gitignore")
		if _, err := os.Stat(giPath); err == nil {
			if compiled, err := gitignore.CompileIgnoreFile(giPath); err == nil {
				gi = compiled
			} else {
				log.Printf("[indexer] could not parse .gitignore at %s: %v", giPath, err)
			}
		}
		feedErr = filepath.WalkDir(cfg.RootPath, func(path string, d os.DirEntry, walkEntryErr error) error {
			if walkEntryErr != nil {
				log.Printf("[indexer] walk error at %s: %v", path, walkEntryErr)
				mu.Lock()
				stats.Errors++
				mu.Unlock()
				return nil
			}
			if d.IsDir() {
				if cfg.SkipDirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if shouldSkipFile(path, cfg.RootPath, cfg.SkipExtensions, cfg.SkipFilenames, gi) {
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
	}

	close(workCh)
	wg.Wait()

	if feedErr != nil {
		return stats, fmt.Errorf("directory walk failed: %w", feedErr)
	}

	if cfg.DB != nil {
		repoMeta := db.RepoMeta{
			Repo:              cfg.RepoName,
			RootPath:          cfg.RootPath,
			CurrentBranch:     stats.Branch,
			LastIndexedCommit: headCommit,
			LastIndexedAt:     time.Now().Format(time.RFC3339),
			FileCount:         int64(stats.FilesScanned),
			ChunkCount:        int64(stats.ChunksUpserted),
			IndexMode:         stats.IndexMode,
			DurationMs:        time.Since(startTime).Milliseconds(),
		}
		if err := cfg.DB.UpsertRepoMeta(ctx, repoMeta); err != nil {
			log.Printf("[indexer] upsert repo meta: %v", err)
		}
	}

	stats.LastCommit = headCommit
	return stats, nil
}

// detectBranchAndCommit performs branch-mismatch detection and returns HEAD SHA.
// If SkipIfWrongBranch is set and branch mismatches, returns empty string to signal skip.
func detectBranchAndCommit(_ context.Context, cfg IndexerConfig, stats *IndexStats) string {
	if !cfg.SkipBranchCheck || cfg.SkipIfWrongBranch {
		detected, err := git.DetectDefaultBranch(cfg.RootPath)
		if err != nil {
			log.Printf("[indexer] WARNING: could not detect default branch for %s: %v", cfg.RepoName, err)
		} else {
			current, cerr := git.CurrentBranch(cfg.RootPath)
			if cerr == nil {
				stats.Branch = current
				expected := cfg.Branch
				if expected == "" {
					expected = detected
				}
				if current != expected {
					if cfg.SkipIfWrongBranch {
						return "" // Signal skip
					}
					msg := fmt.Sprintf("[indexer] WARNING: repo %s is on branch %s, expected %s",
						cfg.RepoName, current, expected)
					if cfg.StrictBranch {
						log.Fatal(msg)
					}
					log.Print(msg)
				}
			}
		}
	} else {
		if current, err := git.CurrentBranch(cfg.RootPath); err == nil {
			stats.Branch = current
		}
	}

	commit, err := git.HeadCommit(cfg.RootPath)
	if err != nil {
		log.Printf("[indexer] WARNING: could not get HEAD commit for %s: %v", cfg.RepoName, err)
		return ""
	}
	return commit
}

// buildFileList decides between incremental (git diff) and full (git ls-files).
// Returns nil, "full" to signal a WalkDir fallback.
func buildFileList(_ context.Context, cfg IndexerConfig, stats *IndexStats, headCommit string) ([]string, string) {
	if headCommit == "" {
		return nil, "full"
	}
	if cfg.DB != nil {
		meta, err := cfg.DB.GetRepoMeta(context.Background(), cfg.RepoName)
		if err == nil && meta != nil &&
			meta.LastIndexedCommit != "" && meta.LastIndexedCommit != headCommit {
			changed, deleted, err := git.DiffFiles(cfg.RootPath, meta.LastIndexedCommit, headCommit)
			if err != nil {
				log.Printf("[indexer] git diff failed for %s, doing full scan: %v", cfg.RepoName, err)
			} else {
				for _, dp := range deleted {
					if err := cfg.DB.DeleteFileChunks(context.Background(), cfg.RepoName, dp); err != nil {
						log.Printf("[indexer] delete chunks %s: %v", dp, err)
					}
					if err := cfg.DB.DeleteFileMeta(context.Background(), cfg.RepoName, dp); err != nil {
						log.Printf("[indexer] delete meta %s: %v", dp, err)
					}
					stats.DeletedFiles++
				}
				log.Printf("[indexer] incremental: %d changed, %d deleted since %s",
					len(changed), len(deleted), meta.LastIndexedCommit[:8])
				return changed, "incremental"
			}
		}
	}
	files, err := git.ListFiles(cfg.RootPath)
	if err != nil {
		log.Printf("[indexer] git ls-files failed for %s, falling back to fs walk: %v", cfg.RepoName, err)
		return nil, "full"
	}
	return files, "full"
}
