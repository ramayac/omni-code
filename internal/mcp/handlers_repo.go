package mcp

import (
	"context"
	"fmt"
	"log"
	"path"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ramayac/omni-code/internal/db"
	"github.com/ramayac/omni-code/internal/git"
	"github.com/ramayac/omni-code/internal/indexer"
)

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

// handleGetRepoSummary returns a rich Markdown summary of a repository including
// metadata, language distribution, top-level directory breakdown, and recent git log.
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
	langCount, dirCount := countLangsAndDirs(files)

	if len(langCount) > 0 {
		sb.WriteString("## Language Distribution\n\n")
		langs := sortLangEntries(langCount)
		total := len(files)
		sb.WriteString("| Language | Files | Share |\n")
		sb.WriteString("|----------|-------|-------|\n")
		for _, e := range langs {
			pct := 0
			if total > 0 {
				pct = e.count * 100 / total
			}
			filled := pct / 10
			if filled == 0 && pct > 0 {
				filled = 1
			}
			bar := strings.Repeat("█", filled) + strings.Repeat("░", 10-filled)
			fmt.Fprintf(&sb, "| %s | %d | %s %d%% |\n", e.lang, e.count, bar, pct)
		}
		sb.WriteString("\n")
	}

	// --- Top-level directory overview ---
	if len(dirCount) > 0 {
		sb.WriteString("## Top-Level Directories\n\n")
		dirs := sortDirEntries(dirCount)
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

// handleSearchRepoSummaries returns a compact Markdown card for every indexed repository.
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

		langCount, _ := countLangsAndDirs(files)
		langs := sortLangEntries(langCount)
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

// --- helper types and functions ---

type langEntry struct {
	lang  string
	count int
}

type dirEntry struct {
	dir   string
	count int
}

func countLangsAndDirs(files []db.FileMeta) (map[string]int, map[string]int) {
	langCount := make(map[string]int)
	dirCount := make(map[string]int)
	for _, f := range files {
		lang := indexer.DetectLanguage(f.Path)
		if lang == "" {
			lang = "other"
		}
		langCount[lang]++

		dir := path.Dir(f.Path)
		if dir == "." || dir == "" {
			dir = "(root)"
		} else {
			parts := strings.SplitN(dir, "/", 2)
			dir = parts[0]
		}
		dirCount[dir]++
	}
	return langCount, dirCount
}

func sortLangEntries(langCount map[string]int) []langEntry {
	langs := make([]langEntry, 0, len(langCount))
	for l, c := range langCount {
		langs = append(langs, langEntry{l, c})
	}
	for i := 1; i < len(langs); i++ {
		for j := i; j > 0 && langs[j].count > langs[j-1].count; j-- {
			langs[j], langs[j-1] = langs[j-1], langs[j]
		}
	}
	return langs
}

func sortDirEntries(dirCount map[string]int) []dirEntry {
	dirs := make([]dirEntry, 0, len(dirCount))
	for d, c := range dirCount {
		dirs = append(dirs, dirEntry{d, c})
	}
	for i := 1; i < len(dirs); i++ {
		for j := i; j > 0 && dirs[j].count > dirs[j-1].count; j-- {
			dirs[j], dirs[j-1] = dirs[j-1], dirs[j]
		}
	}
	return dirs
}
