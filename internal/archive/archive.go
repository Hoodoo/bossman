// Package archive mirrors agent session directories into bossman's own
// directory, so sessions survive the agents' cleanup.
//
// Session logs are append-only, so a source file normally only grows. The
// mirror copies a file when it is new or has grown; when a source file has
// shrunk or its existing bytes changed, the old copy is kept beside the new
// one as <name>.~<UTC timestamp>. The archive never deletes anything.
package archive

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Mapping copies Src (a directory or a single file) to Dst.
type Mapping struct {
	Name string
	Src  string
	Dst  string
}

// Stats reports what one Mirror pass did.
type Stats struct {
	Copied    int   `json:"copied"`
	Grown     int   `json:"grown"`
	Versioned int   `json:"versioned"`
	Unchanged int   `json:"unchanged"`
	Bytes     int64 `json:"bytes"`
	// Errors lists files that could not be copied; the pass continues.
	Errors []string `json:"errors,omitempty"`
}

func (s *Stats) add(o Stats) {
	s.Copied += o.Copied
	s.Grown += o.Grown
	s.Versioned += o.Versioned
	s.Unchanged += o.Unchanged
	s.Bytes += o.Bytes
	s.Errors = append(s.Errors, o.Errors...)
}

// Mirror copies every mapping. A missing source is skipped silently: the
// agent may simply not be installed.
func Mirror(maps []Mapping, now time.Time) (Stats, error) {
	var total Stats
	for _, m := range maps {
		fi, err := os.Stat(m.Src)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return total, err
		}
		if !fi.IsDir() {
			st, err := mirrorFile(m.Src, m.Dst, fi, now)
			if err != nil {
				total.Errors = append(total.Errors, fmt.Sprintf("%s: %v", m.Src, err))
			}
			total.add(st)
			continue
		}
		err = filepath.WalkDir(m.Src, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				total.Errors = append(total.Errors, fmt.Sprintf("%s: %v", path, err))
				return nil
			}
			if d.IsDir() || !d.Type().IsRegular() {
				return nil
			}
			rel, _ := filepath.Rel(m.Src, path)
			fi, err := d.Info()
			if err != nil {
				return nil
			}
			st, err := mirrorFile(path, filepath.Join(m.Dst, rel), fi, now)
			if err != nil {
				total.Errors = append(total.Errors, fmt.Sprintf("%s: %v", path, err))
			}
			total.add(st)
			return nil
		})
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func mirrorFile(src, dst string, sfi os.FileInfo, now time.Time) (Stats, error) {
	var st Stats
	dfi, err := os.Stat(dst)
	switch {
	case os.IsNotExist(err):
		st.Copied = 1
	case err != nil:
		return st, err
	case dfi.Size() == sfi.Size() && dfi.ModTime().Equal(sfi.ModTime()):
		st.Unchanged = 1
		return st, nil
	default:
		same, err := hasPrefix(src, dst, dfi.Size())
		if err != nil {
			return st, err
		}
		switch {
		case same && dfi.Size() == sfi.Size():
			// Same bytes, only the timestamp moved.
			_ = os.Chtimes(dst, sfi.ModTime(), sfi.ModTime())
			st.Unchanged = 1
			return st, nil
		case same:
			st.Grown = 1
		default:
			version := dst + ".~" + now.UTC().Format("20060102T150405Z")
			if err := os.Rename(dst, version); err != nil {
				return st, err
			}
			st.Versioned = 1
		}
	}
	n, err := copyFile(src, dst, sfi)
	if err != nil {
		return Stats{}, err
	}
	st.Bytes = n
	return st, nil
}

// hasPrefix reports whether src starts with the first n bytes of dst.
func hasPrefix(src, dst string, n int64) (bool, error) {
	hs, err := hashPrefix(src, n)
	if err != nil {
		return false, err
	}
	hd, err := hashPrefix(dst, n)
	if err != nil {
		return false, err
	}
	return bytes.Equal(hs, hd), nil
}

func hashPrefix(path string, n int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	got, err := io.Copy(h, io.LimitReader(f, n))
	if err != nil {
		return nil, err
	}
	if got < n {
		return nil, nil // shorter than n: cannot share the prefix
	}
	return h.Sum(nil), nil
}

// copyFile writes src to dst atomically and copies its modification time.
func copyFile(src, dst string, sfi os.FileInfo) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, err
	}
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp*")
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(tmp, in)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o644)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}
	if err != nil {
		os.Remove(tmp.Name())
		return 0, err
	}
	_ = os.Chtimes(dst, sfi.ModTime(), sfi.ModTime())
	return n, nil
}

// IsVersion reports whether path is a kept old copy, not a live file.
func IsVersion(path string) bool { return strings.Contains(filepath.Base(path), ".~") }
