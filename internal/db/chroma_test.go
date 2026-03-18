package db

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/amikos-tech/chroma-go/pkg/embeddings"
)

// chromaURL returns the ChromaDB URL from the environment, or "" if not set.
func chromaURL() string {
	return os.Getenv("CHROMA_URL")
}

// skipIfNoChroma skips the test if CHROMA_URL is not set.
func skipIfNoChroma(t *testing.T) {
	t.Helper()
	if chromaURL() == "" {
		t.Skip("skipping: CHROMA_URL not set")
	}
}

// setupClient creates a test ChromaClient and ensures collections exist.
// It uses the ConsistentHashEmbeddingFunction for deterministic, dependency-free
// testing. Returns the client and a cleanup function.
func setupClient(t *testing.T) (*ChromaClient, func()) {
	t.Helper()
	ctx := context.Background()

	c, err := NewChromaClient(ctx, chromaURL())
	if err != nil {
		t.Fatalf("NewChromaClient: %v", err)
	}

	ef := embeddings.NewConsistentHashEmbeddingFunction()
	if err := c.EnsureCollections(ctx, ef); err != nil {
		t.Fatalf("EnsureCollections: %v", err)
	}

	cleanup := func() {
		// Best-effort cleanup: delete the collections to avoid test pollution.
		_ = c.client.DeleteCollection(ctx, "files")
		_ = c.client.DeleteCollection(ctx, "chunks")
	}
	return c, cleanup
}

