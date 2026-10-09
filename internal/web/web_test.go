package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hoodoo/bossman/internal/catalog"
)

func server(t *testing.T) *Server {
	t.Helper()
	return serverWith(t, Options{})
}

func serverWith(t *testing.T, opts Options) *Server {
	t.Helper()
	dir := t.TempDir()
	claude := filepath.Join(dir, "claude")
	t.Setenv("BOSSMAN_CLAUDE_DIR", claude)
	t.Setenv("BOSSMAN_CODEX_DIR", filepath.Join(dir, "codex"))
	p := filepath.Join(claude, "-w", "s1.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	log := `{"type":"user","timestamp":"2026-10-01T10:00:00.000Z","message":{"role":"user","content":"hi"}}` + "\n" +
		`{"type":"assistant","timestamp":"2026-10-01T10:00:01.000Z","message":{"id":"m1","model":"claude","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}],"usage":{"input_tokens":1,"output_tokens":1}}}` + "\n" +
		`{"type":"user","timestamp":"2026-10-01T10:00:02.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}` + "\n"
	if err := os.WriteFile(p, []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Open(filepath.Join(dir, "home"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	s := New(c, "127.0.0.1", opts)
	if _, err := s.Sync(false); err != nil {
		t.Fatal(err)
	}
	return s
}

func do(s *Server, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://127.0.0.1:7788"+path, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w
}

var jsonHdr = map[string]string{"Content-Type": "application/json"}

func TestAPI(t *testing.T) {
	s := server(t)
	if w := do(s, "GET", "/", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "bossman") {
		t.Errorf("index: %d", w.Code)
	}
	w := do(s, "GET", "/api/sessions", "", nil)
	var rows []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("sessions: %d %s", w.Code, w.Body)
	}
	w = do(s, "GET", "/api/sessions/claude:s1", "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"command_usage":[{"tool":"Bash","command":"go test ./...","activity":"testing","error":false}]`) {
		t.Errorf("command detail: %d %s", w.Code, w.Body)
	}
	w = do(s, "PUT", "/api/sessions/claude:s1/meta", `{"display_name":"Named","notes":"n"}`, jsonHdr)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"display_name":"Named"`) {
		t.Errorf("meta: %d %s", w.Code, w.Body)
	}
	w = do(s, "PUT", "/api/sessions/claude:s1/project", `{"project":"/work/other"}`, jsonHdr)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"project":"/work/other"`) ||
		!strings.Contains(w.Body.String(), `"project_overridden":true`) {
		t.Errorf("project: %d %s", w.Code, w.Body)
	}
	if w = do(s, "GET", "/api/sessions?project=other", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"project":"/work/other"`) {
		t.Errorf("project filter: %d %s", w.Code, w.Body)
	}
	w = do(s, "PUT", "/api/sessions/claude:s1/project", `{"reset":true}`, jsonHdr)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"project":""`) ||
		!strings.Contains(w.Body.String(), `"project_overridden":false`) {
		t.Errorf("project reset: %d %s", w.Code, w.Body)
	}
	if w = do(s, "PUT", "/api/sessions/claude:s1/project", `{}`, jsonHdr); w.Code != 400 {
		t.Errorf("missing project: %d %s", w.Code, w.Body)
	}
	w = do(s, "PUT", "/api/sessions/claude:s1/tags", `{"tags":["a","b"]}`, jsonHdr)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"tags":["a","b"]`) {
		t.Errorf("tags: %d %s", w.Code, w.Body)
	}
	if w = do(s, "PUT", "/api/sessions/claude:s1/tags", `{"tags":["a b"]}`, jsonHdr); w.Code != 400 {
		t.Errorf("bad tag: %d", w.Code)
	}
	if w = do(s, "POST", "/api/sessions/claude:s1/links", `{"url":"https://x.test/1"}`, jsonHdr); w.Code != 200 {
		t.Errorf("link: %d %s", w.Code, w.Body)
	}
	if w = do(s, "POST", "/api/sessions/claude:s1/links", `{"url":"javascript:alert(1)"}`, jsonHdr); w.Code != 400 {
		t.Errorf("javascript link: %d", w.Code)
	}
	if w = do(s, "DELETE", "/api/sessions/claude:s1/links/1", "", jsonHdr); w.Code != 200 || strings.Contains(w.Body.String(), "x.test") {
		t.Errorf("unlink: %d %s", w.Code, w.Body)
	}
	if w = do(s, "PUT", "/api/sessions/claude:nope/meta", `{}`, jsonHdr); w.Code != 404 {
		t.Errorf("unknown session: %d", w.Code)
	}
	if w = do(s, "GET", "/api/sessions/claude:s1/transcript", "", nil); w.Code != 200 {
		t.Errorf("transcript: %d", w.Code)
	}
	for _, by := range []string{"day", "project", "model", "tool"} {
		if w = do(s, "GET", "/api/stats?by="+by, "", nil); w.Code != 200 {
			t.Errorf("stats by %s: %d", by, w.Code)
		}
	}
	if w = do(s, "GET", "/api/stats?by=nope", "", nil); w.Code != 400 {
		t.Errorf("bad grouping: %d", w.Code)
	}
}

func TestRequestGuards(t *testing.T) {
	s := server(t)
	// A cross-site form post cannot set a JSON content type.
	if w := do(s, "PUT", "/api/sessions/claude:s1/meta", `display_name=x`, map[string]string{"Content-Type": "application/x-www-form-urlencoded"}); w.Code != 415 {
		t.Errorf("form post: %d", w.Code)
	}
	if w := do(s, "PUT", "/api/sessions/claude:s1/meta", `{}`, map[string]string{"Content-Type": "application/json", "Origin": "https://evil.test"}); w.Code != 403 {
		t.Errorf("foreign origin: %d", w.Code)
	}
	req := httptest.NewRequest("GET", "http://evil.test/api/sessions", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("rebinding host: %d", w.Code)
	}
}

// TestBehindProxy: a public name and a trusted user header let the server
// run behind Google IAP; requests without the header are refused.
func TestBehindProxy(t *testing.T) {
	s := serverWith(t, Options{AllowHosts: []string{"Bossman.Example.com"}, UserHeader: "X-Goog-Authenticated-User-Email"})
	req := func(method, host, path, body string, hdr map[string]string) int {
		r := httptest.NewRequest(method, "http://"+host+path, strings.NewReader(body))
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if path == "/api/viewer" && w.Code == 200 && !strings.Contains(w.Body.String(), `"user":"alice@example.com"`) {
			t.Errorf("viewer body %s", w.Body.String())
		}
		return w.Code
	}
	user := map[string]string{"X-Goog-Authenticated-User-Email": "accounts.google.com:alice@example.com"}
	if code := req("GET", "bossman.example.com", "/api/viewer", "", user); code != 200 {
		t.Errorf("signed-in viewer: %d", code)
	}
	if code := req("GET", "bossman.example.com", "/api/sessions", "", nil); code != 401 {
		t.Errorf("no user header: %d", code)
	}
	if code := req("GET", "evil.test", "/api/sessions", "", user); code != 403 {
		t.Errorf("unknown host: %d", code)
	}
	write := map[string]string{"Content-Type": "application/json", "Origin": "https://bossman.example.com"}
	for k, v := range user {
		write[k] = v
	}
	if code := req("PUT", "bossman.example.com", "/api/sessions/claude:s1/meta", `{"display_name":"x"}`, write); code != 200 {
		t.Errorf("same-origin write through the proxy: %d", code)
	}
	write["Origin"] = "https://evil.test"
	if code := req("PUT", "bossman.example.com", "/api/sessions/claude:s1/meta", `{}`, write); code != 403 {
		t.Errorf("foreign origin through the proxy: %d", code)
	}
	if v := serverWith(t, Options{}).Viewer(httptest.NewRequest("GET", "/", nil)); v != "" {
		t.Errorf("viewer without a user header option: %q", v)
	}
}

func TestFilterCopies(t *testing.T) {
	for q, skip := range map[string]bool{"": true, "copies=1": false, "copies=0": true} {
		v, _ := url.ParseQuery(q)
		if f, err := filterFrom(v); err != nil || f.SkipCopies != skip {
			t.Errorf("filterFrom(%q).SkipCopies = %v, %v; want %v", q, f.SkipCopies, err, skip)
		}
	}
}

func TestFilterNotTag(t *testing.T) {
	v, _ := url.ParseQuery("notag=sink:*")
	if f, err := filterFrom(v); err != nil || f.NotTag != "sink:*" {
		t.Errorf("filterFrom(notag).NotTag = %q, %v", f.NotTag, err)
	}
}
