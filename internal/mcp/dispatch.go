package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ramayac/omni-code/internal/db"
)

// SimpleTool is a lightweight tool definition usable outside the MCP protocol
// (e.g., by the chat package to build OpenAI function-calling tool definitions).
type SimpleTool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// ToolDefinitions returns the list of available tools as simple structs
// that can be converted to any format (OpenAI, Anthropic, etc.).
func ToolDefinitions() []SimpleTool {
	return []SimpleTool{
		{
			Name:        "search_codebase",
			Description: "Semantic search across all indexed local codebases. Returns relevant code chunks with their file path and line numbers.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"The natural-language search query"},"repo":{"type":"string","description":"Filter results to a specific repository name (optional)"},"n_results":{"type":"integer","description":"Number of results to return (default 10)"}},"required":["query"]}`),
		},
		{
			Name:        "list_repos",
			Description: "List all indexed repositories with stats (branch, last commit, file count, chunk count).",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		},
		{
			Name:        "get_repo_files",
			Description: "List files indexed for a repository, with optional glob filter.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"repo":{"type":"string","description":"Repository name"},"filter":{"type":"string","description":"Optional glob pattern to filter file paths (e.g. '*.go')"}},"required":["repo"]}`),
		},
		{
			Name:        "get_file_content",
			Description: "Read the raw content of a file from disk (max 100 KB).",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"repo":{"type":"string","description":"Repository name"},"path":{"type":"string","description":"File path relative to repo root (or absolute)"}},"required":["repo","path"]}`),
		},
		{
			Name:        "git_status",
			Description: "Show branch, uncommitted changes, and index staleness for a repository.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"repo":{"type":"string","description":"Repository name"}},"required":["repo"]}`),
		},
		{
			Name:        "git_diff",
			Description: "Show diff between current state and last indexed commit.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"repo":{"type":"string","description":"Repository name"}},"required":["repo"]}`),
		},
		{
			Name:        "git_log",
			Description: "Show recent commit history for a repository.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"repo":{"type":"string","description":"Repository name"}},"required":["repo"]}`),
		},
		{
			Name:        "index_status",
			Description: "Detailed breakdown of when/how each repo was last indexed.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		},
		{
			Name:        "grep_codebase",
			Description: "Grep indexed files for a regex pattern. Returns matching lines with file paths and line numbers.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string","description":"RE2 regex pattern to search for"},"repo":{"type":"string","description":"Restrict search to this repository (optional)"},"file_filter":{"type":"string","description":"Optional glob to filter file paths (e.g. '*.go')"},"max_results":{"type":"integer","description":"Maximum number of matching lines to return (default 50)"}},"required":["pattern"]}`),
		},
		{
			Name:        "get_file_symbols",
			Description: "List top-level AST symbols (functions, classes, types, etc.) in a file using tree-sitter.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"repo":{"type":"string","description":"Repository name"},"path":{"type":"string","description":"File path relative to repo root (or absolute)"}},"required":["repo","path"]}`),
		},
		{
			Name:        "reindex_repo",
			Description: "Trigger an incremental (or full) re-index of an already-registered repository.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"repo":{"type":"string","description":"Repository name to re-index"},"full":{"type":"boolean","description":"If true, drop all existing data before indexing (default: incremental)"}},"required":["repo"]}`),
		},
		{
			Name:        "get_repo_summary",
			Description: "Return a rich Markdown summary of a repository: metadata, language distribution, top-level directory overview, and recent git log.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"repo":{"type":"string","description":"Repository name"}},"required":["repo"]}`),
		},
		{
			Name:        "search_repo_summaries",
			Description: "List all indexed repositories with a compact summary of each (language breakdown, file count, recent activity).",
			InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		},
		{
			Name:        "get_top_contributors",
			Description: "Return a ranked leaderboard of git contributors for a repository.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"repo":{"type":"string","description":"Repository name"},"since":{"type":"string","description":"Optional time window for git shortlog (e.g. '6 months ago', '2026-01-01')"}},"required":["repo"]}`),
		},
	}
}

// DispatchTool routes a tool call to the appropriate handler and returns the
// text result. Used by the chat package to execute tools outside the MCP protocol.
func DispatchTool(ctx context.Context, client *db.ChromaClient, name, argsJSON string) (string, error) {
	switch name {
	case "search_codebase":
		var args searchParams
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("parse args: %w", err)
		}
		result, _, err := handleSearch(ctx, client, args)
		return extractText(result, err)

	case "list_repos":
		result, _, err := handleListRepos(ctx, client)
		return extractText(result, err)

	case "get_repo_files":
		var args repoFilesParams
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("parse args: %w", err)
		}
		result, _, err := handleGetRepoFiles(ctx, client, args)
		return extractText(result, err)

	case "get_file_content":
		var args fileContentParams
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("parse args: %w", err)
		}
		result, _, err := handleGetFileContent(ctx, client, args)
		return extractText(result, err)

	case "git_status":
		var args repoParams
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("parse args: %w", err)
		}
		result, _, err := handleGitStatus(ctx, client, args)
		return extractText(result, err)

	case "git_diff":
		var args repoParams
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("parse args: %w", err)
		}
		result, _, err := handleGitDiff(ctx, client, args)
		return extractText(result, err)

	case "git_log":
		var args repoParams
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("parse args: %w", err)
		}
		result, _, err := handleGitLog(ctx, client, args)
		return extractText(result, err)

	case "index_status":
		result, _, err := handleIndexStatus(ctx, client)
		return extractText(result, err)

	case "grep_codebase":
		var args grepParams
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("parse args: %w", err)
		}
		result, _, err := handleGrep(ctx, client, args)
		return extractText(result, err)

	case "get_file_symbols":
		var args fileContentParams
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("parse args: %w", err)
		}
		result, _, err := handleGetFileSymbols(ctx, client, args)
		return extractText(result, err)

	case "reindex_repo":
		var args reindexParams
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("parse args: %w", err)
		}
		result, _, err := handleReindexRepo(ctx, client, args)
		return extractText(result, err)

	case "get_repo_summary":
		var args repoParams
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("parse args: %w", err)
		}
		result, _, err := handleGetRepoSummary(ctx, client, args)
		return extractText(result, err)

	case "search_repo_summaries":
		result, _, err := handleSearchRepoSummaries(ctx, client)
		return extractText(result, err)

	case "get_top_contributors":
		var args topContributorsParams
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("parse args: %w", err)
		}
		result, _, err := handleGetTopContributors(ctx, client, args)
		return extractText(result, err)

	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

// extractText pulls all text from an MCP CallToolResult.
func extractText(result *gomcp.CallToolResult, err error) (string, error) {
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", nil
	}
	var texts []string
	for _, c := range result.Content {
		if tc, ok := c.(*gomcp.TextContent); ok {
			texts = append(texts, tc.Text)
		}
	}
	return strings.Join(texts, "\n"), nil
}
