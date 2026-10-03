package cli

import (
	"fmt"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Hoodoo/bossman/internal/archive"
	"github.com/Hoodoo/bossman/internal/catalog"
)

func (a *app) syncCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Archive new and grown sessions, then index the archive",
		Long: `sync copies the agents' session files into the archive and indexes them.
Run it regularly (cron or a systemd timer): Claude Code deletes sessions
older than cleanupPeriodDays (30 by default).`,
		Args: cobra.NoArgs,
	}
	cmd.Flags().BoolVar(&force, "force", false, "reparse every session, not just changed ones")
	cmd.RunE = a.withCatalog(func(c *catalog.Catalog, _ []string) error {
		st, err := c.Sync(force)
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(st)
		}
		a.archiveReport(st.Archive)
		a.indexReport(st.Index)
		return nil
	})
	return cmd
}

func (a *app) archiveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "archive",
		Short: "Copy new and grown session files into the archive (no indexing)",
		Args:  cobra.NoArgs,
	}
	cmd.RunE = a.withCatalog(func(c *catalog.Catalog, _ []string) error {
		st, err := c.Archive()
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(st)
		}
		a.archiveReport(st)
		return nil
	})
	return cmd
}

func (a *app) indexCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Index the archive into the database (no copying)",
		Long: `index parses archived sessions into the database. The index is derived
data: --force rebuilds it, and your names, notes, tags, and links are kept.`,
		Args: cobra.NoArgs,
	}
	cmd.Flags().BoolVar(&force, "force", false, "reparse every session, not just changed ones")
	cmd.RunE = a.withCatalog(func(c *catalog.Catalog, _ []string) error {
		st, err := c.Index(force)
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(st)
		}
		a.indexReport(st)
		return nil
	})
	return cmd
}

func (a *app) archiveReport(st archive.Stats) {
	fmt.Fprintf(a.out, "archive: %d new, %d grown, %d versioned, %d unchanged files (%s copied)\n",
		st.Copied, st.Grown, st.Versioned, st.Unchanged, count(st.Bytes)+"B")
	for _, e := range st.Errors {
		fmt.Fprintln(a.out, "  error:", e)
	}
}

func (a *app) indexReport(st catalog.IndexStats) {
	fmt.Fprintf(a.out, "index: %d sessions (%d reindexed, %d unchanged, %d kept only in the archive)\n",
		st.Sessions, st.Indexed, st.Unchanged, st.Archived)
	for _, e := range st.Errors {
		fmt.Fprintln(a.out, "  error:", e)
	}
}

func (a *app) scanCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "List sessions in the agents' directories and their archive state",
		Long: `scan reads the agents' own session directories (read-only) and compares
them with the archive: "new" sessions are not archived yet, "changed" ones
have grown since. Sessions kept only in the archive are counted at the end.`,
		Args: cobra.NoArgs,
	}
	cmd.Flags().BoolVar(&all, "all", false, "also list sessions that are already archived")
	cmd.RunE = a.withCatalog(func(c *catalog.Catalog, _ []string) error {
		ss, err := c.Scan()
		if err != nil {
			return err
		}
		only, err := c.ArchivedOnly()
		if err != nil {
			return err
		}
		sort.Slice(ss, func(i, j int) bool { return ss[i].ModTime.After(ss[j].ModTime) })
		if a.json {
			type item struct {
				Key      string   `json:"key"`
				Main     string   `json:"main"`
				Files    []string `json:"files"`
				Size     int64    `json:"size"`
				Modified string   `json:"modified"`
				State    string   `json:"state"`
			}
			out := struct {
				Sessions     []item `json:"sessions"`
				ArchivedOnly int    `json:"archived_only"`
			}{Sessions: []item{}, ArchivedOnly: only}
			for _, s := range ss {
				if all || s.Archived != "archived" {
					out.Sessions = append(out.Sessions, item{s.Key(), s.Main, s.Files, s.Size, s.ModTime.Format("2006-01-02T15:04:05Z07:00"), s.Archived})
				}
			}
			return a.printJSON(out)
		}
		states := map[string]int{}
		tw := tabwriter.NewWriter(a.out, 0, 2, 2, ' ', 0)
		listed := false
		for _, s := range ss {
			states[s.Archived]++
			if !all && s.Archived == "archived" {
				continue
			}
			if !listed {
				fmt.Fprintln(tw, "STATE\tMODIFIED\tSIZE\tFILES\tSESSION")
				listed = true
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", s.Archived, s.ModTime.Local().Format("2006-01-02 15:04"), count(s.Size)+"B", len(s.Files), s.Key())
		}
		tw.Flush()
		fmt.Fprintf(a.out, "%d sessions in agent directories: %d new, %d changed, %d archived; %d kept only in the archive\n",
			len(ss), states["new"], states["changed"], states["archived"], only)
		if states["new"]+states["changed"] > 0 {
			fmt.Fprintln(a.out, "run `bossman sync` to archive and index them")
		}
		return nil
	})
	return cmd
}

func (a *app) pathsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "paths",
		Short: "Show where bossman reads and writes",
		Args:  cobra.NoArgs,
	}
	cmd.RunE = a.withCatalog(func(c *catalog.Catalog, _ []string) error {
		p := map[string]string{
			"home": c.Cfg.Home, "database": c.Cfg.DBPath(), "archive": c.Cfg.ArchiveDir(),
			"config": c.Cfg.ConfigPath(), "pricing": c.Cfg.PricingPath(),
			"claude_source": c.Cfg.ClaudeDir, "codex_source": c.Cfg.CodexDir,
		}
		if a.json {
			return a.printJSON(p)
		}
		tw := tabwriter.NewWriter(a.out, 0, 2, 2, ' ', 0)
		for _, k := range []string{"home", "database", "archive", "config", "pricing", "claude_source", "codex_source"} {
			fmt.Fprintf(tw, "%s\t%s\n", k, p[k])
		}
		return tw.Flush()
	})
	return cmd
}