func TestNewChromaClient(t *testing.T) {
	skipIfNoChroma(t)
	ctx := context.Background()
	c, err := NewChromaClient(ctx, chromaURL())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewChromaClient_Unreachable(t *testing.T) {
	ctx := context.Background()
	_, err := NewChromaClient(ctx, "http://127.0.0.1:19999")
	if err == nil {
		t.Fatal("expected error for unreachable server, got nil")
	}
}

func TestEnsureCollections(t *testing.T) {
	skipIfNoChroma(t)
	_, cleanup := setupClient(t)
	defer cleanup()
	// If we reach here without error, EnsureCollections succeeded.
}

func TestUpsertAndGetFileMeta(t *testing.T) {
	skipIfNoChroma(t)
	c, cleanup := setupClient(t)
	defer cleanup()
	ctx := context.Background()

	repo, path := "testrepo", "cmd/main.go"
	var size int64 = 1234
	var mtime int64 = 1700000000
	hash := "abc123def456"

	if err := c.UpsertFileMeta(ctx, repo, path, size, mtime, hash); err != nil {
		t.Fatalf("UpsertFileMeta: %v", err)
	}

	got, err := c.GetFileMeta(ctx, repo, path)
	if err != nil {
		t.Fatalf("GetFileMeta: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil FileMeta, got nil")
	}
	if got.Repo != repo {
		t.Errorf("Repo: got %q, want %q", got.Repo, repo)
	}
	if got.Path != path {
		t.Errorf("Path: got %q, want %q", got.Path, path)
	}
	if got.Size != size {
		t.Errorf("Size: got %d, want %d", got.Size, size)
	}
	if got.MTime != mtime {
		t.Errorf("MTime: got %d, want %d", got.MTime, mtime)
	}
	if got.Hash != hash {
		t.Errorf("Hash: got %q, want %q", got.Hash, hash)
	}
}

func TestGetFileMeta_NotFound(t *testing.T) {
	skipIfNoChroma(t)
	c, cleanup := setupClient(t)
	defer cleanup()
	ctx := context.Background()

	got, err := c.GetFileMeta(ctx, "nonexistent-repo", "no/such/file.go")
	if err != nil {
		t.Fatalf("GetFileMeta: unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil FileMeta for missing file, got: %+v", got)
	}
}

func TestUpsertFileMeta_Idempotent(t *testing.T) {
	skipIfNoChroma(t)
	c, cleanup := setupClient(t)
	defer cleanup()
	ctx := context.Background()

	repo, path := "myrepo", "pkg/util.go"

	// Upsert once.
	if err := c.UpsertFileMeta(ctx, repo, path, 100, 1000, "hash1"); err != nil {
		t.Fatalf("first UpsertFileMeta: %v", err)
	}

	// Upsert again with different values — must overwrite.
	if err := c.UpsertFileMeta(ctx, repo, path, 200, 2000, "hash2"); err != nil {
		t.Fatalf("second UpsertFileMeta: %v", err)
	}

	got, err := c.GetFileMeta(ctx, repo, path)
	if err != nil {
		t.Fatalf("GetFileMeta: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil FileMeta after upsert")
	}
	if got.Size != 200 || got.Hash != "hash2" {
		t.Errorf("upsert did not overwrite: got Size=%d Hash=%q, want Size=200 Hash=hash2", got.Size, got.Hash)
	}
}

func TestUpsertAndQueryChunks(t *testing.T) {
	skipIfNoChroma(t)
	c, cleanup := setupClient(t)
	defer cleanup()
	ctx := context.Background()

	chunks := []Chunk{
		{
			ID:        "chunk-001",
			Repo:      "myrepo",
			Path:      "main.go",
			Language:  "go",
			Content:   "func main() { fmt.Println(\"hello world\") }",
			StartLine: 1,
			EndLine:   3,
		},
		{
			ID:        "chunk-002",
			Repo:      "myrepo",
			Path:      "util.go",
			Language:  "go",
			Content:   "func Add(a, b int) int { return a + b }",
			StartLine: 1,
			EndLine:   3,
		},
	}

	if err := c.UpsertChunks(ctx, chunks); err != nil {
		t.Fatalf("UpsertChunks: %v", err)
	}

	results, err := c.QueryChunks(ctx, "main function", 5, "")
	if err != nil {
		t.Fatalf("QueryChunks: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result from QueryChunks")
	}
	// All results must have a repo and path.
	for i, r := range results {
		if r.Repo == "" {
			t.Errorf("result[%d]: empty Repo", i)
		}
		if r.Path == "" {
			t.Errorf("result[%d]: empty Path", i)
		}
	}
}

func TestQueryChunks_RepoFilter(t *testing.T) {
	skipIfNoChroma(t)
	c, cleanup := setupClient(t)
	defer cleanup()
	ctx := context.Background()

	chunks := []Chunk{
		{ID: "r1-chunk", Repo: "repo1", Path: "a.go", Language: "go", Content: "func Hello() {}", StartLine: 1, EndLine: 1},
		{ID: "r2-chunk", Repo: "repo2", Path: "b.go", Language: "go", Content: "func World() {}", StartLine: 1, EndLine: 1},
	}
	if err := c.UpsertChunks(ctx, chunks); err != nil {
		t.Fatalf("UpsertChunks: %v", err)
	}

	results, err := c.QueryChunks(ctx, "Hello World function", 10, "repo1")
	if err != nil {
		t.Fatalf("QueryChunks with repoFilter: %v", err)
	}
	for i, r := range results {
		if r.Repo != "repo1" {
			t.Errorf("result[%d]: Repo = %q, want 'repo1'", i, r.Repo)
		}
	}
}

func TestUpsertChunks_Empty(t *testing.T) {
	skipIfNoChroma(t)
	c, cleanup := setupClient(t)
	defer cleanup()
	ctx := context.Background()

	if err := c.UpsertChunks(ctx, nil); err != nil {
		t.Fatalf("UpsertChunks(nil): %v", err)
	}
	if err := c.UpsertChunks(ctx, []Chunk{}); err != nil {
		t.Fatalf("UpsertChunks([]): %v", err)
	}
}

func TestDeleteFileChunks(t *testing.T) {
	skipIfNoChroma(t)
	c, cleanup := setupClient(t)
	defer cleanup()
	ctx := context.Background()

	repo, path := "deleterepo", "todelete.go"
	chunks := make([]Chunk, 3)
	for i := range chunks {
		chunks[i] = Chunk{
			ID:        fmt.Sprintf("del-chunk-%d", i),
			Repo:      repo,
			Path:      path,
			Language:  "go",
			Content:   fmt.Sprintf("func F%d() {}", i),
			StartLine: i + 1,
			EndLine:   i + 1,
		}
	}
	if err := c.UpsertChunks(ctx, chunks); err != nil {
		t.Fatalf("UpsertChunks: %v", err)
	}

	// Delete should not error even when chunks exist.
	if err := c.DeleteFileChunks(ctx, repo, path); err != nil {
		t.Fatalf("DeleteFileChunks: %v", err)
	}

	// After deletion, querying for the same repo/path should return no results.
	results, err := c.QueryChunks(ctx, "func F0", 10, repo)
	if err != nil {
		t.Fatalf("QueryChunks after delete: %v", err)
	}
	for _, r := range results {
		if r.Path == path {
			t.Errorf("found deleted chunk: %+v", r)
		}
	}
}
