package db

import (
	"context"
	"fmt"

	chromadb "github.com/amikos-tech/chroma-go/pkg/api/v2"
	"github.com/amikos-tech/chroma-go/pkg/embeddings"
)

// FileMeta holds change-detection metadata for an indexed file.
type FileMeta struct {
	Repo  string
	Path  string
	Size  int64
	MTime int64 // Unix timestamp (seconds)
	Hash  string
}

// Chunk is a semantic unit of code or text, ready for embedding and storage.
type Chunk struct {
	ID        string
	Repo      string
	Path      string
	Language  string
	Content   string
	StartLine int
	EndLine   int
	Metadata  map[string]string
}

// ChunkResult is returned from a semantic similarity search.
type ChunkResult struct {
	ID        string
	Repo      string
	Path      string
	Language  string
	Content   string
	StartLine int
	EndLine   int
	Score     float32
}

// ChromaClient wraps the ChromaDB HTTP client and manages the files/chunks collections.
type ChromaClient struct {
	client chromadb.Client
	files  chromadb.Collection
	chunks chromadb.Collection
}

// NewChromaClient creates a new ChromaClient, pings the server, and returns the wrapper.
func NewChromaClient(ctx context.Context, baseURL string) (*ChromaClient, error) {
	client, err := chromadb.NewHTTPClient(
		chromadb.WithBaseURL(baseURL),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create chroma HTTP client: %w", err)
	}
	if err := client.Heartbeat(ctx); err != nil {
		return nil, fmt.Errorf("chroma server unreachable at %s: %w", baseURL, err)
	}
	return &ChromaClient{client: client}, nil
}

// EnsureCollections creates or retrieves the `files` and `chunks` collections.
// ef is the embedding function used for semantic search on the `chunks` collection.
func (c *ChromaClient) EnsureCollections(ctx context.Context, ef embeddings.EmbeddingFunction) error {
	var err error
	c.files, err = c.client.GetOrCreateCollection(ctx, "files",
		chromadb.WithIfNotExistsCreate(),
	)
	if err != nil {
		return fmt.Errorf("failed to ensure 'files' collection: %w", err)
	}

	c.chunks, err = c.client.GetOrCreateCollection(ctx, "chunks",
		chromadb.WithIfNotExistsCreate(),
		chromadb.WithEmbeddingFunctionCreate(ef),
	)
	if err != nil {
		return fmt.Errorf("failed to ensure 'chunks' collection: %w", err)
	}
	return nil
}

// fileDocID returns the stable document ID used in the `files` collection.
func fileDocID(repo, path string) chromadb.DocumentID {
	return chromadb.DocumentID(repo + "::" + path)
}

// GetFileMeta retrieves stored change-detection metadata for a file.
// Returns nil (no error) if the file has not been indexed yet.
func (c *ChromaClient) GetFileMeta(ctx context.Context, repo, path string) (*FileMeta, error) {
	result, err := c.files.Get(ctx,
		chromadb.WithIDsGet(fileDocID(repo, path)),
		chromadb.WithIncludeGet(chromadb.IncludeMetadatas),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get file meta for %s/%s: %w", repo, path, err)
	}
	ids := result.GetIDs()
	if len(ids) == 0 {
		return nil, nil
	}
	metas := result.GetMetadatas()
	if len(metas) == 0 || metas[0] == nil {
		return nil, nil
	}
	meta := metas[0]
	fm := &FileMeta{Repo: repo, Path: path}
	fm.Size, _ = meta.GetInt("size")
	fm.MTime, _ = meta.GetInt("mtime")
	fm.Hash, _ = meta.GetString("hash")
	return fm, nil
}

// UpsertFileMeta stores or updates a file's change-detection metadata.
// No text or embedding is stored — the files collection is metadata-only.
func (c *ChromaClient) UpsertFileMeta(ctx context.Context, repo, path string, size, mtime int64, hash string) error {
	docMeta := chromadb.NewDocumentMetadata(
		chromadb.NewStringAttribute("repo", repo),
		chromadb.NewStringAttribute("path", path),
		chromadb.NewStringAttribute("hash", hash),
		chromadb.NewIntAttribute("size", size),
		chromadb.NewIntAttribute("mtime", mtime),
	)
	err := c.files.Upsert(ctx,
		chromadb.WithIDs(fileDocID(repo, path)),
		chromadb.WithMetadatas(docMeta),
	)
	if err != nil {
		return fmt.Errorf("failed to upsert file meta for %s/%s: %w", repo, path, err)
	}
	return nil
}

