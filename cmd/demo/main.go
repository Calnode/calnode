// demo exercises the slot engine with realistic scenarios and prints results.
// Run with: go run ./cmd/demo
package main

import (
	"fmt"
	"time"

	"github.com/calnode/calnode/internal/slots"
)

func main() {
	mustLoc := func(name string) *time.Location {
		l, err := time.LoadLocation(name)
		if err != nil {
			panic(err)
		}
		return l
	}

	auckland := mustLoc("Pacific/Auckland")
	newYork := mustLoc("America/New_York")
	utc := time.UTC

	// ── Scenario 1: Auckland host, booker in New York ────────────────────────
	fmt.Println("══════════════════════════════════════════════════════")
	fmt.Println("Scenario 1: Auckland host (NZST UTC+12), booker in New York")
	fmt.Println("  Host availability: Mon–Fri 09:00–17:00 Auckland time")
	fmt.Println("  Date range: Mon 2026-06-15 (NZ winter)")
	fmt.Println("══════════════════════════════════════════════════════")

	req1 := slots.Request{
		Event: slots.EventConfig{
			DurationMinutes:     30,
			SlotIntervalMinutes: 30,
			MinNoticeMinutes:    60,
			MaxFutureDays:       30,
			RoutingMode:         "fixed",
		},
		Hosts: []slots.HostAvailability{
			{
				HostID:   "wynne",
				Location: auckland,
				Rules: []slots.AvailabilityRule{
					{DayOfWeek: time.Monday, StartTime: "09:00", EndTime: "17:00"},
					{DayOfWeek: time.Tuesday, StartTime: "09:00", EndTime: "17:00"},
					{DayOfWeek: time.Wednesday, StartTime: "09:00", EndTime: "17:00"},
					{DayOfWeek: time.Thursday, StartTime: "09:00", EndTime: "17:00"},
					{DayOfWeek: time.Friday, StartTime: "09:00", EndTime: "17:00"},
				},
				// A 10:00–11:00 Auckland meeting already on the calendar.
				Busy: []slots.Interval{
					{
						Start: time.Date(2026, 6, 14, 22, 0, 0, 0, utc), // 10:00 Mon Auckland NZST
						End:   time.Date(2026, 6, 14, 23, 0, 0, 0, utc), // 11:00 Mon Auckland NZST
					},
				},
			},
		},
		DateFrom: time.Date(2026, 6, 15, 0, 0, 0, 0, utc),
		DateTo:   time.Date(2026, 6, 15, 0, 0, 0, 0, utc),
		BookerTZ: newYork,
		Now:      time.Date(2026, 6, 14, 0, 0, 0, 0, utc),
	}

	printSlots(req1, "New York time")

	// ── Scenario 2: Two-host round-robin ────────────────────────────────────
	fmt.Println("\n══════════════════════════════════════════════════════")
	fmt.Println("Scenario 2: Two hosts, round-robin routing")
	fmt.Println("  Host A: Mon 09:00–12:00 UTC")
	fmt.Println("  Host B: Mon 13:00–16:00 UTC (no overlap with A)")
	fmt.Println("  Expected: slots from BOTH hosts surfaced")
	fmt.Println("══════════════════════════════════════════════════════")

	req2 := slots.Request{
		Event: slots.EventConfig{
			DurationMinutes:     30,
			SlotIntervalMinutes: 30,
			MaxFutureDays:       30,
			RoutingMode:         "round_robin",
		},
		Hosts: []slots.HostAvailability{
			{
				HostID:   "alice",
				Location: utc,
				Rules:    []slots.AvailabilityRule{{DayOfWeek: time.Monday, StartTime: "09:00", EndTime: "12:00"}},
			},
			{
				HostID:   "bob",
				Location: utc,
				Rules:    []slots.AvailabilityRule{{DayOfWeek: time.Monday, StartTime: "13:00", EndTime: "16:00"}},
			},
		},
		DateFrom: time.Date(2026, 6, 15, 0, 0, 0, 0, utc),
		DateTo:   time.Date(2026, 6, 15, 0, 0, 0, 0, utc),
		BookerTZ: utc,
		Now:      time.Date(2026, 6, 14, 0, 0, 0, 0, utc),
	}

	printSlots(req2, "UTC")

	// ── Scenario 3: Collective — all must be free ────────────────────────────
	fmt.Println("\n══════════════════════════════════════════════════════")
	fmt.Println("Scenario 3: Collective routing (all must be free)")
	fmt.Println("  Host A: Mon 09:00–13:00 UTC")
	fmt.Println("  Host B: Mon 11:00–15:00 UTC")
	fmt.Println("  Overlap: 11:00–13:00 → only those slots offered")
	fmt.Println("══════════════════════════════════════════════════════")

	req3 := slots.Request{
		Event: slots.EventConfig{
			DurationMinutes:     30,
			SlotIntervalMinutes: 30,
			MaxFutureDays:       30,
			RoutingMode:         "collective",
		},
		Hosts: []slots.HostAvailability{
			{
				HostID:   "alice",
				Location: utc,
				Rules:    []slots.AvailabilityRule{{DayOfWeek: time.Monday, StartTime: "09:00", EndTime: "13:00"}},
			},
			{
				HostID:   "bob",
				Location: utc,
				Rules:    []slots.AvailabilityRule{{DayOfWeek: time.Monday, StartTime: "11:00", EndTime: "15:00"}},
			},
		},
		DateFrom: time.Date(2026, 6, 15, 0, 0, 0, 0, utc),
		DateTo:   time.Date(2026, 6, 15, 0, 0, 0, 0, utc),
		BookerTZ: utc,
		Now:      time.Date(2026, 6, 14, 0, 0, 0, 0, utc),
	}

	printSlots(req3, "UTC")

	// ── Scenario 4: Buffers ──────────────────────────────────────────────────
	fmt.Println("\n══════════════════════════════════════════════════════")
	fmt.Println("Scenario 4: 15-min buffers around existing bookings")
	fmt.Println("  Window: 09:00–12:00 UTC. Existing booking: 10:00–10:30.")
	fmt.Println("  buffer_before=15, buffer_after=15 → busy expands to 09:45–10:45")
	fmt.Println("══════════════════════════════════════════════════════")

	req4 := slots.Request{
		Event: slots.EventConfig{
			DurationMinutes:     30,
			SlotIntervalMinutes: 30,
			BufferBeforeMinutes: 15,
			BufferAfterMinutes:  15,
			MaxFutureDays:       30,
			RoutingMode:         "fixed",
		},
		Hosts: []slots.HostAvailability{
			{
				HostID:   "host",
				Location: utc,
				Rules:    []slots.AvailabilityRule{{DayOfWeek: time.Monday, StartTime: "09:00", EndTime: "12:00"}},
				Busy: []slots.Interval{
					{
						Start: time.Date(2026, 6, 15, 10, 0, 0, 0, utc),
						End:   time.Date(2026, 6, 15, 10, 30, 0, 0, utc),
					},
				},
			},
		},
		DateFrom: time.Date(2026, 6, 15, 0, 0, 0, 0, utc),
		DateTo:   time.Date(2026, 6, 15, 0, 0, 0, 0, utc),
		BookerTZ: utc,
		Now:      time.Date(2026, 6, 14, 0, 0, 0, 0, utc),
	}

	printSlots(req4, "UTC")
}

func printSlots(req slots.Request, tzLabel string) {
	result, err := slots.Generate(req)
	if err != nil {
		fmt.Printf("  ERROR: %v\n", err)
		return
	}
	if len(result) == 0 {
		fmt.Println("  (no slots available)")
		return
	}
	fmt.Printf("  %d slots (%s):\n", len(result), tzLabel)
	for _, s := range result {
		fmt.Printf("    %-6s  %s → %s  (UTC: %s → %s)\n",
			s.HostIDs,
			s.Start.Format("Mon 15:04"),
			s.End.Format("15:04"),
			s.Start.UTC().Format("15:04"),
			s.End.UTC().Format("15:04"),
		)
	}
}
