package parse

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

const codexID = "019e321d-0ee3-7253-94f6-7a99ce15a42a"

// codexLegacy mimics an older Codex CLI rollout: prompts as user_message
// events, shell commands through exec_command.
const codexLegacy = `
{"timestamp":"2026-05-16T17:47:05.000Z","type":"session_meta","payload":{"id":"` + codexID + `","cwd":"/work/game","cli_version":"0.130.0","originator":"codex-tui","git":{"branch":"dev"}}}
{"timestamp":"2026-05-16T17:47:05.100Z","type":"turn_context","payload":{"model":"gpt-5.5","cwd":"/work/game"}}
{"timestamp":"2026-05-16T17:47:06.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>injected</environment_context>"}]}}
{"timestamp":"2026-05-16T17:47:06.000Z","type":"event_msg","payload":{"type":"user_message","message":"Build the parser"}}
{"timestamp":"2026-05-16T17:47:07.000Z","type":"event_msg","payload":{"type":"task_started"}}
{"timestamp":"2026-05-16T17:47:10.000Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"make test\"}","call_id":"c1"}}
{"timestamp":"2026-05-16T17:47:12.000Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"Chunk ID: 1\nProcess exited with code 2\nOutput: fail"}}
{"timestamp":"2026-05-16T17:47:13.000Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","input":"*** Begin Patch","call_id":"c2"}}
{"timestamp":"2026-05-16T17:47:14.000Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"c2","output":"apply_patch verification failed: no match"}}
{"timestamp":"2026-05-16T17:47:15.000Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"ls\"}","call_id":"c3"}}
{"timestamp":"2026-05-16T17:47:16.000Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c3","output":"Process exited with code 0\nOutput: a b"}}
{"timestamp":"2026-05-16T17:47:17.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":600,"output_tokens":100,"reasoning_output_tokens":40,"total_tokens":1100}}}}
{"timestamp":"2026-05-16T17:47:17.500Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"cached_input_tokens":600,"output_tokens":100,"reasoning_output_tokens":40,"total_tokens":1100}}}}
{"timestamp":"2026-05-16T17:47:18.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Tests fail; patching."}]}}
{"timestamp":"2026-05-16T17:47:20.000Z","type":"event_msg","payload":{"type":"task_complete","duration_ms":13000}}
{"timestamp":"2026-05-16T17:48:00.000Z","type":"event_msg","payload":{"type":"user_message","message":"Stop, try another way"}}
{"timestamp":"2026-05-16T17:48:01.000Z","type":"turn_context","payload":{"model":"gpt-5.6-sol"}}
{"timestamp":"2026-05-16T17:48:30.000Z","type":"event_msg","payload":{"type":"turn_aborted","reason":"interrupted","duration_ms":29000}}
{"timestamp":"2026-05-16T17:48:31.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":3000,"cached_input_tokens":2000,"output_tokens":150,"reasoning_output_tokens":40,"total_tokens":3150}}}}
{"timestamp":"2026-05-16T17:49:00.000Z","type":"compacted","payload":{"message":"Earlier we built a parser."}}
`

func codexFixture(t *testing.T, rollout string, extra map[string]string) (Candidate, CodexMeta) {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "sessions", "2026", "05", "16", "rollout-2026-05-16T19-47-05-"+codexID+".jsonl"), rollout)
	writeFile(t, filepath.Join(root, "sessions", "2026", "05", "16", "notes.txt"), "ignored")
	for name, content := range extra {
		writeFile(t, filepath.Join(root, name), content)
	}
	cands, err := DiscoverCodex(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].ID != codexID {
		t.Fatalf("discovered %+v, want one session %s", cands, codexID)
	}
	return cands[0], LoadCodexMeta(root)
}

func TestParseCodexLegacy(t *testing.T) {
	index := `{"id":"` + codexID + `","thread_name":"Build the par"}
{"id":"` + codexID + `","thread_name":"Parser work"}
`
	c, meta := codexFixture(t, codexLegacy, map[string]string{"session_index.jsonl": index})
	s, events, err := ParseCodex(c, meta, 5*time.Minute, true)
	if err != nil {
		t.Fatal(err)
	}
	check := func(name string, got, want any) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
	check("title", s.Title, "Parser work")
	check("project", s.Project, "/work/game")
	check("branch", s.GitBranch, "dev")
	check("first prompt", s.FirstPrompt, "Build the parser")
	check("prompts", s.Prompts, 2)
	check("interrupts", s.Interrupts, 1)
	check("tool calls", s.ToolCalls, 3)
	check("tool errors", s.ToolErrors, 2) // exit code 2, failed patch
	check("exec_command errors", s.Tools["exec_command"].Errors, 1)
	check("compactions", s.Compactions, 1)
	check("agent seconds", s.AgentSeconds, 42.0)

	// The cumulative counter grows 1100 then 3150 total: the first delta
	// belongs to gpt-5.5, the second to gpt-5.6-sol; the repeat is ignored.
	a, b := s.Models["gpt-5.5"], s.Models["gpt-5.6-sol"]
	if a == nil || b == nil {
		t.Fatalf("models = %v", s.Models)
	}
	check("gpt-5.5 input (uncached)", a.Input, int64(400))
	check("gpt-5.5 cache read", a.CacheRead, int64(600))
	check("gpt-5.5 output", a.Output, int64(100))
	check("gpt-5.5 requests", a.Requests, 1)
	check("gpt-5.6 input (uncached)", b.Input, int64(600))
	check("gpt-5.6 cache read", b.CacheRead, int64(1400))
	check("gpt-5.6 output", b.Output, int64(50))
	if len(events) == 0 {
		t.Error("no transcript events")
	}
}

