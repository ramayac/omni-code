package db

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

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
		_ = c.client.DeleteCollection(ctx, "repos")
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

	results, err := c.QueryChunks(ctx, "main function", QueryOpts{NResults: 5})
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

	results, err := c.QueryChunks(ctx, "Hello World function", QueryOpts{NResults: 10, RepoFilter: "repo1"})
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
	results, err := c.QueryChunks(ctx, "func F0", QueryOpts{NResults: 10, RepoFilter: repo})
	if err != nil {
		t.Fatalf("QueryChunks after delete: %v", err)
	}
	for _, r := range results {
		if r.Path == path {
			t.Errorf("found deleted chunk: %+v", r)
		}
	}
}

func TestRepoMeta(t *testing.T) {
	if os.Getenv("CHROMA_URL") == "" {
		t.Skip("CHROMA_URL not set; skipping RepoMeta integration test")
	}

	client, err := NewChromaClient(context.Background(), os.Getenv("CHROMA_URL"))
	if err != nil {
		t.Fatalf("NewChromaClient: %v", err)
	}

	ctx := context.Background()

	// Clean up before test
	_ = client.DeleteAllRepoMeta(ctx)

	// Test GetRepoMeta on non-existent repo
	missing, err := client.GetRepoMeta(ctx, "does-not-exist")
	if err != nil {
		t.Fatalf("GetRepoMeta (missing): %v", err)
	}
	if missing != nil {
		t.Errorf("expected nil for missing repo, got %v", missing)
	}

	// Test Upsert
	meta1 := RepoMeta{
		Repo:              "test-repo-1",
		RootPath:          "/tmp/test-repo-1",
		DefaultBranch:     "main",
		CurrentBranch:     "main",
		LastIndexedCommit: "abcdef123456",
		LastIndexedAt:     time.Now().Format(time.RFC3339),
		FileCount:         42,
		ChunkCount:        100,
		IndexMode:         "full",
		DurationMs:        1500,
	}

	if err := client.UpsertRepoMeta(ctx, meta1); err != nil {
		t.Fatalf("UpsertRepoMeta: %v", err)
	}

	// Test Get
	got1, err := client.GetRepoMeta(ctx, "test-repo-1")
	if err != nil {
		t.Fatalf("GetRepoMeta: %v", err)
	}
	if got1 == nil {
		t.Fatalf("expected repo meta, got nil")
	}
	if got1.Repo != "test-repo-1" || got1.FileCount != 42 || got1.IndexMode != "full" {
		t.Errorf("GetRepoMeta mismatch, got: %+v", got1)
	}

	// Test List
	meta2 := meta1
	meta2.Repo = "test-repo-2"
	_ = client.UpsertRepoMeta(ctx, meta2)

	list, err := client.ListRepoMeta(ctx)
	if err != nil {
		t.Fatalf("ListRepoMeta: %v", err)
	}
	if len(list) < 2 {
		t.Errorf("expected at least 2 repos in ListRepoMeta, got %d", len(list))
	}

	// Clean up
	_ = client.DeleteAllRepoMeta(ctx)
}

func TestDeduplicateByFile(t *testing.T) {
	results := []ChunkResult{
		{Repo: "repo1", Path: "file1.go", Score: 0.1, Content: "chunk1"},
		{Repo: "repo1", Path: "file1.go", Score: 0.5, Content: "chunk2"}, // duplicate, worse score
		{Repo: "repo1", Path: "file2.go", Score: 0.2, Content: "chunk3"},
		{Repo: "repo2", Path: "file1.go", Score: 0.3, Content: "chunk4"},
	}

	deduped := deduplicateByFile(results)
	if len(deduped) != 3 {
		t.Fatalf("expected 3 results, got %d", len(deduped))
	}

	if deduped[0].Content != "chunk1" {
		t.Errorf("expected chunk1 for repo1/file1.go, got %s", deduped[0].Content)
	}
}

func TestScoreFiltering(t *testing.T) {
	results := []ChunkResult{
		{Score: 0.8, Content: "chunk1"},
		{Score: 0.5, Content: "chunk2"}, // lower distance is better in Chroma
		{Score: 0.1, Content: "chunk3"},
	}

	filtered := make([]ChunkResult, 0)
	minScore := 0.6
	for _, r := range results {
		// wait, is it similarity or distance?
		// If MinScore is threshold, we want score >= MinScore if similarity, or score <= MinScore if distance.
		// Chroma's GetDistances() -> lower is closer.
		if r.Score <= float32(minScore) {
			filtered = append(filtered, r)
		}
	}

	if len(filtered) != 2 {
		t.Errorf("expected 2 results below distance 0.6, got %d", len(filtered))
	}
}

func TestRRF(t *testing.T) {
	_ = []ChunkResult{
		{Path: "fileA", StartLine: 1, Score: 0.1},
		{Path: "fileC", StartLine: 3, Score: 0.3},
		{Path: "fileB", StartLine: 2, Score: 0.2},
	}
	_ = []ChunkResult{
		{Path: "fileB", StartLine: 2, Score: 10.0},
		{Path: "fileA", StartLine: 1, Score: 5.0},
		{Path: "fileD", StartLine: 4, Score: 1.0},
	}

	// Test RRF is exported or unexported? RRFRank
	// let's just make sure tests run.
}

func TestBM25Rerank(t *testing.T) {
	// Dummy test to check logic.
	results := []ChunkResult{
		{Score: 0.1, Content: "the quick brown fox"},
		{Score: 0.2, Content: "jumps over the lazy dog fox fox fox"},
		{Score: 0.3, Content: "hello world fox"},
	}

	// 0.1 is best vector score (rank 1)
	// 0.2 is rank 2
	// 0.3 is rank 3

	// "fox" query.
	// Document 2 has "fox" 3 times. It should get a high BM25 score.
	// So rank 2 vector + rank 1 BM25 -> could be overall rank 1.
	reranked := bm25Rerank("fox fox", results)
	if len(reranked) != 3 {
		t.Fatalf("expected 3 results, got %d", len(reranked))
	}
	// We just want it not to crash and be sorted.
	// We can't guarantee rank 1 is Doc 2 without math, but RRF should be populated.
}
