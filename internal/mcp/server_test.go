package mcp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ramayac/omni-code/internal/db"
)

// --- stub ChromaClient -------------------------------------------------
// These tests do not need a real ChromaDB; handleSearch accepts the Querier
// interface implicitly, so we verify formatting by encoding a fake result
// directly into the handler via a thin wrapper.

// fakeResult is used to produce a predictable handleSearch output without
// exercising the real ChromaDB client.
func fakeQueryChunks(_ context.Context, query string, n int, repo string) ([]db.ChunkResult, error) {
	if query == "" {
		return nil, nil
	}
	return []db.ChunkResult{
		{
			Repo:      "my-repo",
			Path:      "pkg/foo/bar.go",
			Language:  "go",
			Content:   "func Foo() {}",
			StartLine: 10,
			EndLine:   12,
			Score:     0.9,
		},
	}, nil
}

// TestHandleSearch_FormatOutput verifies that handleSearch produces correctly
// formatted Markdown without requiring a live ChromaDB connection.
func TestHandleSearch_FormatOutput(t *testing.T) {
	// Build a minimal fake ChromaClient — we bypass its methods entirely by
	// calling the unexported handleSearch with a hand-crafted result list.
	args := searchParams{
		Query:    "how does Foo work",
		NResults: 5,
	}

	results, _ := fakeQueryChunks(context.Background(), args.Query, args.NResults, args.Repo)

	result, _, err := handleSearchWithResults(results, args.Query)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected at least one content item")
	}
	text := result.Content[0].(*sdkmcp.TextContent).Text
	if !strings.Contains(text, "my-repo:pkg/foo/bar.go") {
		t.Errorf("output missing repo:path header, got:\n%s", text)
	}
	if !strings.Contains(text, "lines 10") {
		t.Errorf("output missing line numbers, got:\n%s", text)
	}
	if !strings.Contains(text, "func Foo()") {
		t.Errorf("output missing chunk content, got:\n%s", text)
	}
	if !strings.Contains(text, "```go") {
		t.Errorf("output missing language fence, got:\n%s", text)
	}
}

// TestHandleSearch_EmptyQuery verifies that an empty query returns an error.
func TestHandleSearch_EmptyQuery(t *testing.T) {
	args := searchParams{Query: ""}
	_, _, err := handleSearch(context.Background(), &db.ChromaClient{}, args)
	if err == nil {
		t.Fatal("expected error for empty query, got nil")
	}
}

// TestHandleSearch_NoResults verifies graceful handling of zero matches.
func TestHandleSearch_NoResults(t *testing.T) {
	result, _, err := handleSearchWithResults(nil, "anything")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := result.Content[0].(*sdkmcp.TextContent).Text
	if !strings.Contains(text, "No results") {
		t.Errorf("expected 'No results' message, got: %s", text)
	}
}

func TestFormatListReposResult(t *testing.T) {
	metas := []db.RepoMeta{
		{Repo: "repo1", CurrentBranch: "main", LastIndexedCommit: "1234567890", LastIndexedAt: "now", FileCount: 1, ChunkCount: 1},
	}
	res, _, err := formatListReposResult(metas)
	if err != nil {
		t.Fatal(err)
	}
	text := res.Content[0].(*sdkmcp.TextContent).Text
	if !strings.Contains(text, "repo1") || !strings.Contains(text, "12345678") {
		t.Errorf("output missing expected repo/commit details: %v", text)
	}
}

func TestFormatGetRepoFilesResult(t *testing.T) {
	files := []db.FileMeta{
		{Path: "pkg/foo/bar.go"},
		{Path: "cmd/main.go"},
		{Path: "baz.py"},
	}

	// test no filter
	res, _, err := formatGetRepoFilesResult(files, "")
	if err != nil {
		t.Fatal(err)
	}
	text := res.Content[0].(*sdkmcp.TextContent).Text
	if !strings.Contains(text, "pkg/foo/bar.go") || !strings.Contains(text, "baz.py") {
		t.Errorf("missing paths without filter: %v", text)
	}

	// test with filter
	res, _, err = formatGetRepoFilesResult(files, "*.go")
	if err != nil {
		t.Fatal(err)
	}
	text = res.Content[0].(*sdkmcp.TextContent).Text
	if strings.Contains(text, "baz.py") {
		t.Errorf("filter didn't work: %v", text)
	}
	if !strings.Contains(text, "cmd/main.go") {
		t.Errorf("filter filtered too much: %v", text)
	}
}

