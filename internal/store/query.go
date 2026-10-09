package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Hoodoo/bossman/internal/model"
)

// Row is one session as listed.
type Row struct {
	Key           string   `json:"key"`
	Agent         string   `json:"agent"`
	ID            string   `json:"id"`
	Project       string   `json:"project"`
	GitBranch     string   `json:"git_branch"`
	Title         string   `json:"title"`
	DisplayName   string   `json:"display_name"`
	FirstPrompt   string   `json:"first_prompt"`
	Summary       string   `json:"summary"`
	Concluded     bool     `json:"concluded"`
	InSource      bool     `json:"in_source"`
	ImportedFrom  string   `json:"imported_from"`
	StartedAt     string   `json:"started_at"`
	EndedAt       string   `json:"ended_at"`
	WallS         float64  `json:"wall_s"`
	ActiveS       float64  `json:"active_s"`
	AgentS        float64  `json:"agent_s"`
	Prompts       int      `json:"prompts"`
	Interrupts    int      `json:"interrupts"`
	Rejections    int      `json:"rejections"`
	Interventions int      `json:"interventions"`
	ToolCalls     int      `json:"tool_calls"`
	ToolErrors    int      `json:"tool_errors"`
	APIErrors     int      `json:"api_errors"`
	Compactions   int      `json:"compactions"`
	Subagents     int      `json:"subagents"`
	Input         int64    `json:"input"`
	CacheWrite    int64    `json:"cache_write"`
	CacheRead     int64    `json:"cache_read"`
	Output        int64    `json:"output"`
	Reasoning     int64    `json:"reasoning"`
	Requests      int      `json:"requests"`
	CostUSD       float64  `json:"cost_usd"`
	CostSource    string   `json:"cost_source"`
	TableCostUSD  float64  `json:"table_cost_usd"`
	Models        string   `json:"models"`
	Tags          []string `json:"tags"`
	Links         int      `json:"links"`
}

// Name is what to call a session: the user's name, else the agent's title,
// else the opening prompt.
func (r *Row) Name() string {
	for _, s := range []string{r.DisplayName, r.Title, r.FirstPrompt} {
		if s = strings.TrimSpace(s); s != "" {
			return strings.Join(strings.Fields(s), " ")
		}
	}
	return r.ID
}

const rowColumns = `s.key, s.agent, s.id, COALESCE(a.project_override, s.project), s.git_branch, s.title,
	COALESCE(a.display_name, ''), s.first_prompt, s.summary, s.concluded, s.in_source,
	s.started_at, s.ended_at, s.wall_s, s.active_s, s.agent_s,
	s.prompts, s.interrupts, s.rejections, s.interventions,
	s.tool_calls, s.tool_errors, s.api_errors, s.compactions, s.subagents,
	s.input, s.cache_write, s.cache_read, s.output, s.reasoning, s.requests,
	s.cost_usd, s.cost_source, s.table_cost_usd, s.models, s.imported_from,
	COALESCE((SELECT group_concat(tag, ',') FROM (SELECT tag FROM tags t WHERE t.key = s.key ORDER BY tag)), ''),
	(SELECT count(*) FROM links l WHERE l.key = s.key)`

func scanRow(sc interface{ Scan(...any) error }) (Row, error) {
	var r Row
	var concluded, inSource int
	var tags string
	err := sc.Scan(&r.Key, &r.Agent, &r.ID, &r.Project, &r.GitBranch, &r.Title,
		&r.DisplayName, &r.FirstPrompt, &r.Summary, &concluded, &inSource,
		&r.StartedAt, &r.EndedAt, &r.WallS, &r.ActiveS, &r.AgentS,
		&r.Prompts, &r.Interrupts, &r.Rejections, &r.Interventions,
		&r.ToolCalls, &r.ToolErrors, &r.APIErrors, &r.Compactions, &r.Subagents,
		&r.Input, &r.CacheWrite, &r.CacheRead, &r.Output, &r.Reasoning, &r.Requests,
		&r.CostUSD, &r.CostSource, &r.TableCostUSD, &r.Models, &r.ImportedFrom, &tags, &r.Links)
	r.Concluded, r.InSource = concluded == 1, inSource == 1
	r.Tags = []string{}
	if tags != "" {
		r.Tags = strings.Split(tags, ",")
	}
	return r, err
}

// Filter narrows a listing or a statistic.
type Filter struct {
	Agent   string
	Project string // substring of the project path
	Query   string // substring of name, prompt, summary, notes, or key
	Tag     string
	Since   time.Time
	Until   time.Time
	// Archived limits to sessions the agent itself no longer has.
	Archived bool
	// SkipCopies drops imported sessions with no activity of their own, so
	// aggregates do not count the same work twice.
	SkipCopies bool
	Sort       string // a column name; see sortColumns
	Asc        bool
	Limit      int
}

