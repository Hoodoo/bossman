package archive

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, path, s string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMirror(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	maps := []Mapping{
		{Src: src, Dst: dst},
		{Src: filepath.Join(src, "..", "missing"), Dst: filepath.Join(dst, "missing")},
	}
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	log := filepath.Join(src, "p", "s.jsonl")
	write(t, log, "line1\n", t0)

	st, err := Mirror(maps, t0)
	if err != nil {
		t.Fatal(err)
	}
	if st.Copied != 1 {
		t.Fatalf("first pass: %+v", st)
	}
	archived := filepath.Join(dst, "p", "s.jsonl")
	if read(t, archived) != "line1\n" {
		t.Fatal("copy differs")
	}

	// Unchanged: nothing copied.
	if st, _ = Mirror(maps, t0); st.Unchanged != 1 || st.Bytes != 0 {
		t.Errorf("unchanged pass: %+v", st)
	}

	// Appended: copied over in place.
	write(t, log, "line1\nline2\n", t0.Add(time.Minute))
	if st, _ = Mirror(maps, t0); st.Grown != 1 {
		t.Errorf("grown pass: %+v", st)
	}
	if read(t, archived) != "line1\nline2\n" {
		t.Error("grown copy differs")
	}

	// Rewritten (or truncated): the old copy is kept as a version.
	write(t, log, "other\n", t0.Add(2*time.Minute))
	now := t0.Add(time.Hour)
	if st, _ = Mirror(maps, now); st.Versioned != 1 {
		t.Errorf("rewrite pass: %+v", st)
	}
	if read(t, archived) != "other\n" {
		t.Error("rewritten copy differs")
	}
	version := archived + ".~20261001T130000Z"
	if read(t, version) != "line1\nline2\n" {
		t.Error("old version not kept")
	}
	if !IsVersion(version) || IsVersion(archived) {
		t.Error("IsVersion misclassifies")
	}

	// Deleted at the source: the archive keeps it.
	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	if _, err = Mirror(maps, now); err != nil {
		t.Fatal(err)
	}
	if read(t, archived) != "other\n" {
		t.Error("archive lost a file the source deleted")
	}
}

func TestMirrorSingleFile(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	f := filepath.Join(src, "index.jsonl")
	write(t, f, "{}\n", time.Now())
	st, err := Mirror([]Mapping{{Src: f, Dst: filepath.Join(dst, "codex", "index.jsonl")}}, time.Now())
	if err != nil || st.Copied != 1 {
		t.Fatalf("Mirror = %+v, %v", st, err)
	}
	if read(t, filepath.Join(dst, "codex", "index.jsonl")) != "{}\n" {
		t.Error("single file not mirrored")
	}
}
