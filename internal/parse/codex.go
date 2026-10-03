package parse

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Hoodoo/bossman/internal/model"
)

// rolloutID matches the session UUID at the end of a Codex rollout file
// name: rollout-<timestamp>-<uuid>.jsonl.
var rolloutID = regexp.MustCompile(`([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\.jsonl$`)

// DiscoverCodex finds Codex rollouts under a Codex home (~/.codex or its
// archive mirror): sessions/YYYY/MM/DD/rollout-*.jsonl.
func DiscoverCodex(root string) ([]Candidate, error) {
	var out []Candidate
	err := filepath.WalkDir(filepath.Join(root, "sessions"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.SkipAll
			}
			return nil
		}
		if d.IsDir() || !strings.HasPrefix(d.Name(), "rollout-") {
			return nil
		}
		m := rolloutID.FindStringSubmatch(d.Name())
		if m == nil {
			return nil
		}
		c := Candidate{Agent: model.AgentCodex, ID: m[1], Main: path, Files: []string{path}}
		c.stat()
		out = append(out, c)
		return nil
	})
	return out, err
}

// CodexImport records a Codex thread created by importing another
// agent's session (Codex Desktop can import Claude Code sessions).
type CodexImport struct {
	// From is the imported session's key, e.g. claude:<id>.
	From  string
	Title string
	At    time.Time
}

// CodexMeta is per-session data Codex keeps outside the rollouts.
type CodexMeta struct {
	Titles  map[string]string
	Imports map[string]CodexImport
}

// CodexMetaFiles are the files under a Codex home that CodexMeta reads.
var CodexMetaFiles = []string{"session_index.jsonl", "external_agent_session_imports.json"}

// LoadCodexMeta reads thread names from session_index.jsonl (Codex appends
// one each time a name changes; the last wins) and imported threads from
// external_agent_session_imports.json.
func LoadCodexMeta(root string) CodexMeta {
	m := CodexMeta{Titles: map[string]string{}, Imports: map[string]CodexImport{}}
	_ = eachLine(filepath.Join(root, "session_index.jsonl"), func(l []byte) {
		var e struct {
			ID         string `json:"id"`
			ThreadName string `json:"thread_name"`
		}
		if json.Unmarshal(l, &e) == nil && e.ID != "" && e.ThreadName != "" {
			m.Titles[e.ID] = e.ThreadName
		}
	})
	var imports struct {
		Records []struct {
			SourcePath string `json:"source_path"`
			ThreadID   string `json:"imported_thread_id"`
			ImportedAt int64  `json:"imported_at"`
			Title      string `json:"title"`
		} `json:"records"`
	}
	if b, err := os.ReadFile(filepath.Join(root, "external_agent_session_imports.json")); err == nil && json.Unmarshal(b, &imports) == nil {
		for _, r := range imports.Records {
			from := r.SourcePath
			if strings.Contains(r.SourcePath, "/.claude/") && strings.HasSuffix(r.SourcePath, ".jsonl") {
				from = model.AgentClaude + ":" + strings.TrimSuffix(filepath.Base(r.SourcePath), ".jsonl")
			}
			m.Imports[r.ThreadID] = CodexImport{From: from, Title: r.Title, At: time.Unix(r.ImportedAt, 0).UTC()}
		}
	}
	return m
}

// Codex Desktop's external-agent import sync (config.toml [desktop]
// external-agent-import-sync-enabled) copies Claude sessions into Codex
// threads on its own, roughly daily, and appends to a thread when the
// Claude session has grown. Each sync writes its batch of copied lines
// within milliseconds and ends it with a token_count whose total is set
// but whose components are all zero. Lines stamped up to importBatch
// before such a marker are copies. A thread without markers falls back to
// everything up to importBatch after the recorded import time.
const importBatch = 2 * time.Minute

