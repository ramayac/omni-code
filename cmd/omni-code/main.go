package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"

	defaultef "github.com/amikos-tech/chroma-go/pkg/embeddings/default_ef"

	"github.com/ramayac/omni-code/internal/chunker"
	"github.com/ramayac/omni-code/internal/db"
	"github.com/ramayac/omni-code/internal/indexer"
	internalmcp "github.com/ramayac/omni-code/internal/mcp"
)

func main() {
	log.SetOutput(os.Stderr)
	log.SetFlags(log.LstdFlags)

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "index":
		runIndex(os.Args[2:])
	case "search":
		runSearch(os.Args[2:])
	case "mcp":
		runMCP(os.Args[2:])
	case "--help", "-h", "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `omni-code — local codebase indexer and MCP server

Usage:
  omni-code <command> [flags]

Commands:
  index   Scan and index a repository into ChromaDB
  search  Query indexed code from the terminal
  mcp     Start the MCP stdio server for Copilot

Run 'omni-code <command> --help' for command-specific flags.
`)
}

// applyLogFlags adjusts the global logger based on --verbose / --quiet flags.
func applyLogFlags(verbose, quiet bool) {
	if quiet {
		log.SetOutput(io.Discard)
		return
	}
	if verbose {
		log.SetFlags(log.LstdFlags | log.Lshortfile)
	}
}

// newClientAndCollections creates a ChromaClient and ensures collections exist.
func newClientAndCollections(ctx context.Context, baseURL string) (*db.ChromaClient, error) {
	client, err := db.NewChromaClient(ctx, baseURL)
	if err != nil {
		return nil, err
	}
	ef, _, err := defaultef.NewDefaultEmbeddingFunction()
	if err != nil {
		return nil, fmt.Errorf("create embedding function: %w", err)
	}
	if err := client.EnsureCollections(ctx, ef); err != nil {
		return nil, fmt.Errorf("ensure collections: %w", err)
	}
	return client, nil
}

func runIndex(args []string) {
	fs := flag.NewFlagSet("index", flag.ExitOnError)
	name := fs.String("name", "", "repository name (required)")
	dbURL := fs.String("db", "http://localhost:8000", "ChromaDB base URL")
	verbose := fs.Bool("verbose", false, "enable verbose logging")
	quiet := fs.Bool("quiet", false, "suppress all log output")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: omni-code index --name <repo-name> [--db <url>] [--verbose] [--quiet] <path>\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}
	applyLogFlags(*verbose, *quiet)

	if *name == "" {
		fmt.Fprintln(os.Stderr, "error: --name is required")
		fs.Usage()
		os.Exit(1)
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "error: path argument is required")
		fs.Usage()
		os.Exit(1)
	}
	repoPath := fs.Arg(0)

	ctx := context.Background()
	log.Printf("[index] connecting to ChromaDB at %s", *dbURL)
	client, err := newClientAndCollections(ctx, *dbURL)
	if err != nil {
		log.Fatalf("[index] %v", err)
	}

	cfg := indexer.IndexerConfig{
		RootPath: repoPath,
		RepoName: *name,
		DB:       client,
		ChunkFn:  chunker.ChunkFile,
	}

	log.Printf("[index] starting index of %q (repo=%s)", repoPath, *name)
	stats, err := indexer.RunIndex(ctx, cfg)
	if err != nil {
		log.Fatalf("[index] %v", err)
	}

	fmt.Fprintf(os.Stderr, "\n[index] complete:\n")
	fmt.Fprintf(os.Stderr, "  files scanned:   %d\n", stats.FilesScanned)
	fmt.Fprintf(os.Stderr, "  files changed:   %d\n", stats.FilesChanged)
	fmt.Fprintf(os.Stderr, "  files unchanged: %d\n", stats.FilesUnchanged)
	fmt.Fprintf(os.Stderr, "  dedup skipped:   %d\n", stats.FilesDedupSkip)
	fmt.Fprintf(os.Stderr, "  chunks upserted: %d\n", stats.ChunksUpserted)
	fmt.Fprintf(os.Stderr, "  errors:          %d\n", stats.Errors)
}

func runSearch(args []string) {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	query := fs.String("query", "", "search query (required)")
	repo := fs.String("repo", "", "filter results to a specific repository (optional)")
	n := fs.Int("n", 10, "number of results to return")
	dbURL := fs.String("db", "http://localhost:8000", "ChromaDB base URL")
	verbose := fs.Bool("verbose", false, "enable verbose logging")
	quiet := fs.Bool("quiet", false, "suppress all log output")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: omni-code search --query <text> [--repo <name>] [--n <count>] [--db <url>] [--verbose] [--quiet]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}
	applyLogFlags(*verbose, *quiet)

	if *query == "" {
		fmt.Fprintln(os.Stderr, "error: --query is required")
		fs.Usage()
		os.Exit(1)
	}

	ctx := context.Background()
	client, err := newClientAndCollections(ctx, *dbURL)
	if err != nil {
		log.Fatalf("[search] %v", err)
	}

	results, err := client.QueryChunks(ctx, *query, *n, *repo)
	if err != nil {
		log.Fatalf("[search] query failed: %v", err)
	}

	if len(results) == 0 {
		fmt.Fprintln(os.Stderr, "no results found")
		return
	}

	for i, r := range results {
		fmt.Printf("## %d. %s:%s (lines %d–%d)\n", i+1, r.Repo, r.Path, r.StartLine, r.EndLine)
		fmt.Printf("```%s\n%s\n```\n\n", r.Language, r.Content)
	}
}

func runMCP(args []string) {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	dbURL := fs.String("db", "http://localhost:8000", "ChromaDB base URL")
	verbose := fs.Bool("verbose", false, "enable verbose logging")
	quiet := fs.Bool("quiet", false, "suppress all log output")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: omni-code mcp [--db <url>] [--verbose] [--quiet]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}
	applyLogFlags(*verbose, *quiet)

	ctx := context.Background()
	client, err := newClientAndCollections(ctx, *dbURL)
	if err != nil {
		log.Fatalf("[mcp] %v", err)
	}

	if err := internalmcp.ServeStdio(ctx, client); err != nil {
		log.Fatalf("[mcp] server error: %v", err)
	}
}