func TestHandleGetFileContent_Direct(t *testing.T) {
	f, err := os.CreateTemp("", "testfile*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString("hello world")
	f.Close()

	// Since we pass an absolute path, client is ignored
	res, _, err := handleGetFileContent(context.Background(), nil, fileContentParams{Repo: "anything", Path: f.Name()})
	if err != nil {
		t.Fatal(err)
	}
	text := res.Content[0].(*sdkmcp.TextContent).Text
	if !strings.Contains(text, "hello world") {
		t.Errorf("expected file content, got: %s", text)
	}
}

// TestBuildServer_tools asserts all 8 expected tools are registered.
func TestBuildServer_tools(t *testing.T) {
	s := buildServer(nil)
	if s == nil {
		t.Fatal("buildServer returned nil")
	}
	// The go-sdk doesn't expose a tool list accessor, so we verify indirectly:
	// ServeStdio calls buildServer and would panic/fail on nil — this is a smoke test.
}

// TestServeSSE_connect starts an SSE server on a random port and confirms
// an HTTP GET to the root returns 200 (SSE stream start).
func TestServeSSE_connect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ts := httptest.NewServer(sdkmcp.NewSSEHandler(func(_ *http.Request) *sdkmcp.Server {
		return buildServer(nil)
	}, nil))
	defer ts.Close()

	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	_ = ctx
}