// UpsertChunks batch-upserts code/text chunks into the `chunks` collection.
// The chunk content is stored as document text and embedded using the collection's EF.
func (c *ChromaClient) UpsertChunks(ctx context.Context, chunks []Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	ids := make([]chromadb.DocumentID, len(chunks))
	texts := make([]string, len(chunks))
	metas := make([]chromadb.DocumentMetadata, len(chunks))

	for i, ch := range chunks {
		ids[i] = chromadb.DocumentID(ch.ID)
		texts[i] = ch.Content
		metas[i] = chromadb.NewDocumentMetadata(
			chromadb.NewStringAttribute("repo", ch.Repo),
			chromadb.NewStringAttribute("path", ch.Path),
			chromadb.NewStringAttribute("language", ch.Language),
			chromadb.NewIntAttribute("start_line", int64(ch.StartLine)),
			chromadb.NewIntAttribute("end_line", int64(ch.EndLine)),
		)
	}

	err := c.chunks.Upsert(ctx,
		chromadb.WithIDs(ids...),
		chromadb.WithTexts(texts...),
		chromadb.WithMetadatas(metas...),
	)
	if err != nil {
		return fmt.Errorf("failed to upsert %d chunks: %w", len(chunks), err)
	}
	return nil
}

// DeleteFileChunks removes all chunks for a given file from the chunks collection.
// Call this before re-indexing a changed file to avoid stale chunk accumulation.
func (c *ChromaClient) DeleteFileChunks(ctx context.Context, repo, path string) error {
	err := c.chunks.Delete(ctx,
		chromadb.WithWhereDelete(
			chromadb.And(
				chromadb.EqString("repo", repo),
				chromadb.EqString("path", path),
			),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to delete chunks for %s/%s: %w", repo, path, err)
	}
	return nil
}

// QueryChunks performs semantic similarity search on the indexed code chunks.
// If repoFilter is non-empty, results are constrained to that repository.
func (c *ChromaClient) QueryChunks(ctx context.Context, queryText string, nResults int, repoFilter string) ([]ChunkResult, error) {
	opts := []chromadb.CollectionQueryOption{
		chromadb.WithQueryTexts(queryText),
		chromadb.WithNResults(nResults),
		chromadb.WithIncludeQuery(
			chromadb.IncludeDocuments,
			chromadb.IncludeMetadatas,
			chromadb.IncludeDistances,
		),
	}
	if repoFilter != "" {
		opts = append(opts, chromadb.WithWhereQuery(chromadb.EqString("repo", repoFilter)))
	}

	result, err := c.chunks.Query(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to query chunks: %w", err)
	}

	idGroups := result.GetIDGroups()
	if len(idGroups) == 0 {
		return nil, nil
	}

	ids := idGroups[0]
	docGroups := result.GetDocumentsGroups()
	metaGroups := result.GetMetadatasGroups()
	distGroups := result.GetDistancesGroups()

	results := make([]ChunkResult, 0, len(ids))
	for i, id := range ids {
		cr := ChunkResult{ID: string(id)}
		if len(docGroups) > 0 && i < len(docGroups[0]) && docGroups[0][i] != nil {
			cr.Content = docGroups[0][i].ContentString()
		}
		if len(metaGroups) > 0 && i < len(metaGroups[0]) && metaGroups[0][i] != nil {
			meta := metaGroups[0][i]
			cr.Repo, _ = meta.GetString("repo")
			cr.Path, _ = meta.GetString("path")
			cr.Language, _ = meta.GetString("language")
			sl, _ := meta.GetInt("start_line")
			el, _ := meta.GetInt("end_line")
			cr.StartLine = int(sl)
			cr.EndLine = int(el)
		}
		if len(distGroups) > 0 && i < len(distGroups[0]) {
			cr.Score = float32(distGroups[0][i])
		}
		results = append(results, cr)
	}
	return results, nil
}
