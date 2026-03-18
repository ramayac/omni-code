package chunker

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_javascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"

	"github.com/ramayac/omni-code/internal/db"
)

const (
	maxChunkChars    = 3200 // ~800 tokens at 4 chars/token
	overlapChars     = 200  // ~50-token overlap for large-node splits
	smallFileThresh  = 1000 // files smaller than this become a single chunk
	lineChunkWords   = 500  // words per line-based chunk
	lineOverlapWords = 50   // word overlap between consecutive line-based chunks
)

// goTopKinds lists the tree-sitter node types that represent top-level Go declarations.
var goTopKinds = map[string]bool{
	"function_declaration": true,
	"method_declaration":   true,
	"type_declaration":     true,
	"const_declaration":    true,
	"var_declaration":      true,
}

// jsTopKinds covers top-level declarations for JavaScript and TypeScript.
var jsTopKinds = map[string]bool{
	"function_declaration":   true,
	"class_declaration":      true,
	"lexical_declaration":    true,
	"variable_declaration":   true,
	"export_statement":       true,
	"interface_declaration":  true,
	"type_alias_declaration": true,
}

// pyTopKinds lists top-level declaration node types for Python.
var pyTopKinds = map[string]bool{
	"function_definition":  true,
	"class_definition":     true,
	"decorated_definition": true,
}

// ChunkFile splits source file content into semantically meaningful chunks suitable for
// embedding. It is the package's primary entry point and satisfies indexer.ChunkFunc.
func ChunkFile(repo, path, content, lang string) ([]db.Chunk, error) {
	// Small-file shortcut: entire file fits in one chunk.
	if len(content) < smallFileThresh {
		lineCount := strings.Count(content, "\n") + 1
		return []db.Chunk{makeChunk(repo, path, lang, content, 1, lineCount, nil)}, nil
	}

	// Try tree-sitter for supported code languages.
	switch lang {
	case "go":
		if chunks, err := chunkCode(repo, path, content, lang,
			sitter.NewLanguage(tree_sitter_go.Language()), goTopKinds); err == nil && len(chunks) > 0 {
			return chunks, nil
		}
	case "javascript":
		if chunks, err := chunkCode(repo, path, content, lang,
			sitter.NewLanguage(tree_sitter_javascript.Language()), jsTopKinds); err == nil && len(chunks) > 0 {
			return chunks, nil
		}
	case "typescript":
		if chunks, err := chunkCode(repo, path, content, lang,
			sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript()), jsTopKinds); err == nil && len(chunks) > 0 {
			return chunks, nil
		}
	case "python":
		if chunks, err := chunkCode(repo, path, content, lang,
			sitter.NewLanguage(tree_sitter_python.Language()), pyTopKinds); err == nil && len(chunks) > 0 {
			return chunks, nil
		}
	}

	// Fallback: line-based splitting for text, markdown, and unsupported languages.
	return chunkByLines(repo, path, content, lang), nil
}

// chunkCode parses source with tree-sitter and emits one chunk per top-level declaration.
// If a declaration exceeds maxChunkChars it is further split with overlap.
func chunkCode(repo, path, content, lang string, language *sitter.Language, topKinds map[string]bool) ([]db.Chunk, error) {
	parser := sitter.NewParser()
	defer parser.Close()

	if err := parser.SetLanguage(language); err != nil {
		return nil, fmt.Errorf("set tree-sitter language: %w", err)
	}

	src := []byte(content)
	tree := parser.Parse(src, nil)
	if tree == nil {
		return nil, fmt.Errorf("tree-sitter parse returned nil tree")
	}
	defer tree.Close()

	root := tree.RootNode()
	cursor := root.Walk()
	defer cursor.Close()

	var chunks []db.Chunk
	for _, child := range root.NamedChildren(cursor) {
		if !topKinds[child.Kind()] {
			continue
		}

		// tree-sitter row indices are 0-based; convert to 1-based line numbers.
		startLine := int(child.StartPosition().Row) + 1
		endLine := int(child.EndPosition().Row) + 1
		nodeText := string(src[child.StartByte():child.EndByte()])

		meta := map[string]string{"kind": child.Kind()}
		if nameNode := child.ChildByFieldName("name"); nameNode != nil {
			meta["name"] = string(src[nameNode.StartByte():nameNode.EndByte()])
		}

		if len(nodeText) <= maxChunkChars {
			chunks = append(chunks, makeChunk(repo, path, lang, nodeText, startLine, endLine, meta))
		} else {
			chunks = append(chunks, splitLargeNode(repo, path, lang, nodeText, startLine, meta)...)
		}
	}
	return chunks, nil
}

