// Package web serves bossman's local UI and its JSON API.
package web

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"bossman/internal/catalog"
	"bossman/internal/store"
)

//go:embed static
var static embed.FS

// Server is an http.Handler for the UI and API.
type Server struct {
	c        *catalog.Catalog
	mux      *http.ServeMux
	allowed  map[string]bool
	syncMu   sync.Mutex
	lastSync time.Time
}

// New builds the server. listenHost is the host part of the listen
// address; requests must name it or a loopback name in their Host header,
// which keeps other web pages from reaching the API by DNS rebinding.
func New(c *catalog.Catalog, listenHost string) *Server {
	s := &Server{c: c, mux: http.NewServeMux(), allowed: map[string]bool{
		"localhost": true, "127.0.0.1": true, "::1": true, listenHost: true,
	}}
	sub, _ := fs.Sub(static, "static")
	s.mux.Handle("GET /", http.FileServer(http.FS(sub)))
	s.mux.HandleFunc("GET /api/sessions", s.listSessions)
	s.mux.HandleFunc("GET /api/sessions/{key}", s.getSession)
	s.mux.HandleFunc("GET /api/sessions/{key}/transcript", s.transcript)
	s.mux.HandleFunc("PUT /api/sessions/{key}/meta", s.putMeta)
	s.mux.HandleFunc("PUT /api/sessions/{key}/tags", s.putTags)
	s.mux.HandleFunc("POST /api/sessions/{key}/links", s.addLink)
	s.mux.HandleFunc("DELETE /api/sessions/{key}/links/{id}", s.removeLink)
	s.mux.HandleFunc("GET /api/stats", s.stats)
	s.mux.HandleFunc("GET /api/facets", s.facets)
	s.mux.HandleFunc("POST /api/sync", s.sync)
	return s
}

// Sync archives and indexes; concurrent calls wait for each other.
func (s *Server) Sync(force bool) (catalog.SyncStats, error) {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	st, err := s.c.Sync(force)
	if err == nil {
		s.lastSync = time.Now()
	}
	return st, err
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if !s.allowed[strings.Trim(host, "[]")] {
		http.Error(w, "forbidden host", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		// Only the UI itself may change data: a JSON body cannot be sent
		// cross-origin without a preflight, and a foreign Origin is refused.
		if o := r.Header.Get("Origin"); o != "" {
			u, err := url.Parse(o)
			if err != nil || u.Host != r.Host {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "want application/json", http.StatusUnsupportedMediaType)
			return
		}
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	s.mux.ServeHTTP(w, r)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	var bad badRequestError
	switch {
	case errors.Is(err, store.ErrNotFound):
		code = http.StatusNotFound
	case errors.As(err, &bad):
		code = http.StatusBadRequest
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
}

type badRequestError string

func (e badRequestError) Error() string { return string(e) }

func badRequest(msg string) error { return badRequestError(msg) }

func filterFrom(q url.Values) (store.Filter, error) {
	f := store.Filter{
		Agent: q.Get("agent"), Project: q.Get("project"), Query: q.Get("q"),
		Tag: q.Get("tag"), Sort: q.Get("sort"), Asc: q.Get("dir") == "asc",
		Archived: q.Get("archived") == "1",
	}
	for name, dst := range map[string]*time.Time{"since": &f.Since, "until": &f.Until} {
		if v := q.Get(name); v != "" {
			t, err := time.ParseInLocation("2006-01-02", v, time.Local)
			if err != nil {
				return f, badRequest(name + " must be YYYY-MM-DD")
			}
			*dst = t
		}
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return f, badRequest("limit must be a number")
		}
		f.Limit = n
	}
	return f, nil
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	f, err := filterFrom(r.URL.Query())
	if err != nil {
		writeErr(w, err)
		return
	}
	rows, err := s.c.Store.List(f)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, rows)
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	d, err := s.c.Store.Get(r.PathValue("key"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, d)
}

func (s *Server) transcript(w http.ResponseWriter, r *http.Request) {
	ev, err := s.c.Transcript(r.PathValue("key"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, ev)
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return badRequest("invalid JSON: " + err.Error())
	}
	return nil
}

// exists makes write endpoints refuse keys that are not indexed.
func (s *Server) exists(key string) error {
	_, err := s.c.Store.Get(key)
	return err
}

func (s *Server) putMeta(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	var body struct {
		DisplayName *string `json:"display_name"`
		Notes       *string `json:"notes"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.exists(key); err != nil {
		writeErr(w, err)
		return
	}
	if body.DisplayName != nil {
		if err := s.c.Store.SetDisplayName(key, *body.DisplayName); err != nil {
			writeErr(w, err)
			return
		}
	}
	if body.Notes != nil {
		if err := s.c.Store.SetNotes(key, *body.Notes); err != nil {
			writeErr(w, err)
			return
		}
	}
	s.getSession(w, r)
}

func (s *Server) putTags(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	var body struct {
		Tags []string `json:"tags"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.exists(key); err != nil {
		writeErr(w, err)
		return
	}
	for _, t := range body.Tags {
		if _, err := store.NormalizeTag(t); err != nil {
			writeErr(w, badRequest(err.Error()))
			return
		}
	}
	if err := s.c.Store.SetTags(key, body.Tags); err != nil {
		writeErr(w, err)
		return
	}
	s.getSession(w, r)
}

func (s *Server) addLink(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	var body struct {
		URL   string `json:"url"`
		Label string `json:"label"`
	}
	if err := decode(r, &body); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.exists(key); err != nil {
		writeErr(w, err)
		return
	}
	if _, err := s.c.Store.AddLink(key, body.URL, body.Label); err != nil {
		writeErr(w, badRequest(err.Error()))
		return
	}
	s.getSession(w, r)
}

func (s *Server) removeLink(w http.ResponseWriter, r *http.Request) {
	if err := s.c.Store.RemoveLink(r.PathValue("key"), r.PathValue("id")); err != nil {
		writeErr(w, fmt.Errorf("%w: %v", store.ErrNotFound, err))
		return
	}
	s.getSession(w, r)
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	f, err := filterFrom(r.URL.Query())
	if err != nil {
		writeErr(w, err)
		return
	}
	by := r.URL.Query().Get("by")
	if by == "" {
		by = "day"
	}
	groups, total, err := s.c.Store.Stats(f, by)
	if err != nil {
		writeErr(w, badRequest(err.Error()))
		return
	}
	writeJSON(w, map[string]any{"by": by, "groups": groups, "total": total})
}

// facets returns the values the UI offers as filters.
func (s *Server) facets(w http.ResponseWriter, r *http.Request) {
	projects, _, err := s.c.Store.Stats(store.Filter{}, "project")
	if err != nil {
		writeErr(w, err)
		return
	}
	tags, err := s.c.Store.AllTags()
	if err != nil {
		writeErr(w, err)
		return
	}
	var names []string
	for _, p := range projects {
		names = append(names, p.Key)
	}
	s.syncMu.Lock()
	last := s.lastSync
	s.syncMu.Unlock()
	var lastStr string
	if !last.IsZero() {
		lastStr = last.UTC().Format(time.RFC3339)
	}
	writeJSON(w, map[string]any{"projects": names, "tags": tags, "last_sync": lastStr})
}

func (s *Server) sync(w http.ResponseWriter, r *http.Request) {
	st, err := s.Sync(false)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, st)
}
