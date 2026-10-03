package cli

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Hoodoo/bossman/internal/catalog"
	"github.com/Hoodoo/bossman/internal/pricing"
	"github.com/Hoodoo/bossman/internal/web"
)

func (a *app) serveCmd() *cobra.Command {
	var addr string
	var open bool
	var every time.Duration
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the local web UI",
		Long: `serve starts the web UI on a loopback address. It syncs once at start-up
(unless --no-sync) and, with --sync-every, keeps archiving while it runs.`,
		Args: cobra.NoArgs,
	}
	var noSync bool
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:7788", "listen address (keep it on loopback)")
	cmd.Flags().BoolVar(&open, "open", false, "open the UI in a browser")
	cmd.Flags().DurationVar(&every, "sync-every", 0, "also sync periodically, e.g. 15m (0 disables)")
	cmd.Flags().BoolVar(&noSync, "no-sync", false, "do not sync at start-up")
	cmd.RunE = a.withCatalog(func(c *catalog.Catalog, _ []string) error {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return fmt.Errorf("--addr %q: %w", addr, err)
		}
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			fmt.Fprintf(os.Stderr, "warning: %s is not a loopback address; anyone who can reach it can read your sessions\n", host)
		}
		srv := web.New(c, host)
		if !noSync {
			if st, err := srv.Sync(false); err != nil {
				fmt.Fprintln(os.Stderr, "sync:", err)
			} else {
				a.archiveReport(st.Archive)
				a.indexReport(st.Index)
			}
		}
		if every > 0 {
			go func() {
				for range time.Tick(every) {
					if _, err := srv.Sync(false); err != nil {
						fmt.Fprintln(os.Stderr, "sync:", err)
					}
				}
			}()
		}
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}
		url := "http://" + ln.Addr().String() + "/"
		fmt.Fprintf(a.out, "bossman UI at %s (Ctrl-C to stop)\n", url)
		if open {
			openBrowser(url)
		}
		hs := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second}
		if err := hs.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})
	return cmd
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

func (a *app) pricesCmd() *cobra.Command {
	var initFile bool
	cmd := &cobra.Command{
		Use:   "prices",
		Short: "Show the pricing table used when an agent records no cost",
		Long: `prices shows the USD-per-million-token rates bossman uses. Claude Code
records its own cost in most sessions and bossman prefers that figure.
Codex records none, so add its models to the table to price its sessions.

--init copies the built-in table to your bossman home for editing; run
` + "`bossman index --force`" + ` afterwards to reprice indexed sessions.`,
		Args: cobra.NoArgs,
	}
	cmd.Flags().BoolVar(&initFile, "init", false, "write the built-in table to the pricing file for editing")
	cmd.RunE = a.withCatalog(func(c *catalog.Catalog, _ []string) error {
		path := c.Cfg.PricingPath()
		if initFile {
			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf("%s already exists; edit it directly", path)
			}
			if err := os.WriteFile(path, []byte(pricing.Default), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(a.out, "wrote %s\n", path)
			return nil
		}
		t, err := pricing.Load(path)
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(t)
		}
		src := "built-in table"
		if _, err := os.Stat(path); err == nil {
			src = path
		}
		fmt.Fprintf(a.out, "pricing from %s (USD per million tokens)\n", src)
		names := make([]string, 0, len(t.Models))
		for m := range t.Models {
			names = append(names, m)
		}
		sort.Strings(names)
		tw := tabwriter.NewWriter(a.out, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "MODEL PREFIX\tINPUT\tOUTPUT\tCACHE READ\tWRITE 5M\tWRITE 1H\t")
		for _, m := range names {
			r := t.Models[m]
			w5, w1 := r.Input*1.25, r.Input*2
			if r.CacheWrite5m != nil {
				w5 = *r.CacheWrite5m
			}
			if r.CacheWrite1h != nil {
				w1 = *r.CacheWrite1h
			}
			fmt.Fprintf(tw, "%s\t%.2f\t%.2f\t%.2f\t%.2f\t%.2f\t\n", m, r.Input, r.Output, r.CacheRead, w5, w1)
		}
		return tw.Flush()
	})
	return cmd
}