// importMarkers returns the end times of the import batches in a rollout.
func importMarkers(path string) ([]time.Time, error) {
	var marks []time.Time
	err := eachLine(path, func(raw []byte) {
		if !bytes.Contains(raw, []byte(`"token_count"`)) {
			return
		}
		var l codexLine
		var pl codexPayload
		if json.Unmarshal(raw, &l) != nil || json.Unmarshal(l.Payload, &pl) != nil {
			return
		}
		if pl.Type != "token_count" || pl.Info == nil || pl.Info.Total == nil {
			return
		}
		t := pl.Info.Total
		if t.Total > 0 && t.Input == 0 && t.Cached == 0 && t.CacheWrite == 0 && t.Output == 0 {
			marks = append(marks, parseTime(l.Timestamp))
		}
	})
	return marks, err
}

// copied reports whether a line stamped at belongs to an import batch.
func (p *codexParser) copied(at time.Time) bool {
	for _, m := range p.importEnds {
		if !at.After(m) && at.After(m.Add(-importBatch)) {
			return true
		}
	}
	return false
}

type codexLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexPayload struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	Cwd        string `json:"cwd"`
	CLIVersion string `json:"cli_version"`
	Originator string `json:"originator"`
	Git        *struct {
		Branch string `json:"branch"`
	} `json:"git"`
	Model      string          `json:"model"`
	Role       string          `json:"role"`
	Name       string          `json:"name"`
	Arguments  string          `json:"arguments"`
	Input      string          `json:"input"`
	CallID     string          `json:"call_id"`
	Output     json.RawMessage `json:"output"`
	Content    json.RawMessage `json:"content"`
	Message    string          `json:"message"`
	Reason     string          `json:"reason"`
	DurationMs float64         `json:"duration_ms"`
	Info       *struct {
		Total *codexTokens `json:"total_token_usage"`
	} `json:"info"`
	Item *codexItem `json:"item"`
}

type codexTokens struct {
	Input      int64 `json:"input_tokens"`
	Cached     int64 `json:"cached_input_tokens"`
	CacheWrite int64 `json:"cache_write_input_tokens"`
	Output     int64 `json:"output_tokens"`
	Reasoning  int64 `json:"reasoning_output_tokens"`
	Total      int64 `json:"total_tokens"`
}

type codexItem struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Server  string `json:"server"`
	Tool    string `json:"tool"`
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

// Codex runs shell commands through wrapper tools. Newer versions also log
// every command a wrapper ran (item_completed/CommandExecution), which is
// the better measure: one code-mode script can run many commands, and its
// own output does not show which of them failed. Both are tallied under
// reserved tool names while parsing and reconciled in finish.
var shellWrappers = map[string]bool{"exec": true, "exec_command": true, "shell": true, "local_shell": true}

const (
	wrapperPrefix  = "\x00wrapper:"
	commandRecords = "\x00commands"
)

var exitCode = regexp.MustCompile(`(?:exited with code|Exit code:|"exit_code":)\s*(-?\d+)`)

type codexParser struct {
	// s is the session being counted: real, or scratch while reading
	// lines copied in by an import, which must not count twice.
	s, real, scratch *model.Session
	importEnds       []time.Time
	importedPrompt   string
	clock            clock
	idle             time.Duration
	want             bool
	events           []model.Event

	model       string
	prev        codexTokens
	concludedAt time.Time
	toolName    map[string]string

	// Codex has logged human prompts as event_msg/user_message and, in
	// newer versions, as item_completed/UserMessage; use whichever exists.
	itemPrompts, eventPrompts []string
	itemAt, eventAt           []time.Time
}