// codexCommands mimics newer Codex, which logs prompts as items and every
// shell command a code-mode script ran.
const codexCommands = `
{"timestamp":"2026-09-28T13:47:48.000Z","type":"session_meta","payload":{"id":"` + codexID + `","cwd":"/work/cat","originator":"Codex Desktop"}}
{"timestamp":"2026-09-28T13:47:49.000Z","type":"turn_context","payload":{"model":"gpt-5.6-sol"}}
{"timestamp":"2026-09-28T13:47:50.000Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","content":[{"type":"text","text":"Run the graph"}]}}}
{"timestamp":"2026-09-28T13:47:50.000Z","type":"event_msg","payload":{"type":"user_message","message":"Run the graph"}}
{"timestamp":"2026-09-28T13:47:51.000Z","type":"response_item","payload":{"type":"custom_tool_call","name":"exec","input":"run()","call_id":"x1"}}
{"timestamp":"2026-09-28T13:47:52.000Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","status":"completed","exit_code":0}}}
{"timestamp":"2026-09-28T13:47:53.000Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"CommandExecution","status":"failed","exit_code":1}}}
{"timestamp":"2026-09-28T13:47:54.000Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"x1","output":[{"type":"input_text","text":"Script completed\nWall time 0.9 seconds"}]}}
{"timestamp":"2026-09-28T13:47:55.000Z","type":"event_msg","payload":{"type":"item_completed","item":{"type":"McpToolCall","server":"app","tool":"create_worktree","status":"failed"}}}
{"timestamp":"2026-09-28T13:47:56.000Z","type":"response_item","payload":{"type":"function_call","name":"update_plan","arguments":"{}","call_id":"x2"}}
`

func TestParseCodexCommandRecords(t *testing.T) {
	c, meta := codexFixture(t, codexCommands, nil)
	s, _, err := ParseCodex(c, meta, 5*time.Minute, false)
	if err != nil {
		t.Fatal(err)
	}
	// Prompts are logged twice; count them once.
	if s.Prompts != 1 {
		t.Errorf("prompts = %d, want 1", s.Prompts)
	}
	// Two commands replace the one exec wrapper; plus MCP and update_plan.
	if s.ToolCalls != 4 || s.ToolErrors != 2 {
		t.Errorf("tool calls/errors = %d/%d, want 4/2 (tools %v)", s.ToolCalls, s.ToolErrors, s.Tools)
	}
	if _, ok := s.Tools["exec"]; ok {
		t.Error("exec wrapper should be replaced by command records")
	}
	if cmd := s.Tools["command"]; cmd == nil || cmd.Calls != 2 || cmd.Errors != 1 {
		t.Errorf("command records = %+v, want 2 calls, 1 error", cmd)
	}
}

