// Package parse discovers agent session files and turns them into
// model.Session records and transcripts.
package parse

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Candidate is one session found on disk, before parsing.
type Candidate struct {
	Agent string
	ID    string
	// Main is the session's primary log file (absolute).
	Main string
	// Files lists every file of the session (absolute), Main first.
	Files []string
	// Size and ModTime summarise Files so callers can skip unchanged
	// sessions without parsing them.
	Size    int64
	ModTime time.Time
}

func (c *Candidate) Key() string { return c.Agent + ":" + c.ID }

func (c *Candidate) stat() {
	c.Size, c.ModTime = 0, time.Time{}
	for _, f := range c.Files {
		fi, err := os.Stat(f)
		if err != nil {
			continue
		}
		c.Size += fi.Size()
		if fi.ModTime().After(c.ModTime) {
			c.ModTime = fi.ModTime()
		}
	}
}

// eachLine calls fn with every non-empty line of path. Lines may be very
// long (tool output), so it does not use bufio.Scanner's fixed limit. A
// truncated final line, as left by an agent mid-write, is passed through
// and simply fails to decode in fn.
func eachLine(path string, fn func(line []byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			fn(line)
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// clock tracks the time span of a session and its active time.
type clock struct {
	times []time.Time
	// turns are intervals the agent reported working, which count as
	// active even when no event was logged for longer than the idle cap
	// (a long build, a slow test run).
	turns []interval
}

type interval struct{ start, end time.Time }

func (c *clock) add(t time.Time) {
	if !t.IsZero() {
		c.times = append(c.times, t)
	}
}

// turn records an agent turn that ended at end and lasted d.
func (c *clock) turn(end time.Time, d time.Duration) {
	if !end.IsZero() && d > 0 {
		c.turns = append(c.turns, interval{end.Add(-d), end})
	}
}

// span returns first, last, and the active seconds: the union of agent
// turns and of inter-event gaps no longer than idle.
func (c *clock) span(idle time.Duration) (first, last time.Time, active float64) {
	if len(c.times) == 0 {
		return
	}
	sort.Slice(c.times, func(i, j int) bool { return c.times[i].Before(c.times[j]) })
	first, last = c.times[0], c.times[len(c.times)-1]
	ivs := append([]interval(nil), c.turns...)
	for i := 1; i < len(c.times); i++ {
		if gap := c.times[i].Sub(c.times[i-1]); gap > 0 && gap <= idle {
			ivs = append(ivs, interval{c.times[i-1], c.times[i]})
		}
	}
	sort.Slice(ivs, func(i, j int) bool { return ivs[i].start.Before(ivs[j].start) })
	var cur interval
	for i, iv := range ivs {
		if i == 0 || iv.start.After(cur.end) {
			if i > 0 {
				active += cur.end.Sub(cur.start).Seconds()
			}
			cur = iv
			continue
		}
		if iv.end.After(cur.end) {
			cur.end = iv.end
		}
	}
	if len(ivs) > 0 {
		active += cur.end.Sub(cur.start).Seconds()
	}
	return
}

// CatalogueMarker is emitted by the session-catalogue-close skill when a
// session is concluded on purpose. Only the HTML-comment form counts, so
// merely mentioning the marker does not conclude a session.
const CatalogueMarker = "<!-- cc-catalogue:session-concluded"

// catalogueSummary extracts the away-summary from a text containing the
// marker. ok reports whether the marker was present at all.
func catalogueSummary(text string) (summary string, ok bool) {
	i := strings.Index(text, CatalogueMarker)
	if i < 0 {
		return "", false
	}
	rest := text[i+len(CatalogueMarker):]
	if j := strings.Index(rest, "-->"); j >= 0 {
		rest = rest[:j]
	}
	k := strings.Index(rest, "away-summary:")
	if k < 0 {
		return "", true
	}
	rest = rest[k+len("away-summary:"):]
	rest = strings.TrimPrefix(strings.TrimLeft(rest, " "), "|")
	var lines []string
	for _, l := range strings.Split(rest, "\n") {
		lines = append(lines, strings.TrimSpace(l))
	}
	return strings.TrimSpace(strings.Join(lines, " ")), true
}

// Truncate shortens s to at most n runes, marking the cut.
func Truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// oneLine collapses whitespace so a prompt reads well in a table.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
