package chunker

import (
	"fmt"
	"strings"
	"testing"
)

// buildGoSource generates a syntactically valid Go source file with n top-level
// functions to produce content that reliably exceeds the small-file threshold.
func buildGoSource(n int) string {
	var sb strings.Builder
	sb.WriteString("package example\n\nimport \"fmt\"\n\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb,
			"// Function%d is a generated function used in chunker tests.\n"+
				"func Function%d(x int) int {\n"+
				"    fmt.Println(\"Function%d called with argument\", x)\n"+
				"    result := x * %d\n"+
				"    return result\n"+
				"}\n\n",
			i, i, i, i+1)
	}
	return sb.String()
}

// ---- Small-file shortcut ----

func TestChunkFile_SmallFile(t *testing.T) {
	content := "hello, world"
	chunks, err := ChunkFile("repo", "tiny.go", content, "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk for small file, got %d", len(chunks))
	}
	c := chunks[0]
	if c.Content != content {
		t.Errorf("content mismatch: want %q, got %q", content, c.Content)
	}
	if c.StartLine != 1 {
		t.Errorf("StartLine: want 1, got %d", c.StartLine)
	}
	if c.Repo != "repo" {
		t.Errorf("Repo: want %q, got %q", "repo", c.Repo)
	}
	if c.ID == "" {
		t.Error("chunk ID must not be empty")
	}
}

// ---- Tree-sitter: Go ----

func TestChunkFile_GoCode(t *testing.T) {
	content := buildGoSource(12) // 12 functions -> well above 1000 chars
	if len(content) < smallFileThresh {
		t.Fatalf("test precondition: Go source must exceed %d chars, got %d", smallFileThresh, len(content))
	}

	chunks, err := ChunkFile("repo", "example.go", content, "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected >= 2 chunks from Go tree-sitter, got %d", len(chunks))
	}

	names := map[string]bool{}
	for _, c := range chunks {
		if c.Path != "example.go" {
			t.Errorf("unexpected Path %q", c.Path)
		}
		if c.Language != "go" {
			t.Errorf("unexpected Language %q", c.Language)
		}
		if c.ID == "" {
			t.Error("chunk ID must not be empty")
		}
		if c.StartLine < 1 {
			t.Errorf("StartLine must be >= 1, got %d", c.StartLine)
		}
		if c.EndLine < c.StartLine {
			t.Errorf("EndLine %d < StartLine %d", c.EndLine, c.StartLine)
		}
		if n, ok := c.Metadata["name"]; ok {
			names[n] = true
		}
	}
	if !names["Function0"] {
		t.Error("expected Function0 to appear in chunk metadata")
	}
}

// ---- Tree-sitter: Python ----

func TestChunkFile_Python(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 15; i++ {
		fmt.Fprintf(&sb,
			"# Function %d - generated for chunker test\n"+
				"def function_%d(x):\n"+
				"    \"\"\"Return x multiplied by %d.\"\"\"\n"+
				"    return x * %d\n\n",
			i, i, i+1, i+1)
	}
	content := sb.String()
	if len(content) < smallFileThresh {
		t.Fatalf("test precondition: Python source must exceed %d chars, got %d", smallFileThresh, len(content))
	}

	chunks, err := ChunkFile("repo", "example.py", content, "python")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected >= 2 chunks from Python tree-sitter, got %d", len(chunks))
	}
	for _, c := range chunks {
		if c.Language != "python" {
			t.Errorf("unexpected Language %q", c.Language)
		}
		if c.StartLine < 1 {
			t.Errorf("StartLine must be >= 1, got %d", c.StartLine)
		}
	}
}

// ---- Tree-sitter: JavaScript ----

func TestChunkFile_JavaScript(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 15; i++ {
		fmt.Fprintf(&sb,
			"// jsFunc%d - generated for chunker test\n"+
				"function jsFunc%d(x) {\n"+
				"    // body %d\n"+
				"    return x * %d;\n"+
				"}\n\n",
			i, i, i, i+1)
	}
	content := sb.String()
	if len(content) < smallFileThresh {
		t.Fatalf("test precondition: JS source must exceed %d chars, got %d", smallFileThresh, len(content))
	}

	chunks, err := ChunkFile("repo", "example.js", content, "javascript")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected >= 2 chunks from JavaScript tree-sitter, got %d", len(chunks))
	}
	for _, c := range chunks {
		if c.Language != "javascript" {
			t.Errorf("unexpected Language %q", c.Language)
		}
	}
}

// ---- Tree-sitter: TypeScript ----

