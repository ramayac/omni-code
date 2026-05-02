# MCP Tools Reference

## Tool Inventory

All tools are registered in `buildServer()` (`internal/mcp/server.go`) and duplicated as `SimpleTool` definitions in `internal/mcp/dispatch.go` for the chat mode bridge.

| Tool | Required Params | Optional Params | Description |
|---|---|---|---|
| `search_codebase` | `query` | `repo`, `n_results` | Semantic search across all indexed repos |
| `list_repos` | — | — | List all indexed repos with stats |
| `get_repo_files` | `repo` | `filter` (glob) | List files indexed for a repo |
| `get_file_content` | `repo`, `path` | — | Read raw file content from disk (max 100 KB) |
| `git_status` | `repo` | — | Branch, uncommitted changes, index staleness |
| `git_diff` | `repo` | — | Diff between current state and last indexed commit |
| `git_log` | `repo` | — | Recent commit history |
| `index_status` | — | — | Detailed breakdown of when/how each repo was last indexed |
| `grep_codebase` | `pattern` (RE2) | `repo`, `file_filter` (glob), `max_results` | Regex grep across indexed files (default cap: 50 lines) |
| `get_file_symbols` | `repo`, `path` | — | Tree-sitter AST top-level symbols (functions, classes, types) |
| `reindex_repo` | `repo` | `full` (bool) | Trigger incremental or full re-index |
| `get_repo_summary` | `repo` | — | Rich Markdown summary: metadata, languages, directories, git log |
| `search_repo_summaries` | — | — | Compact summary card for every indexed repo |
| `get_top_contributors` | `repo` | `since` | Ranked git contributor leaderboard |

## Adding a New Tool

1. Add handler function in the appropriate handler file:
   - `internal/mcp/handlers_search.go` — search_codebase, grep_codebase, get_file_content
   - `internal/mcp/handlers_repo.go` — list_repos, get_repo_files, get_repo_summary, search_repo_summaries
   - `internal/mcp/handlers_git.go` — git_status, git_diff, git_log, get_top_contributors
   - `internal/mcp/handlers_index.go` — index_status, reindex_repo, get_file_symbols
2. Register it in `buildServer()` in `internal/mcp/server.go` with the appropriate shared param type
3. Add a `SimpleTool` entry to `ToolDefinitions()` in `internal/mcp/dispatch.go`
4. Add a dispatch case to `DispatchTool()` in `internal/mcp/dispatch.go`

Shared parameter types (`searchParams`, `repoParams`, `repoFilesParams`, `fileContentParams`, `grepParams`, `reindexParams`, `topContributorsParams`) are defined in `internal/mcp/server.go`.

## Supported Tree-sitter Languages

For `get_file_symbols` and semantic chunking:

| Language | Extension(s) | Top-level Kinds |
|---|---|---|
| Go | `.go` | function_declaration, method_declaration, type_declaration, const_declaration, var_declaration |
| JavaScript | `.js`, `.jsx` | function_declaration, class_declaration, lexical_declaration, variable_declaration, export_statement |
| TypeScript | `.ts`, `.tsx` | Same as JavaScript + interface_declaration, type_alias_declaration |
| Python | `.py` | function_definition, class_definition, decorated_definition |
| Java | `.java` | class_declaration, interface_declaration, enum_declaration, annotation_type_declaration, record_declaration |
| Rust | `.rs` | function_item, struct_item, enum_item, trait_item, impl_item, mod_item, const_item, static_item, type_item, macro_definition |
| C | `.c` | function_definition, struct_specifier, union_specifier, enum_specifier, type_definition, preproc_def, preproc_function_def |
| C++ | `.cpp`, `.cc`, `.cxx` | function_definition, class_specifier, struct_specifier, enum_specifier, type_definition, namespace_definition, template_declaration, linkage_specification |
| PHP | `.php` | class_declaration, function_definition, interface_declaration, trait_declaration, namespace_definition |
| Ruby | `.rb` | class, module, method, singleton_method |
| HTML | `.html` | element, script_element, style_element |
| JSON | `.json` | object, array, pair |
