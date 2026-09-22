package index

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ashankgupta/claudefinder/internal/claude"
	_ "modernc.org/sqlite"
)

type Row struct {
	claude.Session
	Snippet string
}

type DB struct{ sql *sql.DB }

func DefaultPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "claudefinder", "claudefinder.db")
}

const parserVersion = 2

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
    id            TEXT PRIMARY KEY,
    project_dir   TEXT NOT NULL,
    project_name  TEXT,
    title         TEXT,
    git_branch    TEXT,
    started_at    INTEGER,
    updated_at    INTEGER,
    first_prompt  TEXT,
    last_prompt   TEXT,
    message_count INTEGER,
    file_path     TEXT NOT NULL UNIQUE,
    file_size     INTEGER,
    file_mtime    INTEGER
);
CREATE INDEX IF NOT EXISTS sessions_updated ON sessions(updated_at DESC);

-- The FTS table is also the message store: role/ts/session_id ride along
-- unindexed, so there is no second copy of the conversation text.
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    session_id UNINDEXED,
    role       UNINDEXED,
    ts         UNINDEXED,
    content,
    tokenize = 'porter unicode61'
);`

func Open(path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	h, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	h.SetMaxOpenConns(1)
	if _, err := h.Exec(schema); err != nil {
		h.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	var v int
	if err := h.QueryRow(`PRAGMA user_version`).Scan(&v); err == nil && v != parserVersion {
		if _, err := h.Exec(fmt.Sprintf(
			`DELETE FROM messages_fts; DELETE FROM sessions; PRAGMA user_version = %d;`,
			parserVersion)); err != nil {
			h.Close()
			return nil, fmt.Errorf("reindex for parser v%d: %w", parserVersion, err)
		}
	}
	_ = os.Chmod(path, 0o600)
	return &DB{sql: h}, nil
}

func (d *DB) Close() error { return d.sql.Close() }

func (d *DB) Upsert(s *claude.Session) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err = tx.Exec(`DELETE FROM messages_fts WHERE session_id = ?`, s.ID); err != nil {
		return err
	}
	_, err = tx.Exec(`
        INSERT INTO sessions (id, project_dir, project_name, title, git_branch,
            started_at, updated_at, first_prompt, last_prompt, message_count,
            file_path, file_size, file_mtime)
        VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
        ON CONFLICT(id) DO UPDATE SET
            project_dir=excluded.project_dir, project_name=excluded.project_name,
            title=excluded.title, git_branch=excluded.git_branch,
            started_at=excluded.started_at, updated_at=excluded.updated_at,
            first_prompt=excluded.first_prompt, last_prompt=excluded.last_prompt,
            message_count=excluded.message_count, file_path=excluded.file_path,
            file_size=excluded.file_size, file_mtime=excluded.file_mtime`,
		s.ID, s.ProjectDir, s.ProjectName, s.Title, s.GitBranch,
		s.StartedAt.Unix(), s.UpdatedAt.Unix(), s.FirstPrompt, s.LastPrompt,
		s.MessageCount, s.FilePath, s.FileSize, s.FileModTime.Unix())
	if err != nil {
		return err
	}

	st, err := tx.Prepare(`INSERT INTO messages_fts (session_id, role, ts, content) VALUES (?,?,?,?)`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, m := range s.Messages {
		if _, err := st.Exec(s.ID, m.Role, m.Timestamp.Unix(), m.Content); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (d *DB) Delete(id string) error {
	tx, err := d.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM messages_fts WHERE session_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

const sessionCols = `id, project_dir, project_name, title, git_branch,
    started_at, updated_at, first_prompt, last_prompt, message_count,
    file_path, file_size, file_mtime`

func scanRows(rs *sql.Rows) ([]Row, error) {
	defer rs.Close()
	var out []Row
	for rs.Next() {
		var r Row
		var started, updated, mtime int64
		if err := rs.Scan(&r.ID, &r.ProjectDir, &r.ProjectName, &r.Title, &r.GitBranch,
			&started, &updated, &r.FirstPrompt, &r.LastPrompt, &r.MessageCount,
			&r.FilePath, &r.FileSize, &mtime); err != nil {
			return nil, err
		}
		r.StartedAt = time.Unix(started, 0)
		r.UpdatedAt = time.Unix(updated, 0)
		r.FileModTime = time.Unix(mtime, 0)
		out = append(out, r)
	}
	return out, rs.Err()
}

func (d *DB) Recent(limit int) ([]Row, error) {
	rs, err := d.sql.Query(`SELECT `+sessionCols+` FROM sessions ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	return scanRows(rs)
}

