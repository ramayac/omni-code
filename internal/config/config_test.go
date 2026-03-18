package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSkipLists_DefaultsOnly(t *testing.T) {
	global := Config{}
	entry := RepoEntry{Name: "test", Path: "/tmp/test"}
	dirs, exts, names := ResolveSkipLists(global, entry)

	for _, d := range DefaultSkipDirs {
		if !dirs[d] {
			t.Errorf("expected default dir %q to be in skip dirs", d)
		}
	}
	for _, e := range DefaultSkipExtensions {
		if !exts[e] {
			t.Errorf("expected default ext %q to be in skip extensions", e)
		}
	}
	for _, n := range DefaultSkipFilenames {
		if !names[n] {
			t.Errorf("expected default filename %q to be in skip filenames", n)
		}
	}
}

func TestResolveSkipLists_GlobalMerge(t *testing.T) {
	global := Config{
		SkipDirsExtra:       []string{"fixtures"},
		SkipExtensionsExtra: []string{".lock"},
		SkipFilenamesExtra:  []string{"CHANGELOG.md"},
	}
	entry := RepoEntry{Name: "test", Path: "/tmp/test"}
	dirs, exts, names := ResolveSkipLists(global, entry)

	if !dirs["fixtures"] {
		t.Error("expected global extra dir 'fixtures' in skip dirs")
	}
	if !dirs[".git"] {
		t.Error("expected default dir '.git' still in skip dirs after global merge")
	}
	if !exts[".lock"] {
		t.Error("expected global extra ext '.lock' in skip extensions")
	}
	if !names["CHANGELOG.md"] {
		t.Error("expected global extra filename 'CHANGELOG.md' in skip filenames")
	}
}

func TestResolveSkipLists_PerRepoMerge(t *testing.T) {
	global := Config{SkipDirsExtra: []string{"generated"}}
	entry := RepoEntry{
		Name:               "test",
		Path:               "/tmp/test",
		SkipDirsExtra:      []string{"mocks"},
		SkipFilenamesExtra: []string{"proto.lock"},
	}
	dirs, _, names := ResolveSkipLists(global, entry)

	if !dirs["mocks"] {
		t.Error("expected per-repo dir 'mocks'")
	}
	if !dirs["generated"] {
		t.Error("expected global extra dir 'generated'")
	}
	if !dirs[".git"] {
		t.Error("expected default dir '.git' still present")
	}
	if !names["proto.lock"] {
		t.Error("expected per-repo filename 'proto.lock'")
	}
}

func TestResolveSkipLists_PerRepoOverride(t *testing.T) {
	global := Config{SkipDirsExtra: []string{"generated"}}
	entry := RepoEntry{
		Name:             "test",
		Path:             "/tmp/test",
		SkipDirsOverride: []string{"only_this"},
	}
	dirs, _, _ := ResolveSkipLists(global, entry)

	if !dirs["only_this"] {
		t.Error("expected override dir 'only_this'")
	}
	if dirs[".git"] {
		t.Error("default dir '.git' should NOT be present when override is set")
	}
	if dirs["generated"] {
		t.Error("global extra dir 'generated' should NOT be present when override is set")
	}
}

func TestLoadSave_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "repos.yaml")

	original := &Config{
		DB: "http://localhost:8000",
		Repos: []RepoEntry{
			{Name: "myapp", Path: "/opt/myapp", Branch: "main"},
			{Name: "other", Path: "/opt/other"},
		},
	}

	if err := Save(original, path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.DB != original.DB {
		t.Errorf("DB: got %q, want %q", loaded.DB, original.DB)
	}
	if len(loaded.Repos) != 2 {
		t.Fatalf("Repos len: got %d, want 2", len(loaded.Repos))
	}
	if loaded.Repos[0].Name != "myapp" {
		t.Errorf("Repos[0].Name: got %q", loaded.Repos[0].Name)
	}
	if loaded.Repos[1].Path != "/opt/other" {
		t.Errorf("Repos[1].Path: got %q", loaded.Repos[1].Path)
	}
}

func TestLoad_ValidationErrors(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name    string
		content string
	}{
		{"missing_name", "repos:\n  - path: /tmp/foo\n"},
		{"missing_path", "repos:\n  - name: foo\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".yaml")
			if err := os.WriteFile(path, []byte(tc.content), 0644); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if err == nil {
				t.Error("expected validation error, got nil")
			}
		})
	}
}
