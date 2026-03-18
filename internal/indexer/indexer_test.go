package indexer

import (
	"context"
	"github.com/ramayac/omni-code/internal/chunker"
	"github.com/ramayac/omni-code/internal/db"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ramayac/omni-code/internal/config"
)

// ---- ShouldSkipDir ----

func TestShouldSkipDir(t *testing.T) {
	cases := []struct {
		dir  string
		want bool
	}{
		{".git", true},
		{"node_modules", true},
		{"vendor", true},
		{"dist", true},
		{"build", true},
		{".next", true},
		{"__pycache__", true},
		{".venv", true},
		{".tox", true},
		{"src", false},
		{"internal", false},
		{"docs", false},
		{"cmd", false},
	}
	for _, tc := range cases {
		t.Run(tc.dir, func(t *testing.T) {
			if got := ShouldSkipDir(tc.dir); got != tc.want {
				t.Errorf("ShouldSkipDir(%q) = %v, want %v", tc.dir, got, tc.want)
			}
		})
	}
}

// ---- shouldSkipFile (extensions) ----

func TestShouldSkipFile_Extensions(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/repo/doc.pdf", true},
		{"/repo/img.PNG", true}, // case-insensitive
		{"/repo/img.jpg", true},
		{"/repo/img.jpeg", true},
		{"/repo/anim.gif", true},
		{"/repo/icon.svg", true},
		{"/repo/vid.mp4", true},
		{"/repo/audio.mp3", true},
		{"/repo/arch.zip", true},
		{"/repo/arch.tar", true},
		{"/repo/arch.gz", true},
		{"/repo/app.exe", true},
		{"/repo/lib.dll", true},
		{"/repo/lib.so", true},
		{"/repo/lib.dylib", true},
		{"/repo/mod.wasm", true},
		{"/repo/file.bin", true},
		{"/repo/file.dat", true},
		{"/repo/store.db", true},
		{"/repo/store.sqlite", true},
		{"/repo/main.go", false},
		{"/repo/app.ts", false},
		{"/repo/app.tsx", false},
		{"/repo/index.js", false},
		{"/repo/script.py", false},
		{"/repo/README.md", false},
		{"/repo/config.yaml", false},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := shouldSkipFile(tc.path, "/repo", config.DefaultSkipExtensionsMap, config.DefaultSkipFilenamesMap, nil); got != tc.want {
				t.Errorf("shouldSkipFile(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// ---- shouldSkipFile (filenames) ----

func TestShouldSkipFile_Filenames(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/repo/.env", true},
		{"/repo/package-lock.json", true},
		{"/repo/yarn.lock", true},
		{"/repo/go.sum", true},
		{"/repo/.DS_Store", true},
		{"/repo/Thumbs.db", true},
		{"/repo/go.mod", false},
		{"/repo/main.go", false},
		{"/repo/README.md", false},
	}
	for _, tc := range cases {
		t.Run(filepath.Base(tc.path), func(t *testing.T) {
			if got := shouldSkipFile(tc.path, "/repo", config.DefaultSkipExtensionsMap, config.DefaultSkipFilenamesMap, nil); got != tc.want {
				t.Errorf("shouldSkipFile(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// ---- DetectLanguage ----

func TestDetectLanguage(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/repo/main.go", "go"},
		{"/repo/app.ts", "typescript"},
		{"/repo/App.TSX", "typescript"},
		{"/repo/index.js", "javascript"},
		{"/repo/Component.jsx", "javascript"},
		{"/repo/script.py", "python"},
		{"/repo/lib.rs", "rust"},
		{"/repo/Main.java", "java"},
		{"/repo/foo.c", "c"},
		{"/repo/bar.cpp", "cpp"},
		{"/repo/baz.cc", "cpp"},
		{"/repo/qux.cxx", "cpp"},
		{"/repo/gem.rb", "ruby"},
		{"/repo/README.md", "markdown"},
		{"/repo/notes.markdown", "markdown"},
		{"/repo/config.yaml", "text"},
		{"/repo/data.json", "text"},
		{"/repo/no-extension", "text"},
	}
	for _, tc := range cases {
		t.Run(filepath.Base(tc.path), func(t *testing.T) {
			if got := DetectLanguage(tc.path); got != tc.want {
				t.Errorf("DetectLanguage(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

// ---- HashFile ----

func TestHashFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	content := []byte("hello, omni-code")

	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	cache := newHashCache()

	hash1, err := HashFile(path, info.Size(), info.ModTime().Unix(), cache)
	if err != nil {
		t.Fatalf("HashFile: %v", err)
	}
	if len(hash1) != 64 {
		t.Errorf("expected 64-char hex hash, got %d: %q", len(hash1), hash1)
	}

	// Second call with same key must hit the cache and return the same value.
	hash2, err := HashFile(path, info.Size(), info.ModTime().Unix(), cache)
	if err != nil {
		t.Fatalf("HashFile (cached): %v", err)
	}
	if hash1 != hash2 {
		t.Errorf("cache miss: hash1=%q hash2=%q", hash1, hash2)
	}

	// Different content must produce a different hash.
	path2 := filepath.Join(dir, "other.txt")
	if err := os.WriteFile(path2, []byte("different content"), 0600); err != nil {
		t.Fatal(err)
	}
	info2, _ := os.Stat(path2)
	hash3, err := HashFile(path2, info2.Size(), info2.ModTime().Unix(), cache)
	if err != nil {
		t.Fatal(err)
	}
	if hash1 == hash3 {
		t.Error("expected different hashes for different file contents")
	}
}

// ---- Deduplication ----

func TestDeduplication(t *testing.T) {
	seenHashes := &sync.Map{}
	hash := "deadbeef1234"

	// First store: not loaded.
	if _, loaded := seenHashes.LoadOrStore(hash, true); loaded {
		t.Error("first LoadOrStore should not find an existing entry")
	}

	// Second store: loaded (duplicate).
	if _, loaded := seenHashes.LoadOrStore(hash, true); !loaded {
		t.Error("second LoadOrStore should find the existing entry")
	}

	// Different hash is a new entry.
	if _, loaded := seenHashes.LoadOrStore("otherhash", true); loaded {
		t.Error("distinct hash should not be found in seenHashes")
	}
}

// ---- Integration: RunIndex ----

func TestRunIndex_Integration(t *testing.T) {
	// Requires CHROMA_URL to be set. Skipped in normal unit-test runs.
	if os.Getenv("CHROMA_URL") == "" {
		t.Skip("CHROMA_URL not set; skipping RunIndex integration test")
	}
	t.Log("RunIndex integration test placeholder — wire up in Phase 4")
}

func TestRunIndex_GitIncremental(t *testing.T) {
	if os.Getenv("CHROMA_URL") == "" {
		t.Skip("CHROMA_URL not set; skipping integration test")
	}
	ctx := context.Background()
	client, err := db.NewChromaClient(ctx, os.Getenv("CHROMA_URL"))
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	repoDir := t.TempDir()

	// Create git repo
	cmd := exec.Command("git", "init", "-b", "main")
	cmd.Dir = repoDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("git init: %v", err)
	}

	os.WriteFile(filepath.Join(repoDir, "file1.txt"), []byte("v1"), 0644)

	cmd = exec.Command("git", "add", ".")
	cmd.Dir = repoDir
	cmd.Run()

	cmd = exec.Command("git", "commit", "-m", "init")
	cmd.Dir = repoDir
	cmd.Run()
	cfg := IndexerConfig{
		RootPath:       repoDir,
		RepoName:       "test-incremental-repo",
		DB:             client,
		ChunkFn:        chunker.ChunkFile,
		SeenHashes:     &sync.Map{},
		SkipDirs:       config.DefaultSkipDirsMap,
		SkipExtensions: config.DefaultSkipExtensionsMap,
		SkipFilenames:  config.DefaultSkipFilenamesMap,
	}
	// First pass
	stats1, err := RunIndex(ctx, cfg)
	if err != nil {
		t.Fatalf("run index pass 1: %v", err)
	}
	if stats1.FilesScanned != 1 {
		t.Errorf("expected 1 file processed, got %d", stats1.FilesScanned)
	}
	// Edit file
	os.WriteFile(filepath.Join(repoDir, "file1.txt"), []byte("v2 long text to be at least some bytes long"), 0644)
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = repoDir
	cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "update")
	cmd.Dir = repoDir
	cmd.Run()
	// Second pass
	cfg.SeenHashes = &sync.Map{} // reset dedup cache
	stats2, err := RunIndex(ctx, cfg)
	if err != nil {
		t.Fatalf("run index pass 2: %v", err)
	}
	if stats2.FilesScanned != 1 {
		t.Errorf("expected 1 file processed, got %d", stats2.FilesScanned)
	}
	if stats2.FilesChanged != 1 {
		t.Errorf("expected 1 file changed, got %d", stats2.FilesChanged)
	}
	// Third pass (no changes)
	cfg.SeenHashes = &sync.Map{}
	stats3, err := RunIndex(ctx, cfg)
	if err != nil {
		t.Fatalf("run index pass 3: %v", err)
	}
	// It's checked, but skipped and should say 0 changed files
	if stats3.FilesChanged != 0 {
		t.Errorf("expected 0 files changed on third pass, got %d", stats3.FilesChanged)
	}
}
