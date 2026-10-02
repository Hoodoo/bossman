package parse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimLeft(content, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

// claudeMain mimics Claude Code's log shapes as observed in real sessions.
const claudeMain = `
{"type":"user","timestamp":"2026-10-01T10:00:00.000Z","cwd":"/work/proj","gitBranch":"main","version":"2.1.0","entrypoint":"cli","message":{"role":"user","content":"Fix the tests"},"origin":{"kind":"human"}}
{"type":"assistant","timestamp":"2026-10-01T10:00:05.000Z","message":{"id":"m1","model":"claude-opus-5-5","content":[{"type":"text","text":"Looking."}],"usage":{"input_tokens":10,"cache_creation_input_tokens":1000,"cache_read_input_tokens":5000,"output_tokens":50,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":1000}}}}
{"type":"assistant","timestamp":"2026-10-01T10:00:06.000Z","message":{"id":"m1","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}],"usage":{"input_tokens":10,"cache_creation_input_tokens":1000,"cache_read_input_tokens":5000,"output_tokens":50,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":1000}}}}
{"type":"user","timestamp":"2026-10-01T10:00:10.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"Exit code 1"}]}}
{"type":"assistant","timestamp":"2026-10-01T10:00:15.000Z","message":{"id":"m2","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"t2","name":"Edit","input":{"file_path":"/work/proj/a.go"}}],"usage":{"input_tokens":5,"cache_read_input_tokens":6000,"output_tokens":20}}}
{"type":"user","timestamp":"2026-10-01T10:00:20.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","is_error":true,"content":"The user doesn't want to proceed with this tool use. The tool use was rejected."}]}}
{"type":"user","timestamp":"2026-10-01T10:00:21.000Z","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}
{"type":"user","timestamp":"2026-10-01T10:01:00.000Z","isMeta":true,"message":{"role":"user","content":"<local-command-caveat>Caveat: ignore</local-command-caveat>"}}
{"type":"user","timestamp":"2026-10-01T10:01:01.000Z","message":{"role":"user","content":"<command-name>/model</command-name>\n<command-args>opus</command-args>"}}
{"type":"user","timestamp":"2026-10-01T10:01:02.000Z","message":{"role":"user","content":"<task-notification><task-id>x</task-id></task-notification>"},"origin":{"kind":"task-notification"}}
{"type":"assistant","timestamp":"2026-10-01T10:01:03.000Z","isApiErrorMessage":true,"message":{"id":"e1","model":"<synthetic>","content":[{"type":"text","text":"API Error: overloaded"}],"usage":{"input_tokens":0,"output_tokens":0}}}
{"type":"system","subtype":"turn_duration","durationMs":61000,"timestamp":"2026-10-01T10:01:04.000Z"}
{"type":"system","subtype":"compact_boundary","timestamp":"2026-10-01T10:30:00.000Z"}
{"type":"user","timestamp":"2026-10-01T10:30:01.000Z","isCompactSummary":true,"message":{"role":"user","content":"Summary of the earlier conversation"}}
{"type":"system","subtype":"away_summary","content":"We fixed the tests.\n\n(disable recaps in /config)","timestamp":"2026-10-01T10:31:00.000Z"}
{"type":"ai-title","aiTitle":"Fix failing tests"}
{"type":"ai-title","aiTitle":"Fix failing Go tests"}
{"type":"assistant","timestamp":"2026-10-01T10:32:00.000Z","message":{"id":"m3","model":"claude-opus-5-5","content":[{"type":"text","text":"Done. The marker is cc-catalogue:session-concluded, mentioned only."}],"usage":{"input_tokens":1,"output_tokens":5}}}
{"type":"cost-state","totalCostUSD":1.25}
{"type":"assistant","timestamp":"2026-10-01T10:32:00.000Z","message":{"id":"m4", truncated
`

const claudeSubagent = `
{"type":"user","isSidechain":true,"timestamp":"2026-10-01T10:00:30.000Z","message":{"role":"user","content":"sub task"}}
{"type":"assistant","isSidechain":true,"timestamp":"2026-10-01T10:00:31.000Z","message":{"id":"s1","model":"claude-sonnet-5","content":[{"type":"tool_use","id":"st1","name":"Read","input":{"file_path":"x"}}],"usage":{"input_tokens":3,"output_tokens":7}}}
`

func claudeFixture(t *testing.T) Candidate {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "-work-proj", "sess1.jsonl"), claudeMain)
	writeFile(t, filepath.Join(root, "-work-proj", "sess1", "subagents", "agent-a.jsonl"), claudeSubagent)
	writeFile(t, filepath.Join(root, "-work-proj", "sess1", "tool-results", "x.txt"), "big output")
	writeFile(t, filepath.Join(root, "-work-proj", "memory", "MEMORY.md"), "not a session")
	cands, err := DiscoverClaude(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 {
		t.Fatalf("discovered %d sessions, want 1", len(cands))
	}
	return cands[0]
}

func TestDiscoverClaude(t *testing.T) {
	c := claudeFixture(t)
	if c.ID != "sess1" || c.Agent != "claude" {
		t.Errorf("candidate = %s, want claude:sess1", c.Key())
	}
	if len(c.Files) != 3 {
		t.Errorf("files = %v, want main, subagent, and tool result", c.Files)
	}
}