// ParseCodex parses a Codex rollout. meta comes from LoadCodexMeta.
func ParseCodex(c Candidate, meta CodexMeta, idle time.Duration, events bool) (*model.Session, []model.Event, error) {
	newSession := func() *model.Session {
		return &model.Session{
			Agent:  model.AgentCodex,
			ID:     c.ID,
			Models: map[string]*model.Usage{},
			Tools:  map[string]*model.ToolStat{},
		}
	}
	p := &codexParser{
		real:     newSession(),
		scratch:  newSession(),
		idle:     idle,
		want:     events,
		toolName: map[string]string{},
	}
	p.s = p.real
	imp, imported := meta.Imports[c.ID]
	if imported {
		p.real.ImportedFrom = imp.From
		marks, err := importMarkers(c.Main)
		if err != nil {
			return nil, nil, err
		}
		if len(marks) == 0 {
			marks = []time.Time{imp.At.Add(importBatch)}
		}
		p.importEnds = marks
	}
	if err := eachLine(c.Main, p.line); err != nil {
		return nil, nil, err
	}
	p.s = p.real
	p.finish()
	s := p.real
	s.Title = meta.Titles[c.ID]
	if s.Title == "" && imported {
		s.Title = imp.Title
	}
	if s.Title != "" {
		s.Summaries = append([]model.Summary{{Kind: "title", Text: s.Title}}, s.Summaries...)
	}
	return s, p.events, nil
}

func (p *codexParser) line(raw []byte) {
	var l codexLine
	if json.Unmarshal(raw, &l) != nil {
		return
	}
	var pl codexPayload
	if len(l.Payload) > 0 && json.Unmarshal(l.Payload, &pl) != nil {
		return
	}
	at := parseTime(l.Timestamp)
	p.s = p.real
	if l.Type != "session_meta" && p.copied(at) {
		p.s = p.scratch
	} else {
		p.clock.add(at)
	}
	s := p.s
	switch l.Type {
	case "session_meta":
		if pl.Cwd != "" {
			s.Project = pl.Cwd
		}
		s.Version = pl.CLIVersion
		s.Entrypoint = pl.Originator
		if pl.Git != nil {
			s.GitBranch = pl.Git.Branch
		}
	case "turn_context":
		if pl.Model != "" {
			p.model = pl.Model
		}
		if pl.Cwd != "" && s.Project == "" {
			s.Project = pl.Cwd
		}
	case "compacted":
		s.Compactions++
		if pl.Message != "" {
			s.Summaries = append(s.Summaries, model.Summary{Kind: "compaction", Text: pl.Message, At: at})
		}
		if p.want {
			p.events = append(p.events, model.Event{At: at, Role: "system", Text: "Conversation compacted"})
		}
	case "response_item":
		p.responseItem(&pl, at)
	case "event_msg":
		p.event(&pl, at)
	}
}

func (p *codexParser) responseItem(pl *codexPayload, at time.Time) {
	s := p.s
	switch pl.Type {
	case "function_call", "custom_tool_call", "local_shell_call", "web_search_call":
		name := pl.Name
		if name == "" {
			name = strings.TrimSuffix(pl.Type, "_call")
		}
		if pl.CallID != "" {
			p.toolName[pl.CallID] = name
		}
		if shellWrappers[name] {
			// Counted in finish, unless per-command records replace it.
			p.tool(wrapperPrefix+name).Calls++
		} else {
			p.tool(name).Calls++
			s.ToolCalls++
		}
		if p.want {
			in := pl.Arguments
			if in == "" {
				in = pl.Input
			}
			p.events = append(p.events, model.Event{At: at, Role: "tool", Tool: name, Text: codexArgs(in)})
		}
	case "function_call_output", "custom_tool_call_output":
		out := codexOutput(pl.Output)
		failed, aborted := codexFailed(out)
		name := p.toolName[pl.CallID]
		switch {
		case aborted:
			s.Rejections++
		case failed && shellWrappers[name]:
			p.tool(wrapperPrefix+name).Errors++
		case failed:
			s.ToolErrors++
			p.tool(name).Errors++
		}
		if p.want {
			p.events = append(p.events, model.Event{At: at, Role: "result", Tool: p.toolName[pl.CallID], Text: out, IsError: failed || aborted})
		}
	case "message":
		if pl.Role != "assistant" {
			return
		}
		text := codexContentText(pl.Content)
		p.checkConclusion(text, at)
		if p.want && strings.TrimSpace(text) != "" {
			p.events = append(p.events, model.Event{At: at, Role: "assistant", Text: text})
		}
	}
}

