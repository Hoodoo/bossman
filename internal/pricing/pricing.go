// Package pricing turns token usage into an estimated USD cost.
package pricing

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"bossman/internal/model"
)

//go:embed default.toml
var Default string

// Rate is USD per million tokens.
type Rate struct {
	Input        float64  `toml:"input" json:"input"`
	Output       float64  `toml:"output" json:"output"`
	CacheRead    float64  `toml:"cache_read" json:"cache_read"`
	CacheWrite5m *float64 `toml:"cache_write_5m" json:"cache_write_5m,omitempty"`
	CacheWrite1h *float64 `toml:"cache_write_1h" json:"cache_write_1h,omitempty"`
}

// Table maps model id prefixes to rates.
type Table struct {
	Models map[string]Rate `toml:"models" json:"models"`
}

// Load reads the user's table at path, falling back to the built-in one
// when it does not exist.
func Load(path string) (*Table, error) {
	src := Default
	if b, err := os.ReadFile(path); err == nil {
		src = string(b)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var t Table
	if _, err := toml.Decode(src, &t); err != nil {
		return nil, fmt.Errorf("pricing %s: %w", path, err)
	}
	return &t, nil
}

// Lookup returns the rate for the longest key that prefixes model.
func (t *Table) Lookup(model string) (Rate, bool) {
	best, found := "", false
	for k := range t.Models {
		if strings.HasPrefix(model, k) && len(k) >= len(best) {
			best, found = k, true
		}
	}
	return t.Models[best], found
}

// Cost prices one model's usage. ok is false when the model is unknown.
func (t *Table) Cost(model string, u model.Usage) (usd float64, ok bool) {
	r, ok := t.Lookup(model)
	if !ok {
		return 0, false
	}
	w5, w1 := r.Input*1.25, r.Input*2
	if r.CacheWrite5m != nil {
		w5 = *r.CacheWrite5m
	}
	if r.CacheWrite1h != nil {
		w1 = *r.CacheWrite1h
	}
	usd = float64(u.Input)*r.Input + float64(u.Output)*r.Output +
		float64(u.CacheRead)*r.CacheRead +
		float64(u.CacheWrite5m)*w5 + float64(u.CacheWrite1h)*w1
	return usd / 1e6, true
}

// Cost sources, from most to least authoritative.
const (
	SourceAgent   = "agent"   // the agent recorded its own cost
	SourceTable   = "table"   // every model priced from the table
	SourcePartial = "partial" // some models missing from the table
	SourceNone    = "none"    // nothing could be priced
)

// SessionCost prices a session: the agent's own figure when it recorded
// one, otherwise the table. perModel holds table prices by model.
func (t *Table) SessionCost(s *model.Session) (usd float64, source string, perModel map[string]float64) {
	perModel = map[string]float64{}
	priced, unpriced := 0, 0
	names := make([]string, 0, len(s.Models))
	for m := range s.Models {
		names = append(names, m)
	}
	sort.Strings(names)
	var sum float64
	for _, m := range names {
		u := s.Models[m]
		if u.Input+u.Output+u.CacheRead+u.CacheWrite5m+u.CacheWrite1h == 0 {
			continue
		}
		if c, ok := t.Cost(m, *u); ok {
			perModel[m] = c
			sum += c
			priced++
		} else {
			unpriced++
		}
	}
	switch {
	case s.ReportedCostUSD != nil:
		return *s.ReportedCostUSD, SourceAgent, perModel
	case unpriced == 0 && priced > 0:
		return sum, SourceTable, perModel
	case priced > 0:
		return sum, SourcePartial, perModel
	default:
		return 0, SourceNone, perModel
	}
}
