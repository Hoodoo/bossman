package cli

import (
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"bossman/internal/catalog"
	"bossman/internal/model"
	"bossman/internal/parse"
	"bossman/internal/store"
)

type filterFlags struct {
	agent, project, query, tag, since, until string
	archived                                 bool
}

func (f *filterFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.agent, "agent", "", "only this agent (claude, codex)")
	cmd.Flags().StringVarP(&f.project, "project", "p", "", "only projects whose path contains this")
	cmd.Flags().StringVarP(&f.query, "search", "s", "", "match name, title, prompt, summary, notes, or id")
	cmd.Flags().StringVarP(&f.tag, "tag", "t", "", "only sessions with this tag")
	cmd.Flags().StringVar(&f.since, "since", "", "started at or after (2026-09-01, 7d, 2w, 36h)")
	cmd.Flags().StringVar(&f.until, "until", "", "started before")
	cmd.Flags().BoolVar(&f.archived, "archived-only", false, "only sessions the agent itself has deleted")
}

func (f *filterFlags) filter() (store.Filter, error) {
	since, err := parseWhen(f.since)
	if err != nil {
		return store.Filter{}, err
	}
	until, err := parseWhen(f.until)
	if err != nil {
		return store.Filter{}, err
	}
	return store.Filter{Agent: f.agent, Project: f.project, Query: f.query, Tag: f.tag,
		Since: since, Until: until, Archived: f.archived}, nil
}

func (a *app) lsCmd() *cobra.Command {
	var ff filterFlags
	var sortBy string
	var asc bool
	var limit int
	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List indexed sessions",
		Args:    cobra.NoArgs,
	}
	ff.register(cmd)
	cmd.Flags().StringVar(&sortBy, "sort", "started", "sort by: "+strings.Join(sortedKeys(), ", "))
	cmd.Flags().BoolVar(&asc, "asc", false, "ascending order")
	cmd.Flags().IntVarP(&limit, "limit", "n", 30, "at most this many sessions (0 for all)")
	cmd.RunE = a.withCatalog(func(c *catalog.Catalog, _ []string) error {
		f, err := ff.filter()
		if err != nil {
			return err
		}
		f.Sort, f.Asc, f.Limit = sortBy, asc, limit
		rows, err := c.Store.List(f)
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(rows)
		}
		if len(rows) == 0 {
			fmt.Fprintln(a.out, "no sessions (run `bossman sync` to archive and index)")
			return nil
		}
		width := shortIDWidth(rows)
		tw := tabwriter.NewWriter(a.out, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tSTARTED\tAGENT\tPROJECT\tCOST\tTOKENS\tPROMPTS\tERR%\tACTIVE\tNAME")
		for _, r := range rows {
			flags := ""
			if !r.InSource {
				flags += " [archived]"
			}
			if r.Concluded {
				flags += " [concluded]"
			}
			if r.ImportedFrom != "" {
				flags += " [Claude copy]"
			}
			if len(r.Tags) > 0 {
				flags += " #" + strings.Join(r.Tags, " #")
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s%s\n",
				r.ID[:min(width, len(r.ID))], localTime(r.StartedAt), r.Agent, clip(shortPath(r.Project), 28),
				costLabel(r.CostUSD, r.CostSource, r.Input+r.CacheWrite+r.CacheRead+r.Output), count(r.Input+r.CacheWrite+r.CacheRead+r.Output),
				r.Prompts, pct(int64(r.ToolErrors), int64(r.ToolCalls)), dur(r.ActiveS),
				clip(r.Name(), 60), flags)
		}
		return tw.Flush()
	})
	return cmd
}

// shortIDWidth is the shortest id prefix, at least 8 characters, that
// tells the listed sessions apart. Codex ids are time-ordered UUIDs, so
// sessions started together share long prefixes.
func shortIDWidth(rows []store.Row) int {
	for w := 8; w < 36; w++ {
		seen := map[string]bool{}
		dup := false
		for _, r := range rows {
			p := r.ID[:min(w, len(r.ID))]
			if seen[p] {
				dup = true
				break
			}
			seen[p] = true
		}
		if !dup {
			return w
		}
	}
	return 36
}

func sortedKeys() []string {
	k := store.SortKeys()
	sort.Strings(k)
	return k
}

// costLabel marks costs that are incomplete: "?" for usage that could
// not be priced, "-" when there was no usage at all.
func costLabel(v float64, source string, tokens int64) string {
	switch {
	case source == "none" && tokens == 0:
		return "-"
	case source == "none":
		return "?"
	case source == "partial":
		return money(v) + "+"
	}
	return money(v)
}