var sortColumns = map[string]string{
	"started": "s.started_at", "ended": "s.ended_at", "cost": "s.cost_usd",
	"wall": "s.wall_s", "active": "s.active_s", "prompts": "s.prompts",
	"interventions": "s.interventions", "tools": "s.tool_calls",
	"errors": "s.tool_errors", "tokens": "(s.input + s.cache_write + s.cache_read + s.output)",
	"output": "s.output", "project": "COALESCE(a.project_override, s.project)",
	"name": "lower(COALESCE(NULLIF(a.display_name, ''), NULLIF(s.title, ''), s.first_prompt))",
}

// SortKeys lists the accepted Filter.Sort values.
func SortKeys() []string {
	keys := make([]string, 0, len(sortColumns))
	for k := range sortColumns {
		keys = append(keys, k)
	}
	return keys
}

func (f Filter) where() (string, []any) {
	var conds []string
	var args []any
	if f.Agent != "" {
		conds = append(conds, "s.agent = ?")
		args = append(args, f.Agent)
	}
	if f.Project != "" {
		conds = append(conds, "COALESCE(a.project_override, s.project) LIKE ? ESCAPE '\\'")
		args = append(args, "%"+escapeLike(f.Project)+"%")
	}
	if f.Query != "" {
		cols := []string{"s.title", "COALESCE(a.display_name, '')", "s.first_prompt", "s.summary", "COALESCE(a.notes, '')", "s.key"}
		var ors []string
		for _, c := range cols {
			ors = append(ors, c+" LIKE ? ESCAPE '\\'")
			args = append(args, "%"+escapeLike(f.Query)+"%")
		}
		conds = append(conds, "("+strings.Join(ors, " OR ")+")")
	}
	if f.Tag != "" {
		conds = append(conds, "EXISTS (SELECT 1 FROM tags t WHERE t.key = s.key AND t.tag = ?)")
		args = append(args, f.Tag)
	}
	if !f.Since.IsZero() {
		conds = append(conds, "s.started_at >= ?")
		args = append(args, f.Since.UTC().Format(TimeFormat))
	}
	if !f.Until.IsZero() {
		conds = append(conds, "s.started_at < ?")
		args = append(args, f.Until.UTC().Format(TimeFormat))
	}
	if f.Archived {
		conds = append(conds, "s.in_source = 0")
	}
	if f.SkipCopies {
		conds = append(conds, "NOT (s.imported_from != '' AND s.prompts = 0)")
	}
	if len(conds) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// List returns sessions matching f.
func (s *Store) List(f Filter) ([]Row, error) {
	where, args := f.where()
	order := sortColumns[f.Sort]
	if order == "" {
		order = "s.started_at"
	}
	dir := "DESC"
	if f.Asc {
		dir = "ASC"
	}
	q := `SELECT ` + rowColumns + ` FROM sessions s LEFT JOIN annotations a ON a.key = s.key` +
		where + ` ORDER BY ` + order + ` ` + dir + `, s.key`
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", f.Limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Row{}
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ModelRow is one model's usage within a session.
type ModelRow struct {
	Model        string   `json:"model"`
	Input        int64    `json:"input"`
	CacheWrite5m int64    `json:"cache_write_5m"`
	CacheWrite1h int64    `json:"cache_write_1h"`
	CacheRead    int64    `json:"cache_read"`
	Output       int64    `json:"output"`
	Reasoning    int64    `json:"reasoning"`
	Requests     int      `json:"requests"`
	CostUSD      *float64 `json:"cost_usd"`
}

// ToolRow is one tool's use within a session.
type ToolRow struct {
	Tool   string `json:"tool"`
	Calls  int    `json:"calls"`
	Errors int    `json:"errors"`
}

// Link is a user-attached URL.
type Link struct {
	ID        int64  `json:"id"`
	URL       string `json:"url"`
	Label     string `json:"label"`
	CreatedAt string `json:"created_at"`
}

// Detail is everything known about one session.
type Detail struct {
	Row
	DetectedProject   string          `json:"detected_project"`
	ProjectOverridden bool            `json:"project_overridden"`
	Path              string          `json:"path"`
	Files             []string        `json:"files"`
	Version           string          `json:"version"`
	Entrypoint        string          `json:"entrypoint"`
	Notes             string          `json:"notes"`
	LinkList          []Link          `json:"link_list"`
	ModelUsage        []ModelRow      `json:"model_usage"`
	ToolUsage         []ToolRow       `json:"tool_usage"`
	Summaries         []model.Summary `json:"summaries"`
}

// Get loads one session by key.
func (s *Store) Get(key string) (*Detail, error) {
	row := s.db.QueryRow(`SELECT `+rowColumns+`, s.path, s.files, s.version, s.entrypoint, COALESCE(a.notes, ''),
		s.project, a.project_override
		FROM sessions s LEFT JOIN annotations a ON a.key = s.key WHERE s.key = ?`, key)
	var d Detail
	var files string
	var concluded, inSource int
	var tags string
	var projectOverride sql.NullString
	r := &d.Row
	err := row.Scan(&r.Key, &r.Agent, &r.ID, &r.Project, &r.GitBranch, &r.Title,
		&r.DisplayName, &r.FirstPrompt, &r.Summary, &concluded, &inSource,
		&r.StartedAt, &r.EndedAt, &r.WallS, &r.ActiveS, &r.AgentS,
		&r.Prompts, &r.Interrupts, &r.Rejections, &r.Interventions,
		&r.ToolCalls, &r.ToolErrors, &r.APIErrors, &r.Compactions, &r.Subagents,
		&r.Input, &r.CacheWrite, &r.CacheRead, &r.Output, &r.Reasoning, &r.Requests,
		&r.CostUSD, &r.CostSource, &r.TableCostUSD, &r.Models, &r.ImportedFrom, &tags, &r.Links,
		&d.Path, &files, &d.Version, &d.Entrypoint, &d.Notes, &d.DetectedProject, &projectOverride)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	if err != nil {
		return nil, err
	}
	r.Concluded, r.InSource = concluded == 1, inSource == 1
	d.ProjectOverridden = projectOverride.Valid
	r.Tags = []string{}
	if tags != "" {
		r.Tags = strings.Split(tags, ",")
	}
	_ = json.Unmarshal([]byte(files), &d.Files)

	if d.LinkList, err = s.Links(key); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT model, input, cache_write_5m, cache_write_1h, cache_read, output, reasoning, requests, cost_usd
		FROM session_models WHERE key = ? ORDER BY output DESC`, key)
	if err != nil {
		return nil, err
	}
	d.ModelUsage = []ModelRow{}
	for rows.Next() {
		var m ModelRow
		var c sql.NullFloat64
		if err := rows.Scan(&m.Model, &m.Input, &m.CacheWrite5m, &m.CacheWrite1h, &m.CacheRead, &m.Output, &m.Reasoning, &m.Requests, &c); err != nil {
			rows.Close()
			return nil, err
		}
		if c.Valid {
			m.CostUSD = &c.Float64
		}
		d.ModelUsage = append(d.ModelUsage, m)
	}
	rows.Close()
	rows, err = s.db.Query(`SELECT tool, calls, errors FROM session_tools WHERE key = ? ORDER BY calls DESC, tool`, key)
	if err != nil {
		return nil, err
	}
	d.ToolUsage = []ToolRow{}
	for rows.Next() {
		var t ToolRow
		if err := rows.Scan(&t.Tool, &t.Calls, &t.Errors); err != nil {
			rows.Close()
			return nil, err
		}
		d.ToolUsage = append(d.ToolUsage, t)
	}
	rows.Close()
	rows, err = s.db.Query(`SELECT kind, text, at FROM session_summaries WHERE key = ? ORDER BY seq`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	d.Summaries = []model.Summary{}
	for rows.Next() {
		var sm model.Summary
		var at string
		if err := rows.Scan(&sm.Kind, &sm.Text, &at); err != nil {
			return nil, err
		}
		sm.At, _ = time.Parse(TimeFormat, at)
		d.Summaries = append(d.Summaries, sm)
	}
	return &d, rows.Err()
}

// Group is one bucket of a statistic.
type Group struct {
	Key           string  `json:"key"`
	Sessions      int     `json:"sessions"`
	CostUSD       float64 `json:"cost_usd"`
	Input         int64   `json:"input"`
	CacheWrite    int64   `json:"cache_write"`
	CacheRead     int64   `json:"cache_read"`
	Output        int64   `json:"output"`
	Requests      int64   `json:"requests"`
	Prompts       int64   `json:"prompts"`
	Interventions int64   `json:"interventions"`
	ToolCalls     int64   `json:"tool_calls"`
	ToolErrors    int64   `json:"tool_errors"`
	APIErrors     int64   `json:"api_errors"`
	WallS         float64 `json:"wall_s"`
	ActiveS       float64 `json:"active_s"`
	AgentS        float64 `json:"agent_s"`
	// Unpriced counts sessions (or model rows) whose cost is unknown.
	Unpriced int `json:"unpriced"`
}

// GroupBys lists the accepted Stats dimensions.
var GroupBys = []string{"agent", "project", "model", "tool", "day", "week", "month"}

// Stats aggregates sessions matching f by one dimension. Model and tool
// groups count a session in every group it touches.
func (s *Store) Stats(f Filter, by string) ([]Group, Group, error) {
	f.SkipCopies = true
	where, args := f.where()
	from := ` FROM sessions s LEFT JOIN annotations a ON a.key = s.key` + where
	var q string
	sessionCols := `count(*), sum(s.cost_usd), sum(s.input), sum(s.cache_write), sum(s.cache_read),
		sum(s.output), sum(s.requests), sum(s.prompts), sum(s.interventions), sum(s.tool_calls),
		sum(s.tool_errors), sum(s.api_errors), sum(s.wall_s), sum(s.active_s), sum(s.agent_s),
		sum(s.cost_source = 'none' OR s.cost_source = 'partial')`
	switch by {
	case "agent":
		q = `SELECT s.agent, ` + sessionCols + from + ` GROUP BY 1 ORDER BY 2 DESC`
	case "project":
		q = `SELECT COALESCE(a.project_override, s.project), ` + sessionCols + from + ` GROUP BY 1 ORDER BY 2 DESC`
	case "day", "week", "month":
		expr := map[string]string{
			"day":   `date(s.started_at, 'localtime')`,
			"week":  `date(s.started_at, 'localtime', 'weekday 0', '-6 days')`,
			"month": `strftime('%Y-%m', s.started_at, 'localtime')`,
		}[by]
		q = `SELECT ` + expr + `, ` + sessionCols + from + ` GROUP BY 1 ORDER BY 1`
	case "model":
		q = `SELECT m.model, count(DISTINCT s.key), sum(COALESCE(m.cost_usd, 0)), sum(m.input),
			sum(m.cache_write_5m + m.cache_write_1h), sum(m.cache_read), sum(m.output), sum(m.requests),
			0, 0, 0, 0, 0, 0, 0, 0, sum(m.cost_usd IS NULL)
			FROM session_models m JOIN sessions s ON s.key = m.key LEFT JOIN annotations a ON a.key = s.key` +
			where + ` GROUP BY 1 ORDER BY 3 DESC, 7 DESC`
	case "tool":
		q = `SELECT t.tool, count(DISTINCT s.key), 0, 0, 0, 0, 0, 0, 0, 0, sum(t.calls), sum(t.errors),
			0, 0, 0, 0, 0
			FROM session_tools t JOIN sessions s ON s.key = t.key LEFT JOIN annotations a ON a.key = s.key` +
			where + ` GROUP BY 1 ORDER BY 11 DESC`
	default:
		return nil, Group{}, fmt.Errorf("unknown grouping %q (want one of %s)", by, strings.Join(GroupBys, ", "))
	}
	groups, err := s.groups(q, args)
	if err != nil {
		return nil, Group{}, err
	}
	totals, err := s.groups(`SELECT 'total', `+sessionCols+from, args)
	if err != nil {
		return nil, Group{}, err
	}
	var total Group
	if len(totals) == 1 {
		total = totals[0]
	}
	return groups, total, nil
}

func (s *Store) groups(q string, args []any) ([]Group, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Group{}
	for rows.Next() {
		var g Group
		var key sql.NullString
		var cost, wall, active, agent sql.NullFloat64
		var ints [10]sql.NullInt64
		var unpriced sql.NullInt64
		if err := rows.Scan(&key, &g.Sessions, &cost, &ints[0], &ints[1], &ints[2], &ints[3], &ints[4],
			&ints[5], &ints[6], &ints[7], &ints[8], &ints[9], &wall, &active, &agent, &unpriced); err != nil {
			return nil, err
		}
		g.Key = key.String
		g.CostUSD, g.WallS, g.ActiveS, g.AgentS = cost.Float64, wall.Float64, active.Float64, agent.Float64
		g.Input, g.CacheWrite, g.CacheRead, g.Output, g.Requests = ints[0].Int64, ints[1].Int64, ints[2].Int64, ints[3].Int64, ints[4].Int64
		g.Prompts, g.Interventions, g.ToolCalls, g.ToolErrors, g.APIErrors = ints[5].Int64, ints[6].Int64, ints[7].Int64, ints[8].Int64, ints[9].Int64
		g.Unpriced = int(unpriced.Int64)
		out = append(out, g)
	}
	return out, rows.Err()
}
