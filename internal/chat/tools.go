package chat

import (
	"context"
	"fmt"
	"strings"

	"github.com/ramayac/omni-code/internal/db"
	internalmcp "github.com/ramayac/omni-code/internal/mcp"
)

// ToolRegistry holds the OpenAI tool definitions and a dispatch function
// that routes tool call names to MCP handler results.
type ToolRegistry struct {
	Defs     []ToolDef
	dispatch func(ctx context.Context, name, argsJSON string) (string, error)
}

// BuildToolRegistry creates tool definitions and a dispatcher from the MCP tool set.
// The MCP server.go handlers are called through the public DispatchTool function
// exposed by the mcp package.
func BuildToolRegistry(client *db.ChromaClient) *ToolRegistry {
	defs := internalmcp.ToolDefinitions()
	oaiDefs := make([]ToolDef, len(defs))
	for i, d := range defs {
		oaiDefs[i] = ToolDef{
			Type: "function",
			Function: FunctionDef{
				Name:        d.Name,
				Description: d.Description,
				Parameters:  d.InputSchema,
			},
		}
	}

	return &ToolRegistry{
		Defs: oaiDefs,
		dispatch: func(ctx context.Context, name, argsJSON string) (string, error) {
			return internalmcp.DispatchTool(ctx, client, name, argsJSON)
		},
	}
}

// Call executes a tool by name with the given JSON arguments and returns the text result.
func (r *ToolRegistry) Call(ctx context.Context, name, argsJSON string) (string, error) {
	return r.dispatch(ctx, name, argsJSON)
}

// FormatToolResult formats a tool result for display in the terminal.
func FormatToolResult(name, result string) string {
	// Truncate very long results for terminal display.
	const maxDisplay = 2000
	display := result
	if len(display) > maxDisplay {
		display = display[:maxDisplay] + fmt.Sprintf("\n... (truncated, %d bytes total)", len(result))
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "  [tool: %s]\n", name)
	for _, line := range strings.Split(display, "\n") {
		fmt.Fprintf(&sb, "  │ %s\n", line)
	}
	return sb.String()
}
