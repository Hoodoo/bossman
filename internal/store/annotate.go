package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

func now() string { return time.Now().UTC().Format(TimeFormat) }

// SetDisplayName sets (or, with "", clears) a session's display name.
func (s *Store) SetDisplayName(key, name string) error {
	_, err := s.db.Exec(`INSERT INTO annotations (key, display_name, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET display_name = excluded.display_name, updated_at = excluded.updated_at`,
		key, strings.TrimSpace(name), now())
	return err
}

// SetNotes sets (or, with "", clears) a session's free-form notes.
func (s *Store) SetNotes(key, notes string) error {
	_, err := s.db.Exec(`INSERT INTO annotations (key, notes, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET notes = excluded.notes, updated_at = excluded.updated_at`,
		key, notes, now())
	return err
}

// SetProjectOverride changes the project used to attribute a session. A nil
// project clears the override and returns to the project detected by the agent.
func (s *Store) SetProjectOverride(key string, project *string) error {
	var value any
	if project != nil {
		value = strings.TrimSpace(*project)
	}
	_, err := s.db.Exec(`INSERT INTO annotations (key, project_override, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET project_override = excluded.project_override, updated_at = excluded.updated_at`,
		key, value, now())
	return err
}

// AddLink attaches a URL to a session. Re-adding a URL updates its label.
func (s *Store) AddLink(key, rawURL, label string) (Link, error) {
	rawURL = strings.TrimSpace(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" {
		return Link{}, fmt.Errorf("invalid link %q: want an absolute URL such as https://…", rawURL)
	}
	switch strings.ToLower(u.Scheme) {
	case "javascript", "data", "vbscript":
		return Link{}, fmt.Errorf("invalid link %q: %s: URLs are not allowed", rawURL, u.Scheme)
	}
	var id int64
	err = s.db.QueryRow(`SELECT id FROM links WHERE key = ? AND url = ?`, key, rawURL).Scan(&id)
	if err == nil {
		_, err = s.db.Exec(`UPDATE links SET label = ? WHERE id = ?`, label, id)
		return Link{ID: id, URL: rawURL, Label: label}, err
	}
	at := now()
	res, err := s.db.Exec(`INSERT INTO links (key, url, label, created_at) VALUES (?, ?, ?, ?)`, key, rawURL, label, at)
	if err != nil {
		return Link{}, err
	}
	id, _ = res.LastInsertId()
	return Link{ID: id, URL: rawURL, Label: label, CreatedAt: at}, nil
}

// RemoveLink detaches a link by id or by URL.
func (s *Store) RemoveLink(key, idOrURL string) error {
	res, err := s.db.Exec(`DELETE FROM links WHERE key = ? AND (CAST(id AS TEXT) = ? OR url = ?)`, key, idOrURL, idOrURL)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no link %q on %s", idOrURL, key)
	}
	return nil
}

// Links lists a session's links, oldest first.
func (s *Store) Links(key string) ([]Link, error) {
	rows, err := s.db.Query(`SELECT id, url, label, created_at FROM links WHERE key = ? ORDER BY id`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Link{}
	for rows.Next() {
		var l Link
		if err := rows.Scan(&l.ID, &l.URL, &l.Label, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// NormalizeTag lowercases a tag and rejects ones that would not survive a
// comma-separated listing.
func NormalizeTag(t string) (string, error) {
	t = strings.ToLower(strings.TrimSpace(t))
	if t == "" || strings.ContainsAny(t, ", \t\n") {
		return "", fmt.Errorf("invalid tag %q: tags are single words without commas", t)
	}
	return t, nil
}

// SetTags replaces a session's tags.
func (s *Store) SetTags(key string, tags []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM tags WHERE key = ?`, key); err != nil {
		return err
	}
	for _, t := range tags {
		n, err := NormalizeTag(t)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO tags (key, tag) VALUES (?, ?)`, key, n); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Tags returns a session's tags, sorted.
func (s *Store) Tags(key string) ([]string, error) {
	rows, err := s.db.Query(`SELECT tag FROM tags WHERE key = ? ORDER BY tag`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// AllTags lists every tag in use with its session count.
func (s *Store) AllTags() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT tag, count(*) FROM tags GROUP BY tag`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var t string
		var n int
		if err := rows.Scan(&t, &n); err != nil {
			return nil, err
		}
		out[t] = n
	}
	return out, rows.Err()
}

// Annotation is the user-written data for one session, as exported.
type Annotation struct {
	Key         string   `json:"key"`
	DisplayName string   `json:"display_name,omitempty"`
	Notes       string   `json:"notes,omitempty"`
	Project     *string  `json:"project,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Links       []Link   `json:"links,omitempty"`
}

// ExportAnnotations returns all user-written data, keyed by session.
func (s *Store) ExportAnnotations() ([]Annotation, error) {
	byKey := map[string]*Annotation{}
	get := func(k string) *Annotation {
		if a := byKey[k]; a != nil {
			return a
		}
		a := &Annotation{Key: k}
		byKey[k] = a
		return a
	}
	rows, err := s.db.Query(`SELECT key, display_name, notes, project_override FROM annotations`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k, n, notes string
		var project sql.NullString
		if err := rows.Scan(&k, &n, &notes, &project); err != nil {
			rows.Close()
			return nil, err
		}
		if n != "" || notes != "" || project.Valid {
			a := get(k)
			a.DisplayName, a.Notes = n, notes
			if project.Valid {
				a.Project = &project.String
			}
		}
	}
	rows.Close()
	rows, err = s.db.Query(`SELECT key, tag FROM tags ORDER BY tag`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k, t string
		if err := rows.Scan(&k, &t); err != nil {
			rows.Close()
			return nil, err
		}
		a := get(k)
		a.Tags = append(a.Tags, t)
	}
	rows.Close()
	rows, err = s.db.Query(`SELECT key, id, url, label, created_at FROM links ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k string
		var l Link
		if err := rows.Scan(&k, &l.ID, &l.URL, &l.Label, &l.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		a := get(k)
		a.Links = append(a.Links, l)
	}
	rows.Close()
	out := make([]Annotation, 0, len(byKey))
	for _, a := range byKey {
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// ImportAnnotations merges exported data: names and notes are replaced
// when present, tags and links are added.
func (s *Store) ImportAnnotations(in []Annotation) error {
	for _, a := range in {
		if a.Key == "" {
			continue
		}
		if a.DisplayName != "" {
			if err := s.SetDisplayName(a.Key, a.DisplayName); err != nil {
				return err
			}
		}
		if a.Notes != "" {
			if err := s.SetNotes(a.Key, a.Notes); err != nil {
				return err
			}
		}
		if a.Project != nil {
			if err := s.SetProjectOverride(a.Key, a.Project); err != nil {
				return err
			}
		}
		if len(a.Tags) > 0 {
			have, err := s.Tags(a.Key)
			if err != nil {
				return err
			}
			if err := s.SetTags(a.Key, append(have, a.Tags...)); err != nil {
				return err
			}
		}
		for _, l := range a.Links {
			if _, err := s.AddLink(a.Key, l.URL, l.Label); err != nil {
				return err
			}
		}
	}
	return nil
}
