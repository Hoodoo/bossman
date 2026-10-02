// Package cli implements the bossman command line.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"bossman/internal/catalog"
	"bossman/internal/version"
)

type app struct {
	home string
	json bool
	out  io.Writer
}

// Execute runs the command line and returns the exit code.
func Execute() int {
	a := &app{out: os.Stdout}
	if err := a.root().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "bossman:", err)
		return 1
	}
	return 0
}

func (a *app) root() *cobra.Command {
	root := &cobra.Command{
		Use:   "bossman",
		Short: "Archive, index, and analyze local coding-agent sessions",
		Long: `bossman keeps copies of your Claude Code and Codex sessions (the agents
delete old ones), indexes them, and reports cost, tokens, turns, tool
errors, time, and human interventions. Name, tag, annotate, and link
sessions from the CLI or the local web UI (bossman serve).

Start with: bossman sync`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version.Version,
	}
	root.PersistentFlags().StringVar(&a.home, "home", "", "bossman data directory (default $BOSSMAN_HOME or ~/.local/share/bossman)")
	root.PersistentFlags().BoolVar(&a.json, "json", false, "print JSON")
	root.AddCommand(
		a.syncCmd(), a.archiveCmd(), a.indexCmd(), a.scanCmd(),
		a.lsCmd(), a.showCmd(), a.statsCmd(),
		a.nameCmd(), a.noteCmd(), a.tagCmd(), a.linkCmd(), a.metaCmd(),
		a.serveCmd(), a.pricesCmd(), a.pathsCmd(),
	)
	return root
}

func (a *app) open() (*catalog.Catalog, error) { return catalog.Open(a.home) }

// withCatalog wraps a command body that needs an open catalog.
func (a *app) withCatalog(fn func(c *catalog.Catalog, args []string) error) func(*cobra.Command, []string) error {
	return func(_ *cobra.Command, args []string) error {
		c, err := a.open()
		if err != nil {
			return err
		}
		defer c.Close()
		return fn(c, args)
	}
}

func (a *app) printJSON(v any) error {
	enc := json.NewEncoder(a.out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// parseWhen accepts a date (2026-09-01), an RFC 3339 time, or a duration
// back from now such as 36h, 7d, or 2w.
func parseWhen(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if n := len(s); n > 1 && (s[n-1] == 'd' || s[n-1] == 'w') {
		if v, err := strconv.Atoi(s[:n-1]); err == nil {
			days := v
			if s[n-1] == 'w' {
				days *= 7
			}
			return time.Now().AddDate(0, 0, -days), nil
		}
	}
	if d, err := time.ParseDuration(s); err == nil {
		return time.Now().Add(-d), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, errors.New("invalid time " + strconv.Quote(s) + ": use 2026-09-01, 7d, 2w, or 36h")
}

func money(v float64) string {
	switch {
	case v == 0:
		return "$0"
	case v < 0.01:
		return "<$0.01"
	case v < 100:
		return fmt.Sprintf("$%.2f", v)
	default:
		return fmt.Sprintf("$%.0f", v)
	}
}

func count(n int64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1e4:
		return fmt.Sprintf("%.0fk", float64(n)/1e3)
	case n >= 1e3:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return strconv.FormatInt(n, 10)
}

func dur(sec float64) string {
	d := time.Duration(sec * float64(time.Second))
	switch {
	case d <= 0:
		return "-"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%02dh", int(d.Hours())/24, int(d.Hours())%24)
}

func pct(part, whole int64) string {
	if whole == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(part)/float64(whole))
}

// localTime renders a stored UTC timestamp in local time.
func localTime(s string) string {
	t, err := time.Parse("2006-01-02T15:04:05.000Z", s)
	if err != nil {
		return s
	}
	return t.Local().Format("2006-01-02 15:04")
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// shortPath trims the home directory from a project path.
func shortPath(p string) string {
	if h, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, h) {
		return "~" + p[len(h):]
	}
	return p
}
