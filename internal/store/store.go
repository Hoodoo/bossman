// Package store is bossman's SQLite database. It holds two kinds of data:
//
//   - the index (sessions, session_models, session_tools,
//     session_summaries), derived from the archive and safe to rebuild;
//   - annotations, links, and tags, which the user wrote and which nothing
//     derives, so a reindex never touches them.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Hoodoo/bossman/internal/model"
)

// TimeFormat is how timestamps are stored: UTC, fixed width, sortable.
const TimeFormat = "2006-01-02T15:04:05.000Z"

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	key TEXT PRIMARY KEY,
	agent TEXT NOT NULL,
	id TEXT NOT NULL,
	path TEXT NOT NULL,
	files TEXT NOT NULL DEFAULT '[]',
	project TEXT NOT NULL DEFAULT '',
	git_branch TEXT NOT NULL DEFAULT '',
	version TEXT NOT NULL DEFAULT '',
	entrypoint TEXT NOT NULL DEFAULT '',
	title TEXT NOT NULL DEFAULT '',
	first_prompt TEXT NOT NULL DEFAULT '',
	summary TEXT NOT NULL DEFAULT '',
	concluded INTEGER NOT NULL DEFAULT 0,
	started_at TEXT NOT NULL DEFAULT '',
	ended_at TEXT NOT NULL DEFAULT '',
	wall_s REAL NOT NULL DEFAULT 0,
	active_s REAL NOT NULL DEFAULT 0,
	agent_s REAL NOT NULL DEFAULT 0,
	prompts INTEGER NOT NULL DEFAULT 0,
	interrupts INTEGER NOT NULL DEFAULT 0,
	rejections INTEGER NOT NULL DEFAULT 0,
	interventions INTEGER NOT NULL DEFAULT 0,
	tool_calls INTEGER NOT NULL DEFAULT 0,
	tool_errors INTEGER NOT NULL DEFAULT 0,
	api_errors INTEGER NOT NULL DEFAULT 0,
	compactions INTEGER NOT NULL DEFAULT 0,
	subagents INTEGER NOT NULL DEFAULT 0,
	input INTEGER NOT NULL DEFAULT 0,
	cache_write INTEGER NOT NULL DEFAULT 0,
	cache_read INTEGER NOT NULL DEFAULT 0,
	output INTEGER NOT NULL DEFAULT 0,
	reasoning INTEGER NOT NULL DEFAULT 0,
	requests INTEGER NOT NULL DEFAULT 0,
	cost_usd REAL NOT NULL DEFAULT 0,
	cost_source TEXT NOT NULL DEFAULT 'none',
	table_cost_usd REAL NOT NULL DEFAULT 0,
	models TEXT NOT NULL DEFAULT '',
	src_size INTEGER NOT NULL DEFAULT 0,
	src_mtime TEXT NOT NULL DEFAULT '',
	in_source INTEGER NOT NULL DEFAULT 1,
	imported_from TEXT NOT NULL DEFAULT '',
	indexed_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS sessions_started ON sessions(started_at);
