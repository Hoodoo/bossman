// Package catalog ties the pieces together: it mirrors the agents'
// session directories into the archive and indexes the archive.
package catalog

import (
	"fmt"
	"path/filepath"
	"time"

	"bossman/internal/archive"
	"bossman/internal/config"
	"bossman/internal/model"
	"bossman/internal/parse"
	"bossman/internal/pricing"
	"bossman/internal/store"
)

// Catalog is an opened bossman home.
type Catalog struct {
	Cfg   *config.Config
	Store *store.Store
}

// Open loads the configuration and opens the database.
func Open(home string) (*Catalog, error) {
	cfg, err := config.Load(home)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return nil, err
	}
	return &Catalog{Cfg: cfg, Store: st}, nil
}

func (c *Catalog) Close() error { return c.Store.Close() }

// Archive layout: archive/claude mirrors Claude's projects directory and
// archive/codex mirrors the parts of Codex's home that hold sessions, so
// the same discovery code reads sources and archive alike.
func (c *Catalog) claudeArchive() string { return filepath.Join(c.Cfg.ArchiveDir(), "claude") }
func (c *Catalog) codexArchive() string  { return filepath.Join(c.Cfg.ArchiveDir(), "codex") }

func (c *Catalog) mappings() []archive.Mapping {
	m := []archive.Mapping{
		{Name: "claude", Src: c.Cfg.ClaudeDir, Dst: c.claudeArchive()},
		{Name: "codex", Src: filepath.Join(c.Cfg.CodexDir, "sessions"), Dst: filepath.Join(c.codexArchive(), "sessions")},
	}
	for _, f := range parse.CodexMetaFiles {
		m = append(m, archive.Mapping{Name: "codex", Src: filepath.Join(c.Cfg.CodexDir, f), Dst: filepath.Join(c.codexArchive(), f)})
	}
	return m
}

// Archive mirrors the agents' directories into the archive.
func (c *Catalog) Archive() (archive.Stats, error) {
	return archive.Mirror(c.mappings(), time.Now())
}

// SourceSession is a session as seen in the agents' own directories.
type SourceSession struct {
	parse.Candidate
	// Archived is the archive state: "new" (not archived yet), "changed"
	// (archived copy is behind), or "archived".
	Archived string
}

// Scan lists sessions in the agents' directories and compares them with
// the archive, without copying anything.
func (c *Catalog) Scan() ([]SourceSession, error) {
	src, err := c.discover(c.Cfg.ClaudeDir, c.Cfg.CodexDir)
	if err != nil {
		return nil, err
	}
	arch, err := c.discover(c.claudeArchive(), c.codexArchive())
	if err != nil {
		return nil, err
	}
	have := map[string]parse.Candidate{}
	for _, a := range arch {
		have[a.Key()] = a
	}
	out := make([]SourceSession, 0, len(src))
	for _, s := range src {
		ss := SourceSession{Candidate: s, Archived: "archived"}
		a, ok := have[s.Key()]
		switch {
		case !ok:
			ss.Archived = "new"
		case a.Size != s.Size || len(a.Files) != len(s.Files):
			ss.Archived = "changed"
		}
		out = append(out, ss)
	}
	return out, nil
}

// ArchivedOnly counts archived sessions the agents no longer have.
func (c *Catalog) ArchivedOnly() (int, error) {
	live, err := c.liveKeys()
	if err != nil {
		return 0, err
	}
	arch, err := c.discover(c.claudeArchive(), c.codexArchive())
	if err != nil {
		return 0, err
	}
	n := 0
	for _, a := range arch {
		if !live[a.Key()] {
			n++
		}
	}
	return n, nil
}

func (c *Catalog) discover(claudeRoot, codexRoot string) ([]parse.Candidate, error) {
	cl, err := parse.DiscoverClaude(claudeRoot)
	if err != nil {
		return nil, fmt.Errorf("discover claude sessions in %s: %w", claudeRoot, err)
	}
	cx, err := parse.DiscoverCodex(codexRoot)
	if err != nil {
		return nil, fmt.Errorf("discover codex sessions in %s: %w", codexRoot, err)
	}
	return append(cl, cx...), nil
}

func (c *Catalog) liveKeys() (map[string]bool, error) {
	src, err := c.discover(c.Cfg.ClaudeDir, c.Cfg.CodexDir)
	if err != nil {
		return nil, err
	}
	live := map[string]bool{}
	for _, s := range src {
		live[s.Key()] = true
	}
	return live, nil
}

// IndexStats reports what one Index pass did.
type IndexStats struct {
	Sessions  int      `json:"sessions"`
	Indexed   int      `json:"indexed"`
	Unchanged int      `json:"unchanged"`
	Archived  int      `json:"archived_only"`
	Errors    []string `json:"errors,omitempty"`
}

