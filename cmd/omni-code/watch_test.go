package main

import (
	"context"
	"testing"

	"github.com/ramayac/omni-code/internal/config"
	"github.com/ramayac/omni-code/internal/db"
	"github.com/ramayac/omni-code/internal/indexer"
)

// stubMeta builds a minimal RepoMeta for a given commit hash.
func stubMeta(commit string) *db.RepoMeta {
	return &db.RepoMeta{LastIndexedCommit: commit}
}

// TestPollOnce_SkipsUnchangedRepo verifies that pollOnce does NOT call runIndex
// when the HEAD commit matches the stored last_indexed_commit.
func TestPollOnce_SkipsUnchangedRepo(t *testing.T) {
	const sha = "abc1234"
	reindexed := false

	cfg := &config.Config{
		Repos: []config.RepoEntry{{Name: "myrepo", Path: "/fake/path"}},
	}
	deps := watchDeps{
		isGitRepo:   func(string) bool { return true },
		headCommit:  func(string) (string, error) { return sha, nil },
		getRepoMeta: func(_ context.Context, _ string) (*db.RepoMeta, error) { return stubMeta(sha), nil },
		runIndex: func(_ context.Context, _ indexer.IndexerConfig) (*indexer.IndexStats, error) {
			reindexed = true
			return &indexer.IndexStats{}, nil
		},
	}

	pollOnce(context.Background(), cfg, deps)

	if reindexed {
		t.Error("runIndex should NOT have been called when HEAD == last_indexed_commit")
	}
}

// TestPollOnce_TriggersReindexOnChange verifies that pollOnce calls runIndex
// when the HEAD commit differs from the stored last_indexed_commit.
func TestPollOnce_TriggersReindexOnChange(t *testing.T) {
	reindexedRepo := ""

	cfg := &config.Config{
		Repos: []config.RepoEntry{{Name: "myrepo", Path: "/fake/path"}},
	}
	deps := watchDeps{
		isGitRepo:   func(string) bool { return true },
		headCommit:  func(string) (string, error) { return "newsha123", nil },
		getRepoMeta: func(_ context.Context, _ string) (*db.RepoMeta, error) { return stubMeta("oldsha456"), nil },
		runIndex: func(_ context.Context, cfg indexer.IndexerConfig) (*indexer.IndexStats, error) {
			reindexedRepo = cfg.RepoName
			return &indexer.IndexStats{FilesChanged: 3, ChunksUpserted: 12}, nil
		},
	}

	pollOnce(context.Background(), cfg, deps)

	if reindexedRepo != "myrepo" {
		t.Errorf("expected runIndex called for %q, got %q", "myrepo", reindexedRepo)
	}
}

// TestPollOnce_SkipsNonGitRepo verifies that non-git directories are ignored.
func TestPollOnce_SkipsNonGitRepo(t *testing.T) {
	reindexed := false

	cfg := &config.Config{
		Repos: []config.RepoEntry{{Name: "notgit", Path: "/some/directory"}},
	}
	deps := watchDeps{
		isGitRepo:   func(string) bool { return false },
		headCommit:  func(string) (string, error) { return "abc", nil },
		getRepoMeta: func(_ context.Context, _ string) (*db.RepoMeta, error) { return stubMeta("xyz"), nil },
		runIndex: func(_ context.Context, _ indexer.IndexerConfig) (*indexer.IndexStats, error) {
			reindexed = true
			return &indexer.IndexStats{}, nil
		},
	}

	pollOnce(context.Background(), cfg, deps)

	if reindexed {
		t.Error("runIndex should NOT have been called for a non-git directory")
	}
}

// TestPollOnce_MultipleRepos verifies that only repos with changed HEADs are reindexed
// when multiple repos are configured.
func TestPollOnce_MultipleRepos(t *testing.T) {
	commits := map[string]string{
		"repo-a": "same-sha",
		"repo-b": "new-sha",
		"repo-c": "same-sha",
	}
	stored := map[string]string{
		"repo-a": "same-sha", // unchanged
		"repo-b": "old-sha",  // changed
		"repo-c": "same-sha", // unchanged
	}
	var reindexed []string

	cfg := &config.Config{
		Repos: []config.RepoEntry{
			{Name: "repo-a", Path: "/repo-a"},
			{Name: "repo-b", Path: "/repo-b"},
			{Name: "repo-c", Path: "/repo-c"},
		},
	}
	deps := watchDeps{
		isGitRepo: func(string) bool { return true },
		headCommit: func(root string) (string, error) {
			name := root[1:] // "/repo-a" → "repo-a"
			return commits[name], nil
		},
		getRepoMeta: func(_ context.Context, name string) (*db.RepoMeta, error) {
			return stubMeta(stored[name]), nil
		},
		runIndex: func(_ context.Context, cfg indexer.IndexerConfig) (*indexer.IndexStats, error) {
			reindexed = append(reindexed, cfg.RepoName)
			return &indexer.IndexStats{}, nil
		},
	}

	pollOnce(context.Background(), cfg, deps)

	if len(reindexed) != 1 || reindexed[0] != "repo-b" {
		t.Errorf("expected only repo-b reindexed, got %v", reindexed)
	}
}
