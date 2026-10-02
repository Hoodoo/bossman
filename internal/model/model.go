// Package model defines the agent-neutral session record that parsers
// produce and the store indexes.
package model

import "time"

// Agents bossman understands.
const (
	AgentClaude = "claude"
	AgentCodex  = "codex"
)

// Usage is token usage for one model. Input excludes cached and
// cache-write tokens; Output includes Reasoning.
type Usage struct {
	Input        int64 `json:"input"`
	CacheWrite5m int64 `json:"cache_write_5m"`
	CacheWrite1h int64 `json:"cache_write_1h"`
	CacheRead    int64 `json:"cache_read"`
	Output       int64 `json:"output"`
	Reasoning    int64 `json:"reasoning"`
	Requests     int   `json:"requests"`
}

func (u *Usage) Add(o Usage) {
	u.Input += o.Input
	u.CacheWrite5m += o.CacheWrite5m
	u.CacheWrite1h += o.CacheWrite1h
	u.CacheRead += o.CacheRead
	u.Output += o.Output
	u.Reasoning += o.Reasoning
	u.Requests += o.Requests
}

// ToolStat counts calls to one tool and how many of them failed.
type ToolStat struct {
	Calls  int `json:"calls"`
	Errors int `json:"errors"`
}

// Summary is text the agent itself wrote about the session.
type Summary struct {
	// Kind is title, recap (Claude away summary), compaction, or
	// conclusion (the cc-catalogue marker).
	Kind string    `json:"kind"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// Session is everything bossman derives from one agent session's files.
type Session struct {
	Agent string `json:"agent"`
	ID    string `json:"id"`
	// Path is the main log file, relative to the archive directory.
	Path string `json:"path"`
	// Files lists every file of the session relative to the archive
	// directory (main log, subagent logs, sidecars).
	Files []string `json:"files"`

	// ImportedFrom names the session this one was imported from (Codex
	// can import Claude sessions); the copied part is not counted.
	ImportedFrom string `json:"imported_from,omitempty"`

	Project    string `json:"project"`
	GitBranch  string `json:"git_branch"`
	Version    string `json:"version"`
	Entrypoint string `json:"entrypoint"`

	Title       string    `json:"title"`
	FirstPrompt string    `json:"first_prompt"`
	Summaries   []Summary `json:"summaries"`
	Concluded   bool      `json:"concluded"`

	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at"`
	// ActiveSeconds sums gaps between consecutive events up to the idle cap.
	ActiveSeconds float64 `json:"active_seconds"`
	// AgentSeconds sums the turn durations the agent itself reported.
	AgentSeconds float64 `json:"agent_seconds"`

	// Prompts counts messages the human typed.
	Prompts int `json:"prompts"`
	// Interrupts counts turns the human stopped.
	Interrupts int `json:"interrupts"`
	// Rejections counts tool calls the human declined.
	Rejections  int `json:"rejections"`
	ToolCalls   int `json:"tool_calls"`
	ToolErrors  int `json:"tool_errors"`
	APIErrors   int `json:"api_errors"`
	Compactions int `json:"compactions"`
	Subagents   int `json:"subagents"`

	Models map[string]*Usage    `json:"models"`
	Tools  map[string]*ToolStat `json:"tools"`
	// ReportedCostUSD is the cost the agent itself recorded, if any.
	ReportedCostUSD *float64 `json:"reported_cost_usd,omitempty"`
}

// Key identifies a session across agents.
func (s *Session) Key() string { return s.Agent + ":" + s.ID }

// WallSeconds is the time from the first to the last event.
func (s *Session) WallSeconds() float64 {
	if s.StartedAt.IsZero() || s.EndedAt.IsZero() {
		return 0
	}
	return s.EndedAt.Sub(s.StartedAt).Seconds()
}

// Interventions counts human steering after the opening prompt: follow-up
// prompts, interrupts, and rejected tool calls.
func (s *Session) Interventions() int {
	n := s.Interrupts + s.Rejections
	if s.Prompts > 1 {
		n += s.Prompts - 1
	}
	return n
}

// Total sums usage across models.
func (s *Session) Total() Usage {
	var t Usage
	for _, u := range s.Models {
		t.Add(*u)
	}
	return t
}

// Event is one transcript line for display.
type Event struct {
	At time.Time `json:"at"`
	// Role is user, assistant, tool, result, or system.
	Role    string `json:"role"`
	Text    string `json:"text"`
	Tool    string `json:"tool,omitempty"`
	IsError bool   `json:"is_error,omitempty"`
	// Sidechain marks events from a subagent.
	Sidechain bool `json:"sidechain,omitempty"`
}
