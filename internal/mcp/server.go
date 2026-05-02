package mcp

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ramayac/omni-code/internal/db"
)

// Shared parameter types used by multiple tools.

// searchParams defines the input schema for the search_codebase tool.
type searchParams struct {
	Query    string `json:"query"     jsonschema:"required,The natural-language search query"`
	Repo     string `json:"repo"      jsonschema:"Filter results to a specific repository name (optional)"`
	NResults int    `json:"n_results" jsonschema:"Number of results to return (default 10)"`
}

type repoParams struct {
	Repo string `json:"repo" jsonschema:"required,Repository name"`
}

type repoFilesParams struct {
	Repo   string `json:"repo"   jsonschema:"required,Repository name"`
	Filter string `json:"filter" jsonschema:"Optional glob pattern to filter file paths (e.g. '*.go')"`
}

type fileContentParams struct {
	Repo string `json:"repo" jsonschema:"required,Repository name"`
	Path string `json:"path" jsonschema:"required,File path relative to repo root (or absolute)"`
}

type grepParams struct {
	Pattern    string `json:"pattern"     jsonschema:"required,RE2 regex pattern to search for"`
	Repo       string `json:"repo"        jsonschema:"Restrict search to this repository (optional)"`
	FileFilter string `json:"file_filter" jsonschema:"Optional glob pattern to filter file paths (e.g. '*.go')"`
	MaxResults int    `json:"max_results" jsonschema:"Maximum number of matching lines to return (default 50)"`
}

type reindexParams struct {
	Repo string `json:"repo" jsonschema:"required,Repository name to re-index"`
	Full bool   `json:"full" jsonschema:"If true, drop all existing data before indexing (default: incremental)"`
}

type topContributorsParams struct {
	Repo  string `json:"repo"  jsonschema:"required,Repository name"`
	Since string `json:"since" jsonschema:"Optional time window for git shortlog (e.g. '6 months ago', '2026-01-01')"`
}

// buildServer constructs and returns an MCP server with all tools registered.
// Handler implementations live in handlers_*.go files.
func buildServer(client *db.ChromaClient) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "omni-code", Version: "1.0.0"}, nil)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "search_codebase",
		Description: "Semantic search across all indexed local codebases. Returns relevant code chunks with their file path and line numbers.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args searchParams) (*mcp.CallToolResult, any, error) {
		return handleSearch(ctx, client, args)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_repos",
		Description: "List all indexed repositories with stats (branch, last commit, file count, chunk count).",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, any, error) {
		return handleListRepos(ctx, client)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_repo_files",
		Description: "List files indexed for a repository, with optional glob filter.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args repoFilesParams) (*mcp.CallToolResult, any, error) {
		return handleGetRepoFiles(ctx, client, args)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_file_content",
		Description: "Read the raw content of a file from disk (max 100 KB).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args fileContentParams) (*mcp.CallToolResult, any, error) {
		return handleGetFileContent(ctx, client, args)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "git_status",
		Description: "Show branch, uncommitted changes, and index staleness for a repository.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args repoParams) (*mcp.CallToolResult, any, error) {
		return handleGitStatus(ctx, client, args)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "git_diff",
		Description: "Show diff between current state and last indexed commit.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args repoParams) (*mcp.CallToolResult, any, error) {
		return handleGitDiff(ctx, client, args)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "git_log",
		Description: "Show recent commit history for a repository.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args repoParams) (*mcp.CallToolResult, any, error) {
		return handleGitLog(ctx, client, args)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "index_status",
		Description: "Detailed breakdown of when/how each repo was last indexed.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, any, error) {
		return handleIndexStatus(ctx, client)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "grep_codebase",
		Description: "Grep indexed files for a regex pattern. Returns matching lines with file paths and line numbers.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args grepParams) (*mcp.CallToolResult, any, error) {
		return handleGrep(ctx, client, args)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_file_symbols",
		Description: "List top-level AST symbols (functions, classes, types, etc.) in a file using tree-sitter.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args fileContentParams) (*mcp.CallToolResult, any, error) {
		return handleGetFileSymbols(ctx, client, args)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "reindex_repo",
		Description: "Trigger an incremental (or full) re-index of an already-registered repository.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args reindexParams) (*mcp.CallToolResult, any, error) {
		return handleReindexRepo(ctx, client, args)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_repo_summary",
		Description: "Return a rich Markdown summary of a repository: metadata, language distribution, top-level directory overview, and recent git log.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args repoParams) (*mcp.CallToolResult, any, error) {
		return handleGetRepoSummary(ctx, client, args)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "search_repo_summaries",
		Description: "List all indexed repositories with a compact summary of each (language breakdown, file count, recent activity). Use this to identify which repo to target for a task.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, any, error) {
		return handleSearchRepoSummaries(ctx, client)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_top_contributors",
		Description: "Return a ranked leaderboard of git contributors for a repository.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args topContributorsParams) (*mcp.CallToolResult, any, error) {
		return handleGetTopContributors(ctx, client, args)
	})

	return s
}

// transport helpers ------------------------------------------------------------

// ServeStdio starts the MCP server and blocks until stdin is closed.
// All log output goes to os.Stderr — stdout is reserved for the JSON-RPC stream.
func ServeStdio(ctx context.Context, client *db.ChromaClient) error {
	s := buildServer(client)
	log.Printf("[mcp] server starting on stdio")
	return s.Run(ctx, &mcp.StdioTransport{})
}

// ServeSSE starts the MCP server using the legacy SSE HTTP transport (MCP spec 2024-11-05).
// This is the broadest-compatibility mode; VS Code and most GUI clients support it.
// Blocks until ctx is cancelled or the listener fails.
func ServeSSE(ctx context.Context, client *db.ChromaClient, addr string, cors bool) error {
	s := buildServer(client)
	var handler http.Handler = mcp.NewSSEHandler(func(_ *http.Request) *mcp.Server { return s }, nil)
	if cors {
		handler = corsMiddleware(handler)
	}
	srv := &http.Server{Addr: addr, Handler: handler}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx) //nolint:errcheck
	}()
	log.Printf("[mcp] SSE server listening on http://%s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// ServeStreamable starts the MCP server using the modern Streamable HTTP transport (MCP spec 2025-03-26).
// Mounts at /mcp with a /health liveness probe.
// Blocks until ctx is cancelled or the listener fails.
func ServeStreamable(ctx context.Context, client *db.ChromaClient, addr string, stateless bool, cors bool) error {
	s := buildServer(client)
	opts := &mcp.StreamableHTTPOptions{Stateless: stateless}
	var mcpHandler http.Handler = mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server { return s }, opts)
	if cors {
		mcpHandler = corsMiddleware(mcpHandler)
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpHandler)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`)) //nolint:errcheck
	})
	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx) //nolint:errcheck
	}()
	log.Printf("[mcp] streamable HTTP server listening on http://%s/mcp  (health: http://%s/health)", addr, addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// corsMiddleware wraps handler with permissive CORS headers required by browser-based GUI clients.
// Only use with --cors flag; never enable by default.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, Mcp-Protocol-Version, Mcp-Session-Id")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
