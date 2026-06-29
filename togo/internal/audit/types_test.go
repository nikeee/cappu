package audit

import "testing"

func TestSeverityRank(t *testing.T) {
	for i, s := range SeverityOrder {
		if got := severityRank(s); got != i {
			t.Errorf("severityRank(%q) = %d, want %d", s, got, i)
		}
	}
	if got := severityRank(Severity("bogus")); got != len(SeverityOrder) {
		t.Errorf("severityRank(bogus) = %d, want %d", got, len(SeverityOrder))
	}
}

func TestCountsIncAndGet(t *testing.T) {
	var c Counts
	for _, s := range []Severity{
		SeverityCritical, SeverityHigh, SeverityModerate, SeverityLow,
		SeverityUnknown, Severity("bogus"), // an unrecognized value tallies as unknown
	} {
		c.inc(s)
	}
	want := map[Severity]int{
		SeverityCritical: 1, SeverityHigh: 1, SeverityModerate: 1,
		SeverityLow: 1, SeverityUnknown: 2,
	}
	for s, n := range want {
		if got := c.Get(s); got != n {
			t.Errorf("Get(%q) = %d, want %d", s, got, n)
		}
	}
	if c.Total() != 6 {
		t.Errorf("Total() = %d, want 6", c.Total())
	}
}
