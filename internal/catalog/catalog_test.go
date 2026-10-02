package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

const claudeSession = `{"type":"user","timestamp":"2026-10-01T10:00:00.000Z","cwd":"/work/p","message":{"role":"user","content":"hello"}}
{"type":"assistant","timestamp":"2026-10-01T10:00:05.000Z","message":{"id":"m1","model":"claude-opus-5-5","content":[{"type":"text","text":"hi"}],"usage":{"input_tokens":1000000,"output_tokens":0}}}
`

const codexSession = `{"timestamp":"2026-10-01T11:00:00.000Z","type":"session_meta","payload":{"id":"019e321d-0ee3-7253-94f6-7a99ce15a42a","cwd":"/work/q"}}
{"timestamp":"2026-10-01T11:00:01.000Z","type":"event_msg","payload":{"type":"user_message","message":"build it"}}
`

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T) (*Catalog, string, string) {
	t.Helper()
	dir := t.TempDir()
	claude, codex := filepath.Join(dir, "claude-projects"), filepath.Join(dir, "codex-home")
	t.Setenv("BOSSMAN_CLAUDE_DIR", claude)
	t.Setenv("BOSSMAN_CODEX_DIR", codex)
	write(t, filepath.Join(claude, "-work-p", "c1.jsonl"), claudeSession)
	write(t, filepath.Join(codex, "sessions", "2026", "10", "01", "rollout-2026-10-01T11-00-00-019e321d-0ee3-7253-94f6-7a99ce15a42a.jsonl"), codexSession)
	write(t, filepath.Join(codex, "session_index.jsonl"), `{"id":"019e321d-0ee3-7253-94f6-7a99ce15a42a","thread_name":"Build"}`+"\n")
	c, err := Open(filepath.Join(dir, "home"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, claude, codex
}

func TestSyncArchivesAndSurvivesDeletion(t *testing.T) {
	c, claude, _ := setup(t)

	scan, err := c.Scan()
	if err != nil || len(scan) != 2 || scan[0].Archived != "new" {
		t.Fatalf("scan before sync = %+v, %v", scan, err)
	}
	st, err := c.Sync(false)
	if err != nil {
		t.Fatal(err)
	}
	if st.Index.Sessions != 2 || st.Index.Indexed != 2 || st.Archive.Copied != 3 {
		t.Fatalf("first sync = %+v", st)
	}
	d, err := c.Store.Get("claude:c1")
	if err != nil {
		t.Fatal(err)
	}
	if d.CostUSD != 4 || d.CostSource != "table" || !d.InSource {
		t.Errorf("claude session = cost %v (%s), in source %v", d.CostUSD, d.CostSource, d.InSource)
	}
	if x, _ := c.Store.Get("codex:019e321d-0ee3-7253-94f6-7a99ce15a42a"); x == nil || x.Title != "Build" {
		t.Errorf("codex session = %+v", x)
	}

	// Unchanged: nothing reparsed.
	if st, _ = c.Sync(false); st.Index.Indexed != 0 || st.Index.Unchanged != 2 {
		t.Errorf("second sync = %+v", st.Index)
	}

	// The agent cleans up its old session; bossman keeps it.
	if err := c.Store.SetDisplayName("claude:c1", "keep me"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(claude, "-work-p", "c1.jsonl")); err != nil {
		t.Fatal(err)
	}
	if st, err = c.Sync(true); err != nil {
		t.Fatal(err)
	}
	if st.Index.Sessions != 2 || st.Index.Archived != 1 {
		t.Errorf("sync after deletion = %+v", st.Index)
	}
	d, err = c.Store.Get("claude:c1")
	if err != nil {
		t.Fatal(err)
	}
	if d.InSource || d.DisplayName != "keep me" {
		t.Errorf("after deletion: in source %v, name %q", d.InSource, d.DisplayName)
	}
	if n, _ := c.ArchivedOnly(); n != 1 {
		t.Errorf("archived only = %d", n)
	}
	ev, err := c.Transcript("claude:c1")
	if err != nil || len(ev) != 2 {
		t.Errorf("transcript from archive = %+v, %v", ev, err)
	}
}

func TestSessionGrowthIsReindexed(t *testing.T) {
	c, claude, _ := setup(t)
	if _, err := c.Sync(false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(claude, "-work-p", "c1.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"type":"user","timestamp":"2026-10-01T10:01:00.000Z","message":{"role":"user","content":"more"}}` + "\n")
	f.Close()
	st, err := c.Sync(false)
	if err != nil {
		t.Fatal(err)
	}
	if st.Archive.Grown != 1 || st.Index.Indexed != 1 {
		t.Errorf("sync after growth = %+v", st)
	}
	if d, _ := c.Store.Get("claude:c1"); d.Prompts != 2 {
		t.Errorf("prompts = %d, want 2", d.Prompts)
	}
}
