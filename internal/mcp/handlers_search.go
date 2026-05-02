package mcp

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ramayac/omni-code/internal/db"
)

const maxFileContentBytes = 100 * 1024 // 100 KB

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
			absPath = filepath.Clean(absPath)

			file, err := os.Open(absPath)
			if err != nil {
				continue
			}

			scanner := bufio.NewScanner(file)
			// Use a larger buffer for long lines.
			scanner.Buffer(make([]byte, 64*1024), 1024*1024)
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
						file.Close()
						break repoLoop
					}
				}
			}
			file.Close()
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
				Text: fmt.Sprintf("File is too large (%d bytes > %d KB limit). Use search_codebase instead.", info.Size(), maxFileContentBytes/1024),
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
