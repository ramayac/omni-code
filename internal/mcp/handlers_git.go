package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ramayac/omni-code/internal/db"
	"github.com/ramayac/omni-code/internal/git"
)

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

	out, err := git.RunGit(meta.RootPath, "log", "-n", "10", "--oneline")
	if err != nil {
		return nil, nil, fmt.Errorf("git log: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: out}},
	}, nil, nil
}

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