func TestChunkFile_TypeScript(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 15; i++ {
		fmt.Fprintf(&sb,
			"// tsFunc%d - generated for chunker test\n"+
				"function tsFunc%d(x: number): number {\n"+
				"    // body %d\n"+
				"    return x * %d;\n"+
				"}\n\n",
			i, i, i, i+1)
	}
	content := sb.String()
	if len(content) < smallFileThresh {
		t.Fatalf("test precondition: TS source must exceed %d chars, got %d", smallFileThresh, len(content))
	}

	chunks, err := ChunkFile("repo", "example.ts", content, "typescript")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected >= 2 chunks from TypeScript tree-sitter, got %d", len(chunks))
	}
}

// ---- Line-based fallback ----

func TestChunkFile_PlainText(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&sb, "Line %03d: this is a plain text line used to test the line-based splitter logic.\n", i)
	}
	content := sb.String()

	chunks, err := ChunkFile("repo", "notes.txt", content, "text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected >= 2 chunks for large text file, got %d", len(chunks))
	}
	for _, c := range chunks {
		if c.Content == "" {
			t.Error("empty chunk content")
		}
		if c.StartLine < 1 {
			t.Errorf("StartLine must be >= 1, got %d", c.StartLine)
		}
	}
}

// ---- Large single tree-sitter node -> split with overlap ----

func TestChunkFile_LargeGoFunction(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("package example\n\nimport \"fmt\"\n\n")
	sb.WriteString("func MassiveFunction() {\n")
	for i := 0; i < 250; i++ {
		fmt.Fprintf(&sb, "    fmt.Println(\"step %d: performing some operation on the data\")\n", i)
	}
	sb.WriteString("}\n")
	content := sb.String()

	chunks, err := ChunkFile("repo", "big.go", content, "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("large function should produce >= 2 chunks, got %d", len(chunks))
	}
	for _, c := range chunks {
		if len(c.Content) > maxChunkChars*2 {
			t.Errorf("chunk too large: %d chars (max %d)", len(c.Content), maxChunkChars)
		}
	}
}

// ---- Deterministic IDs ----

func TestChunkFile_DeterministicIDs(t *testing.T) {
	content := buildGoSource(5)
	chunks1, err1 := ChunkFile("repo", "a.go", content, "go")
	chunks2, err2 := ChunkFile("repo", "a.go", content, "go")
	if err1 != nil || err2 != nil {
		t.Fatalf("errors: %v / %v", err1, err2)
	}
	if len(chunks1) != len(chunks2) {
		t.Fatalf("non-deterministic chunk count: %d vs %d", len(chunks1), len(chunks2))
	}
	for i := range chunks1 {
		if chunks1[i].ID != chunks2[i].ID {
			t.Errorf("non-deterministic ID at chunk %d: %q vs %q", i, chunks1[i].ID, chunks2[i].ID)
		}
	}
}

// ---- ID uniqueness across files ----

func TestChunkFile_IDUniqueness(t *testing.T) {
	content := buildGoSource(5)
	chunksA, _ := ChunkFile("repo", "a.go", content, "go")
	chunksB, _ := ChunkFile("repo", "b.go", content, "go")

	seen := map[string]bool{}
	for _, c := range chunksA {
		seen[c.ID] = true
	}
	for _, c := range chunksB {
		if seen[c.ID] {
			t.Errorf("ID collision between a.go and b.go at chunk ID %q", c.ID)
		}
	}
}

// ---- ExtractSymbols ----

func TestExtractSymbols_Go(t *testing.T) {
	src := "package main\n\nfunc Foo() {}\nfunc Bar(x int) int { return x }\ntype MyType struct{}\n"
	syms := ExtractSymbols(src, "go")
	if len(syms) == 0 {
		t.Fatal("expected symbols for Go source, got none")
	}
	names := make(map[string]bool)
	kinds := make(map[string]bool)
	for _, s := range syms {
		names[s.Name] = true
		kinds[s.Kind] = true
		if s.StartLine <= 0 {
			t.Errorf("symbol %q has invalid StartLine %d", s.Name, s.StartLine)
		}
		if s.Kind == "" {
			t.Errorf("symbol %q has empty Kind", s.Name)
		}
	}
	// Foo and Bar are function_declarations with accessible name fields.
	for _, want := range []string{"Foo", "Bar"} {
		if !names[want] {
			t.Errorf("expected symbol %q, got %v", want, syms)
		}
	}
	// type_declaration is present (name may be empty depending on grammar depth).
	if !kinds["type_declaration"] {
		t.Errorf("expected kind 'type_declaration', got kinds: %v", kinds)
	}
}

func TestExtractSymbols_Python(t *testing.T) {
	src := "def greet(name):\n    return 'hello ' + name\n\nclass Animal:\n    pass\n"
	syms := ExtractSymbols(src, "python")
	if len(syms) < 2 {
		t.Fatalf("expected at least 2 symbols, got %d: %v", len(syms), syms)
	}
}

func TestExtractSymbols_Unsupported(t *testing.T) {
	syms := ExtractSymbols("some text content", "text")
	if syms != nil {
		t.Errorf("expected nil for unsupported language, got %v", syms)
	}
}