func TestParseCodexImport(t *testing.T) {
	// The import copied the Claude conversation at 09:08:19; the human
	// continued in Codex ten minutes later.
	rollout := `
{"timestamp":"2026-10-02T09:08:19.150Z","type":"session_meta","payload":{"id":"` + codexID + `","cwd":"/work/owcli"}}
{"timestamp":"2026-10-02T09:08:19.160Z","type":"event_msg","payload":{"type":"user_message","message":"Original Claude prompt"}}
{"timestamp":"2026-10-02T09:08:19.170Z","type":"event_msg","payload":{"type":"task_complete","duration_ms":50000}}
{"timestamp":"2026-10-02T09:08:19.176Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":0,"output_tokens":0,"total_tokens":167071}}}}
{"timestamp":"2026-10-02T09:18:00.000Z","type":"turn_context","payload":{"model":"gpt-5.5"}}
{"timestamp":"2026-10-02T09:18:00.000Z","type":"event_msg","payload":{"type":"user_message","message":"Continue here"}}
{"timestamp":"2026-10-02T09:18:30.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":500,"output_tokens":20,"total_tokens":520}}}}
`
	imports := `{"records":[{"source_path":"/home/u/.claude/projects/-work-owcli/211383a8-4217-4388-ae87-ef52cdf8cffe.jsonl","imported_thread_id":"` + codexID + `","imported_at":1790932099,"title":"Tool installation"}]}`
	c, meta := codexFixture(t, rollout, map[string]string{"external_agent_session_imports.json": imports})
	s, _, err := ParseCodex(c, meta, 5*time.Minute, false)
	if err != nil {
		t.Fatal(err)
	}
	if s.ImportedFrom != "claude:211383a8-4217-4388-ae87-ef52cdf8cffe" {
		t.Errorf("imported from = %q", s.ImportedFrom)
	}
	if s.Title != "Tool installation" {
		t.Errorf("title = %q, want the import's title", s.Title)
	}
	if s.Prompts != 1 || s.FirstPrompt != "Continue here" {
		t.Errorf("prompts = %d (%q), want only the one typed after the import", s.Prompts, s.FirstPrompt)
	}
	if s.AgentSeconds != 0 {
		t.Errorf("agent seconds = %v, want 0: the copied turns ran in Claude", s.AgentSeconds)
	}
	u := s.Models["gpt-5.5"]
	if u == nil || u.Input != 500 || u.Output != 20 {
		t.Errorf("usage = %+v, want only post-import tokens", s.Models)
	}
	if got := s.StartedAt.Format(time.RFC3339); got != "2026-10-02T09:08:19Z" {
		t.Errorf("started = %s", got)
	}
}

func TestCodexFailed(t *testing.T) {
	for out, want := range map[string][2]bool{
		"Process exited with code 0":                {false, false},
		"Process exited with code 127":              {true, false},
		"Exit code: 1\nWall time: 0.1 seconds":      {true, false},
		`{"output":"x","metadata":{"exit_code":0}}`: {false, false},
		"Script failed\nError: boom":                {true, false},
		"aborted by user after 3.2s":                {false, true},
		"Plan updated":                              {false, false},
	} {
		f, a := codexFailed(out)
		if f != want[0] || a != want[1] {
			t.Errorf("codexFailed(%q) = %v, %v; want %v", out, f, a, want)
		}
	}
}

// Codex Desktop's import sync appends a new batch when the Claude session
// grows. Work done in Codex between batches must still count.
func TestParseCodexImportResync(t *testing.T) {
	rollout := `
{"timestamp":"2026-09-25T21:08:19.344Z","type":"session_meta","payload":{"id":"` + codexID + `","cwd":"/work/cat"}}
{"timestamp":"2026-09-25T21:08:19.349Z","type":"event_msg","payload":{"type":"user_message","message":"Copied prompt 1"}}
{"timestamp":"2026-09-25T21:08:19.350Z","type":"event_msg","payload":{"type":"task_complete","duration_ms":90000}}
{"timestamp":"2026-09-25T21:08:19.355Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":0,"output_tokens":0,"total_tokens":37942}}}}
{"timestamp":"2026-09-25T22:00:00.000Z","type":"turn_context","payload":{"model":"gpt-5.5"}}
{"timestamp":"2026-09-25T22:00:00.000Z","type":"event_msg","payload":{"type":"user_message","message":"Typed in Codex"}}
{"timestamp":"2026-09-25T22:00:30.000Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000,"output_tokens":100,"total_tokens":1100}}}}
{"timestamp":"2026-09-25T22:00:31.000Z","type":"event_msg","payload":{"type":"task_complete","duration_ms":31000}}
{"timestamp":"2026-09-26T21:08:19.700Z","type":"event_msg","payload":{"type":"user_message","message":"Copied prompt 2"}}
{"timestamp":"2026-09-26T21:08:19.787Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":0,"output_tokens":0,"total_tokens":57472}}}}
`
	// The imports file records only the latest sync.
	imports := `{"records":[{"source_path":"/home/u/.claude/projects/-work-cat/7f9aa3a6-4660-44fb-a703-27e5f74a1be0.jsonl","imported_thread_id":"` + codexID + `","imported_at":` + strconv.FormatInt(time.Date(2026, 10, 2, 9, 8, 18, 0, time.UTC).Unix(), 10) + `}]}`
	c, meta := codexFixture(t, rollout, map[string]string{"external_agent_session_imports.json": imports})
	s, _, err := ParseCodex(c, meta, 5*time.Minute, false)
	if err != nil {
		t.Fatal(err)
	}
	if s.Prompts != 1 || s.FirstPrompt != "Typed in Codex" {
		t.Errorf("prompts = %d (%q), want only the one typed between syncs", s.Prompts, s.FirstPrompt)
	}
	if s.AgentSeconds != 31 {
		t.Errorf("agent seconds = %v, want 31", s.AgentSeconds)
	}
	if u := s.Models["gpt-5.5"]; u == nil || u.Input != 1000 || u.Output != 100 {
		t.Errorf("usage = %+v", s.Models)
	}
}
