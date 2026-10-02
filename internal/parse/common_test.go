package parse

import (
	"testing"
	"time"
)

func TestClockActive(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	var c clock
	c.add(t0)
	c.add(t0.Add(time.Minute))      // 1m active
	c.add(t0.Add(20 * time.Minute)) // 19m gap: idle on its own…
	// …but the agent reported a 15-minute turn ending then.
	c.turn(t0.Add(20*time.Minute), 15*time.Minute)
	c.add(t0.Add(22 * time.Minute)) // 2m active
	first, last, active := c.span(5 * time.Minute)
	if !first.Equal(t0) || !last.Equal(t0.Add(22*time.Minute)) {
		t.Errorf("span = %v – %v", first, last)
	}
	if want := (1 + 15 + 2) * 60.0; active != want {
		t.Errorf("active = %v, want %v", active, want)
	}
}

func TestCatalogueSummary(t *testing.T) {
	if _, ok := catalogueSummary("mentions cc-catalogue:session-concluded in prose"); ok {
		t.Error("a bare mention must not conclude")
	}
	sum, ok := catalogueSummary("<!-- cc-catalogue:session-concluded\naway-summary: |\n  Did A.\n  Left B.\n-->")
	if !ok || sum != "Did A. Left B." {
		t.Errorf("summary = %q, %v", sum, ok)
	}
}