// Index parses archived sessions into the database. Unless force is set
// it skips sessions whose files have not changed since the last pass.
func (c *Catalog) Index(force bool) (IndexStats, error) {
	var st IndexStats
	prices, err := pricing.Load(c.Cfg.PricingPath())
	if err != nil {
		return st, err
	}
	cands, err := c.discover(c.claudeArchive(), c.codexArchive())
	if err != nil {
		return st, err
	}
	sigs, err := c.Store.Signatures()
	if err != nil {
		return st, err
	}
	meta := parse.LoadCodexMeta(c.codexArchive())
	now := time.Now()
	st.Sessions = len(cands)
	for _, cand := range cands {
		sig := store.Indexed{Size: cand.Size, MTime: cand.ModTime.UTC().Format(store.TimeFormat)}
		if old, ok := sigs[cand.Key()]; ok && old == sig && !force {
			st.Unchanged++
			continue
		}
		sess, err := c.parse(cand, meta, false)
		if err != nil {
			st.Errors = append(st.Errors, fmt.Sprintf("%s: %v", cand.Main, err))
			continue
		}
		if err := c.Store.Put(sess, Price(prices, sess), sig, now); err != nil {
			return st, err
		}
		st.Indexed++
	}
	live, err := c.liveKeys()
	if err != nil {
		return st, err
	}
	if err := c.Store.SetInSource(live); err != nil {
		return st, err
	}
	for _, cand := range cands {
		if !live[cand.Key()] {
			st.Archived++
		}
	}
	return st, nil
}

// Price computes a session's cost figures. When the agent recorded its
// own cost, each model's share is scaled so the shares add up to it.
func Price(t *pricing.Table, s *model.Session) store.Priced {
	usd, source, per := t.SessionCost(s)
	var table float64
	for _, v := range per {
		table += v
	}
	p := store.Priced{CostUSD: usd, Source: source, TableCostUSD: table, PerModel: per}
	if source == pricing.SourceAgent && table > 0 {
		scaled := map[string]float64{}
		for m, v := range per {
			scaled[m] = v / table * usd
		}
		p.PerModel = scaled
	}
	return p
}

func (c *Catalog) parse(cand parse.Candidate, meta parse.CodexMeta, events bool) (*model.Session, error) {
	sess, _, err := c.parseEvents(cand, meta, events)
	return sess, err
}

func (c *Catalog) parseEvents(cand parse.Candidate, meta parse.CodexMeta, events bool) (*model.Session, []model.Event, error) {
	var (
		sess *model.Session
		ev   []model.Event
		err  error
	)
	switch cand.Agent {
	case model.AgentClaude:
		sess, ev, err = parse.ParseClaude(cand, c.Cfg.Idle(), events)
	case model.AgentCodex:
		sess, ev, err = parse.ParseCodex(cand, meta, c.Cfg.Idle(), events)
	default:
		return nil, nil, fmt.Errorf("unknown agent %q", cand.Agent)
	}
	if err != nil {
		return nil, nil, err
	}
	base := c.Cfg.ArchiveDir()
	sess.Path, _ = filepath.Rel(base, cand.Main)
	for _, f := range cand.Files {
		rel, _ := filepath.Rel(base, f)
		sess.Files = append(sess.Files, rel)
	}
	return sess, ev, nil
}

// Transcript parses the archived session for display.
func (c *Catalog) Transcript(key string) ([]model.Event, error) {
	d, err := c.Store.Get(key)
	if err != nil {
		return nil, err
	}
	base := c.Cfg.ArchiveDir()
	cand := parse.Candidate{Agent: d.Agent, ID: d.ID, Main: filepath.Join(base, d.Path)}
	for _, f := range d.Files {
		cand.Files = append(cand.Files, filepath.Join(base, f))
	}
	var meta parse.CodexMeta
	if cand.Agent == model.AgentCodex {
		meta = parse.LoadCodexMeta(c.codexArchive())
	}
	_, ev, err := c.parseEvents(cand, meta, true)
	if ev == nil {
		ev = []model.Event{}
	}
	return ev, err
}

// SyncStats combines an archive and an index pass.
type SyncStats struct {
	Archive archive.Stats `json:"archive"`
	Index   IndexStats    `json:"index"`
}

// Sync archives, then indexes.
func (c *Catalog) Sync(force bool) (SyncStats, error) {
	var st SyncStats
	var err error
	if st.Archive, err = c.Archive(); err != nil {
		return st, err
	}
	st.Index, err = c.Index(force)
	return st, err
}