func (a *app) showCmd() *cobra.Command {
	var transcript bool
	var full bool
	cmd := &cobra.Command{
		Use:   "show <session>",
		Short: "Show one session's metrics, summaries, and metadata",
		Long: `show prints everything bossman knows about a session. <session> is an
id, a unique id prefix (4+ characters), agent:id, or a display name.`,
		Args: cobra.ExactArgs(1),
	}
	cmd.Flags().BoolVar(&transcript, "transcript", false, "also print the transcript")
	cmd.Flags().BoolVar(&full, "full", false, "with --transcript, do not shorten long messages")
	cmd.RunE = a.withCatalog(func(c *catalog.Catalog, args []string) error {
		key, err := c.Store.Resolve(args[0])
		if err != nil {
			return err
		}
		d, err := c.Store.Get(key)
		if err != nil {
			return err
		}
		var events []model.Event
		if transcript {
			if events, err = c.Transcript(key); err != nil {
				return err
			}
		}
		if a.json {
			return a.printJSON(struct {
				*store.Detail
				Transcript []model.Event `json:"transcript,omitempty"`
			}{d, events})
		}
		a.printDetail(d)
		if transcript {
			fmt.Fprintln(a.out, "\nTranscript")
			for _, e := range events {
				a.printEvent(e, full)
			}
		}
		return nil
	})
	return cmd
}

func (a *app) printDetail(d *store.Detail) {
	w := a.out
	fmt.Fprintf(w, "%s\n%s\n\n", d.Name(), d.Key)
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	kv := func(k, v string) {
		if v != "" {
			fmt.Fprintf(tw, "%s\t%s\n", k, v)
		}
	}
	kv("display name", d.DisplayName)
	kv("agent title", d.Title)
	kv("project", shortPath(d.Project))
	kv("branch", d.GitBranch)
	kv("agent", strings.TrimSpace(d.Agent+" "+d.Version+" "+d.Entrypoint))
	kv("models", d.Models)
	kv("started", localTime(d.StartedAt))
	kv("ended", localTime(d.EndedAt))
	kv("time", fmt.Sprintf("%s wall, %s active, %s agent working", dur(d.WallS), dur(d.ActiveS), dur(d.AgentS)))
	kv("cost", fmt.Sprintf("%s (%s)", costLabel(d.CostUSD, d.CostSource, d.Input+d.CacheWrite+d.CacheRead+d.Output), costSourceText(d)))
	kv("tokens", fmt.Sprintf("%s input, %s cache write, %s cache read, %s output (%s reasoning), %d requests",
		count(d.Input), count(d.CacheWrite), count(d.CacheRead), count(d.Output), count(d.Reasoning), d.Requests))
	kv("human", fmt.Sprintf("%d prompts, %d interrupts, %d rejected tool calls → %d interventions",
		d.Prompts, d.Interrupts, d.Rejections, d.Interventions))
	kv("tools", fmt.Sprintf("%d calls, %d errors (%s), %d API errors", d.ToolCalls, d.ToolErrors, pct(int64(d.ToolErrors), int64(d.ToolCalls)), d.APIErrors))
	kv("context", fmt.Sprintf("%d compactions, %d subagents", d.Compactions, d.Subagents))
	state := "in the agent's directory and the archive"
	if !d.InSource {
		state = "only in the archive (the agent deleted it)"
	}
	if d.Concluded {
		state += "; concluded with the catalogue marker"
	}
	kv("state", state)
	if d.ImportedFrom != "" {
		kv("copy of", d.ImportedFrom+" (made by Codex Desktop's external-agent import sync; only work done in Codex counts)")
	}
	kv("archived as", d.Path)
	if len(d.Tags) > 0 {
		kv("tags", "#"+strings.Join(d.Tags, " #"))
	}
	tw.Flush()
	if len(d.LinkList) > 0 {
		fmt.Fprintln(w, "\nLinks")
		for _, l := range d.LinkList {
			label := ""
			if l.Label != "" {
				label = "  " + l.Label
			}
			fmt.Fprintf(w, "  [%d] %s%s\n", l.ID, l.URL, label)
		}
	}
	if d.Notes != "" {
		fmt.Fprintf(w, "\nNotes\n  %s\n", strings.ReplaceAll(strings.TrimSpace(d.Notes), "\n", "\n  "))
	}
	if d.FirstPrompt != "" {
		fmt.Fprintf(w, "\nFirst prompt\n  %s\n", clip(d.FirstPrompt, 400))
	}
	if len(d.Summaries) > 0 {
		fmt.Fprintln(w, "\nSummaries (written by the agent)")
		for _, s := range d.Summaries {
			if s.Kind == "title" {
				continue
			}
			at := ""
			if !s.At.IsZero() {
				at = " " + s.At.Local().Format("2006-01-02 15:04")
			}
			fmt.Fprintf(w, "  %s%s: %s\n", s.Kind, at, clip(s.Text, 600))
		}
	}
	if len(d.ModelUsage) > 0 {
		fmt.Fprintln(w, "\nModels")
		tw = tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "  MODEL\tREQUESTS\tINPUT\tCACHE W\tCACHE R\tOUTPUT\tCOST")
		for _, m := range d.ModelUsage {
			c := "?"
			if m.CostUSD != nil {
				c = money(*m.CostUSD)
			}
			fmt.Fprintf(tw, "  %s\t%d\t%s\t%s\t%s\t%s\t%s\n", m.Model, m.Requests, count(m.Input),
				count(m.CacheWrite5m+m.CacheWrite1h), count(m.CacheRead), count(m.Output), c)
		}
		tw.Flush()
	}
	if len(d.ToolUsage) > 0 {
		fmt.Fprintln(w, "\nTools")
		tw = tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		for _, t := range d.ToolUsage {
			e := ""
			if t.Errors > 0 {
				e = fmt.Sprintf("%d errors", t.Errors)
			}
			fmt.Fprintf(tw, "  %s\t%d\t%s\n", t.Tool, t.Calls, e)
		}
		tw.Flush()
	}
}

