package pricing

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"bossman/internal/model"
)

func TestDefaultTable(t *testing.T) {
	tab, err := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatal(err)
	}
	r, ok := tab.Lookup("claude-haiku-4-5-20251001")
	if !ok || r.Input != 1 {
		t.Errorf("dated id should match its prefix: %+v %v", r, ok)
	}
	// claude-sonnet-5-5 must not fall back to the claude-sonnet-5 entry by
	// accident; both exist, and the longer prefix wins.
	if _, ok := tab.Models["claude-sonnet-5-5"]; !ok {
		t.Error("missing claude-sonnet-5-5")
	}
	if _, ok := tab.Lookup("gpt-5.5"); ok {
		t.Error("Codex models are unpriced by default")
	}
}

// A real session where Claude Code recorded $41.70: the table should land
// close (the agent also counts calls it does not log per message).
func TestOpusCostMatchesAgent(t *testing.T) {
	tab, _ := Load("")
	u := model.Usage{Input: 5365, CacheWrite1h: 755076, CacheRead: 131791654, Output: 464284}
	got, ok := tab.Cost("claude-opus-5-5", u)
	if !ok || math.Abs(got-41.697) > 0.05 {
		t.Errorf("cost = %.4f, want about 41.70", got)
	}
}

func TestSessionCostSources(t *testing.T) {
	tab, _ := Load("")
	priced := &model.Usage{Input: 1e6}
	unknown := &model.Usage{Input: 1e6}
	reported := 9.0
	for _, tc := range []struct {
		name   string
		s      model.Session
		cost   float64
		source string
	}{
		{"table", model.Session{Models: map[string]*model.Usage{"claude-opus-5-5": priced}}, 4, SourceTable},
		{"partial", model.Session{Models: map[string]*model.Usage{"claude-opus-5-5": priced, "gpt-5.5": unknown}}, 4, SourcePartial},
		{"none", model.Session{Models: map[string]*model.Usage{"gpt-5.5": unknown}}, 0, SourceNone},
		{"agent", model.Session{Models: map[string]*model.Usage{"claude-opus-5-5": priced}, ReportedCostUSD: &reported}, 9, SourceAgent},
	} {
		got, src, _ := tab.SessionCost(&tc.s)
		if math.Abs(got-tc.cost) > 1e-9 || src != tc.source {
			t.Errorf("%s: %v %s, want %v %s", tc.name, got, src, tc.cost, tc.source)
		}
	}
}

func TestUserTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pricing.toml")
	if err := os.WriteFile(path, []byte(`
[models."gpt-5.5"]
input = 1.0
output = 8.0
cache_read = 0.1
cache_write_5m = 0.0
`), 0o644); err != nil {
		t.Fatal(err)
	}
	tab, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := tab.Cost("gpt-5.5", model.Usage{Input: 1e6, Output: 1e6, CacheRead: 1e6, CacheWrite5m: 1e6})
	if !ok || math.Abs(got-9.1) > 1e-9 {
		t.Errorf("cost = %v, %v; want 9.1", got, ok)
	}
	if err := os.WriteFile(path, []byte("not toml ["), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("broken table accepted")
	}
}
