package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the top-level structure of repos.yaml.
type Config struct {
	DB               string `yaml:"db"`
	EmbeddingBackend string `yaml:"embedding_backend"`
	EmbeddingModel   string `yaml:"embedding_model"`
	EmbeddingURL     string `yaml:"embedding_url"`
	// Global skip-list additions merged with built-in defaults for every repo.
	SkipDirsExtra       []string `yaml:"skip_dirs_extra"`
	SkipExtensionsExtra []string `yaml:"skip_extensions_extra"`
	SkipFilenamesExtra  []string `yaml:"skip_filenames_extra"`
	// SkipIfWrongBranch stops scanning/indexing if the repo is not on the expected branch.
	SkipIfWrongBranch bool        `yaml:"skip_if_wrong_branch"`
	Repos             []RepoEntry `yaml:"repos"`
}

// RepoEntry describes a single repository to be indexed.
type RepoEntry struct {
	Name   string `yaml:"name"`
	Path   string `yaml:"path"`
	Branch string `yaml:"branch"` // optional; empty = auto-detect
	// Per-repo skip-list additions merged with global + built-in defaults.
	SkipDirsExtra       []string `yaml:"skip_dirs_extra"`
	SkipExtensionsExtra []string `yaml:"skip_extensions_extra"`
	SkipFilenamesExtra  []string `yaml:"skip_filenames_extra"`
	// Per-repo full overrides — when set, completely replaces the merged list for that repo.
	SkipDirsOverride       []string `yaml:"skip_dirs_override"`
	SkipExtensionsOverride []string `yaml:"skip_extensions_override"`
	SkipFilenamesOverride  []string `yaml:"skip_filenames_override"`
	// SkipBranchCheck disables branch-mismatch warnings for this repo.
	SkipBranchCheck bool `yaml:"skip_branch_check"`
	// SkipIfWrongBranch stops scanning/indexing if the repo is not on the expected branch.
	// Merged with global setting (true if either is true).
	SkipIfWrongBranch bool `yaml:"skip_if_wrong_branch"`
}

// Load reads and validates a repos.yaml config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file %q: %w", path, err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config file %q: %w", path, err)
	}
	if err := validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Save writes a Config back to disk as YAML.
func Save(cfg *Config, path string) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write config file %q: %w", path, err)
	}
	return nil
}

func validate(cfg *Config) error {
	for i, r := range cfg.Repos {
		if r.Name == "" {
			return fmt.Errorf("repos[%d]: name is required", i)
		}
		if r.Path == "" {
			return fmt.Errorf("repos[%d] (%s): path is required", i, r.Name)
		}
	}
	return nil
}

// ResolveSkipLists returns the three resolved skip-list maps for a repo entry,
// applying the 4-level merge/override priority:
//  1. Built-in defaults
//  2. Global skip_*_extra (from top-level config)
//  3. Per-repo skip_*_extra
//  4. Per-repo skip_*_override (if set, replaces the entire merged list)
func ResolveSkipLists(global Config, entry RepoEntry) (dirs, exts, filenames map[string]bool) {
	dirs = resolveList(DefaultSkipDirs, global.SkipDirsExtra, entry.SkipDirsExtra, entry.SkipDirsOverride)
	exts = resolveList(DefaultSkipExtensions, global.SkipExtensionsExtra, entry.SkipExtensionsExtra, entry.SkipExtensionsOverride)
	filenames = resolveList(DefaultSkipFilenames, global.SkipFilenamesExtra, entry.SkipFilenamesExtra, entry.SkipFilenamesOverride)
	return
}

func resolveList(defaults, globalExtra, repoExtra, repoOverride []string) map[string]bool {
	if len(repoOverride) > 0 {
		m := make(map[string]bool, len(repoOverride))
		for _, v := range repoOverride {
			m[v] = true
		}
		return m
	}
	all := make([]string, 0, len(defaults)+len(globalExtra)+len(repoExtra))
	all = append(all, defaults...)
	all = append(all, globalExtra...)
	all = append(all, repoExtra...)
	m := make(map[string]bool, len(all))
	for _, v := range all {
		m[v] = true
	}
	return m
}
