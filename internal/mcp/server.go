package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ramayac/omni-code/internal/chunker"
	"github.com/ramayac/omni-code/internal/db"
	"github.com/ramayac/omni-code/internal/git"
	"github.com/ramayac/omni-code/internal/indexer"
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

// ---------------------------------------------------------------------------
// Phase 5 handlers
// ---------------------------------------------------------------------------

type grepParams struct {
	Pattern    string `json:"pattern"     jsonschema:"required,RE2 regex pattern to search for"`
	Repo       string `json:"repo"        jsonschema:"Restrict search to this repository (optional)"`
	FileFilter string `json:"file_filter" jsonschema:"Optional glob pattern to filter file paths (e.g. '*.go')"`
	MaxResults int    `json:"max_results" jsonschema:"Maximum number of matching lines to return (default 50)"`
}

// handleGrep searches all indexed files for lines matching a regex pattern.
func handleGrep(ctx context.Context, client *db.ChromaClient, args grepParams) (*mcp.CallToolResult, any, error) {
	if args.Pattern == "" {
		return nil, nil, fmt.Errorf("pattern parameter is required")
	}

	re, err := regexp.Compile(args.Pattern)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid regex pattern: %w", err)
	}

	maxResults := args.MaxResults
	if maxResults <= 0 {
		maxResults = 50
	}

	// Determine which repos to scan.
	var repoNames []string
	if args.Repo != "" {
		repoNames = []string{args.Repo}
	} else {
		metas, err := client.ListRepoMeta(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("list repos: %w", err)
		}
		for _, m := range metas {
			repoNames = append(repoNames, m.Repo)
		}
	}

	type match struct {
		repo string
		file string
		line int
		text string
	}
	var matches []match

repoLoop:
	for _, repoName := range repoNames {
		meta, err := client.GetRepoMeta(ctx, repoName)
		if err != nil || meta == nil {
			continue
		}
		files, err := client.QueryAllFileMeta(ctx, repoName)
		if err != nil {
			continue
		}
		for _, f := range files {
			// Apply optional glob filter.
			if args.FileFilter != "" {
				base := path.Base(f.Path)
				ok, _ := path.Match(args.FileFilter, base)
				if !ok {
					ok, _ = path.Match(args.FileFilter, f.Path)
				}
				if !ok {
					continue
				}
			}

			absPath := f.Path
			if !filepath.IsAbs(absPath) {
				absPath = filepath.Join(meta.RootPath, f.Path)
			}

			data, err := os.ReadFile(filepath.Clean(absPath))
			if err != nil {
				continue
			}

			scanner := bufio.NewScanner(bytes.NewReader(data))
			lineNum := 0
			for scanner.Scan() {
				lineNum++
				line := scanner.Text()
				if re.MatchString(line) {
					matches = append(matches, match{
						repo: repoName,
						file: f.Path,
						line: lineNum,
						text: line,
					})
					if len(matches) >= maxResults {
						break repoLoop
					}
				}
			}
		}
	}

	if len(matches) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("No matches found for pattern: %s", args.Pattern)}},
		}, nil, nil
	}

	var sb strings.Builder
	for _, m := range matches {
		fmt.Fprintf(&sb, "%s:%s:%d: %s\n", m.repo, m.file, m.line, m.text)
	}
	if len(matches) == maxResults {
		fmt.Fprintf(&sb, "\n(results capped at %d — narrow with repo or file_filter)", maxResults)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: sb.String()}},
	}, nil, nil
}

// handleGetFileSymbols extracts top-level AST symbols from a file via tree-sitter.
func handleGetFileSymbols(ctx context.Context, client *db.ChromaClient, args fileContentParams) (*mcp.CallToolResult, any, error) {
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
	absPath = filepath.Clean(absPath)

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, nil, fmt.Errorf("file not found: %w", err)
	}

	lang := indexer.DetectLanguage(absPath)
	symbols := chunker.ExtractSymbols(string(data), lang)

	if len(symbols) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("No symbols found in %s (language: %s)", args.Path, lang)}},
		}, nil, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Symbols in %s:%s\n\n", args.Repo, args.Path)
	fmt.Fprintf(&sb, "| Name | Kind | Lines |\n")
	fmt.Fprintf(&sb, "|------|------|-------|\n")
	for _, sym := range symbols {
		name := sym.Name
		if name == "" {
			name = "(anonymous)"
		}
		fmt.Fprintf(&sb, "| %s | %s | %d–%d |\n", name, sym.Kind, sym.StartLine, sym.EndLine)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: sb.String()}},
	}, nil, nil
}