func (d *DB) Messages(sessionID string) ([]claude.Message, error) {
	rs, err := d.sql.Query(`SELECT role, ts, content FROM messages_fts WHERE session_id = ? ORDER BY rowid`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []claude.Message
	for rs.Next() {
		var m claude.Message
		var ts int64
		if err := rs.Scan(&m.Role, &ts, &m.Content); err != nil {
			return nil, err
		}
		m.Timestamp = time.Unix(ts, 0)
		out = append(out, m)
	}
	return out, rs.Err()
}

func (d *DB) Count() (int, error) {
	var n int
	err := d.sql.QueryRow(`SELECT count(*) FROM sessions`).Scan(&n)
	return n, err
}

func (d *DB) Search(query string, limit int) ([]Row, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return d.Recent(limit)
	}

	like := "%" + q + "%"
	rs, err := d.sql.Query(`SELECT `+sessionCols+` FROM sessions
        WHERE title LIKE ? OR first_prompt LIKE ? OR last_prompt LIKE ?
           OR project_dir LIKE ? OR project_name LIKE ? OR id LIKE ?
        ORDER BY updated_at DESC LIMIT ?`,
		like, like, like, like, like, like, limit)
	if err != nil {
		return nil, err
	}
	out, err := scanRows(rs)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(out))
	for _, r := range out {
		seen[r.ID] = true
	}
	if len(out) >= limit {
		return out, nil
	}

	match := ftsQuery(q)
	if match == "" {
		return out, nil
	}

	rs, err = d.sql.Query(`SELECT session_id, snippet(messages_fts, 3, '', '', '…', 14)
        FROM messages_fts WHERE messages_fts MATCH ? ORDER BY rank LIMIT 500`, match)
	if err != nil {
		return out, nil
	}
	defer rs.Close()
	var ids []string
	snip := map[string]string{}
	for rs.Next() {
		var id, s string
		if err := rs.Scan(&id, &s); err != nil {
			return out, nil
		}
		if seen[id] || snip[id] != "" {
			continue
		}
		snip[id] = claude.Clean(s)
		ids = append(ids, id)
		if len(out)+len(ids) >= limit {
			break
		}
	}
	if len(ids) == 0 {
		return out, nil
	}

	rs2, err := d.sql.Query(`SELECT `+sessionCols+` FROM sessions WHERE id IN (`+placeholders(len(ids))+`)`, anySlice(ids)...)
	if err != nil {
		return out, nil
	}
	found, err := scanRows(rs2)
	if err != nil {
		return out, nil
	}
	byID := make(map[string]Row, len(found))
	for _, r := range found {
		byID[r.ID] = r
	}
	for _, id := range ids {
		if r, ok := byID[id]; ok {
			r.Snippet = snip[id]
			out = append(out, r)
		}
	}
	return out, nil
}

func ftsQuery(q string) string {
	fields := strings.FieldsFunc(q, func(r rune) bool {
		return !(r == '_' || r == '\'' || r == '.' || r == '/' || r == '-' ||
			r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r > 127)
	})
	var terms []string
	for i, f := range fields {
		f = strings.ReplaceAll(f, `"`, "")
		if f == "" {
			continue
		}
		t := `"` + f + `"`
		if i == len(fields)-1 {
			t += "*"
		}
		terms = append(terms, t)
	}
	return strings.Join(terms, " AND ")
}

func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }

func anySlice(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

