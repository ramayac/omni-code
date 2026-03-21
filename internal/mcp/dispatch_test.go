package mcp

import (
	"encoding/json"
	"testing"
)

func TestToolDefinitions(t *testing.T) {
	defs := ToolDefinitions()
	if len(defs) == 0 {
		t.Fatal("expected at least one tool definition")
	}

	// Verify all definitions have required fields and valid JSON schemas.
	names := make(map[string]bool)
	for _, d := range defs {
		if d.Name == "" {
			t.Error("tool definition has empty name")
		}
		if d.Description == "" {
			t.Errorf("tool %q has empty description", d.Name)
		}
		if names[d.Name] {
			t.Errorf("duplicate tool name: %s", d.Name)
		}
		names[d.Name] = true

		// Verify InputSchema is valid JSON.
		var schema map[string]interface{}
		if err := json.Unmarshal(d.InputSchema, &schema); err != nil {
			t.Errorf("tool %q has invalid InputSchema JSON: %v", d.Name, err)
		}
	}

	// Spot-check known tools exist.
	expectedTools := []string{
		"search_codebase", "list_repos", "get_repo_files",
		"get_file_content", "git_status", "git_diff", "git_log",
		"index_status", "grep_codebase", "get_file_symbols",
		"reindex_repo", "get_repo_summary", "search_repo_summaries",
		"get_top_contributors",
	}
	for _, name := range expectedTools {
		if !names[name] {
			t.Errorf("expected tool %q not found in definitions", name)
		}
	}
}

func TestDispatchToolUnknown(t *testing.T) {
	_, err := DispatchTool(nil, nil, "nonexistent_tool", "{}")
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
	if got := err.Error(); got != "unknown tool: nonexistent_tool" {
		t.Errorf("unexpected error: %s", got)
	}
}