type reindexParams struct {
	Repo string `json:"repo" jsonschema:"required,Repository name to re-index"`
	Full bool   `json:"full" jsonschema:"If true, drop all existing data before indexing (default: incremental)"`
}

// handleReindexRepo triggers an incremental or full re-index of a registered repository.
func handleReindexRepo(ctx context.Context, client *db.ChromaClient, args reindexParams) (*mcp.CallToolResult, any, error) {
	if args.Repo == "" {
		return nil, nil, fmt.Errorf("repo parameter is required")
	}

	meta, err := client.GetRepoMeta(ctx, args.Repo)
	if err != nil || meta == nil {
		return nil, nil, fmt.Errorf("repo %q not found in index", args.Repo)
	}

	if args.Full {
		if err := client.DeleteRepoChunks(ctx, args.Repo); err != nil {
			log.Printf("[mcp] reindex_repo: delete chunks for %s: %v", args.Repo, err)
		}
		if err := client.DeleteRepoFileMeta(ctx, args.Repo); err != nil {
			log.Printf("[mcp] reindex_repo: delete file meta for %s: %v", args.Repo, err)
		}
	}

	idxCfg := indexer.IndexerConfig{
		RootPath:   meta.RootPath,
		RepoName:   args.Repo,
		DB:         client,
		ChunkFn:    chunker.ChunkFile,
		SeenHashes: &sync.Map{},
	}

	stats, err := indexer.RunIndex(ctx, idxCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("reindex failed: %w", err)
	}

	mode := "incremental"
	if args.Full {
		mode = "full"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "## Reindex Complete: %s (%s)\n\n", args.Repo, mode)
	fmt.Fprintf(&sb, "| Metric | Value |\n")
	fmt.Fprintf(&sb, "|--------|-------|\n")
	fmt.Fprintf(&sb, "| Branch | %s |\n", stats.Branch)
	fmt.Fprintf(&sb, "| Last Commit | %s |\n", stats.LastCommit)
	fmt.Fprintf(&sb, "| Files Scanned | %d |\n", stats.FilesScanned)
	fmt.Fprintf(&sb, "| Files Changed | %d |\n", stats.FilesChanged)
	fmt.Fprintf(&sb, "| Files Unchanged | %d |\n", stats.FilesUnchanged)
	fmt.Fprintf(&sb, "| Files Deleted | %d |\n", stats.DeletedFiles)
	fmt.Fprintf(&sb, "| Chunks Upserted | %d |\n", stats.ChunksUpserted)
	fmt.Fprintf(&sb, "| Errors | %d |\n", stats.Errors)

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: sb.String()}},
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

// ---------------------------------------------------------------------------
// get_repo_summary handler
// ---------------------------------------------------------------------------

// handleGetRepoSummary returns a rich Markdown summary of a repository including
// metadata, language distribution, top-level directory breakdown, and recent git log.
// It derives language from indexed file paths using indexer.DetectLanguage so no
// additional embeddings or LLM calls are required.
func handleGetRepoSummary(ctx context.Context, client *db.ChromaClient, args repoParams) (*mcp.CallToolResult, any, error) {
if args.Repo == "" {
return nil, nil, fmt.Errorf("repo parameter is required")
}

meta, err := client.GetRepoMeta(ctx, args.Repo)
if err != nil || meta == nil {
return nil, nil, fmt.Errorf("repo %q not found in index", args.Repo)
}

files, err := client.QueryAllFileMeta(ctx, args.Repo)
if err != nil {
return nil, nil, fmt.Errorf("list files: %w", err)
}

return formatRepoSummary(meta, files, args.Repo)
}

