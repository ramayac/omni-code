package estimator

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ramayac/omni-code/internal/git"
)

// ScanEstimate holds pre-scan complexity metrics for a single repository.
type ScanEstimate struct {
	RepoName string
	RootPath string
	// Full-scan proxies
	TotalFiles int
	TotalBytes int64
	// Incremental proxies (-1 when no previous commit is known)
	ChangedFiles int
	ChangedBytes int64
	// Derived score used for sorting (lower = cheaper)
	Score int64
}

// Estimate computes a ScanEstimate for a repo without touching ChromaDB.
// lastCommit is the SHA previously indexed; empty string forces full-scan estimate.
// skipExts and skipNames are the resolved skip maps for the repo.
func Estimate(
	rootPath, repoName, lastCommit string,
	skipExts, skipNames map[string]bool,
) (*ScanEstimate, error) {
	files, err := git.ListFiles(rootPath)
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}

	est := &ScanEstimate{
		RepoName:     repoName,
		RootPath:     rootPath,
		ChangedFiles: -1,
	}

	filtered := make(map[string]bool, len(files))
	for _, rel := range files {
		if shouldSkipFile(rel, skipExts, skipNames) {
			continue
		}
		abs := filepath.Join(rootPath, rel)
		info, err := os.Stat(abs)
		if err != nil {
			continue
		}
		if info.IsDir() {
			continue
		}
		filtered[rel] = true
		est.TotalFiles++
		est.TotalBytes += info.Size()
	}

	if lastCommit != "" {
		changed, err := git.DiffFiles(rootPath, lastCommit, "HEAD")
		if err != nil {
			return nil, fmt.Errorf("diff files: %w", err)
		}
		est.ChangedFiles = 0
		for _, rel := range changed {
			if !filtered[rel] {
				continue
			}
			abs := filepath.Join(rootPath, rel)
			info, err := os.Stat(abs)
			if err != nil || info.IsDir() {
				continue
			}
			est.ChangedFiles++
			est.ChangedBytes += info.Size()
		}
	}

	if est.ChangedFiles >= 0 {
		est.Score = est.ChangedBytes
	} else {
		est.Score = est.TotalBytes
	}

	return est, nil
}

// SortByScore sorts estimates in-place, cheapest first.
func SortByScore(estimates []*ScanEstimate) {
	sort.SliceStable(estimates, func(i, j int) bool {
		if estimates[i].Score == estimates[j].Score {
			return estimates[i].RepoName < estimates[j].RepoName
		}
		return estimates[i].Score < estimates[j].Score
	})
}

func shouldSkipFile(path string, skipExts, skipNames map[string]bool) bool {
	base := filepath.Base(path)
	if skipNames != nil && skipNames[base] {
		return true
	}
	if skipExts != nil {
		ext := strings.ToLower(filepath.Ext(path))
		if ext != "" && skipExts[ext] {
			return true
		}
	}
	return false
}