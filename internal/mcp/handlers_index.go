package mcp

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ramayac/omni-code/internal/chunker"
	"github.com/ramayac/omni-code/internal/db"
	"github.com/ramayac/omni-code/internal/indexer"
)

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
