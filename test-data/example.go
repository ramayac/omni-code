// Package example contains sample Go code used for development testing.
package example

import (
	"fmt"
	"strings"
)

// Greeter says hello to people.
type Greeter struct {
	Prefix string
}

// Greet returns a greeting for the given name.
func (g *Greeter) Greet(name string) string {
	return g.Prefix + " " + name + "!"
}

// ParseNames splits a comma-separated list of names.
func ParseNames(input string) []string {
	parts := strings.Split(input, ",")
	names := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			names = append(names, trimmed)
		}
	}
	return names
}

// GreetAll greets every name in the list and returns the messages.
func GreetAll(g *Greeter, names []string) []string {
	msgs := make([]string, len(names))
	for i, n := range names {
		msgs[i] = g.Greet(n)
	}
	return msgs
}

// PrintAll writes each message to stdout.
func PrintAll(msgs []string) {
	for _, m := range msgs {
		fmt.Println(m)
	}
}
