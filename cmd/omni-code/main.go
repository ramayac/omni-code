package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	defaultef "github.com/amikos-tech/chroma-go/pkg/embeddings/default_ef"

	"github.com/ramayac/omni-code/internal/chunker"
	"github.com/ramayac/omni-code/internal/config"
	"github.com/ramayac/omni-code/internal/db"
	"github.com/ramayac/omni-code/internal/embedder"
	"github.com/ramayac/omni-code/internal/estimator"
	"github.com/ramayac/omni-code/internal/git"
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
	case "repos":
		runRepos(os.Args[2:])
	case "reset":
		runReset(os.Args[2:])
	case "watch":
		runWatch(os.Args[2:])
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
  index   Scan and index repositories into ChromaDB
  search  Query indexed code from the terminal
  mcp     Start the MCP stdio server for Copilot
  repos   Manage the repository registry (list / add / remove)
  reset   Drop indexed data (--all or --repo <name>)
  watch   Poll for git HEAD changes and re-index automatically

Run 'omni-code <command> --help' for command-specific flags.
`)
}

func applyLogFlags(verbose, quiet bool) {
	if quiet {
		log.SetOutput(io.Discard)
		return
	}
	if verbose {
		log.SetFlags(log.LstdFlags | log.Lshortfile)
	}
}

// newClientAndCollections creates a ChromaClient and ensures all collections exist.
// If embBackend is non-empty and not "chroma-default", an external embedder is wired in.
func newClientAndCollections(ctx context.Context, baseURL, embBackend, embModel, embURL string) (*db.ChromaClient, error) {
	client, err := db.NewChromaClient(ctx, baseURL)
	if err != nil {
		return nil, err
	}

	switch embBackend {
	case "", "chroma-default":
		ef, _, err := defaultef.NewDefaultEmbeddingFunction()
		if err != nil {
			return nil, fmt.Errorf("create default embedding function: %w", err)
		}
		if err := client.EnsureCollections(ctx, ef); err != nil {
			return nil, fmt.Errorf("ensure collections: %w", err)
		}
	default:
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			apiKey = os.Getenv("EMBEDDING_API_KEY")
		}
		ext, err := embedder.NewEmbedder(embBackend, embModel, embURL, apiKey)
		if err != nil {
			return nil, fmt.Errorf("create embedder: %w", err)
		}
		if err := client.EnsureCollections(ctx, nil); err != nil {
			return nil, fmt.Errorf("ensure collections: %w", err)
		}
		client.SetEmbedder(ext)
	}
	return client, nil
}

func printIndexStats(stats *indexer.IndexStats) {
	fmt.Fprintf(os.Stderr, "\n[index] complete (%s):\n", stats.IndexMode)
	fmt.Fprintf(os.Stderr, "  branch:          %s\n", stats.Branch)
	fmt.Fprintf(os.Stderr, "  commit:          %s\n", stats.LastCommit)
	fmt.Fprintf(os.Stderr, "  files scanned:   %d\n", stats.FilesScanned)
	fmt.Fprintf(os.Stderr, "  files changed:   %d\n", stats.FilesChanged)
	fmt.Fprintf(os.Stderr, "  files unchanged: %d\n", stats.FilesUnchanged)
	fmt.Fprintf(os.Stderr, "  files deleted:   %d\n", stats.DeletedFiles)
	fmt.Fprintf(os.Stderr, "  dedup skipped:   %d\n", stats.FilesDedupSkip)
	fmt.Fprintf(os.Stderr, "  chunks upserted: %d\n", stats.ChunksUpserted)
	fmt.Fprintf(os.Stderr, "  errors:          %d\n", stats.Errors)
}

func runIndex(args []string) {
	fs := flag.NewFlagSet("index", flag.ExitOnError)
	name := fs.String("name", "", "repository name (single-repo mode)")
	dbURL := fs.String("db", "http://localhost:8000", "ChromaDB base URL")
	cfgPath := fs.String("config", "", "path to repos.yaml config file")
	repoFilter := fs.String("repo", "", "index only this repo name from the config")
	parallel := fs.Bool("parallel", false, "index multiple repos in parallel (config mode)")
	dryRun := fs.Bool("dry-run", false, "estimate/sort scan order and exit without indexing")
	strictBranch := fs.Bool("strict-branch", false, "fatal if repo is on wrong branch")
	embBackend := fs.String("embedding-backend", "", "embedding backend: chroma-default, ollama, openai, openai-compatible")
	embModel := fs.String("embedding-model", "", "embedding model name")
	embURL := fs.String("embedding-url", "", "embedding service base URL")
	verbose := fs.Bool("verbose", false, "enable verbose logging")
	quiet := fs.Bool("quiet", false, "suppress all log output")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: omni-code index [--config repos.yaml] [--name <name>] [--db <url>] [flags] [path]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}
	applyLogFlags(*verbose, *quiet)

	ctx := context.Background()
	log.Printf("[index] connecting to ChromaDB at %s", *dbURL)
	client, err := newClientAndCollections(ctx, *dbURL, *embBackend, *embModel, *embURL)
	if err != nil {
		log.Fatalf("[index] %v", err)
	}

	if *cfgPath != "" {
		cfg, err := config.Load(*cfgPath)
		if err != nil {
			log.Fatalf("[index] load config: %v", err)
		}

		var repos []config.RepoEntry
		for _, repo := range cfg.Repos {
			if *repoFilter != "" && repo.Name != *repoFilter {
				continue
			}
			repos = append(repos, repo)
		}

		var estimates []*estimator.ScanEstimate
		estByRepo := map[string]*estimator.ScanEstimate{}
		for _, repo := range repos {
			_, exts, names := config.ResolveSkipLists(*cfg, repo)
			last := ""
			meta, err := client.GetRepoMeta(ctx, repo.Name)
			if err == nil && meta != nil {
				last = meta.LastIndexedCommit
			}
			est, err := estimator.Estimate(repo.Path, repo.Name, last, exts, names)
			if err != nil {
				log.Printf("[index] estimate %s: %v", repo.Name, err)
				est = &estimator.ScanEstimate{RepoName: repo.Name, RootPath: repo.Path, ChangedFiles: -1}
			}
			estimates = append(estimates, est)
			estByRepo[repo.Name] = est
		}
		estimator.SortByScore(estimates)

		if *dryRun {
			printDryRunTable(estimates)
			return
		}

		log.Printf("[index] scan estimates (cheapest first):")
		for _, est := range estimates {
			if est.ChangedFiles >= 0 {
				log.Printf("[index] repo=%s files=%d total=%s changed=%d changedBytes=%s score=%s",
					est.RepoName, est.TotalFiles, humanBytes(est.TotalBytes),
					est.ChangedFiles, humanBytes(est.ChangedBytes), humanBytes(est.Score))
			} else {
				log.Printf("[index] repo=%s files=%d total=%s changed=-- changedBytes=-- score=%s (full scan)",
					est.RepoName, est.TotalFiles, humanBytes(est.TotalBytes), humanBytes(est.Score))
			}
		}

		if !*parallel {
			sortedRepos := make([]config.RepoEntry, 0, len(repos))
			byName := map[string]config.RepoEntry{}
			for _, r := range repos {
				byName[r.Name] = r
			}
			for _, e := range estimates {
				if r, ok := byName[e.RepoName]; ok {
					sortedRepos = append(sortedRepos, r)
				}
			}
			repos = sortedRepos
		}

		sharedHashes := &sync.Map{}
		runOne := func(repo config.RepoEntry) {
			dirs, exts, names := config.ResolveSkipLists(*cfg, repo)
			idxCfg := indexer.IndexerConfig{
				RootPath:        repo.Path,
				RepoName:        repo.Name,
				DB:              client,
				ChunkFn:         chunker.ChunkFile,
				SeenHashes:      sharedHashes,
				SkipDirs:        dirs,
				SkipExtensions:  exts,
				SkipFilenames:   names,
				Branch:          repo.Branch,
				StrictBranch:    *strictBranch,
				SkipBranchCheck: repo.SkipBranchCheck,
			}
			if est, ok := estByRepo[repo.Name]; ok {
				log.Printf("[index] starting repo %q at %s (score=%s)", repo.Name, repo.Path, humanBytes(est.Score))
			} else {
				log.Printf("[index] starting repo %q at %s", repo.Name, repo.Path)
			}
			stats, err := indexer.RunIndex(ctx, idxCfg)
			if err != nil {
				log.Printf("[index] repo %s: %v", repo.Name, err)
				return
			}
			printIndexStats(stats)
		}

		if *parallel {
			var wg sync.WaitGroup
			for _, repo := range repos {
				wg.Add(1)
				r := repo
				go func() { defer wg.Done(); runOne(r) }()
			}
			wg.Wait()
		} else {
			for _, repo := range repos {
				runOne(repo)
			}
		}
		return
	}

	// Single-repo mode.
	if *name == "" {
		fmt.Fprintln(os.Stderr, "error: --name is required (or use --config)")
		fs.Usage()
		os.Exit(1)
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "error: path argument is required")
		fs.Usage()
		os.Exit(1)
	}
	repoPath := fs.Arg(0)

	cfg := indexer.IndexerConfig{
		RootPath:     repoPath,
		RepoName:     *name,
		DB:           client,
		ChunkFn:      chunker.ChunkFile,
		StrictBranch: *strictBranch,
	}

	log.Printf("[index] starting index of %q (repo=%s)", repoPath, *name)
	stats, err := indexer.RunIndex(ctx, cfg)
	if err != nil {
		log.Fatalf("[index] %v", err)
	}
	printIndexStats(stats)
}

func printDryRunTable(estimates []*estimator.ScanEstimate) {
	fmt.Fprintln(os.Stderr, "[index] dry-run scan estimates (sorted by score, cheapest first):")
	w := tabwriter.NewWriter(os.Stderr, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "REPO\tFILES\tTOTAL\tCHANGED\tCHANGED BYTES\tSCORE")
	for _, est := range estimates {
		changed := "--"
		changedBytes := "--"
		if est.ChangedFiles >= 0 {
			changed = fmt.Sprintf("%d", est.ChangedFiles)
			changedBytes = humanBytes(est.ChangedBytes)
		}
		fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\t%s\n",
			est.RepoName, est.TotalFiles, humanBytes(est.TotalBytes), changed, changedBytes, humanBytes(est.Score))
	}
	w.Flush()
}

func humanBytes(n int64) string {
	const unit = int64(1024)
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := unit, 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func runSearch(args []string) {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	query := fs.String("query", "", "search query (required)")
	repo := fs.String("repo", "", "filter by repository name")
	n := fs.Int("n", 10, "number of results to return")
	lang := fs.String("lang", "", "filter by language (e.g. go, python)")
	ext := fs.String("ext", "", "filter by extensions, comma-separated (e.g. .go,.ts)")
	minScore := fs.Float64("min-score", 0, "minimum similarity score 0\u20131")
	dedup := fs.Bool("dedup", false, "keep only the top chunk per file")
	contextLines := fs.Int("context-lines", 0, "expand results by N lines above/below")
	hybrid := fs.Bool("hybrid", false, "enable BM25+vector hybrid re-ranking")
	dbURL := fs.String("db", "http://localhost:8000", "ChromaDB base URL")
	verbose := fs.Bool("verbose", false, "enable verbose logging")
	quiet := fs.Bool("quiet", false, "suppress all log output")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: omni-code search --query <text> [flags]\n\nFlags:\n")
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
	client, err := newClientAndCollections(ctx, *dbURL, "", "", "")
	if err != nil {
		log.Fatalf("[search] %v", err)
	}

	var extFilters []string
	if *ext != "" {
		for _, e := range strings.Split(*ext, ",") {
			e = strings.TrimSpace(e)
			if e != "" {
				extFilters = append(extFilters, e)
			}
		}
	}

	opts := db.QueryOpts{
		NResults:     *n,
		RepoFilter:   *repo,
		LangFilter:   *lang,
		ExtFilters:   extFilters,
		MinScore:     float32(*minScore),
		Dedup:        *dedup,
		ContextLines: *contextLines,
		Hybrid:       *hybrid,
	}

	results, err := client.QueryChunks(ctx, *query, opts)
	if err != nil {
		log.Fatalf("[search] query failed: %v", err)
	}

	if len(results) == 0 {
		fmt.Fprintln(os.Stderr, "no results found")
		return
	}

	for i, r := range results {
		fmt.Printf("## %d. %s:%s (lines %d\u2013%d)\n", i+1, r.Repo, r.Path, r.StartLine, r.EndLine)
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
	client, err := newClientAndCollections(ctx, *dbURL, "", "", "")
	if err != nil {
		log.Fatalf("[mcp] %v", err)
	}

	if err := internalmcp.ServeStdio(ctx, client); err != nil {
		log.Fatalf("[mcp] server error: %v", err)
	}
}

// runRepos dispatches repos sub-subcommands.
func runRepos(args []string) {
	if len(args) == 0 || args[0] == "list" {
		reposList(args)
		return
	}
	switch args[0] {
	case "add":
		reposAdd(args[1:])
	case "remove":
		reposRemove(args[1:])
	default:
		reposList(args)
	}
}

func reposList(args []string) {
	fs := flag.NewFlagSet("repos list", flag.ExitOnError)
	dbURL := fs.String("db", "http://localhost:8000", "ChromaDB base URL")
	cfgPath := fs.String("config", "", "path to repos.yaml (overrides --db)")
	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if *cfgPath != "" {
		cfg, err := config.Load(*cfgPath)
		if err != nil {
			log.Fatalf("[repos] load config: %v", err)
		}
		if cfg.DB != "" {
			*dbURL = cfg.DB
		}
	}

	ctx := context.Background()
	client, err := newClientAndCollections(ctx, *dbURL, "", "", "")
	if err != nil {
		log.Fatalf("[repos] %v", err)
	}

	metas, err := client.ListRepoMeta(ctx)
	if err != nil {
		log.Fatalf("[repos] list: %v", err)
	}

	if len(metas) == 0 {
		fmt.Fprintln(os.Stderr, "no repos indexed yet")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "REPO\tBRANCH\tCOMMIT\tLAST INDEXED\tFILES\tCHUNKS\tMODE\tDURATION")
	for _, m := range metas {
		commit := m.LastIndexedCommit
		if len(commit) > 8 {
			commit = commit[:8]
		}
		indexedAt := m.LastIndexedAt
		if t, err := time.Parse(time.RFC3339, m.LastIndexedAt); err == nil {
			indexedAt = t.Local().Format("2006-01-02 15:04:05")
		}
		duration := fmt.Sprintf("%dms", m.DurationMs)
		if m.DurationMs >= 1000 {
			duration = fmt.Sprintf("%.1fs", float64(m.DurationMs)/1000)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\n",
			m.Repo, m.CurrentBranch, commit, indexedAt, m.FileCount, m.ChunkCount, m.IndexMode, duration)
	}
	w.Flush()
}

func reposAdd(args []string) {
	fs := flag.NewFlagSet("repos add", flag.ExitOnError)
	name := fs.String("name", "", "repository name (required)")
	path := fs.String("path", "", "filesystem path (required)")
	branch := fs.String("branch", "", "expected branch (optional)")
	cfgPath := fs.String("config", "repos.yaml", "path to repos.yaml")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: omni-code repos add --name <name> --path <path> [--branch <branch>] [--config repos.yaml]\n")
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}
	if *name == "" || *path == "" {
		fmt.Fprintln(os.Stderr, "error: --name and --path are required")
		fs.Usage()
		os.Exit(1)
	}

	var cfg config.Config
	if existing, err := config.Load(*cfgPath); err == nil {
		cfg = *existing
	}
	for _, r := range cfg.Repos {
		if r.Name == *name {
			fmt.Fprintf(os.Stderr, "error: repo %q already exists in %s\n", *name, *cfgPath)
			os.Exit(1)
		}
	}
	cfg.Repos = append(cfg.Repos, config.RepoEntry{
		Name:   *name,
		Path:   *path,
		Branch: *branch,
	})
	if err := config.Save(&cfg, *cfgPath); err != nil {
		log.Fatalf("[repos add] %v", err)
	}
	fmt.Printf("added repo %q to %s\n", *name, *cfgPath)
}

func reposRemove(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: omni-code repos remove <name> [--config repos.yaml] [--db <url>]")
		os.Exit(1)
	}
	name := args[0]

	fs := flag.NewFlagSet("repos remove", flag.ExitOnError)
	cfgPath := fs.String("config", "repos.yaml", "path to repos.yaml")
	dbURL := fs.String("db", "http://localhost:8000", "ChromaDB base URL")
	if err := fs.Parse(args[1:]); err != nil {
		os.Exit(1)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("[repos remove] load config: %v", err)
	}

	found := false
	newRepos := cfg.Repos[:0]
	for _, r := range cfg.Repos {
		if r.Name == name {
			found = true
			continue
		}
		newRepos = append(newRepos, r)
	}
	if !found {
		fmt.Fprintf(os.Stderr, "repo %q not found in %s\n", name, *cfgPath)
		os.Exit(1)
	}
	cfg.Repos = newRepos
	if err := config.Save(cfg, *cfgPath); err != nil {
		log.Fatalf("[repos remove] save config: %v", err)
	}

	ctx := context.Background()
	client, err := newClientAndCollections(ctx, *dbURL, "", "", "")
	if err != nil {
		log.Fatalf("[repos remove] db connect: %v", err)
	}
	deleteRepo(ctx, client, name)
	fmt.Printf("removed repo %q from %s and deleted its index data\n", name, *cfgPath)
}

// runReset handles: reset --all | reset --repo <name>
func runReset(args []string) {
	fs := flag.NewFlagSet("reset", flag.ExitOnError)
	all := fs.Bool("all", false, "drop and recreate all collections")
	repo := fs.String("repo", "", "delete data for a specific repo")
	dbURL := fs.String("db", "http://localhost:8000", "ChromaDB base URL")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: omni-code reset --all | omni-code reset --repo <name>\n")
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}
	if !*all && *repo == "" {
		fmt.Fprintln(os.Stderr, "error: --all or --repo <name> is required")
		fs.Usage()
		os.Exit(1)
	}

	ctx := context.Background()
	client, err := newClientAndCollections(ctx, *dbURL, "", "", "")
	if err != nil {
		log.Fatalf("[reset] %v", err)
	}

	if *all {
		ef, _, err := defaultef.NewDefaultEmbeddingFunction()
		if err != nil {
			log.Fatalf("[reset] create EF: %v", err)
		}
		if err := client.ResetAllCollections(ctx, ef); err != nil {
			log.Fatalf("[reset] %v", err)
		}
		fmt.Fprintln(os.Stderr, "[reset] all collections dropped and recreated")
		return
	}

	deleteRepo(ctx, client, *repo)
	fmt.Fprintf(os.Stderr, "[reset] data for repo %q deleted\n", *repo)
}

// deleteRepo removes all data for a repo from all three collections.
func deleteRepo(ctx context.Context, client *db.ChromaClient, name string) {
	if err := client.DeleteRepoChunks(ctx, name); err != nil {
		log.Printf("[reset] delete chunks for %s: %v", name, err)
	}
	if err := client.DeleteRepoFileMeta(ctx, name); err != nil {
		log.Printf("[reset] delete file metas for %s: %v", name, err)
	}
	if err := client.DeleteRepoMeta(ctx, name); err != nil {
		log.Printf("[reset] delete repo meta for %s: %v", name, err)
	}
}

// watchDeps holds injectable functions for the poll loop, used in production and tests.
type watchDeps struct {
	isGitRepo   func(root string) bool
	headCommit  func(root string) (string, error)
	getRepoMeta func(ctx context.Context, name string) (*db.RepoMeta, error)
	runIndex    func(ctx context.Context, cfg indexer.IndexerConfig) (*indexer.IndexStats, error)
	dbClient    *db.ChromaClient // placed into IndexerConfig.DB; may be nil in tests
}

// pollOnce iterates all repos in cfg, checks if HEAD changed since last index,
// and re-indexes any repo whose HEAD commit differs from the stored value.
func pollOnce(ctx context.Context, cfg *config.Config, deps watchDeps) {
	for _, repo := range cfg.Repos {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if !deps.isGitRepo(repo.Path) {
			continue
		}
		current, err := deps.headCommit(repo.Path)
		if err != nil {
			log.Printf("[watch] HEAD commit for %s: %v", repo.Name, err)
			continue
		}
		meta, err := deps.getRepoMeta(ctx, repo.Name)
		if err != nil || meta == nil || meta.LastIndexedCommit == current {
			continue
		}
		log.Printf("[watch] repo %s changed (%s..%s), re-indexing",
			repo.Name, meta.LastIndexedCommit[:min(8, len(meta.LastIndexedCommit))], current[:min(8, len(current))])
		dirs, exts, names := config.ResolveSkipLists(*cfg, repo)
		idxCfg := indexer.IndexerConfig{
			RootPath:        repo.Path,
			RepoName:        repo.Name,
			DB:              deps.dbClient,
			ChunkFn:         chunker.ChunkFile,
			SkipDirs:        dirs,
			SkipExtensions:  exts,
			SkipFilenames:   names,
			Branch:          repo.Branch,
			SkipBranchCheck: repo.SkipBranchCheck,
		}
		stats, err := deps.runIndex(ctx, idxCfg)
		if err != nil {
			log.Printf("[watch] index %s: %v", repo.Name, err)
			continue
		}
		log.Printf("[watch] %s: %d changed, %d chunks", repo.Name, stats.FilesChanged, stats.ChunksUpserted)
	}
}

// runWatch handles: watch --config repos.yaml [--interval 5m] [--once] [--install-hook]
func runWatch(args []string) {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	cfgPath := fs.String("config", "repos.yaml", "path to repos.yaml")
	interval := fs.Duration("interval", 5*time.Minute, "polling interval for HEAD changes")
	once := fs.Bool("once", false, "check once and exit (useful in post-commit hooks)")
	installHook := fs.Bool("install-hook", false, "install git post-commit hooks for all repos")
	dbURL := fs.String("db", "http://localhost:8000", "ChromaDB base URL")
	embBackend := fs.String("embedding-backend", "", "embedding backend")
	embModel := fs.String("embedding-model", "", "embedding model name")
	embURL := fs.String("embedding-url", "", "embedding service URL")
	verbose := fs.Bool("verbose", false, "enable verbose logging")
	quiet := fs.Bool("quiet", false, "suppress all log output")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: omni-code watch --config repos.yaml [--interval 5m] [--once] [--install-hook]\n")
	}
	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}
	applyLogFlags(*verbose, *quiet)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("[watch] load config: %v", err)
	}

	if *installHook {
		installGitHooks(cfg, *cfgPath)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client, err := newClientAndCollections(ctx, *dbURL, *embBackend, *embModel, *embURL)
	if err != nil {
		log.Fatalf("[watch] %v", err)
	}

	deps := watchDeps{
		isGitRepo:   git.IsGitRepo,
		headCommit:  git.HeadCommit,
		getRepoMeta: client.GetRepoMeta,
		runIndex:    indexer.RunIndex,
		dbClient:    client,
	}
	checkAndReindex := func() { pollOnce(ctx, cfg, deps) }

	if *once {
		checkAndReindex()
		return
	}

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	log.Printf("[watch] polling every %s (Ctrl-C to stop)", *interval)
	checkAndReindex()
	for {
		select {
		case <-ctx.Done():
			log.Printf("[watch] shutting down")
			return
		case <-ticker.C:
			checkAndReindex()
		}
	}
}

func installGitHooks(cfg *config.Config, cfgAbsPath string) {
	for _, repo := range cfg.Repos {
		hookDir := repo.Path + "/.git/hooks"
		hookFile := hookDir + "/post-commit"
		if _, err := os.Stat(hookDir); err != nil {
			log.Printf("[hook] %s: .git/hooks not found, skipping", repo.Name)
			continue
		}
		script := fmt.Sprintf("#!/bin/sh\nomni-code watch --config %s --once 2>/dev/null &\n",
			cfgAbsPath)
		if err := os.WriteFile(hookFile, []byte(script), 0o755); err != nil {
			log.Printf("[hook] write %s: %v", hookFile, err)
			continue
		}
		fmt.Printf("installed post-commit hook for %s at %s\n", repo.Name, hookFile)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