func TestParseClaude(t *testing.T) {
	s, events, err := ParseClaude(claudeFixture(t), 5*time.Minute, true)
	if err != nil {
		t.Fatal(err)
	}
	check := func(name string, got, want any) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	check("project", s.Project, "/work/proj")
	check("title", s.Title, "Fix failing Go tests")
	check("first prompt", s.FirstPrompt, "Fix the tests")
	// "Fix the tests" and the /model command; not the caveat, the task
	// notification, the tool results, or the interrupt marker.
	check("prompts", s.Prompts, 2)
	check("interrupts", s.Interrupts, 1)
	check("rejections", s.Rejections, 1)
	check("tool calls", s.ToolCalls, 3) // Bash, Edit, subagent Read
	check("tool errors", s.ToolErrors, 1)
	check("Bash errors", s.Tools["Bash"].Errors, 1)
	check("api errors", s.APIErrors, 1)
	check("compactions", s.Compactions, 1)
	check("subagents", s.Subagents, 1)
	check("agent seconds", s.AgentSeconds, 61.0)
	check("concluded", s.Concluded, false)
	check("interventions", s.Interventions(), 3) // 1 follow-up + interrupt + rejection

	// m1 appears on two lines and must be counted once.
	opus := s.Models["claude-opus-5-5"]
	if opus == nil {
		t.Fatalf("no opus usage: %v", s.Models)
	}
	check("opus requests", opus.Requests, 3)
	check("opus input", opus.Input, int64(16))
	check("opus cache write 1h", opus.CacheWrite1h, int64(1000))
	check("opus cache read", opus.CacheRead, int64(11000))
	check("opus output", opus.Output, int64(75))
	check("sonnet output", s.Models["claude-sonnet-5"].Output, int64(7))
	if _, ok := s.Models["<synthetic>"]; ok {
		t.Error("synthetic API-error messages must not count as usage")
	}
	if s.ReportedCostUSD == nil || *s.ReportedCostUSD != 1.25 {
		t.Errorf("reported cost = %v, want 1.25", s.ReportedCostUSD)
	}
	kinds := map[string]bool{}
	for _, sm := range s.Summaries {
		kinds[sm.Kind] = true
		if sm.Kind == "recap" && sm.Text != "We fixed the tests." {
			t.Errorf("recap = %q, want Claude Code's footer stripped", sm.Text)
		}
	}
	for _, k := range []string{"title", "recap", "compaction"} {
		if !kinds[k] {
			t.Errorf("missing %s summary in %+v", k, s.Summaries)
		}
	}
	check("started", s.StartedAt.Format(time.RFC3339), "2026-10-01T10:00:00Z")
	check("ended", s.EndedAt.Format(time.RFC3339), "2026-10-01T10:32:00Z")
	// 10:00:00–10:01:04 and 10:30:00–10:32:00; the gap between is idle.
	check("active seconds", s.ActiveSeconds, 64.0+120)
	if len(events) == 0 || events[0].Role != "user" {
		t.Errorf("transcript should start with the prompt: %+v", events)
	}
}

func TestClaudeConclusion(t *testing.T) {
	marker := `<!-- cc-catalogue:session-concluded\naway-summary: |\n  We shipped it.\n  Nothing is left.\n-->`
	base := `{"type":"user","timestamp":"2026-10-01T10:00:00.000Z","message":{"role":"user","content":"go"}}
{"type":"assistant","timestamp":"2026-10-01T10:05:00.000Z","message":{"id":"m","model":"claude-opus-5-5","content":[{"type":"text","text":"` + marker + `"}],"usage":{"output_tokens":1}}}
`
	for _, tc := range []struct {
		name  string
		extra string
		want  bool
	}{
		{"concluded", "", true},
		{"resumed after concluding", `{"type":"user","timestamp":"2026-10-01T11:00:00.000Z","message":{"role":"user","content":"one more thing"}}` + "\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "p", "s.jsonl"), base+tc.extra)
			cands, _ := DiscoverClaude(root)
			s, _, err := ParseClaude(cands[0], 5*time.Minute, false)
			if err != nil {
				t.Fatal(err)
			}
			if s.Concluded != tc.want {
				t.Errorf("concluded = %v, want %v", s.Concluded, tc.want)
			}
			var got string
			for _, sm := range s.Summaries {
				if sm.Kind == "conclusion" {
					got = sm.Text
				}
			}
			if got != "We shipped it. Nothing is left." {
				t.Errorf("conclusion summary = %q", got)
			}
		})
	}
}

func TestHumanPrompt(t *testing.T) {
	for in, want := range map[string]string{
		"hello":                               "hello",
		"<command-name>/clear</command-name>": "/clear",
		"<local-command-stdout>x</local-command-stdout>": "",
		"<system-reminder>x</system-reminder>":           "",
		"   ":                                            "",
	} {
		got, ok := humanPrompt(in)
		if got != want || ok != (want != "") {
			t.Errorf("humanPrompt(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}
