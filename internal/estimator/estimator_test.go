package estimator

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestEstimateTotalAndChangedAndSkips(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}

	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "tester")

	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("a.go", "package main\n")
	write("b.txt", "hello")
	write("img.png", "png-bytes")
	run("add", ".")
	run("commit", "-m", "initial")

	lastCmd := exec.Command("git", "rev-parse", "HEAD")
	lastCmd.Dir = root
	lastOut, err := lastCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse failed: %v\n%s", err, string(lastOut))
	}
	last := string(lastOut)
	if len(last) > 0 && last[len(last)-1] == '\n' {
		last = last[:len(last)-1]
	}

	write("b.txt", "hello world changed")
	run("add", "b.txt")
	run("commit", "-m", "change b")

	estFull, err := Estimate(root, "repo", "", map[string]bool{".png": true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if estFull.TotalFiles != 2 {
		t.Fatalf("expected 2 files (png skipped), got %d", estFull.TotalFiles)
	}
	if estFull.ChangedFiles != -1 {
		t.Fatalf("expected ChangedFiles=-1 for full scan, got %d", estFull.ChangedFiles)
	}
	if estFull.Score != estFull.TotalBytes {
		t.Fatalf("expected full scan score=total bytes")
	}

	estInc, err := Estimate(root, "repo", last, map[string]bool{".png": true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if estInc.ChangedFiles != 1 {
		t.Fatalf("expected 1 changed file, got %d", estInc.ChangedFiles)
	}
	if estInc.ChangedBytes <= 0 {
		t.Fatalf("expected changed bytes > 0")
	}
	if estInc.Score != estInc.ChangedBytes {
		t.Fatalf("expected incremental score=changed bytes")
	}
}

func TestSortByScore(t *testing.T) {
	a := &ScanEstimate{RepoName: "a", Score: 300}
	b := &ScanEstimate{RepoName: "b", Score: 10}
	c := &ScanEstimate{RepoName: "c", Score: 200}
	list := []*ScanEstimate{a, b, c}
	SortByScore(list)
	if list[0] != b || list[1] != c || list[2] != a {
		t.Fatalf("unexpected order: %#v", list)
	}
}