package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"bossman/internal/catalog"
	"bossman/internal/store"
)

func (a *app) nameCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "name <session> <display name…>",
		Short: "Give a session a display name (bossman only; agent files are untouched)",
		Long: `name sets the name bossman shows for a session. Pass "-" to clear it and
fall back to the agent's own title.`,
		Args: cobra.MinimumNArgs(2),
	}
	cmd.RunE = a.withCatalog(func(c *catalog.Catalog, args []string) error {
		key, err := c.Store.Resolve(args[0])
		if err != nil {
			return err
		}
		name := strings.Join(args[1:], " ")
		if name == "-" {
			name = ""
		}
		if err := c.Store.SetDisplayName(key, name); err != nil {
			return err
		}
		if name == "" {
			fmt.Fprintf(a.out, "%s: display name cleared\n", key)
		} else {
			fmt.Fprintf(a.out, "%s: %s\n", key, name)
		}
		return nil
	})
	return cmd
}

func (a *app) noteCmd() *cobra.Command {
	var appendNote bool
	cmd := &cobra.Command{
		Use:   "note <session> [text…]",
		Short: "Set a session's notes (\"-\" reads stdin; no text prints them)",
		Args:  cobra.MinimumNArgs(1),
	}
	cmd.Flags().BoolVarP(&appendNote, "append", "a", false, "append a line instead of replacing")
	cmd.RunE = a.withCatalog(func(c *catalog.Catalog, args []string) error {
		key, err := c.Store.Resolve(args[0])
		if err != nil {
			return err
		}
		d, err := c.Store.Get(key)
		if err != nil {
			return err
		}
		if len(args) == 1 {
			fmt.Fprintln(a.out, d.Notes)
			return nil
		}
		text := strings.Join(args[1:], " ")
		if text == "-" {
			b, err := io.ReadAll(os.Stdin)
			if err != nil {
				return err
			}
			text = strings.TrimRight(string(b), "\n")
		}
		if appendNote && d.Notes != "" {
			text = d.Notes + "\n" + text
		}
		return c.Store.SetNotes(key, text)
	})
	return cmd
}

func (a *app) tagCmd() *cobra.Command {
	var remove []string
	cmd := &cobra.Command{
		Use:   "tag <session> [tag…] [--rm tag]",
		Short: "Add or remove a session's tags; with no tags, list them",
		Args:  cobra.MinimumNArgs(1),
	}
	cmd.Flags().StringSliceVar(&remove, "rm", nil, "remove this tag (repeatable or comma-separated)")
	cmd.RunE = a.withCatalog(func(c *catalog.Catalog, args []string) error {
		key, err := c.Store.Resolve(args[0])
		if err != nil {
			return err
		}
		tags, err := c.Store.Tags(key)
		if err != nil {
			return err
		}
		set := map[string]bool{}
		for _, t := range tags {
			set[t] = true
		}
		for _, arg := range args[1:] {
			t, err := store.NormalizeTag(strings.TrimPrefix(arg, "#"))
			if err != nil {
				return err
			}
			set[t] = true
		}
		for _, arg := range remove {
			t, err := store.NormalizeTag(strings.TrimPrefix(arg, "#"))
			if err != nil {
				return err
			}
			delete(set, t)
		}
		if len(args) > 1 || len(remove) > 0 {
			tags = tags[:0]
			for t := range set {
				tags = append(tags, t)
			}
			if err := c.Store.SetTags(key, tags); err != nil {
				return err
			}
		}
		if tags, err = c.Store.Tags(key); err != nil {
			return err
		}
		if a.json {
			return a.printJSON(tags)
		}
		if len(tags) == 0 {
			fmt.Fprintf(a.out, "%s: no tags\n", key)
		} else {
			fmt.Fprintf(a.out, "%s: #%s\n", key, strings.Join(tags, " #"))
		}
		return nil
	})
	return cmd
}

func (a *app) linkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "link",
		Short: "Attach URLs (PRs, issues, docs) to sessions",
	}
	var label string
	add := &cobra.Command{
		Use:   "add <session> <url>",
		Short: "Attach a URL; re-adding it updates the label",
		Args:  cobra.ExactArgs(2),
	}
	add.Flags().StringVarP(&label, "label", "l", "", "short description")
	add.RunE = a.withCatalog(func(c *catalog.Catalog, args []string) error {
		key, err := c.Store.Resolve(args[0])
		if err != nil {
			return err
		}
		l, err := c.Store.AddLink(key, args[1], label)
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(l)
		}
		fmt.Fprintf(a.out, "%s: [%d] %s\n", key, l.ID, l.URL)
		return nil
	})
	rm := &cobra.Command{
		Use:   "rm <session> <id|url>",
		Short: "Detach a link",
		Args:  cobra.ExactArgs(2),
	}
	rm.RunE = a.withCatalog(func(c *catalog.Catalog, args []string) error {
		key, err := c.Store.Resolve(args[0])
		if err != nil {
			return err
		}
		return c.Store.RemoveLink(key, args[1])
	})
	ls := &cobra.Command{
		Use:   "ls <session>",
		Short: "List a session's links",
		Args:  cobra.ExactArgs(1),
	}
	ls.RunE = a.withCatalog(func(c *catalog.Catalog, args []string) error {
		key, err := c.Store.Resolve(args[0])
		if err != nil {
			return err
		}
		links, err := c.Store.Links(key)
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(links)
		}
		for _, l := range links {
			fmt.Fprintf(a.out, "[%d] %s  %s\n", l.ID, l.URL, l.Label)
		}
		return nil
	})
	cmd.AddCommand(add, rm, ls)
	return cmd
}

func (a *app) metaCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "meta",
		Short: "Export or import your names, notes, tags, and links",
	}
	export := &cobra.Command{
		Use:   "export",
		Short: "Print all user metadata as JSON",
		Args:  cobra.NoArgs,
	}
	export.RunE = a.withCatalog(func(c *catalog.Catalog, _ []string) error {
		ann, err := c.Store.ExportAnnotations()
		if err != nil {
			return err
		}
		return a.printJSON(ann)
	})
	imp := &cobra.Command{
		Use:   "import <file|->",
		Short: "Merge metadata exported by `meta export`",
		Args:  cobra.ExactArgs(1),
	}
	imp.RunE = a.withCatalog(func(c *catalog.Catalog, args []string) error {
		var r io.Reader = os.Stdin
		if args[0] != "-" {
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer f.Close()
			r = f
		}
		var ann []store.Annotation
		if err := json.NewDecoder(r).Decode(&ann); err != nil {
			return fmt.Errorf("read %s: %w", args[0], err)
		}
		if err := c.Store.ImportAnnotations(ann); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "imported metadata for %d sessions\n", len(ann))
		return nil
	})
	cmd.AddCommand(export, imp)
	return cmd
}