func costSourceText(d *store.Detail) string {
	switch d.CostSource {
	case "agent":
		return fmt.Sprintf("recorded by the agent; pricing table says %s", money(d.TableCostUSD))
	case "table":
		return "from the pricing table"
	case "partial":
		return "some models are missing from the pricing table"
	}
	if d.Input+d.CacheWrite+d.CacheRead+d.Output == 0 {
		return "no usage recorded"
	}
	return "no model in the pricing table; see `bossman prices`"
}

func (a *app) printEvent(e model.Event, full bool) {
	text := e.Text
	limit := 300
	if e.Role == "user" || e.Role == "assistant" {
		limit = 2000
	}
	if !full {
		text = parse.Truncate(strings.TrimSpace(text), limit)
	}
	at := ""
	if !e.At.IsZero() {
		at = e.At.Local().Format("15:04:05")
	}
	who := e.Role
	if e.Tool != "" {
		who += " " + e.Tool
	}
	if e.Sidechain {
		who = "  ↳ " + who
	}
	if e.IsError {
		who += " ERROR"
	}
	fmt.Fprintf(a.out, "\n[%s] %s\n", at, who)
	if text != "" {
		fmt.Fprintf(a.out, "  %s\n", strings.ReplaceAll(text, "\n", "\n  "))
	}
}

func (a *app) statsCmd() *cobra.Command {
	var ff filterFlags
	var by string
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Aggregate cost, tokens, errors, time, and interventions",
		Long: `stats groups sessions by agent, project, model, tool, day, week, or month.
Model and tool groups count a session once in each group it touches.`,
		Args: cobra.NoArgs,
	}
	ff.register(cmd)
	cmd.Flags().StringVarP(&by, "by", "b", "project", "group by: "+strings.Join(store.GroupBys, ", "))
	cmd.RunE = a.withCatalog(func(c *catalog.Catalog, _ []string) error {
		f, err := ff.filter()
		if err != nil {
			return err
		}
		groups, total, err := c.Store.Stats(f, by)
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(map[string]any{"by": by, "groups": groups, "total": total})
		}
		tw := tabwriter.NewWriter(a.out, 0, 2, 2, ' ', 0)
		switch by {
		case "tool":
			fmt.Fprintln(tw, "TOOL\tSESSIONS\tCALLS\tERRORS\tERR%\t")
			for _, g := range groups {
				fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%s\t\n", g.Key, g.Sessions, g.ToolCalls, g.ToolErrors, pct(g.ToolErrors, g.ToolCalls))
			}
		case "model":
			fmt.Fprintln(tw, "MODEL\tSESSIONS\tREQUESTS\tINPUT\tCACHE W\tCACHE R\tOUTPUT\tCOST\t")
			for _, g := range groups {
				c := money(g.CostUSD)
				if g.Unpriced > 0 {
					c = "?"
				}
				fmt.Fprintf(tw, "%s\t%d\t%d\t%s\t%s\t%s\t%s\t%s\t\n", g.Key, g.Sessions, g.Requests,
					count(g.Input), count(g.CacheWrite), count(g.CacheRead), count(g.Output), c)
			}
		default:
			fmt.Fprintf(tw, "%s\tSESSIONS\tCOST\tTOKENS\tPROMPTS\tINTERV.\tTOOLS\tERR%%\tACTIVE\tWALL\t\n", strings.ToUpper(by))
			for _, g := range append(groups, total) {
				key := g.Key
				if by == "project" {
					key = clip(shortPath(key), 40)
				}
				c := money(g.CostUSD)
				switch {
				case g.Unpriced == g.Sessions:
					c = "-"
				case g.Unpriced > 0:
					c += fmt.Sprintf(" (+%d unpriced)", g.Unpriced)
				}
				fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%d\t%d\t%d\t%s\t%s\t%s\t\n", key, g.Sessions, c,
					count(g.Input+g.CacheWrite+g.CacheRead+g.Output), g.Prompts, g.Interventions,
					g.ToolCalls, pct(g.ToolErrors, g.ToolCalls), dur(g.ActiveS), dur(g.WallS))
			}
		}
		return tw.Flush()
	})
	return cmd
}