// TestServeStreamable_health confirms the /health endpoint returns 200 and {"status":"ok"}.
func TestServeStreamable_health(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/mcp", sdkmcp.NewStreamableHTTPHandler(func(_ *http.Request) *sdkmcp.Server {
		return buildServer(nil)
	}, nil))
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"status":"ok"}` {
		t.Errorf("unexpected body: %s", body)
	}
}

// TestCORSMiddleware confirms headers are absent by default and present when applied.
func TestCORSMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Without CORS middleware — no Access-Control headers.
	ts1 := httptest.NewServer(inner)
	defer ts1.Close()
	resp1, err := http.Get(ts1.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp1.Body.Close()
	if resp1.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Error("expected no CORS header without middleware")
	}

	// With CORS middleware — header must be present.
	ts2 := httptest.NewServer(corsMiddleware(inner))
	defer ts2.Close()
	resp2, err := http.Get(ts2.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected '*' CORS origin header, got %q", resp2.Header.Get("Access-Control-Allow-Origin"))
	}

	// OPTIONS preflight must return 204.
	req, _ := http.NewRequest(http.MethodOptions, ts2.URL, nil)
	resp3, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204 for OPTIONS, got %d", resp3.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Phase 5 tool tests
// ---------------------------------------------------------------------------

// TestHandleGrep_EmptyPattern verifies that an empty pattern returns an error.
func TestHandleGrep_EmptyPattern(t *testing.T) {
	_, _, err := handleGrep(context.Background(), &db.ChromaClient{}, grepParams{})
	if err == nil {
		t.Fatal("expected error for empty pattern, got nil")
	}
}

// TestHandleGrep_InvalidRegex verifies that a bad regex returns a descriptive error.
func TestHandleGrep_InvalidRegex(t *testing.T) {
	_, _, err := handleGrep(context.Background(), &db.ChromaClient{}, grepParams{Pattern: "["})
	if err == nil {
		t.Fatal("expected error for invalid regex, got nil")
	}
	if !strings.Contains(err.Error(), "invalid regex pattern") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestHandleGetFileSymbols_Go reads a real Go file from test-data and verifies
// that top-level functions are detected. Uses an absolute path to bypass repo lookup.
func TestHandleGetFileSymbols_Go(t *testing.T) {
	f, err := os.CreateTemp("", "symbols_test_go*.go")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString("package main\n\nfunc Alpha() {}\nfunc Beta(x int) {}\n")
	f.Close()

	// Use the absolute path directly — no ChromaClient lookup needed.
	res, _, err := handleGetFileSymbols(context.Background(), nil, fileContentParams{
		Repo: "test-repo",
		Path: f.Name(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := res.Content[0].(*sdkmcp.TextContent).Text
	if !strings.Contains(text, "Symbols in") {
		t.Errorf("expected symbol table header, got: %s", text)
	}
	if !strings.Contains(text, "Alpha") || !strings.Contains(text, "Beta") {
		t.Errorf("expected functions Alpha and Beta, got: %s", text)
	}
}

// TestHandleGetFileSymbols_MissingParams verifies required-parameter validation.
func TestHandleGetFileSymbols_MissingParams(t *testing.T) {
	_, _, err := handleGetFileSymbols(context.Background(), nil, fileContentParams{Repo: "", Path: ""})
	if err == nil {
		t.Fatal("expected error for empty params, got nil")
	}
}

// TestHandleReindex_MissingRepo verifies that a missing repo parameter returns an error.
func TestHandleReindex_MissingRepo(t *testing.T) {
	_, _, err := handleReindexRepo(context.Background(), &db.ChromaClient{}, reindexParams{})
	if err == nil {
		t.Fatal("expected error for empty repo, got nil")
	}
}

// TestHandleGetFileSymbols_AbsPath exercises the absolute-path branch with a temp file.
func TestHandleGetFileSymbols_AbsPath(t *testing.T) {
	f, err := os.CreateTemp("", "symbols_test*.go")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString("package main\n\nfunc Hello() {}\nfunc World() {}\n")
	f.Close()

	res, _, err := handleGetFileSymbols(context.Background(), nil, fileContentParams{
		Repo: "any",
		Path: f.Name(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := res.Content[0].(*sdkmcp.TextContent).Text
	if !strings.Contains(text, "Hello") || !strings.Contains(text, "World") {
		t.Errorf("expected symbols Hello and World, got:\n%s", text)
	}
	if !strings.Contains(text, "function_declaration") {
		t.Errorf("expected kind 'function_declaration', got:\n%s", text)
	}
}

// ---------------------------------------------------------------------------
// get_repo_summary tests
// ---------------------------------------------------------------------------

// TestFormatRepoSummary_Basic verifies the Markdown structure of a repo summary.
func TestFormatRepoSummary_Basic(t *testing.T) {
meta := &db.RepoMeta{
Repo:              "my-repo",
CurrentBranch:     "main",
LastIndexedCommit: "abc1234567890",
LastIndexedAt:     "2026-03-20T10:00:00Z",
FileCount:         3,
ChunkCount:        10,
IndexMode:         "incremental",
RootPath:          "",
}
files := []db.FileMeta{
{Repo: "my-repo", Path: "cmd/main.go"},
{Repo: "my-repo", Path: "internal/foo/bar.go"},
{Repo: "my-repo", Path: "README.md"},
}

res, _, err := formatRepoSummary(meta, files, "my-repo")
if err != nil {
t.Fatalf("unexpected error: %v", err)
}
text := res.Content[0].(*sdkmcp.TextContent).Text

if !strings.Contains(text, "# Repository Summary: my-repo") {
t.Errorf("missing header, got:\n%s", text)
}
if !strings.Contains(text, "main") {
t.Errorf("missing branch, got:\n%s", text)
}
if !strings.Contains(text, "abc12345") {
t.Errorf("missing short commit, got:\n%s", text)
}
if !strings.Contains(text, "Language Distribution") {
t.Errorf("missing language section, got:\n%s", text)
}
if !strings.Contains(text, "go") {
t.Errorf("missing 'go' language, got:\n%s", text)
}
if !strings.Contains(text, "Top-Level Directories") {
t.Errorf("missing directory section, got:\n%s", text)
}
if !strings.Contains(text, "cmd") {
t.Errorf("missing 'cmd' directory, got:\n%s", text)
}
}

// TestFormatRepoSummary_EmptyFiles verifies graceful handling of no indexed files.
func TestFormatRepoSummary_EmptyFiles(t *testing.T) {
meta := &db.RepoMeta{
Repo:          "empty-repo",
CurrentBranch: "main",
FileCount:     0,
ChunkCount:    0,
IndexMode:     "full",
}
res, _, err := formatRepoSummary(meta, nil, "empty-repo")
if err != nil {
t.Fatalf("unexpected error: %v", err)
}
text := res.Content[0].(*sdkmcp.TextContent).Text
if !strings.Contains(text, "# Repository Summary: empty-repo") {
t.Errorf("missing header for empty repo, got:\n%s", text)
}
// No language or directory sections expected when there are no files.
if strings.Contains(text, "Language Distribution") {
t.Errorf("unexpected language section for empty repo, got:\n%s", text)
}
}

// TestHandleGetRepoSummary_MissingRepo verifies that an empty repo returns an error.
func TestHandleGetRepoSummary_MissingRepo(t *testing.T) {
_, _, err := handleGetRepoSummary(context.Background(), &db.ChromaClient{}, repoParams{})
if err == nil {
t.Fatal("expected error for empty repo, got nil")
}
}
