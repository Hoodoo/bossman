package parse

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Hoodoo/bossman/internal/model"
)

// DiscoverClaude finds Claude Code sessions under a projects directory
// (~/.claude/projects or its archive mirror). Each session is
// <project>/<id>.jsonl plus an optional <project>/<id>/ directory holding
// subagent transcripts and tool-result sidecars.
func DiscoverClaude(root string) ([]Candidate, error) {
	projects, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Candidate
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		dir := filepath.Join(root, p.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || filepath.Ext(name) != ".jsonl" {
				continue
			}
			id := strings.TrimSuffix(name, ".jsonl")
			c := Candidate{Agent: model.AgentClaude, ID: id, Main: filepath.Join(dir, name)}
			c.Files = append(c.Files, c.Main)
			side := filepath.Join(dir, id)
			_ = filepath.WalkDir(side, func(path string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					c.Files = append(c.Files, path)
				}
				return nil
			})
			c.stat()
			out = append(out, c)
		}
	}
	return out, nil
}

type claudeEntry struct {
	Type              string                 `json:"type"`
	Subtype           string                 `json:"subtype"`
	Timestamp         string                 `json:"timestamp"`
	Cwd               string                 `json:"cwd"`
	GitBranch         string                 `json:"gitBranch"`
	Version           string                 `json:"version"`
	Entrypoint        string                 `json:"entrypoint"`
	IsSidechain       bool                   `json:"isSidechain"`
	IsMeta            bool                   `json:"isMeta"`
	IsCompactSummary  bool                   `json:"isCompactSummary"`
	IsAPIErrorMessage bool                   `json:"isApiErrorMessage"`
	Origin            *struct{ Kind string } `json:"origin"`
	Message           *claudeMessage         `json:"message"`
	Content           json.RawMessage        `json:"content"`
	DurationMs        float64                `json:"durationMs"`
	AITitle           string                 `json:"aiTitle"`
	CustomTitle       string                 `json:"customTitle"`
	Summary           string                 `json:"summary"`
	TotalCostUSD      *float64               `json:"totalCostUSD"`
}

