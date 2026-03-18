package mcp

import (
	"context"
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
