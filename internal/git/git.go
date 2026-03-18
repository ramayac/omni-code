// Package git provides helpers for interacting with git repositories.
package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// IsGitRepo returns true if root contains a .git directory or is inside a git repo.
func IsGitRepo(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".git"))
	return err == nil
}

// DetectDefaultBranch attempts to determine the canonical default branch for
// the repository at root using the following algorithm:
//  1. Read refs/remotes/origin/HEAD symbolic ref (e.g. → refs/remotes/origin/main)
//  2. Fall back by probing well-known local branch names in order:
//     main, master, th-main, develop, trunk
//
// Returns an error if no default branch can be determined.
func DetectDefaultBranch(root string) (string, error) {
	out, err := runGit(root, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err == nil {
		ref := strings.TrimSpace(out)
		// ref looks like "refs/remotes/origin/main"
		parts := strings.SplitN(ref, "/", 4)
		if len(parts) == 4 && parts[3] != "" {
			return parts[3], nil
		}
	}

	for _, branch := range []string{"main", "master", "th-main", "develop", "trunk"} {
		_, err := runGit(root, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
		if err == nil {
			return branch, nil
		}
	}
	return "", fmt.Errorf("could not detect default branch for %s", root)
}

// CurrentBranch returns the name of the currently checked-out branch.
// Returns "HEAD" if in detached HEAD state.
func CurrentBranch(root string) (string, error) {
	out, err := runGit(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("get current branch: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// HeadCommit returns the full SHA of the current HEAD commit.
func HeadCommit(root string) (string, error) {
	out, err := runGit(root, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("get HEAD commit: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// ListFiles returns absolute paths for all tracked and untracked-but-not-ignored
// files in the repository. Respects all .gitignore files automatically.
func ListFiles(root string) ([]string, error) {
	out, err := runGit(root, "ls-files", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	raw := strings.TrimSpace(out)
	if raw == "" {
		return nil, nil
	}
	lines := strings.Split(raw, "\n")
	files := make([]string, 0, len(lines))
	for _, l := range lines {
		if l != "" {
			files = append(files, filepath.Join(root, filepath.FromSlash(l)))
		}
	}
	return files, nil
}

// DiffFiles returns the lists of changed/added and deleted files between two
// commits. Paths are absolute.
func DiffFiles(root, from, to string) (changed, deleted []string, err error) {
	// Changed/added files.
	out, err := runGit(root, "diff", "--name-only", "--diff-filter=ACMR", from, to)
	if err != nil {
		return nil, nil, fmt.Errorf("git diff %s..%s: %w", from, to, err)
	}
	for _, l := range splitLines(out) {
		changed = append(changed, filepath.Join(root, filepath.FromSlash(l)))
	}

	// Files deleted in this range.
	out, err = runGit(root, "diff", "--name-only", "--diff-filter=D", from, to)
	if err != nil {
		return nil, nil, fmt.Errorf("git diff deleted %s..%s: %w", from, to, err)
	}
	for _, l := range splitLines(out) {
		deleted = append(deleted, filepath.Join(root, filepath.FromSlash(l)))
	}
	return changed, deleted, nil
}

// runGit executes a git command rooted at dir and returns combined stdout.
// stderr is included in the error message on failure.
func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func splitLines(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
