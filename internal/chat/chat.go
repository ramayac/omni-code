package chat

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
)

const systemPrompt = `You are omni-code, a local codebase assistant. You have access to tools that let you search indexed code, inspect repositories, read files, view git history, and more. Use these tools to answer the user's questions about their local codebases. Be concise and helpful.`

const maxToolRoundtrips = 10

// RunREPL starts the interactive chat loop. It reads user input from stdin,
// sends messages to the OpenAI-compatible API, executes tool calls, and
// prints assistant responses to stdout.
func RunREPL(ctx context.Context, client *Client, tools *ToolRegistry) error {
	messages := []Message{
		{Role: RoleSystem, Content: systemPrompt},
	}

	reader := bufio.NewReader(os.Stdin)

	fmt.Println("omni-code chat (type /quit to exit, /clear to reset)")
	fmt.Println()

	for {
		fmt.Print("\033[1;36myou>\033[0m ")
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				fmt.Println()
				return nil
			}
			return fmt.Errorf("read input: %w", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		switch {
		case line == "/quit" || line == "/exit":
			fmt.Println("bye!")
			return nil
		case line == "/clear":
			messages = []Message{{Role: RoleSystem, Content: systemPrompt}}
			fmt.Println("  (conversation cleared)")
			continue
		case line == "/tools":
			fmt.Println("  Available tools:")
			for _, d := range tools.Defs {
				fmt.Printf("    - %s: %s\n", d.Function.Name, d.Function.Description)
			}
			continue
		case line == "/help":
			fmt.Println("  Commands:")
			fmt.Println("    /quit    exit chat")
			fmt.Println("    /clear   reset conversation")
			fmt.Println("    /tools   list available tools")
			fmt.Println("    /help    show this help")
			continue
		}

		messages = append(messages, Message{Role: RoleUser, Content: line})

		// Tool call loop: the LLM may request multiple rounds of tool calls.
		for round := 0; round < maxToolRoundtrips; round++ {
			resp, err := client.Complete(ctx, messages, tools.Defs)
			if err != nil {
				fmt.Fprintf(os.Stderr, "\033[1;31merror:\033[0m %v\n", err)
				break
			}

			messages = append(messages, *resp)

			if len(resp.ToolCalls) == 0 {
				// Final text response.
				if resp.Content != "" {
					fmt.Printf("\n\033[1;32momni-code>\033[0m %s\n\n", resp.Content)
				}
				break
			}

			// Execute each tool call and feed results back.
			for _, tc := range resp.ToolCalls {
				fmt.Printf("\033[2m  ⚙ %s\033[0m\n", tc.Function.Name)
				log.Printf("[chat] tool call: %s(%s)", tc.Function.Name, truncate(tc.Function.Arguments, 200))

				result, err := tools.Call(ctx, tc.Function.Name, tc.Function.Arguments)
				if err != nil {
					result = fmt.Sprintf("error: %v", err)
					log.Printf("[chat] tool error: %s: %v", tc.Function.Name, err)
				}

				messages = append(messages, Message{
					Role:       RoleTool,
					Content:    result,
					ToolCallID: tc.ID,
				})
			}
		}
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