func (p *codexParser) event(pl *codexPayload, at time.Time) {
	s := p.s
	switch pl.Type {
	case "token_count":
		if pl.Info == nil || pl.Info.Total == nil {
			return
		}
		p.tokens(*pl.Info.Total)
	case "task_complete", "turn_aborted":
		s.AgentSeconds += pl.DurationMs / 1000
		if p.s == p.real {
			p.clock.turn(at, time.Duration(pl.DurationMs*float64(time.Millisecond)))
		}
		if pl.Type == "task_complete" {
			return
		}
		if pl.Reason == "interrupted" {
			s.Interrupts++
			if p.want {
				p.events = append(p.events, model.Event{At: at, Role: "system", Text: "Interrupted by user"})
			}
		}
	case "user_message":
		p.prompt(pl.Message, at, false)
	case "error", "stream_error":
		s.APIErrors++
		if p.want {
			p.events = append(p.events, model.Event{At: at, Role: "system", Text: pl.Message, IsError: true})
		}
	case "item_completed":
		it := pl.Item
		if it == nil {
			return
		}
		switch it.Type {
		case "UserMessage":
			var parts []string
			for _, c := range it.Content {
				parts = append(parts, c.Text)
			}
			p.prompt(strings.Join(parts, "\n"), at, true)
		case "CommandExecution":
			t := p.tool(commandRecords)
			t.Calls++
			if it.Status == "failed" {
				t.Errors++
			}
		case "McpToolCall":
			name := "mcp:" + it.Server + "/" + it.Tool
			t := p.tool(name)
			t.Calls++
			s.ToolCalls++
			if it.Status == "failed" {
				t.Errors++
				s.ToolErrors++
			}
			if p.want {
				p.events = append(p.events, model.Event{At: at, Role: "tool", Tool: name, IsError: it.Status == "failed"})
			}
		}
	}
}

func (p *codexParser) prompt(text string, at time.Time, item bool) {
	if p.s == p.scratch {
		if p.importedPrompt == "" {
			p.importedPrompt = strings.TrimSpace(text)
		}
		if p.want {
			p.events = append(p.events, model.Event{At: at, Role: "user", Text: text})
		}
		return
	}
	if item {
		p.itemPrompts = append(p.itemPrompts, text)
		p.itemAt = append(p.itemAt, at)
	} else {
		p.eventPrompts = append(p.eventPrompts, text)
		p.eventAt = append(p.eventAt, at)
	}
}

// tokens attributes the growth of Codex's cumulative counter to the model
// in effect.
func (p *codexParser) tokens(t codexTokens) {
	d := codexTokens{
		Input: t.Input - p.prev.Input, Cached: t.Cached - p.prev.Cached,
		CacheWrite: t.CacheWrite - p.prev.CacheWrite, Output: t.Output - p.prev.Output,
		Reasoning: t.Reasoning - p.prev.Reasoning, Total: t.Total - p.prev.Total,
	}
	if t.Total < p.prev.Total {
		d = t // counter reset
	}
	p.prev = t
	if d.Input == 0 && d.Output == 0 && d.Cached == 0 && d.CacheWrite == 0 {
		return
	}
	name := p.model
	if name == "" {
		name = "unknown"
	}
	u := p.s.Models[name]
	if u == nil {
		u = &model.Usage{}
		p.s.Models[name] = u
	}
	// OpenAI input counts include cached tokens; split them out.
	in := d.Input - d.Cached - d.CacheWrite
	if in < 0 {
		in = 0
	}
	u.Add(model.Usage{Input: in, CacheRead: d.Cached, CacheWrite5m: d.CacheWrite, Output: d.Output, Reasoning: d.Reasoning, Requests: 1})
}

