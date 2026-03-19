package db

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strings"

	chromadb "github.com/amikos-tech/chroma-go/pkg/api/v2"
	"github.com/amikos-tech/chroma-go/pkg/embeddings"

	"github.com/ramayac/omni-code/internal/embedder"
)

// RepoMeta holds per-repository indexing state stored in the `repos` collection.
type RepoMeta struct {
	Repo              string
	RootPath          string
	DefaultBranch     string
	CurrentBranch     string
	LastIndexedCommit string
	LastIndexedAt     string // RFC3339
	FileCount         int64
	ChunkCount        int64
	IndexMode         string // "full" or "incremental"
	DurationMs        int64
}

// QueryOpts controls how QueryChunks filters, ranks, and post-processes results.
type QueryOpts struct {
	NResults     int      // number of results to return (0 → default 10)
	RepoFilter   string   // restrict to a single repo (empty → all)
	LangFilter   string   // restrict by language metadata field (empty → all)
	ExtFilters   []string // post-filter by file extension, e.g. [".go", ".ts"]
	MinScore     float32  // suppress results below this similarity score (0 → disabled)
	Dedup        bool     // keep only the highest-score chunk per file
	ContextLines int      // extend start/end lines by this many lines (reads disk)
	Hybrid       bool     // enable BM25 + vector hybrid re-ranking (RRF)
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

// FileMeta holds change-detection metadata for an indexed file.
type FileMeta struct {
	Repo  string
	Path  string
	Size  int64
	MTime int64 // Unix timestamp (seconds)
	Hash  string
}

// ChromaClient wraps the ChromaDB HTTP client and manages collections.
type ChromaClient struct {
	client      chromadb.Client
	files       chromadb.Collection
	chunks      chromadb.Collection
	repos       chromadb.Collection
	extEmbedder embedder.Embedder // nil = use ChromaDB built-in EF
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

// EnsureCollections creates or retrieves the `files`, `chunks`, and `repos`
// collections. ef is the embedding function for the chunks collection; pass
// nil to skip setting a server-side EF (use when providing external embeddings).
func (c *ChromaClient) EnsureCollections(ctx context.Context, ef embeddings.EmbeddingFunction) error {
	var err error
	c.files, err = c.client.GetOrCreateCollection(ctx, "files",
		chromadb.WithIfNotExistsCreate(),
	)
	if err != nil {
		return fmt.Errorf("failed to ensure 'files' collection: %w", err)
	}

	chunkOpts := []chromadb.CreateCollectionOption{chromadb.WithIfNotExistsCreate()}
	if ef != nil {
		chunkOpts = append(chunkOpts, chromadb.WithEmbeddingFunctionCreate(ef))
	}
	c.chunks, err = c.client.GetOrCreateCollection(ctx, "chunks", chunkOpts...)
	if err != nil {
		return fmt.Errorf("failed to ensure 'chunks' collection: %w", err)
	}

	c.repos, err = c.client.GetOrCreateCollection(ctx, "repos",
		chromadb.WithIfNotExistsCreate(),
	)
	if err != nil {
		return fmt.Errorf("failed to ensure 'repos' collection: %w", err)
	}
	return nil
}

// ResetAllCollections drops and recreates the files, chunks, and repos
// collections. ef is the embedding function for the chunks collection.
func (c *ChromaClient) ResetAllCollections(ctx context.Context, ef embeddings.EmbeddingFunction) error {
	for _, name := range []string{"files", "chunks", "repos"} {
		if err := c.client.DeleteCollection(ctx, name); err != nil {
			log.Printf("[db] delete collection %q: %v (may not exist, continuing)", name, err)
		}
	}
	return c.EnsureCollections(ctx, ef)
}

// SetEmbedder configures an external embedding backend. When set, UpsertChunks
// will compute embeddings via this backend instead of ChromaDB's built-in EF.
func (c *ChromaClient) SetEmbedder(e embedder.Embedder) {
	c.extEmbedder = e
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
		chromadb.WithTexts(path),
		chromadb.WithMetadatas(docMeta),
	)
	if err != nil {
		return fmt.Errorf("failed to upsert file meta for %s/%s: %w", repo, path, err)
	}
	return nil
}

// UpsertBatchFileMeta stores or updates file metadata in one request.
func (c *ChromaClient) UpsertBatchFileMeta(ctx context.Context, metas []FileMeta) error {
	if len(metas) == 0 {
		return nil
	}
	ids := make([]chromadb.DocumentID, len(metas))
	texts := make([]string, len(metas))
	docMetas := make([]chromadb.DocumentMetadata, len(metas))
	for i, fm := range metas {
		ids[i] = fileDocID(fm.Repo, fm.Path)
		texts[i] = fm.Path
		docMetas[i] = chromadb.NewDocumentMetadata(
			chromadb.NewStringAttribute("repo", fm.Repo),
			chromadb.NewStringAttribute("path", fm.Path),
			chromadb.NewStringAttribute("hash", fm.Hash),
			chromadb.NewIntAttribute("size", fm.Size),
			chromadb.NewIntAttribute("mtime", fm.MTime),
		)
	}
	if err := c.files.Upsert(ctx,
		chromadb.WithIDs(ids...),
		chromadb.WithTexts(texts...),
		chromadb.WithMetadatas(docMetas...),
	); err != nil {
		return fmt.Errorf("failed to upsert %d file metas: %w", len(metas), err)
	}
	return nil
}

// UpsertChunks batch-upserts code/text chunks into the `chunks` collection.
// When an external embedder is configured (SetEmbedder), embeddings are computed
// externally and passed to ChromaDB; otherwise ChromaDB's built-in EF is used.
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

	opts := []chromadb.CollectionAddOption{
		chromadb.WithIDs(ids...),
		chromadb.WithTexts(texts...),
		chromadb.WithMetadatas(metas...),
	}

	if c.extEmbedder != nil {
		vecs, err := c.extEmbedder.Embed(ctx, texts)
		if err != nil {
			return fmt.Errorf("external embedder: %w", err)
		}
		embValues := make([]embeddings.Embedding, len(vecs))
		for i, v := range vecs {
			embValues[i] = embeddings.NewEmbeddingFromFloat32(v)
		}
		opts = append(opts, chromadb.WithEmbeddings(embValues...))
	}

	if err := c.chunks.Upsert(ctx, opts...); err != nil {
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

// DeleteFileMeta removes the change-detection metadata for a single file.
func (c *ChromaClient) DeleteFileMeta(ctx context.Context, repo, path string) error {
	err := c.files.Delete(ctx,
		chromadb.WithIDsDelete(fileDocID(repo, path)),
	)
	if err != nil {
		return fmt.Errorf("failed to delete file meta for %s/%s: %w", repo, path, err)
	}
	return nil
}

// DeleteRepoChunks removes all chunks for every file in a repository.
func (c *ChromaClient) DeleteRepoChunks(ctx context.Context, repo string) error {
	err := c.chunks.Delete(ctx,
		chromadb.WithWhereDelete(chromadb.EqString("repo", repo)),
	)
	if err != nil {
		return fmt.Errorf("failed to delete chunks for repo %s: %w", repo, err)
	}
	return nil
}

// DeleteRepoFileMeta removes the change-detection metadata for all files in a repo.
func (c *ChromaClient) DeleteRepoFileMeta(ctx context.Context, repo string) error {
	err := c.files.Delete(ctx,
		chromadb.WithWhereDelete(chromadb.EqString("repo", repo)),
	)
	if err != nil {
		return fmt.Errorf("failed to delete file metas for repo %s: %w", repo, err)
	}
	return nil
}

// QueryAllFileMeta returns the change-detection metadata for all indexed files
// belonging to the given repository.
func (c *ChromaClient) QueryAllFileMeta(ctx context.Context, repo string) ([]FileMeta, error) {
	result, err := c.files.Get(ctx,
		chromadb.WithWhereGet(chromadb.EqString("repo", repo)),
		chromadb.WithIncludeGet(chromadb.IncludeMetadatas),
	)
	if err != nil {
		return nil, fmt.Errorf("query file meta for repo %s: %w", repo, err)
	}
	metas := result.GetMetadatas()
	out := make([]FileMeta, 0, len(metas))
	for _, m := range metas {
		if m == nil {
			continue
		}
		fm := FileMeta{Repo: repo}
		fm.Path, _ = m.GetString("path")
		fm.Size, _ = m.GetInt("size")
		fm.MTime, _ = m.GetInt("mtime")
		fm.Hash, _ = m.GetString("hash")
		out = append(out, fm)
	}
	return out, nil
}

// GetBatchFileMeta returns file metadata keyed by absolute path for one repo.
func (c *ChromaClient) GetBatchFileMeta(ctx context.Context, repo string) (map[string]*FileMeta, error) {
	rows, err := c.QueryAllFileMeta(ctx, repo)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*FileMeta, len(rows))
	for i := range rows {
		row := rows[i]
		rowCopy := row
		out[row.Path] = &rowCopy
	}
	return out, nil
}

// UpsertRepoMeta stores or updates per-repository indexing state.
func (c *ChromaClient) UpsertRepoMeta(ctx context.Context, meta RepoMeta) error {
	docMeta := chromadb.NewDocumentMetadata(
		chromadb.NewStringAttribute("repo", meta.Repo),
		chromadb.NewStringAttribute("root_path", meta.RootPath),
		chromadb.NewStringAttribute("default_branch", meta.DefaultBranch),
		chromadb.NewStringAttribute("current_branch", meta.CurrentBranch),
		chromadb.NewStringAttribute("last_indexed_commit", meta.LastIndexedCommit),
		chromadb.NewStringAttribute("last_indexed_at", meta.LastIndexedAt),
		chromadb.NewStringAttribute("index_mode", meta.IndexMode),
		chromadb.NewIntAttribute("file_count", meta.FileCount),
		chromadb.NewIntAttribute("chunk_count", meta.ChunkCount),
		chromadb.NewIntAttribute("duration_ms", meta.DurationMs),
	)
	err := c.repos.Upsert(ctx,
		chromadb.WithIDs(chromadb.DocumentID(meta.Repo)),
		chromadb.WithTexts(meta.Repo),
		chromadb.WithMetadatas(docMeta),
	)
	if err != nil {
		return fmt.Errorf("upsert repo meta for %s: %w", meta.Repo, err)
	}
	return nil
}

// GetRepoMeta retrieves indexing state for a single repository.
// Returns nil without error if the repo has never been indexed.
func (c *ChromaClient) GetRepoMeta(ctx context.Context, repo string) (*RepoMeta, error) {
	result, err := c.repos.Get(ctx,
		chromadb.WithIDsGet(chromadb.DocumentID(repo)),
		chromadb.WithIncludeGet(chromadb.IncludeMetadatas),
	)
	if err != nil {
		return nil, fmt.Errorf("get repo meta for %s: %w", repo, err)
	}
	ids := result.GetIDs()
	if len(ids) == 0 {
		return nil, nil
	}
	metas := result.GetMetadatas()
	if len(metas) == 0 || metas[0] == nil {
		return nil, nil
	}
	return repoMetaFromDoc(metas[0]), nil
}

// ListRepoMeta returns indexing state for all known repositories.
func (c *ChromaClient) ListRepoMeta(ctx context.Context) ([]RepoMeta, error) {
	result, err := c.repos.Get(ctx,
		chromadb.WithIncludeGet(chromadb.IncludeMetadatas),
		chromadb.WithLimitGet(10000),
	)
	if err != nil {
		return nil, fmt.Errorf("list repo meta: %w", err)
	}
	metas := result.GetMetadatas()
	out := make([]RepoMeta, 0, len(metas))
	for _, m := range metas {
		if m == nil {
			continue
		}
		out = append(out, *repoMetaFromDoc(m))
	}
	return out, nil
}

// DeleteRepoMeta removes indexing state for a single repository.
func (c *ChromaClient) DeleteRepoMeta(ctx context.Context, repo string) error {
	err := c.repos.Delete(ctx,
		chromadb.WithIDsDelete(chromadb.DocumentID(repo)),
	)
	if err != nil {
		return fmt.Errorf("delete repo meta for %s: %w", repo, err)
	}
	return nil
}

// DeleteAllRepoMeta removes indexing state for all repositories.
func (c *ChromaClient) DeleteAllRepoMeta(ctx context.Context) error {
	result, err := c.repos.Get(ctx, chromadb.WithLimitGet(10000))
	if err != nil {
		return fmt.Errorf("list repos for deletion: %w", err)
	}
	ids := result.GetIDs()
	if len(ids) == 0 {
		return nil
	}
	return c.repos.Delete(ctx, chromadb.WithIDsDelete(ids...))
}

func repoMetaFromDoc(m chromadb.DocumentMetadata) *RepoMeta {
	rm := &RepoMeta{}
	rm.Repo, _ = m.GetString("repo")
	rm.RootPath, _ = m.GetString("root_path")
	rm.DefaultBranch, _ = m.GetString("default_branch")
	rm.CurrentBranch, _ = m.GetString("current_branch")
	rm.LastIndexedCommit, _ = m.GetString("last_indexed_commit")
	rm.LastIndexedAt, _ = m.GetString("last_indexed_at")
	rm.IndexMode, _ = m.GetString("index_mode")
	rm.FileCount, _ = m.GetInt("file_count")
	rm.ChunkCount, _ = m.GetInt("chunk_count")
	rm.DurationMs, _ = m.GetInt("duration_ms")
	return rm
}

// QueryChunks performs semantic similarity search on the indexed code chunks.
// opts controls filtering, deduplication, context expansion, and hybrid ranking.
func (c *ChromaClient) QueryChunks(ctx context.Context, queryText string, opts QueryOpts) ([]ChunkResult, error) {
	nResults := opts.NResults
	if nResults <= 0 {
		nResults = 10
	}
	// Fetch more candidates when hybrid or dedup is enabled so we have room
	// to filter/re-rank and still return nResults items.
	fetch := nResults
	if opts.Hybrid || opts.Dedup || len(opts.ExtFilters) > 0 || opts.MinScore > 0 {
		fetch = nResults * 5
		if fetch < 50 {
			fetch = 50
		}
	}

	qopts := []chromadb.CollectionQueryOption{
		chromadb.WithQueryTexts(queryText),
		chromadb.WithNResults(fetch),
		chromadb.WithIncludeQuery(
			chromadb.IncludeDocuments,
			chromadb.IncludeMetadatas,
			chromadb.IncludeDistances,
		),
	}

	// Build where filter.
	var whereClauses []chromadb.WhereClause
	if opts.RepoFilter != "" {
		whereClauses = append(whereClauses, chromadb.EqString("repo", opts.RepoFilter))
	}
	if opts.LangFilter != "" {
		whereClauses = append(whereClauses, chromadb.EqString("language", opts.LangFilter))
	}
	switch len(whereClauses) {
	case 1:
		qopts = append(qopts, chromadb.WithWhereQuery(whereClauses[0]))
	case 2:
		qopts = append(qopts, chromadb.WithWhereQuery(chromadb.And(whereClauses[0], whereClauses[1])))
	}

	result, err := c.chunks.Query(ctx, qopts...)
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

	// Post-processing pipeline.
	results = applyExtFilter(results, opts.ExtFilters)
	results = applyMinScore(results, opts.MinScore)
	if opts.Dedup {
		results = deduplicateByFile(results)
	}
	if opts.Hybrid {
		results = bm25Rerank(queryText, results)
	}
	if len(results) > nResults {
		results = results[:nResults]
	}
	if opts.ContextLines > 0 {
		results = expandContextLines(results, opts.ContextLines)
	}
	return results, nil
}

// applyExtFilter post-filters results by file extension (case-insensitive).
func applyExtFilter(results []ChunkResult, exts []string) []ChunkResult {
	if len(exts) == 0 {
		return results
	}
	extSet := make(map[string]bool, len(exts))
	for _, e := range exts {
		extSet[strings.ToLower(e)] = true
	}
	out := results[:0]
	for _, r := range results {
		dot := strings.LastIndex(r.Path, ".")
		if dot >= 0 && extSet[strings.ToLower(r.Path[dot:])] {
			out = append(out, r)
		}
	}
	return out
}

// applyMinScore removes results whose similarity score exceeds the threshold.
// ChromaDB returns distance (lower = better); 0 min-score disables this filter.
func applyMinScore(results []ChunkResult, minScore float32) []ChunkResult {
	if minScore <= 0 {
		return results
	}
	out := results[:0]
	for _, r := range results {
		if r.Score <= minScore {
			out = append(out, r)
		}
	}
	return out
}

// deduplicateByFile keeps only the highest-scoring (lowest distance) chunk per file.
func deduplicateByFile(results []ChunkResult) []ChunkResult {
	seen := make(map[string]int) // repo:path index in out
	out := results[:0]
	for _, r := range results {
		if idx, ok := seen[r.Repo+":"+r.Path]; ok {
			if r.Score < out[idx].Score {
				out[idx] = r
			}
		} else {
			seen[r.Repo+":"+r.Path] = len(out)
			out = append(out, r)
		}
	}
	return out
}

// expandContextLines reads extra lines from disk around each chunk's boundaries.
func expandContextLines(results []ChunkResult, n int) []ChunkResult {
	for i, r := range results {
		content, start, end, err := readLinesFromFile(r.Path, r.StartLine-n, r.EndLine+n)
		if err != nil {
			continue // leave the original if the file is unreadable
		}
		results[i].Content = content
		results[i].StartLine = start
		results[i].EndLine = end
	}
	return results
}

// readLinesFromFile reads lines [startLine, endLine] (1-based, inclusive) from
// path. It clamps the range to the actual file bounds.
func readLinesFromFile(path string, startLine, endLine int) (content string, actualStart, actualEnd int, err error) {
	f, openErr := os.Open(path)
	if openErr != nil {
		return "", 0, 0, openErr
	}
	defer f.Close()

	if startLine < 1 {
		startLine = 1
	}

	var lines []string
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum >= startLine {
			lines = append(lines, scanner.Text())
		}
		if lineNum >= endLine {
			break
		}
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return "", 0, 0, scanErr
	}
	actualStart = startLine
	actualEnd = startLine + len(lines) - 1
	return strings.Join(lines, "\n"), actualStart, actualEnd, nil
}

