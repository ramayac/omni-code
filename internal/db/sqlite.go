package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

type sqliteDB struct {
	db *sql.DB
}

func initSQLite(dbPath string) (*sqliteDB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("create sqlite dir: %w", err)
	}

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL")
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}

	if err := setupSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("setup sqlite schema: %w", err)
	}

	return &sqliteDB{db: db}, nil
}

func setupSchema(db *sql.DB) error {
	const schema = `
	CREATE TABLE IF NOT EXISTS repos (
		repo TEXT PRIMARY KEY,
		root_path TEXT,
		default_branch TEXT,
		current_branch TEXT,
		last_indexed_commit TEXT,
		last_indexed_at TEXT,
		index_mode TEXT,
		file_count INTEGER,
		chunk_count INTEGER,
		duration_ms INTEGER
	);

	CREATE TABLE IF NOT EXISTS files (
		repo TEXT,
		path TEXT,
		hash TEXT,
		size INTEGER,
		mtime INTEGER,
		PRIMARY KEY (repo, path)
	);

	CREATE INDEX IF NOT EXISTS idx_files_repo ON files(repo);
	`
	_, err := db.Exec(schema)
	return err
}

func (s *sqliteDB) close() error {
	return s.db.Close()
}

func (s *sqliteDB) resetAll(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "DROP TABLE IF EXISTS files")
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, "DROP TABLE IF EXISTS repos")
	if err != nil {
		return err
	}
	return setupSchema(s.db)
}

// -- Repos --

func (s *sqliteDB) UpsertRepoMeta(ctx context.Context, meta RepoMeta) error {
	const q = `
		INSERT INTO repos (repo, root_path, default_branch, current_branch, last_indexed_commit, last_indexed_at, index_mode, file_count, chunk_count, duration_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(repo) DO UPDATE SET
			root_path=excluded.root_path,
			default_branch=excluded.default_branch,
			current_branch=excluded.current_branch,
			last_indexed_commit=excluded.last_indexed_commit,
			last_indexed_at=excluded.last_indexed_at,
			index_mode=excluded.index_mode,
			file_count=excluded.file_count,
			chunk_count=excluded.chunk_count,
			duration_ms=excluded.duration_ms
	`
	_, err := s.db.ExecContext(ctx, q,
		meta.Repo, meta.RootPath, meta.DefaultBranch, meta.CurrentBranch,
		meta.LastIndexedCommit, meta.LastIndexedAt, meta.IndexMode,
		meta.FileCount, meta.ChunkCount, meta.DurationMs,
	)
	return err
}

func (s *sqliteDB) GetRepoMeta(ctx context.Context, repo string) (*RepoMeta, error) {
	row := s.db.QueryRowContext(ctx, "SELECT repo, root_path, default_branch, current_branch, last_indexed_commit, last_indexed_at, index_mode, file_count, chunk_count, duration_ms FROM repos WHERE repo = ?", repo)
	var m RepoMeta
	err := row.Scan(&m.Repo, &m.RootPath, &m.DefaultBranch, &m.CurrentBranch, &m.LastIndexedCommit, &m.LastIndexedAt, &m.IndexMode, &m.FileCount, &m.ChunkCount, &m.DurationMs)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *sqliteDB) ListRepoMeta(ctx context.Context) ([]RepoMeta, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT repo, root_path, default_branch, current_branch, last_indexed_commit, last_indexed_at, index_mode, file_count, chunk_count, duration_ms FROM repos")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var metas []RepoMeta
	for rows.Next() {
		var m RepoMeta
		if err := rows.Scan(&m.Repo, &m.RootPath, &m.DefaultBranch, &m.CurrentBranch, &m.LastIndexedCommit, &m.LastIndexedAt, &m.IndexMode, &m.FileCount, &m.ChunkCount, &m.DurationMs); err != nil {
			return nil, err
		}
		metas = append(metas, m)
	}
	return metas, rows.Err()
}

func (s *sqliteDB) DeleteRepoMeta(ctx context.Context, repo string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM repos WHERE repo = ?", repo)
	return err
}

func (s *sqliteDB) DeleteAllRepoMeta(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM repos")
	return err
}

// -- Files --

func (s *sqliteDB) UpsertFileMeta(ctx context.Context, repo, path string, size, mtime int64, hash string) error {
	const q = `
		INSERT INTO files (repo, path, hash, size, mtime)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(repo, path) DO UPDATE SET
			hash=excluded.hash,
			size=excluded.size,
			mtime=excluded.mtime
	`
	_, err := s.db.ExecContext(ctx, q, repo, path, hash, size, mtime)
	return err
}

func (s *sqliteDB) UpsertBatchFileMeta(ctx context.Context, metas []FileMeta) error {
	if len(metas) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO files (repo, path, hash, size, mtime)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(repo, path) DO UPDATE SET
			hash=excluded.hash,
			size=excluded.size,
			mtime=excluded.mtime
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, m := range metas {
		_, err := stmt.ExecContext(ctx, m.Repo, m.Path, m.Hash, m.Size, m.MTime)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *sqliteDB) QueryAllFileMeta(ctx context.Context, repo string) ([]FileMeta, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT repo, path, hash, size, mtime FROM files WHERE repo = ?", repo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var metas []FileMeta
	for rows.Next() {
		var m FileMeta
		if err := rows.Scan(&m.Repo, &m.Path, &m.Hash, &m.Size, &m.MTime); err != nil {
			return nil, err
		}
		metas = append(metas, m)
	}
	return metas, rows.Err()
}

func (s *sqliteDB) GetBatchFileMeta(ctx context.Context, repo string) (map[string]*FileMeta, error) {
	metas, err := s.QueryAllFileMeta(ctx, repo)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*FileMeta, len(metas))
	for i := range metas {
		m := metas[i]
		out[m.Path] = &m
	}
	return out, nil
}

func (s *sqliteDB) DeleteFileMeta(ctx context.Context, repo, path string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM files WHERE repo = ? AND path = ?", repo, path)
	return err
}

func (s *sqliteDB) DeleteRepoFileMeta(ctx context.Context, repo string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM files WHERE repo = ?", repo)
	return err
}

func (s *sqliteDB) GetFileMeta(ctx context.Context, repo, path string) (*FileMeta, error) {
	row := s.db.QueryRowContext(ctx, "SELECT repo, path, hash, size, mtime FROM files WHERE repo = ? AND path = ?", repo, path)
	var m FileMeta
	err := row.Scan(&m.Repo, &m.Path, &m.Hash, &m.Size, &m.MTime)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}
