package slots

import (
	"testing"
	"time"
)

func at(h, m int) time.Time { return time.Date(2026, 6, 15, h, m, 0, 0, time.UTC) }

func ivlsEqual(a, b []Interval) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Start.Equal(b[i].Start) || !a[i].End.Equal(b[i].End) {
			return false
		}
	}
	return true
}

// TestSubtractIntervals covers the own-event-exclusion cases the slots handler
// relies on: Calnode's own calendar events are cut out of Google free/busy so a
// confirmed booking isn't double-counted and a cancelled-but-not-yet-deleted one
// doesn't keep blocking the freed slot.
func TestSubtractIntervals(t *testing.T) {
	tests := []struct {
		name         string
		blocks, cuts []Interval
		want         []Interval
	}{
		{
			name:   "no cuts returns blocks unchanged",
			blocks: []Interval{{at(9, 0), at(10, 0)}},
			cuts:   nil,
			want:   []Interval{{at(9, 0), at(10, 0)}},
		},
		{
			name:   "cut exactly covering a block removes it (cancelled own event frees the slot)",
			blocks: []Interval{{at(9, 0), at(9, 30)}},
			cuts:   []Interval{{at(9, 0), at(9, 30)}},
			want:   nil,
		},
		{
			name:   "external event not matching any own event is kept",
			blocks: []Interval{{at(14, 0), at(15, 0)}},
			cuts:   []Interval{{at(9, 0), at(9, 30)}},
			want:   []Interval{{at(14, 0), at(15, 0)}},
		},
		{
			name:   "free/busy merged our event with an adjacent external one: external remainder survives",
			blocks: []Interval{{at(9, 0), at(11, 0)}}, // free/busy merged [9,10) ours + [10,11) external
			cuts:   []Interval{{at(9, 0), at(10, 0)}}, // our event
			want:   []Interval{{at(10, 0), at(11, 0)}},
		},
		{
			name:   "our event in the middle of a longer external block splits it",
			blocks: []Interval{{at(9, 0), at(12, 0)}},
			cuts:   []Interval{{at(10, 0), at(11, 0)}},
			want:   []Interval{{at(9, 0), at(10, 0)}, {at(11, 0), at(12, 0)}},
		},
		{
			name:   "multiple own events cut from one block",
			blocks: []Interval{{at(9, 0), at(17, 0)}},
			cuts:   []Interval{{at(10, 0), at(10, 30)}, {at(13, 0), at(14, 0)}},
			want:   []Interval{{at(9, 0), at(10, 0)}, {at(10, 30), at(13, 0)}, {at(14, 0), at(17, 0)}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SubtractIntervals(tt.blocks, tt.cuts)
			if !ivlsEqual(got, tt.want) {
				t.Errorf("SubtractIntervals() = %v; want %v", got, tt.want)
			}
		})
	}
}