// formatRepoSummary builds the Markdown summary from repo metadata and file list.
// Extracted for testability so tests can inject values without a real ChromaDB.
func formatRepoSummary(meta *db.RepoMeta, files []db.FileMeta, repoName string) (*mcp.CallToolResult, any, error) {
var sb strings.Builder

// --- Metadata block ---
commit := meta.LastIndexedCommit
if len(commit) > 8 {
commit = commit[:8]
}
fmt.Fprintf(&sb, "# Repository Summary: %s\n\n", repoName)
fmt.Fprintf(&sb, "| Field | Value |\n")
fmt.Fprintf(&sb, "|-------|-------|\n")
fmt.Fprintf(&sb, "| Branch | %s |\n", meta.CurrentBranch)
fmt.Fprintf(&sb, "| Last Indexed Commit | %s |\n", commit)
fmt.Fprintf(&sb, "| Last Indexed At | %s |\n", meta.LastIndexedAt)
fmt.Fprintf(&sb, "| Total Files | %d |\n", meta.FileCount)
fmt.Fprintf(&sb, "| Total Chunks | %d |\n", meta.ChunkCount)
fmt.Fprintf(&sb, "| Index Mode | %s |\n", meta.IndexMode)
sb.WriteString("\n")

// --- Language distribution ---
langCount := make(map[string]int)
dirCount := make(map[string]int)
for _, f := range files {
lang := indexer.DetectLanguage(f.Path)
if lang == "" {
lang = "other"
}
langCount[lang]++

// Capture top-level directory (first path segment).
dir := path.Dir(f.Path)
if dir == "." || dir == "" {
dir = "(root)"
} else {
// Use only the first segment for top-level grouping.
parts := strings.SplitN(dir, "/", 2)
dir = parts[0]
}
dirCount[dir]++
}

if len(langCount) > 0 {
sb.WriteString("## Language Distribution\n\n")
// Sort languages by count descending.
type langEntry struct {
lang  string
count int
}
langs := make([]langEntry, 0, len(langCount))
total := len(files)
for l, c := range langCount {
langs = append(langs, langEntry{l, c})
}
// Simple insertion sort (small N).
for i := 1; i < len(langs); i++ {
for j := i; j > 0 && langs[j].count > langs[j-1].count; j-- {
langs[j], langs[j-1] = langs[j-1], langs[j]
}
}
sb.WriteString("| Language | Files | Share |\n")
sb.WriteString("|----------|-------|-------|\n")
for _, e := range langs {
pct := 0
if total > 0 {
pct = e.count * 100 / total
}
filled := pct / 10
if filled == 0 && pct > 0 {
filled = 1 // show at least one block for non-zero shares
}
bar := strings.Repeat("█", filled) + strings.Repeat("░", 10-filled)
fmt.Fprintf(&sb, "| %s | %d | %s %d%% |\n", e.lang, e.count, bar, pct)
}
sb.WriteString("\n")
}

// --- Top-level directory overview ---
if len(dirCount) > 0 {
sb.WriteString("## Top-Level Directories\n\n")
type dirEntry struct {
dir   string
count int
}
dirs := make([]dirEntry, 0, len(dirCount))
for d, c := range dirCount {
dirs = append(dirs, dirEntry{d, c})
}
for i := 1; i < len(dirs); i++ {
for j := i; j > 0 && dirs[j].count > dirs[j-1].count; j-- {
dirs[j], dirs[j-1] = dirs[j-1], dirs[j]
}
}
sb.WriteString("| Directory | Files |\n")
sb.WriteString("|-----------|-------|\n")
for _, e := range dirs {
fmt.Fprintf(&sb, "| %s | %d |\n", e.dir, e.count)
}
sb.WriteString("\n")
}

// --- Recent git log ---
if meta.RootPath != "" {
gitOut, err := git.RunGit(meta.RootPath, "log", "-n", "10", "--oneline")
if err == nil && strings.TrimSpace(gitOut) != "" {
sb.WriteString("## Recent Commits\n\n")
sb.WriteString("```\n")
sb.WriteString(strings.TrimSpace(gitOut))
sb.WriteString("\n```\n")
}
}

return &mcp.CallToolResult{
Content: []mcp.Content{&mcp.TextContent{Text: sb.String()}},
}, nil, nil
}

// ---------------------------------------------------------------------------
// search_repo_summaries handler
// ---------------------------------------------------------------------------