type claudeMessage struct {
	ID      string          `json:"id"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   *claudeUsage    `json:"usage"`
}

type claudeUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	OutputTokensDetails      *struct {
		ThinkingTokens int64 `json:"thinking_tokens"`
	} `json:"output_tokens_details"`
	CacheCreation *struct {
		Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
		Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

// recapFooter is the hint Claude Code appends to every away summary.
const recapFooter = "(disable recaps in /config)"

// Markers Claude Code writes into user turns.
const (
	claudeInterrupt = "[Request interrupted by user"
	claudeRejection = "The user doesn't want to proceed with this tool use"
)

var (
	commandName = regexp.MustCompile(`(?s)<command-name>(.*?)</command-name>`)
	commandArgs = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
)

type claudeParser struct {
	s      *model.Session
	clock  clock
	idle   time.Duration
	events []model.Event
	want   bool // collect events

	usage    map[string]claudeUsage // per message id; last line wins
	msgModel map[string]string
	toolName map[string]string // tool_use id -> name
	seenTool map[string]bool
	title    string
	custom   string
	compacts int
	boundary int

	concludedAt, lastPromptAt time.Time
}

// ParseClaude parses a discovered Claude session. With events set it
// also returns the transcript.
func ParseClaude(c Candidate, idle time.Duration, events bool) (*model.Session, []model.Event, error) {
	p := &claudeParser{
		s: &model.Session{
			Agent:  model.AgentClaude,
			ID:     c.ID,
			Models: map[string]*model.Usage{},
			Tools:  map[string]*model.ToolStat{},
		},
		idle:     idle,
		want:     events,
		usage:    map[string]claudeUsage{},
		msgModel: map[string]string{},
		toolName: map[string]string{},
		seenTool: map[string]bool{},
	}
	if err := eachLine(c.Main, func(l []byte) { p.line(l, false) }); err != nil {
		return nil, nil, err
	}
	for _, f := range c.Files[1:] {
		if filepath.Ext(f) != ".jsonl" || filepath.Base(filepath.Dir(f)) != "subagents" {
			continue
		}
		p.s.Subagents++
		if err := eachLine(f, func(l []byte) { p.line(l, true) }); err != nil {
			return nil, nil, err
		}
	}
	p.finish()
	return p.s, p.events, nil
}

func (p *claudeParser) line(raw []byte, subagent bool) {
	var e claudeEntry
	if json.Unmarshal(raw, &e) != nil {
		return
	}
	s := p.s
	at := parseTime(e.Timestamp)
	side := subagent || e.IsSidechain
	if !side {
		p.clock.add(at)
		if s.Project == "" && e.Cwd != "" {
			s.Project = e.Cwd
		}
		if e.GitBranch != "" {
			s.GitBranch = e.GitBranch
		}
		if e.Version != "" {
			s.Version = e.Version
		}
		if s.Entrypoint == "" && e.Entrypoint != "" {
			s.Entrypoint = e.Entrypoint
		}
	}
	switch e.Type {
	case "assistant":
		p.assistant(&e, at, side)
	case "user":
		p.user(&e, at, side)
	case "system":
		p.system(&e, at, side)
	case "ai-title":
		if e.AITitle != "" {
			p.title = e.AITitle
		}
	case "custom-title":
		if e.CustomTitle != "" {
			p.custom = e.CustomTitle
		}
	case "summary":
		if e.Summary != "" {
			s.Summaries = append(s.Summaries, model.Summary{Kind: "summary", Text: e.Summary})
		}
	case "cost-state":
		if e.TotalCostUSD != nil && !subagent {
			v := *e.TotalCostUSD
			s.ReportedCostUSD = &v
		}
	}
}

func (p *claudeParser) assistant(e *claudeEntry, at time.Time, side bool) {
	m := e.Message
	if m == nil {
		return
	}
	if e.IsAPIErrorMessage {
		p.s.APIErrors++
		if p.want {
			p.events = append(p.events, model.Event{At: at, Role: "system", Text: blocksText(m.Content), IsError: true, Sidechain: side})
		}
		return
	}
	if m.ID != "" && m.Usage != nil && m.Model != "" && m.Model != "<synthetic>" {
		// Claude Code writes one line per content block, each repeating the
		// message's usage; count every message once.
		p.usage[m.ID] = *m.Usage
		p.msgModel[m.ID] = m.Model
	}
	var blocks []claudeBlock
	if json.Unmarshal(m.Content, &blocks) != nil {
		return
	}
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if sum, ok := catalogueSummary(b.Text); ok && !side {
				p.s.Concluded = true
				p.concludedAt = at
				if sum != "" {
					p.s.Summaries = append(p.s.Summaries, model.Summary{Kind: "conclusion", Text: sum, At: at})
				}
			}
			if p.want && strings.TrimSpace(b.Text) != "" {
				p.events = append(p.events, model.Event{At: at, Role: "assistant", Text: b.Text, Sidechain: side})
			}
		case "tool_use":
			if b.ID != "" {
				if p.seenTool[b.ID] {
					continue
				}
				p.seenTool[b.ID] = true
				p.toolName[b.ID] = b.Name
			}
			p.tool(b.Name).Calls++
			p.s.ToolCalls++
			if p.want {
				p.events = append(p.events, model.Event{At: at, Role: "tool", Tool: b.Name, Text: toolInput(b.Input), Sidechain: side})
			}
		}
	}
}

func (p *claudeParser) tool(name string) *model.ToolStat {
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

func (p *claudeParser) user(e *claudeEntry, at time.Time, side bool) {
	m := e.Message
	if m == nil {
		return
	}
	s := p.s
	if e.IsCompactSummary {
		if !side {
			p.compacts++
			s.Summaries = append(s.Summaries, model.Summary{Kind: "compaction", Text: blocksText(m.Content), At: at})
		}
		return
	}
	var text string
	var blocks []claudeBlock
	if json.Unmarshal(m.Content, &text) != nil {
		if json.Unmarshal(m.Content, &blocks) != nil {
			return
		}
	}
	hasResult := false
	for _, b := range blocks {
		switch b.Type {
		case "text":
			text += b.Text
		case "tool_result":
			hasResult = true
			out := resultText(b.Content)
			rejected := strings.Contains(out, claudeRejection)
			if rejected && !side {
				s.Rejections++
			}
			if b.IsError && !rejected {
				s.ToolErrors++
				p.tool(p.toolName[b.ToolUseID]).Errors++
			}
			if p.want {
				p.events = append(p.events, model.Event{At: at, Role: "result", Tool: p.toolName[b.ToolUseID], Text: out, IsError: b.IsError, Sidechain: side})
			}
		}
	}
	if strings.Contains(text, claudeInterrupt) {
		if !side {
			s.Interrupts++
		}
		if p.want {
			p.events = append(p.events, model.Event{At: at, Role: "system", Text: "Interrupted by user", Sidechain: side})
		}
		return
	}
	if hasResult || side || e.IsMeta || (e.Origin != nil && e.Origin.Kind != "human") {
		return
	}
	prompt, ok := humanPrompt(text)
	if !ok {
		return
	}
	s.Prompts++
	if at.After(p.lastPromptAt) {
		p.lastPromptAt = at
	}
	if s.FirstPrompt == "" {
		s.FirstPrompt = prompt
	}
	if p.want {
		p.events = append(p.events, model.Event{At: at, Role: "user", Text: prompt})
	}
}

// humanPrompt returns the text a human typed, or false for messages
// Claude Code injects into the user role.
func humanPrompt(text string) (string, bool) {
	t := strings.TrimSpace(text)
	if t == "" {
		return "", false
	}
	if m := commandName.FindStringSubmatch(t); m != nil && strings.HasPrefix(t, "<command-") {
		cmd := strings.TrimSpace(m[1])
		if a := commandArgs.FindStringSubmatch(t); a != nil {
			cmd = strings.TrimSpace(cmd + " " + strings.TrimSpace(a[1]))
		}
		return cmd, true
	}
	for _, injected := range []string{"<local-command-", "<task-notification>", "<system-reminder>", "Caveat: The messages below"} {
		if strings.HasPrefix(t, injected) {
			return "", false
		}
	}
	return t, true
}

func (p *claudeParser) system(e *claudeEntry, at time.Time, side bool) {
	if side {
		return
	}
	s := p.s
	switch e.Subtype {
	case "turn_duration":
		s.AgentSeconds += e.DurationMs / 1000
		p.clock.turn(at, time.Duration(e.DurationMs*float64(time.Millisecond)))
	case "away_summary":
		var text string
		if json.Unmarshal(e.Content, &text) == nil && text != "" {
			text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), recapFooter))
			s.Summaries = append(s.Summaries, model.Summary{Kind: "recap", Text: text, At: at})
			if p.want {
				p.events = append(p.events, model.Event{At: at, Role: "system", Text: "Recap: " + text})
			}
		}
	case "compact_boundary":
		p.boundary++
		if p.want {
			p.events = append(p.events, model.Event{At: at, Role: "system", Text: "Conversation compacted"})
		}
	case "api_error":
		s.APIErrors++
	}
}

func (p *claudeParser) finish() {
	s := p.s
	for id, u := range p.usage {
		m := p.msgModel[id]
		mu := s.Models[m]
		if mu == nil {
			mu = &model.Usage{}
			s.Models[m] = mu
		}
		w5, w1 := u.CacheCreationInputTokens, int64(0)
		if u.CacheCreation != nil && u.CacheCreation.Ephemeral5m+u.CacheCreation.Ephemeral1h > 0 {
			w5, w1 = u.CacheCreation.Ephemeral5m, u.CacheCreation.Ephemeral1h
		}
		var think int64
		if u.OutputTokensDetails != nil {
			think = u.OutputTokensDetails.ThinkingTokens
		}
		mu.Add(model.Usage{
			Input: u.InputTokens, CacheWrite5m: w5, CacheWrite1h: w1,
			CacheRead: u.CacheReadInputTokens, Output: u.OutputTokens,
			Reasoning: think, Requests: 1,
		})
	}
	if s.Concluded && p.lastPromptAt.After(p.concludedAt) {
		s.Concluded = false // the human carried on after concluding
	}
	s.Compactions = p.boundary
	if p.compacts > s.Compactions {
		s.Compactions = p.compacts
	}
	switch {
	case p.custom != "":
		s.Title = p.custom
	case p.title != "":
		s.Title = p.title
	}
	if p.title != "" {
		s.Summaries = append([]model.Summary{{Kind: "title", Text: p.title}}, s.Summaries...)
	}
	s.StartedAt, s.EndedAt, s.ActiveSeconds = p.clock.span(p.idle)
	sort.SliceStable(p.events, func(i, j int) bool { return p.events[i].At.Before(p.events[j].At) })
}

// blocksText joins the text of a string-or-blocks content field.
func blocksText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []claudeBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func resultText(raw json.RawMessage) string { return blocksText(raw) }

// toolInput renders a tool call's input compactly for the transcript.
func toolInput(raw json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return string(raw)
	}
	for _, k := range []string{"command", "file_path", "pattern", "url", "query", "description", "prompt"} {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	b, _ := json.Marshal(m)
	return string(b)
}