// splitLargeNode breaks an oversized tree-sitter node into line-bounded sub-chunks,
// applying a character-counted overlap between consecutive chunks.
// fileStartLine is the 1-based line number where the node begins within the file.
func splitLargeNode(repo, path, lang, text string, fileStartLine int, meta map[string]string) []db.Chunk {
	lines := strings.Split(text, "\n")
	var chunks []db.Chunk

	start := 0
	for start < len(lines) {
		// Accumulate lines until the next one would push the chunk over maxChunkChars.
		end := start
		size := 0
		for end < len(lines) {
			size += len(lines[end]) + 1 // +1 for the \n
			if size > maxChunkChars && end > start {
				break
			}
			end++
		}
		if end == start {
			end = start + 1 // always include at least one line
		}

		chunkText := strings.Join(lines[start:end], "\n")
		chunkStart := fileStartLine + start
		chunkEnd := fileStartLine + end - 1
		chunks = append(chunks, makeChunk(repo, path, lang, chunkText, chunkStart, chunkEnd, meta))

		// Walk back from end to compute how many lines sum to ~overlapChars.
		overlapLineCount := 0
		overlapSize := 0
		for i := end - 1; i >= start && overlapSize < overlapChars; i-- {
			overlapSize += len(lines[i]) + 1
			overlapLineCount++
		}
		// Never overlap so much that the window cannot advance.
		if overlapLineCount >= end-start {
			overlapLineCount = (end - start) / 2
		}

		next := end - overlapLineCount
		if next <= start {
			next = end
		}
		start = next
	}
	return chunks
}

// chunkByLines splits content into word-bounded chunks with a word-count overlap.
// Source lines are preserved so the original formatting is visible in each chunk.
func chunkByLines(repo, path, content, lang string) []db.Chunk {
	lines := strings.Split(content, "\n")
	// Drop the empty trailing element produced by a file that ends with \n.
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var chunks []db.Chunk

	start := 0
	for start < len(lines) {
		// Accumulate lines until the word count target is reached.
		wordCount := 0
		end := start
		for end < len(lines) {
			wordCount += len(strings.Fields(lines[end]))
			end++
			if wordCount >= lineChunkWords {
				break
			}
		}

		chunkText := strings.Join(lines[start:end], "\n")
		startLine := start + 1 // 1-based
		endLine := end         // lines[end-1] is the last line ⇒ 1-based index is end

		chunks = append(chunks, makeChunk(repo, path, lang, chunkText, startLine, endLine, nil))

		// Overlap: walk back from end to find how many lines cover lineOverlapWords words.
		overlapLines := 0
		overlapWords := 0
		for i := end - 1; i >= start && overlapWords < lineOverlapWords; i-- {
			overlapWords += len(strings.Fields(lines[i]))
			overlapLines++
		}
		if overlapLines >= end-start {
			overlapLines = (end - start) / 2
		}

		next := end - overlapLines
		if next <= start {
			next = end
		}
		start = next
	}
	return chunks
}

// chunkID generates a deterministic SHA-256 identifier for a chunk based on its
// repository, file path, and starting line number.
func chunkID(repo, path string, startLine int) string {
	key := fmt.Sprintf("%s\x00%s\x00%d", repo, path, startLine)
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// makeChunk constructs a db.Chunk with a deterministic ID.
func makeChunk(repo, path, lang, content string, startLine, endLine int, meta map[string]string) db.Chunk {
	return db.Chunk{
		ID:        chunkID(repo, path, startLine),
		Repo:      repo,
		Path:      path,
		Language:  lang,
		Content:   content,
		StartLine: startLine,
		EndLine:   endLine,
		Metadata:  meta,
	}
}
