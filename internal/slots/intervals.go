package slots

import (
	"sort"
	"time"
)

// Interval is a half-open UTC time window [Start, End).
type Interval struct {
	Start time.Time
	End   time.Time
}

func (iv Interval) IsEmpty() bool { return !iv.End.After(iv.Start) }

func (iv Interval) overlaps(other Interval) bool {
	return iv.Start.Before(other.End) && other.Start.Before(iv.End)
}

// subtract returns the portions of windows that are not covered by any busy interval.
// Both slices may be in any order and may contain overlaps — they are normalised internally.
func subtract(windows, busy []Interval) []Interval {
	if len(busy) == 0 {
		return windows
	}
	merged := mergeIntervals(busy)

	var result []Interval
	for _, w := range windows {
		remaining := []Interval{w}
		for _, b := range merged {
			var next []Interval
			for _, r := range remaining {
				next = append(next, cutOut(r, b)...)
			}
			remaining = next
		}
		result = append(result, remaining...)
	}
	return result
}

// cutOut removes busy from window, returning 0, 1, or 2 intervals.
func cutOut(window, busy Interval) []Interval {
	if !window.overlaps(busy) {
		return []Interval{window}
	}
	var out []Interval
	if window.Start.Before(busy.Start) {
		out = append(out, Interval{Start: window.Start, End: busy.Start})
	}
	if busy.End.Before(window.End) {
		out = append(out, Interval{Start: busy.End, End: window.End})
	}
	return out
}

// expandBusy widens each busy interval by the given before/after durations.
func expandBusy(busy []Interval, before, after time.Duration) []Interval {
	if before == 0 && after == 0 {
		return busy
	}
	expanded := make([]Interval, len(busy))
	for i, b := range busy {
		expanded[i] = Interval{Start: b.Start.Add(-before), End: b.End.Add(after)}
	}
	return expanded
}

// mergeIntervals sorts and merges overlapping/adjacent intervals.
func mergeIntervals(ivs []Interval) []Interval {
	if len(ivs) == 0 {
		return nil
	}
	sorted := make([]Interval, len(ivs))
	copy(sorted, ivs)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Start.Before(sorted[j].Start)
	})

	merged := []Interval{sorted[0]}
	for _, iv := range sorted[1:] {
		last := &merged[len(merged)-1]
		if !iv.Start.After(last.End) {
			if iv.End.After(last.End) {
				last.End = iv.End
			}
		} else {
			merged = append(merged, iv)
		}
	}
	return merged
}
