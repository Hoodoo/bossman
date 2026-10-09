package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hoodoo/bossman/internal/model"
)

func session(id, project string, started time.Time, cost float64) (*model.Session, Priced) {
	s := &model.Session{
		Agent: model.AgentClaude, ID: id, Path: "claude/p/" + id + ".jsonl",
		Project: project, Title: "title " + id, FirstPrompt: "prompt " + id,
		StartedAt: started, EndedAt: started.Add(time.Hour), ActiveSeconds: 600,
		Prompts: 3, Interrupts: 1, ToolCalls: 10, ToolErrors: 2,
		Models: map[string]*model.Usage{"claude-opus-5-5": {Input: 100, CacheRead: 1000, Output: 50, Requests: 4}},
		Tools:  map[string]*model.ToolStat{"Bash": {Calls: 10, Errors: 2}},
		Summaries: []model.Summary{
			{Kind: "recap", Text: "older recap"},
			{Kind: "recap", Text: "latest recap"},
			{Kind: "compaction", Text: "compacted"},
		},
	}
	return s, Priced{CostUSD: cost, Source: "table", TableCostUSD: cost, PerModel: map[string]float64{"claude-opus-5-5": cost}}
}

func open(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "db", "bossman.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

var day = time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)

func TestAnnotationsSurviveReindex(t *testing.T) {
	st := open(t)
	s, p := session("aaaa1111", "/work/a", day, 1.5)
	if err := st.Put(s, p, Indexed{Size: 1}, day); err != nil {
		t.Fatal(err)
	}
	key := s.Key()
	if err := st.SetDisplayName(key, "My name"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNotes(key, "remember this"); err != nil {
		t.Fatal(err)
	}
	project := "/work/reassigned"
	if err := st.SetProjectOverride(key, &project); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTags(key, []string{"Bug", "bug", "infra"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddLink(key, "https://example.com/pr/1", "PR"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddLink(key, "not a url", ""); err == nil {
		t.Error("relative link accepted")
	}

	// Reindex with changed data.
	s.Prompts = 7
	if err := st.Put(s, p, Indexed{Size: 2}, day); err != nil {
		t.Fatal(err)
	}
	d, err := st.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if d.Prompts != 7 {
		t.Errorf("index not updated: prompts = %d", d.Prompts)
	}
	if d.DisplayName != "My name" || d.Notes != "remember this" {
		t.Errorf("annotations lost: %q %q", d.DisplayName, d.Notes)
	}
	if d.Project != project || d.DetectedProject != "/work/a" || !d.ProjectOverridden {
		t.Errorf("project override lost: project=%q detected=%q overridden=%v", d.Project, d.DetectedProject, d.ProjectOverridden)
	}
	if len(d.Tags) != 2 || d.Tags[0] != "bug" || d.Tags[1] != "infra" {
		t.Errorf("tags = %v", d.Tags)
	}
	if len(d.LinkList) != 1 || d.LinkList[0].Label != "PR" {
		t.Errorf("links = %+v", d.LinkList)
	}
	if d.Summary != "latest recap" {
		t.Errorf("summary = %q, want the latest recap", d.Summary)
	}
	if d.Name() != "My name" {
		t.Errorf("name = %q", d.Name())
	}
	if len(d.ModelUsage) != 1 || len(d.ToolUsage) != 1 || len(d.Summaries) != 3 {
		t.Errorf("detail rows: %d models, %d tools, %d summaries", len(d.ModelUsage), len(d.ToolUsage), len(d.Summaries))
	}

	exp, err := st.ExportAnnotations()
	if err != nil || len(exp) != 1 {
		t.Fatalf("export = %+v, %v", exp, err)
	}
	other := open(t)
	if err := other.ImportAnnotations(exp); err != nil {
		t.Fatal(err)
	}
	again, _ := other.ExportAnnotations()
	if len(again) != 1 || again[0].DisplayName != "My name" || again[0].Project == nil || *again[0].Project != project || len(again[0].Links) != 1 || len(again[0].Tags) != 2 {
		t.Errorf("round trip = %+v", again)
	}
}

func TestResolve(t *testing.T) {
	st := open(t)
	for _, id := range []string{"01a0fbdf-204a", "01a0fbdf-204d", "9e12f129"} {
		s, p := session(id, "/w", day, 1)
		if err := st.Put(s, p, Indexed{}, day); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.SetDisplayName("claude:9e12f129", "release prep")
	for ref, want := range map[string]string{
		"9e12f129":        "claude:9e12f129",
		"claude:9e12f129": "claude:9e12f129",
		"9e12":            "claude:9e12f129",
		"01a0fbdf-204d":   "claude:01a0fbdf-204d",
		"release prep":    "claude:9e12f129",
	} {
		if got, err := st.Resolve(ref); err != nil || got != want {
			t.Errorf("Resolve(%q) = %q, %v; want %q", ref, got, err, want)
		}
	}
	if _, err := st.Resolve("01a0fbdf"); err == nil {
		t.Error("ambiguous prefix resolved")
	}
	if _, err := st.Resolve("zzzz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown ref: %v", err)
	}
}

func TestListAndStats(t *testing.T) {
	st := open(t)
	a, pa := session("a1", "/work/alpha", day, 2)
	b, pb := session("b2", "/work/beta", day.Add(24*time.Hour), 3)
	c, pc := session("c3", "/work/beta", day.Add(48*time.Hour), 0)
	c.Agent, c.ImportedFrom, c.Prompts = model.AgentCodex, "claude:a1", 0
	pc.Source = "none"
	for _, x := range []struct {
		s *model.Session
		p Priced
	}{{a, pa}, {b, pb}, {c, pc}} {
		if err := st.Put(x.s, x.p, Indexed{}, day); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.SetTags(b.Key(), []string{"keep"})
	override := "/work/gamma"
	_ = st.SetProjectOverride(b.Key(), &override)

	rows, err := st.List(Filter{Sort: "cost"})
	if err != nil || len(rows) != 3 || rows[0].ID != "b2" {
		t.Fatalf("List by cost = %+v, %v", rows, err)
	}
	if rows, _ = st.List(Filter{SkipCopies: true}); len(rows) != 2 {
		t.Errorf("skip copies = %d rows, want 2", len(rows))
	}
	if rows, _ = st.List(Filter{Project: "gamma", Agent: "claude"}); len(rows) != 1 || rows[0].ID != "b2" {
		t.Errorf("project+agent filter = %+v", rows)
	}
	if rows, _ = st.List(Filter{Query: "prompt a1"}); len(rows) != 1 {
		t.Errorf("search = %+v", rows)
	}
	if rows, _ = st.List(Filter{Tag: "keep"}); len(rows) != 1 || rows[0].Tags[0] != "keep" {
		t.Errorf("tag filter = %+v", rows)
	}
	_ = st.SetTags(a.Key(), []string{"sink:proto"})
	if rows, _ = st.List(Filter{NotTag: "sink:*"}); len(rows) != 2 {
		t.Errorf("not-tag prefix = %d rows, want 2", len(rows))
	}
	if rows, _ = st.List(Filter{NotTag: "KEEP"}); len(rows) != 2 {
		t.Errorf("not-tag exact = %d rows, want 2", len(rows))
	}
	if rows, _ = st.List(Filter{NotTag: "si%*"}); len(rows) != 3 {
		t.Errorf("not-tag escapes LIKE wildcards = %d rows, want 3", len(rows))
	}
	_ = st.SetTags(a.Key(), nil)
	if rows, _ = st.List(Filter{Since: day.Add(time.Hour)}); len(rows) != 2 {
		t.Errorf("since filter = %d rows", len(rows))
	}
	if err := st.SetInSource(map[string]bool{a.Key(): true}); err != nil {
		t.Fatal(err)
	}
	if rows, _ = st.List(Filter{Archived: true}); len(rows) != 2 {
		t.Errorf("archived-only = %d rows, want 2", len(rows))
	}

	groups, total, err := st.Stats(Filter{}, "project")
	if err != nil {
		t.Fatal(err)
	}
	// The empty import (c3) is left out of aggregates.
	if total.Sessions != 2 || total.CostUSD != 5 || total.Prompts != 6 {
		t.Errorf("total = %+v", total)
	}
	if len(groups) != 2 {
		t.Errorf("groups = %+v", groups)
	}
	if groups[0].Key != "/work/gamma" {
		t.Errorf("project override absent from stats: %+v", groups)
	}
	for _, by := range GroupBys {
		if _, _, err := st.Stats(Filter{}, by); err != nil {
			t.Errorf("Stats by %s: %v", by, err)
		}
	}
	if _, _, err := st.Stats(Filter{}, "nope"); err == nil {
		t.Error("unknown grouping accepted")
	}
	models, _, _ := st.Stats(Filter{}, "model")
	if len(models) != 1 || models[0].CostUSD != 5 || models[0].Sessions != 2 {
		t.Errorf("by model = %+v", models)
	}
}
