package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initTestRepo creates a temporary git repository with an initial commit
// containing the given files (map of relative path → content).
// Returns the repo root.
func initTestRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("initTestRepo: %v", err)
		}
	}
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	git("init", "-b", "main")
	git("config", "user.email", "test@test.com")
	git("config", "user.name", "test")
	for rel, content := range files {
		abs := filepath.Join(dir, rel)
		must(os.MkdirAll(filepath.Dir(abs), 0755))
		must(os.WriteFile(abs, []byte(content), 0644))
	}
	git("add", ".")
	git("commit", "-m", "initial commit")
	return dir
}

func TestIsGitRepo(t *testing.T) {
	dir := initTestRepo(t, map[string]string{"README.md": "hello"})
	if !IsGitRepo(dir) {
		t.Error("expected IsGitRepo(valid git repo) = true")
	}
	tmp := t.TempDir()
	if IsGitRepo(tmp) {
		t.Error("expected IsGitRepo(plain dir) = false")
	}
}

func TestCurrentBranch(t *testing.T) {
	dir := initTestRepo(t, map[string]string{"README.md": "hello"})
	branch, err := CurrentBranch(dir)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if branch != "main" {
		t.Errorf("CurrentBranch = %q, want %q", branch, "main")
	}
}

func TestHeadCommit(t *testing.T) {
	dir := initTestRepo(t, map[string]string{"README.md": "hello"})
	commit, err := HeadCommit(dir)
	if err != nil {
		t.Fatalf("HeadCommit: %v", err)
	}
	if len(commit) != 40 {
		t.Errorf("HeadCommit length = %d, want 40", len(commit))
	}
}

func TestListFiles(t *testing.T) {
	dir := initTestRepo(t, map[string]string{
		"main.go":     "package main",
		"sub/util.go": "package sub",
	})
	files, err := ListFiles(dir)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("ListFiles returned %d files, want 2; got: %v", len(files), files)
	}
	for _, f := range files {
		if !filepath.IsAbs(f) {
			t.Errorf("expected absolute path, got %q", f)
		}
		if !strings.HasPrefix(f, dir) {
			t.Errorf("expected path under %q, got %q", dir, f)
		}
	}
}

func TestDiffFiles(t *testing.T) {
	dir := initTestRepo(t, map[string]string{
		"a.go": "package a",
		"b.go": "package b",
	})
	firstCommit, err := HeadCommit(dir)
	if err != nil {
		t.Fatalf("HeadCommit: %v", err)
	}

	// Make a change: modify a.go, delete b.go, add c.go
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a // changed"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "b.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "c.go"), []byte("package c"), 0644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "second commit")

	secondCommit, err := HeadCommit(dir)
	if err != nil {
		t.Fatalf("HeadCommit 2: %v", err)
	}

	changed, deleted, err := DiffFiles(dir, firstCommit, secondCommit)
	if err != nil {
		t.Fatalf("DiffFiles: %v", err)
	}

	// a.go and c.go should be changed
	wantChanged := map[string]bool{
		filepath.Join(dir, "a.go"): true,
		filepath.Join(dir, "c.go"): true,
	}
	for _, f := range changed {
		if !wantChanged[f] {
			t.Errorf("unexpected changed file: %q", f)
		}
		delete(wantChanged, f)
	}
	if len(wantChanged) > 0 {
		t.Errorf("missing changed files: %v", wantChanged)
	}

	// b.go should be deleted
	if len(deleted) != 1 || deleted[0] != filepath.Join(dir, "b.go") {
		t.Errorf("deleted = %v, want [b.go]", deleted)
	}
}

func TestDetectDefaultBranch(t *testing.T) {
	dir := initTestRepo(t, map[string]string{"README.md": "hello"})
	branch, err := DetectDefaultBranch(dir)
	if err != nil {
		t.Fatalf("DetectDefaultBranch: %v", err)
	}
	if branch != "main" {
		t.Errorf("DetectDefaultBranch = %q, want %q", branch, "main")
	}
}
