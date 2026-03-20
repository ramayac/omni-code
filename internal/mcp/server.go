package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ramayac/omni-code/internal/db"
	"github.com/ramayac/omni-code/internal/git"
)

// searchParams defines the input schema for the search_codebase tool.
type searchParams struct {
	Query    string `json:"query"    jsonschema:"required,The natural-language search query"`
	Repo     string `json:"repo"     jsonschema:"Filter results to a specific repository name (optional)"`
	NResults int    `json:"n_results" jsonschema:"Number of results to return (default 10)"`
}

// buildServer constructs and returns an MCP server with all tools registered.
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

	return s
}

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
		srv.Shutdown(context.Background()) //nolint:errcheck
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
		srv.Shutdown(context.Background()) //nolint:errcheck
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

// handleSearch executes the search_codebase tool call.
func handleSearch(ctx context.Context, client *db.ChromaClient, args searchParams) (*mcp.CallToolResult, any, error) {
	if args.Query == "" {
		return nil, nil, fmt.Errorf("query parameter is required")
	}
	n := args.NResults
	if n <= 0 {
		n = 10
	}

	results, err := client.QueryChunks(ctx, args.Query, db.QueryOpts{
		NResults:   n,
		RepoFilter: args.Repo,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("search failed: %w", err)
	}

	return handleSearchWithResults(results, args.Query)
}

// handleSearchWithResults formats a slice of ChunkResults into MCP markdown output.
// Extracted for testability so tests can inject results without a real ChromaDB.
func handleSearchWithResults(results []db.ChunkResult, query string) (*mcp.CallToolResult, any, error) {
	if len(results) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: "No results found for query: " + query},
			},
		}, nil, nil
	}

	var sb strings.Builder
	for _, r := range results {
		fmt.Fprintf(&sb, "## %s:%s (lines %d–%d)\n", r.Repo, r.Path, r.StartLine, r.EndLine)
		fmt.Fprintf(&sb, "```%s\n%s\n```\n\n", r.Language, r.Content)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: sb.String()},
		},
	}, nil, nil
}

// handleListRepos returns a markdown table of all indexed repos.
func handleListRepos(ctx context.Context, client *db.ChromaClient) (*mcp.CallToolResult, any, error) {
	metas, err := client.ListRepoMeta(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list repos: %w", err)
	}
	return formatListReposResult(metas)
}

func formatListReposResult(metas []db.RepoMeta) (*mcp.CallToolResult, any, error) {
	if len(metas) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "No repositories indexed yet."}},
		}, nil, nil
	}

	var sb strings.Builder
	sb.WriteString("| Repo | Branch | Last Commit | Last Indexed | Files | Chunks |\n")
	sb.WriteString("|------|--------|-------------|--------------|-------|--------|\n")
	for _, m := range metas {
		commit := m.LastIndexedCommit
		if len(commit) > 8 {
			commit = commit[:8]
		}
		fmt.Fprintf(&sb, "| %s | %s | %s | %s | %d | %d |\n",
			m.Repo, m.CurrentBranch, commit, m.LastIndexedAt, m.FileCount, m.ChunkCount)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: sb.String()}},
	}, nil, nil
}

type repoFilesParams struct {
	Repo   string `json:"repo"   jsonschema:"required,Repository name"`
	Filter string `json:"filter" jsonschema:"Optional glob pattern to filter file paths (e.g. '*.go')"`
}

// handleGetRepoFiles lists indexed files for a repo, applying an optional glob filter.
func handleGetRepoFiles(ctx context.Context, client *db.ChromaClient, args repoFilesParams) (*mcp.CallToolResult, any, error) {
	if args.Repo == "" {
		return nil, nil, fmt.Errorf("repo parameter is required")
	}
	files, err := client.QueryAllFileMeta(ctx, args.Repo)
	if err != nil {
		return nil, nil, fmt.Errorf("list files: %w", err)
	}

	return formatGetRepoFilesResult(files, args.Filter)
}

func formatGetRepoFilesResult(files []db.FileMeta, filter string) (*mcp.CallToolResult, any, error) {
	var matched []string
	for _, f := range files {
		if filter != "" {
			base := path.Base(f.Path)
			ok, _ := path.Match(filter, base)
			if !ok {
				ok, _ = path.Match(filter, f.Path)
			}
			if !ok {
				continue
			}
		}
		matched = append(matched, f.Path)
	}

	if len(matched) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "No files found."}},
		}, nil, nil
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: strings.Join(matched, "\n")}},
	}, nil, nil
}

type fileContentParams struct {
	Repo string `json:"repo" jsonschema:"required,Repository name"`
	Path string `json:"path" jsonschema:"required,File path relative to repo root (or absolute)"`
}

const maxFileContentBytes = 100 * 1024 // 100 KB