// ---- BM25 re-ranking ----

const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// bm25Rerank applies BM25 scoring on the candidate set and fuses with the
// original vector rank using Reciprocal Rank Fusion (k=60).
func bm25Rerank(query string, results []ChunkResult) []ChunkResult {
	if len(results) <= 1 {
		return results
	}
	queryTerms := tokenize(strings.ToLower(query))
	if len(queryTerms) == 0 {
		return results
	}

	// Build per-document term frequencies and corpus DF.
	docFreqs := make(map[string]int)
	docTermFreqs := make([]map[string]int, len(results))
	docLens := make([]float64, len(results))
	for i, r := range results {
		words := tokenize(strings.ToLower(r.Content))
		docLens[i] = float64(len(words))
		tf := countTermFreq(words)
		docTermFreqs[i] = tf
		for term := range tf {
			docFreqs[term]++
		}
	}

	avgDocLen := 0.0
	for _, l := range docLens {
		avgDocLen += l
	}
	avgDocLen /= float64(len(results))

	n := float64(len(results))
	bm25Scores := make([]float64, len(results))
	for i := range results {
		score := 0.0
		for _, qt := range queryTerms {
			f := float64(docTermFreqs[i][qt])
			if f == 0 {
				continue
			}
			df := float64(docFreqs[qt])
			idf := math.Log((n-df+0.5)/(df+0.5) + 1)
			tfNorm := (f * (bm25K1 + 1)) / (f + bm25K1*(1-bm25B+bm25B*(docLens[i]/avgDocLen)))
			score += idf * tfNorm
		}
		bm25Scores[i] = score
	}

	// Build BM25 rank order.
	bm25Order := make([]int, len(results))
	for i := range bm25Order {
		bm25Order[i] = i
	}
	sort.Slice(bm25Order, func(a, b int) bool {
		return bm25Scores[bm25Order[a]] > bm25Scores[bm25Order[b]]
	})
	bm25Rank := make([]int, len(results))
	for rank, origIdx := range bm25Order {
		bm25Rank[origIdx] = rank
	}

	// Compute RRF scores and sort final result.
	const k = 60
	type scored struct {
		result ChunkResult
		rrf    float64
	}
	scoreds := make([]scored, len(results))
	for i, r := range results {
		rrf := 1.0/float64(k+i+1) + 1.0/float64(k+bm25Rank[i]+1)
		scoreds[i] = scored{result: r, rrf: rrf}
	}
	sort.Slice(scoreds, func(a, b int) bool {
		return scoreds[a].rrf > scoreds[b].rrf
	})
	out := make([]ChunkResult, len(scoreds))
	for i, s := range scoreds {
		out[i] = s.result
	}
	return out
}

func tokenize(text string) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_')
	})
	return fields
}

func countTermFreq(words []string) map[string]int {
	m := make(map[string]int, len(words))
	for _, w := range words {
		m[w]++
	}
	return m
}