func (p *codexParser) tool(name string) *model.ToolStat {
	if name == "" {
		name = "?"
	}
	t := p.s.Tools[name]
	if t == nil {
		t = &model.ToolStat{}
		p.s.Tools[name] = t
	}
	return t
}

func (p *codexParser) checkConclusion(text string, at time.Time) {
	if sum, ok := catalogueSummary(text); ok {
		p.s.Concluded = true
		p.concludedAt = at
		if sum != "" {
			p.s.Summaries = append(p.s.Summaries, model.Summary{Kind: "conclusion", Text: sum, At: at})
		}
	}
}

func (p *codexParser) finish() {
	s := p.s
	if cmd := s.Tools[commandRecords]; cmd != nil {
		delete(s.Tools, commandRecords)
		for name := range s.Tools {
			if strings.HasPrefix(name, wrapperPrefix) {
				delete(s.Tools, name)
			}
		}
		s.Tools["command"] = cmd
		s.ToolCalls += cmd.Calls
		s.ToolErrors += cmd.Errors
	} else {
		for name, t := range s.Tools {
			if strings.HasPrefix(name, wrapperPrefix) {
				delete(s.Tools, name)
				s.Tools[strings.TrimPrefix(name, wrapperPrefix)] = t
				s.ToolCalls += t.Calls
				s.ToolErrors += t.Errors
			}
		}
	}
	prompts, at := p.itemPrompts, p.itemAt
	if len(prompts) == 0 {
		prompts, at = p.eventPrompts, p.eventAt
	}
	for i, text := range prompts {
		t := strings.TrimSpace(text)
		if t == "" {
			continue
		}
		s.Prompts++
		if s.FirstPrompt == "" {
			s.FirstPrompt = t
		}
		if s.Concluded && at[i].After(p.concludedAt) {
			s.Concluded = false // the human carried on after concluding
		}
		if p.want {
			p.events = append(p.events, model.Event{At: at[i], Role: "user", Text: t})
		}
	}
	if s.FirstPrompt == "" {
		s.FirstPrompt = p.importedPrompt
	}
	s.StartedAt, s.EndedAt, s.ActiveSeconds = p.clock.span(p.idle)
	sort.SliceStable(p.events, func(i, j int) bool { return p.events[i].At.Before(p.events[j].At) })
}

// codexOutput flattens a tool output, which is a string or a list of
// {text} parts.
func codexOutput(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return string(raw)
	}
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

func codexContentText(raw json.RawMessage) string {
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var out []string
	for _, p := range parts {
		if p.Text != "" {
			out = append(out, p.Text)
		}
	}
	return strings.Join(out, "\n")
}

// codexFailed classifies a tool output: a non-zero exit code or a failed
// script or patch is an error; a command the user aborted is not.
func codexFailed(out string) (failed, aborted bool) {
	head := out
	if len(head) > 400 {
		head = head[:400]
	}
	if strings.Contains(head, "aborted by user") {
		return false, true
	}
	if strings.HasPrefix(head, "Script failed") || strings.Contains(head, "verification failed") {
		return true, false
	}
	if m := exitCode.FindStringSubmatch(head); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n != 0, false
	}
	return false, false
}

// codexArgs renders tool arguments compactly for the transcript.
func codexArgs(in string) string {
	var m map[string]any
	if json.Unmarshal([]byte(in), &m) != nil {
		return in
	}
	if c, ok := m["cmd"].(string); ok {
		return c
	}
	if c, ok := m["command"].([]any); ok {
		var parts []string
		for _, x := range c {
			parts = append(parts, toString(x))
		}
		return strings.Join(parts, " ")
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func toString(x any) string {
	if s, ok := x.(string); ok {
		return s
	}
	b, _ := json.Marshal(x)
	return string(b)
}
