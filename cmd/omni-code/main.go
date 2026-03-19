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

// ... existing code unchanged above runIndex ...

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

		if *dryRun {
			printDryRunTable(estimates)
			return
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

// ... existing code unchanged ...

func pollOnce(ctx context.Context, cfg *config.Config, deps watchDeps) {
	type candidate struct {
		repo config.RepoEntry
		meta *db.RepoMeta
	}

	var changed []candidate
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
		changed = append(changed, candidate{repo: repo, meta: meta})
	}

	if len(changed) == 0 {
		return
	}

	var estimates []*estimator.ScanEstimate
	estimateByRepo := make(map[string]*estimator.ScanEstimate, len(changed))
	for _, c := range changed {
		_, exts, names := config.ResolveSkipLists(*cfg, c.repo)
		est, err := estimator.Estimate(c.repo.Path, c.repo.Name, c.meta.LastIndexedCommit, exts, names)
		if err != nil {
			log.Printf("[watch] estimate %s: %v", c.repo.Name, err)
			est = &estimator.ScanEstimate{
				RepoName:     c.repo.Name,
				RootPath:     c.repo.Path,
				ChangedFiles: -1,
			}
		}
		estimateByRepo[c.repo.Name] = est
		estimates = append(estimates, est)
	}
	estimator.SortByScore(estimates)

	changedByName := map[string]candidate{}
	for _, c := range changed {
		changedByName[c.repo.Name] = c
	}

	for _, est := range estimates {
		c := changedByName[est.RepoName]
		repo := c.repo
		meta := c.meta
		current, err := deps.headCommit(repo.Path)
		if err != nil {
			log.Printf("[watch] HEAD commit for %s: %v", repo.Name, err)
			continue
		}

		log.Printf("[watch] repo %s changed (%s..%s), re-indexing (score=%s)",
			repo.Name, meta.LastIndexedCommit[:min(8, len(meta.LastIndexedCommit))], current[:min(8, len(current))], humanBytes(est.Score))

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