// handleGetFileContent reads a file from disk, resolving the path via repo metadata.
func handleGetFileContent(ctx context.Context, client *db.ChromaClient, args fileContentParams) (*mcp.CallToolResult, any, error) {
	if args.Repo == "" || args.Path == "" {
		return nil, nil, fmt.Errorf("repo and path parameters are required")
	}

	absPath := args.Path
	if !filepath.IsAbs(absPath) {
		meta, err := client.GetRepoMeta(ctx, args.Repo)
		if err != nil || meta == nil {
			return nil, nil, fmt.Errorf("repo %q not found", args.Repo)
		}
		absPath = filepath.Join(meta.RootPath, args.Path)
	}

	// Guard against path traversal.
	absPath = filepath.Clean(absPath)

	info, err := os.Stat(absPath)
	if err != nil {
		return nil, nil, fmt.Errorf("file not found: %w", err)
	}
	if info.Size() > maxFileContentBytes {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{
				Text: fmt.Sprintf("File is too large (%d bytes > 100 KB limit). Use search_codebase instead.", info.Size()),
			}},
		}, nil, nil
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read file: %w", err)
	}

	ext := strings.TrimPrefix(filepath.Ext(absPath), ".")
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{
			Text: fmt.Sprintf("```%s\n%s\n```", ext, string(data)),
		}},
	}, nil, nil
}

type repoParams struct {
	Repo string `json:"repo" jsonschema:"required,Repository name"`
}

func handleGitStatus(ctx context.Context, client *db.ChromaClient, args repoParams) (*mcp.CallToolResult, any, error) {
	if args.Repo == "" {
		return nil, nil, fmt.Errorf("repo parameter is required")
	}
	meta, err := client.GetRepoMeta(ctx, args.Repo)
	if err != nil {
		return nil, nil, fmt.Errorf("get repo meta: %w", err)
	}

	out, err := git.RunGit(meta.RootPath, "status", "--short", "--branch")
	if err != nil {
		return nil, nil, fmt.Errorf("git status: %w", err)
	}

	headCommit, _ := git.HeadCommit(meta.RootPath)
	stalenessLine := fmt.Sprintf("\n[Index Staleness]\nLast Indexed Commit: %s\nCurrent HEAD:        %s", meta.LastIndexedCommit, headCommit)
	if headCommit != meta.LastIndexedCommit {
		stalenessLine += "\nStatus: STALE (Needs re-indexing)"
	} else {
		stalenessLine += "\nStatus: UP TO DATE"
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: strings.TrimSpace(out) + "\n" + stalenessLine}},
	}, nil, nil
}

func handleGitDiff(ctx context.Context, client *db.ChromaClient, args repoParams) (*mcp.CallToolResult, any, error) {
	if args.Repo == "" {
		return nil, nil, fmt.Errorf("repo parameter is required")
	}
	meta, err := client.GetRepoMeta(ctx, args.Repo)
	if err != nil {
		return nil, nil, fmt.Errorf("get repo meta: %w", err)
	}

	out, err := git.RunGit(meta.RootPath, "diff", meta.LastIndexedCommit, "HEAD")
	if err != nil {
		return nil, nil, fmt.Errorf("git diff: %w", err)
	}

	if strings.TrimSpace(out) == "" {
		out = "No diff between HEAD and last indexed commit (" + meta.LastIndexedCommit + ")"
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: out}},
	}, nil, nil
}

func handleGitLog(ctx context.Context, client *db.ChromaClient, args repoParams) (*mcp.CallToolResult, any, error) {
	if args.Repo == "" {
		return nil, nil, fmt.Errorf("repo parameter is required")
	}
	meta, err := client.GetRepoMeta(ctx, args.Repo)
	if err != nil {
		return nil, nil, fmt.Errorf("get repo meta: %w", err)
	}

	// Show last 10 commits
	out, err := git.RunGit(meta.RootPath, "log", "-n", "10", "--oneline")
	if err != nil {
		return nil, nil, fmt.Errorf("git log: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: out}},
	}, nil, nil
}

func handleIndexStatus(ctx context.Context, client *db.ChromaClient) (*mcp.CallToolResult, any, error) {
	metas, err := client.ListRepoMeta(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list repos: %w", err)
	}

	if len(metas) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "No repositories indexed yet."}},
		}, nil, nil
	}

	var sb strings.Builder
	sb.WriteString("| Repo | Mode | Duration (ms) | Last Indexed At |\n")
	sb.WriteString("|------|------|---------------|-----------------|\n")
	for _, m := range metas {
		fmt.Fprintf(&sb, "| %s | %s | %d | %s |\n", m.Repo, m.IndexMode, m.DurationMs, m.LastIndexedAt)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: sb.String()}},
	}, nil, nil
}
