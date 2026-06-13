package slots

import (
	"testing"
	"time"
)

// ref is a fixed UTC baseline for interval tests.
var ref = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func mins(n int) time.Duration { return time.Duration(n) * time.Minute }

func iv(startMin, endMin int) Interval {
	return Interval{Start: ref.Add(mins(startMin)), End: ref.Add(mins(endMin))}
}

// ─── subtract ────────────────────────────────────────────────────────────────

func TestSubtract_noBusy(t *testing.T) {
	windows := []Interval{iv(0, 60)}
	got := subtract(windows, nil)
	assertIntervals(t, got, []Interval{iv(0, 60)})
}

func TestSubtract_fullyBlocked(t *testing.T) {
	got := subtract([]Interval{iv(0, 60)}, []Interval{iv(0, 60)})
	if len(got) != 0 {
		t.Errorf("expected empty; got %v", got)
	}
}

func TestSubtract_busyAtStart(t *testing.T) {
	got := subtract([]Interval{iv(0, 60)}, []Interval{iv(0, 30)})
	assertIntervals(t, got, []Interval{iv(30, 60)})
}

func TestSubtract_busyAtEnd(t *testing.T) {
	got := subtract([]Interval{iv(0, 60)}, []Interval{iv(30, 60)})
	assertIntervals(t, got, []Interval{iv(0, 30)})
}

func TestSubtract_busyInMiddle(t *testing.T) {
	got := subtract([]Interval{iv(0, 60)}, []Interval{iv(20, 40)})
	assertIntervals(t, got, []Interval{iv(0, 20), iv(40, 60)})
}

func TestSubtract_multipleWindows(t *testing.T) {
	windows := []Interval{iv(0, 30), iv(60, 90)}
	busy := []Interval{iv(15, 75)}
	got := subtract(windows, busy)
	assertIntervals(t, got, []Interval{iv(0, 15), iv(75, 90)})
}

func TestSubtract_busyDoesNotOverlap(t *testing.T) {
	got := subtract([]Interval{iv(0, 30)}, []Interval{iv(60, 90)})
	assertIntervals(t, got, []Interval{iv(0, 30)})
}

func TestSubtract_overlappingBusyIntervals(t *testing.T) {
	// Busy intervals overlap each other — should still work correctly.
	busy := []Interval{iv(10, 40), iv(30, 50)}
	got := subtract([]Interval{iv(0, 60)}, busy)
	assertIntervals(t, got, []Interval{iv(0, 10), iv(50, 60)})
}

// ─── expandBusy ──────────────────────────────────────────────────────────────

func TestExpandBusy_zero(t *testing.T) {
	busy := []Interval{iv(30, 60)}
	got := expandBusy(busy, 0, 0)
	assertIntervals(t, got, busy)
}

func TestExpandBusy_before(t *testing.T) {
	got := expandBusy([]Interval{iv(30, 60)}, mins(10), 0)
	assertIntervals(t, got, []Interval{iv(20, 60)})
}

func TestExpandBusy_after(t *testing.T) {
	got := expandBusy([]Interval{iv(30, 60)}, 0, mins(10))
	assertIntervals(t, got, []Interval{iv(30, 70)})
}

func TestExpandBusy_both(t *testing.T) {
	got := expandBusy([]Interval{iv(30, 60)}, mins(10), mins(10))
	assertIntervals(t, got, []Interval{iv(20, 70)})
}

// ─── mergeIntervals ──────────────────────────────────────────────────────────

func TestMergeIntervals_adjacent(t *testing.T) {
	got := mergeIntervals([]Interval{iv(0, 30), iv(30, 60)})
	assertIntervals(t, got, []Interval{iv(0, 60)})
}

func TestMergeIntervals_overlapping(t *testing.T) {
	got := mergeIntervals([]Interval{iv(0, 40), iv(30, 60)})
	assertIntervals(t, got, []Interval{iv(0, 60)})
}

func TestMergeIntervals_disjoint(t *testing.T) {
	got := mergeIntervals([]Interval{iv(0, 20), iv(40, 60)})
	assertIntervals(t, got, []Interval{iv(0, 20), iv(40, 60)})
}

func TestMergeIntervals_unsortedInput(t *testing.T) {
	got := mergeIntervals([]Interval{iv(40, 60), iv(0, 30), iv(25, 50)})
	assertIntervals(t, got, []Interval{iv(0, 60)})
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func assertIntervals(t *testing.T, got, want []Interval) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len(got)=%d len(want)=%d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if !got[i].Start.Equal(want[i].Start) || !got[i].End.Equal(want[i].End) {
			t.Errorf("[%d] got %v-%v; want %v-%v",
				i,
				got[i].Start.Format(time.RFC3339),
				got[i].End.Format(time.RFC3339),
				want[i].Start.Format(time.RFC3339),
				want[i].End.Format(time.RFC3339),
			)
		}
	}
}