// handleSearchRepoSummaries returns a compact Markdown card for every indexed
// repository so the AI can quickly identify which repo to target for a task.
func handleSearchRepoSummaries(ctx context.Context, client *db.ChromaClient) (*mcp.CallToolResult, any, error) {
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
sb.WriteString("# All Repository Summaries\n\n")

for _, meta := range metas {
files, err := client.QueryAllFileMeta(ctx, meta.Repo)
if err != nil {
log.Printf("[mcp] search_repo_summaries: list files for %s: %v", meta.Repo, err)
}

// Build language distribution (top 3 languages only for brevity).
langCount := make(map[string]int)
for _, f := range files {
lang := indexer.DetectLanguage(f.Path)
if lang == "" {
lang = "other"
}
langCount[lang]++
}
type langEntry struct {
lang  string
count int
}
langs := make([]langEntry, 0, len(langCount))
for l, c := range langCount {
langs = append(langs, langEntry{l, c})
}
for i := 1; i < len(langs); i++ {
for j := i; j > 0 && langs[j].count > langs[j-1].count; j-- {
langs[j], langs[j-1] = langs[j-1], langs[j]
}
}
topLangs := make([]string, 0, 3)
for i, e := range langs {
if i >= 3 {
break
}
topLangs = append(topLangs, fmt.Sprintf("%s(%d)", e.lang, e.count))
}

commit := meta.LastIndexedCommit
if len(commit) > 8 {
commit = commit[:8]
}
fmt.Fprintf(&sb, "## %s\n", meta.Repo)
fmt.Fprintf(&sb, "- **Branch**: %s  **Commit**: %s\n", meta.CurrentBranch, commit)
fmt.Fprintf(&sb, "- **Files**: %d  **Chunks**: %d\n", meta.FileCount, meta.ChunkCount)
if len(topLangs) > 0 {
fmt.Fprintf(&sb, "- **Languages**: %s\n", strings.Join(topLangs, ", "))
}
fmt.Fprintf(&sb, "- **Last Indexed**: %s\n\n", meta.LastIndexedAt)
}

return &mcp.CallToolResult{
Content: []mcp.Content{&mcp.TextContent{Text: sb.String()}},
}, nil, nil
}

// ---------------------------------------------------------------------------
// get_top_contributors handler
// ---------------------------------------------------------------------------

type topContributorsParams struct {
Repo  string `json:"repo"  jsonschema:"required,Repository name"`
Since string `json:"since" jsonschema:"Optional time window for git shortlog (e.g. '6 months ago', '2026-01-01')"`
}

// handleGetTopContributors returns a ranked contributor leaderboard for a repository.
func handleGetTopContributors(ctx context.Context, client *db.ChromaClient, args topContributorsParams) (*mcp.CallToolResult, any, error) {
if args.Repo == "" {
return nil, nil, fmt.Errorf("repo parameter is required")
}

meta, err := client.GetRepoMeta(ctx, args.Repo)
if err != nil || meta == nil {
return nil, nil, fmt.Errorf("repo %q not found in index", args.Repo)
}

gitArgs := []string{"shortlog", "-sn", "--no-merges", "HEAD"}
if args.Since != "" {
gitArgs = append(gitArgs, "--since="+args.Since)
}

out, err := git.RunGit(meta.RootPath, gitArgs...)
if err != nil {
return nil, nil, fmt.Errorf("git shortlog: %w", err)
}

lines := strings.Split(strings.TrimSpace(out), "\n")
if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
return &mcp.CallToolResult{
Content: []mcp.Content{&mcp.TextContent{Text: "No contributors found."}},
}, nil, nil
}

var sb strings.Builder
title := fmt.Sprintf("## Top Contributors: %s", args.Repo)
if args.Since != "" {
title += fmt.Sprintf(" (since %s)", args.Since)
}
fmt.Fprintln(&sb, title)
fmt.Fprintln(&sb)
sb.WriteString("| Rank | Commits | Author |\n")
sb.WriteString("|------|---------|--------|\n")

for i, line := range lines {
line = strings.TrimSpace(line)
if line == "" {
continue
}
// git shortlog -sn output format: "   42\tAuthor Name"
parts := strings.SplitN(line, "\t", 2)
if len(parts) != 2 {
continue
}
commits := strings.TrimSpace(parts[0])
author := strings.TrimSpace(parts[1])
fmt.Fprintf(&sb, "| %d | %s | %s |\n", i+1, commits, author)
}

return &mcp.CallToolResult{
Content: []mcp.Content{&mcp.TextContent{Text: sb.String()}},
}, nil, nil
}