CREATE TABLE IF NOT EXISTS session_models (
	key TEXT NOT NULL,
	model TEXT NOT NULL,
	input INTEGER NOT NULL, cache_write_5m INTEGER NOT NULL, cache_write_1h INTEGER NOT NULL,
	cache_read INTEGER NOT NULL, output INTEGER NOT NULL, reasoning INTEGER NOT NULL,
	requests INTEGER NOT NULL,
	cost_usd REAL,
	PRIMARY KEY (key, model)
);
CREATE TABLE IF NOT EXISTS session_tools (
	key TEXT NOT NULL, tool TEXT NOT NULL,
	calls INTEGER NOT NULL, errors INTEGER NOT NULL,
	PRIMARY KEY (key, tool)
);
CREATE TABLE IF NOT EXISTS session_summaries (
	key TEXT NOT NULL, seq INTEGER NOT NULL,
	kind TEXT NOT NULL, text TEXT NOT NULL, at TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (key, seq)
);
CREATE TABLE IF NOT EXISTS annotations (
	key TEXT PRIMARY KEY,
	display_name TEXT NOT NULL DEFAULT '',
	notes TEXT NOT NULL DEFAULT '',
	project_override TEXT,
	updated_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS links (
	id INTEGER PRIMARY KEY,
	key TEXT NOT NULL,
	url TEXT NOT NULL,
	label TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS links_key ON links(key);
CREATE TABLE IF NOT EXISTS tags (
	key TEXT NOT NULL, tag TEXT NOT NULL,
	PRIMARY KEY (key, tag)
);
`

// Store wraps the database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(on)")
	if err != nil {
		return nil, err
	}
	// One connection serialises writers; reads are fast enough for a
	// single user's sessions.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	// CREATE TABLE IF NOT EXISTS does not add columns to databases created by
	// older versions. Existing catalogues need this small in-place migration.
	if _, err := db.Exec(`ALTER TABLE annotations ADD COLUMN project_override TEXT`); err != nil &&
		!strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		db.Close()
		return nil, fmt.Errorf("migrate annotations: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Indexed is what the index needs to decide whether to reparse a session.
type Indexed struct {
	Size  int64
	MTime string
}

// Signatures returns the source signature of every indexed session.
func (s *Store) Signatures() (map[string]Indexed, error) {
	rows, err := s.db.Query(`SELECT key, src_size, src_mtime FROM sessions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Indexed{}
	for rows.Next() {
		var k string
		var ix Indexed
		if err := rows.Scan(&k, &ix.Size, &ix.MTime); err != nil {
			return nil, err
		}
		out[k] = ix
	}
	return out, rows.Err()
}

// Priced carries the cost figures computed for a session.
type Priced struct {
	CostUSD      float64
	Source       string
	TableCostUSD float64
	// PerModel is each model's share of CostUSD.
	PerModel map[string]float64
}

// Put replaces the index rows of one session.
func (s *Store) Put(sess *model.Session, p Priced, sig Indexed, now time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	key := sess.Key()
	for _, t := range []string{"session_models", "session_tools", "session_summaries"} {
		if _, err := tx.Exec(`DELETE FROM `+t+` WHERE key = ?`, key); err != nil {
			return err
		}
	}
	files, _ := json.Marshal(sess.Files)
	tot := sess.Total()
	var models []string
	for m := range sess.Models {
		models = append(models, m)
	}
	sort.Strings(models)
	_, err = tx.Exec(`INSERT OR REPLACE INTO sessions (
		key, agent, id, path, files, project, git_branch, version, entrypoint,
		title, first_prompt, summary, concluded, started_at, ended_at,
		wall_s, active_s, agent_s, prompts, interrupts, rejections, interventions,
		tool_calls, tool_errors, api_errors, compactions, subagents,
		input, cache_write, cache_read, output, reasoning, requests,
		cost_usd, cost_source, table_cost_usd, models, src_size, src_mtime,
		imported_from, in_source, indexed_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,
		COALESCE((SELECT in_source FROM sessions WHERE key = ?), 1), ?)`,
		key, sess.Agent, sess.ID, sess.Path, string(files), sess.Project, sess.GitBranch,
		sess.Version, sess.Entrypoint, sess.Title, sess.FirstPrompt, latestSummary(sess),
		b2i(sess.Concluded), fmtTime(sess.StartedAt), fmtTime(sess.EndedAt),
		sess.WallSeconds(), sess.ActiveSeconds, sess.AgentSeconds,
		sess.Prompts, sess.Interrupts, sess.Rejections, sess.Interventions(),
		sess.ToolCalls, sess.ToolErrors, sess.APIErrors, sess.Compactions, sess.Subagents,
		tot.Input, tot.CacheWrite5m+tot.CacheWrite1h, tot.CacheRead, tot.Output, tot.Reasoning, tot.Requests,
		p.CostUSD, p.Source, p.TableCostUSD, strings.Join(models, ","), sig.Size, sig.MTime,
		sess.ImportedFrom, key, now.UTC().Format(TimeFormat))
	if err != nil {
		return err
	}
	for m, u := range sess.Models {
		var cost any
		if c, ok := p.PerModel[m]; ok {
			cost = c
		}
		if _, err := tx.Exec(`INSERT INTO session_models VALUES (?,?,?,?,?,?,?,?,?,?)`,
			key, m, u.Input, u.CacheWrite5m, u.CacheWrite1h, u.CacheRead, u.Output, u.Reasoning, u.Requests, cost); err != nil {
			return err
		}
	}
	for t, st := range sess.Tools {
		if _, err := tx.Exec(`INSERT INTO session_tools VALUES (?,?,?,?)`, key, t, st.Calls, st.Errors); err != nil {
			return err
		}
	}
	for i, sm := range sess.Summaries {
		if _, err := tx.Exec(`INSERT INTO session_summaries VALUES (?,?,?,?,?)`, key, i, sm.Kind, sm.Text, fmtTime(sm.At)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetInSource records which sessions still exist in the agents' own
// directories; the rest survive only in the archive.
func (s *Store) SetInSource(live map[string]bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT key FROM sessions`)
	if err != nil {
		return err
	}
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return err
		}
		keys = append(keys, k)
	}
	rows.Close()
	for _, k := range keys {
		if _, err := tx.Exec(`UPDATE sessions SET in_source = ? WHERE key = ?`, b2i(live[k]), k); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ErrNotFound is returned when a reference matches no session.
var ErrNotFound = errors.New("no such session")

// Resolve turns a reference into a session key. A reference is a full key
// (agent:id), an id, a unique id prefix of at least four characters, or
// an exact display name.
func (s *Store) Resolve(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", ErrNotFound
	}
	var keys []string
	q := func(query string, args ...any) error {
		rows, err := s.db.Query(query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err != nil {
				return err
			}
			keys = append(keys, k)
		}
		return rows.Err()
	}
	if err := q(`SELECT key FROM sessions WHERE key = ? OR id = ?`, ref, ref); err != nil {
		return "", err
	}
	if len(keys) == 0 && len(ref) >= 4 {
		pattern := escapeLike(ref) + "%"
		if err := q(`SELECT key FROM sessions WHERE id LIKE ? ESCAPE '\' OR key LIKE ? ESCAPE '\'`, pattern, pattern); err != nil {
			return "", err
		}
	}
	if len(keys) == 0 {
		if err := q(`SELECT a.key FROM annotations a JOIN sessions s ON s.key = a.key WHERE a.display_name = ?`, ref); err != nil {
			return "", err
		}
	}
	switch len(keys) {
	case 0:
		return "", fmt.Errorf("%w: %q", ErrNotFound, ref)
	case 1:
		return keys[0], nil
	default:
		if len(keys) > 5 {
			keys = append(keys[:5], "…")
		}
		return "", fmt.Errorf("ambiguous reference %q matches %s", ref, strings.Join(keys, ", "))
	}
}

func latestSummary(s *model.Session) string {
	// Prefer what the session ended on: a conclusion, then the latest
	// recap, then the latest compaction summary.
	for _, kind := range []string{"conclusion", "recap", "summary", "compaction"} {
		for i := len(s.Summaries) - 1; i >= 0; i-- {
			if s.Summaries[i].Kind == kind {
				return s.Summaries[i].Text
			}
		}
	}
	return ""
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(TimeFormat)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
