package booking

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// QueryRower is satisfied by both *sql.DB and *sql.Tx.
type QueryRower interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ISOWeek identifies an ISO 8601 week (Monday-Sunday).
type ISOWeek struct {
	Year int
	Week int
}

// FrequencyCounts holds confirmed booking counts bucketed by local calendar date
// ("2006-01-02") and local ISO week.
type FrequencyCounts struct {
	ByDay  map[string]int
	ByWeek map[ISOWeek]int
}

// Exceeded reports whether adding a booking at instant `at` in `loc` would meet or
// exceed either configured frequency cap.
func (c FrequencyCounts) Exceeded(at time.Time, loc *time.Location, maxPerDay, maxPerWeek *int) bool {
	if loc == nil {
		loc = time.UTC
	}
	local := at.In(loc)
	if maxPerDay != nil && *maxPerDay > 0 {
		if c.ByDay[local.Format("2006-01-02")] >= *maxPerDay {
			return true
		}
	}
	if maxPerWeek != nil && *maxPerWeek > 0 {
		y, w := local.ISOWeek()
		if c.ByWeek[ISOWeek{Year: y, Week: w}] >= *maxPerWeek {
			return true
		}
	}
	return false
}

// LoadLocationOrUTC resolves an IANA timezone name, falling back to UTC on empty or invalid input.
func LoadLocationOrUTC(ianaTZ string) *time.Location {
	if ianaTZ == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(ianaTZ)
	if err != nil || loc == nil {
		return time.UTC
	}
	return loc
}

// ISOWeekWindow returns the UTC [start, end) bounds of the Monday-Sunday ISO week
// containing `at` when evaluated in `loc`.
func ISOWeekWindow(at time.Time, loc *time.Location) (time.Time, time.Time) {
	if loc == nil {
		loc = time.UTC
	}
	local := at.In(loc)
	dayMidnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	// Go Weekday(): Sunday=0, Monday=1, ..., Saturday=6. Shift so Monday=0..Sunday=6.
	wd := (int(local.Weekday()) + 6) % 7
	weekStart := dayMidnight.AddDate(0, 0, -wd)
	weekEnd := weekStart.AddDate(0, 0, 7)
	return weekStart.UTC(), weekEnd.UTC()
}

// CountFrequencyBookings counts all non-cancelled bookings for eventTypeID whose start_at
// falls near [rangeStartUTC, rangeEndUTC) (padded cleanly so timezone conversions never miss
// boundary events) and buckets them into loc's calendar days and ISO weeks.
func CountFrequencyBookings(ctx context.Context, q QueryRower, eventTypeID string, rangeStartUTC, rangeEndUTC time.Time, loc *time.Location) (FrequencyCounts, error) {
	if loc == nil {
		loc = time.UTC
	}
	counts := FrequencyCounts{
		ByDay:  make(map[string]int),
		ByWeek: make(map[ISOWeek]int),
	}
	padStart := rangeStartUTC.UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	padEnd := rangeEndUTC.UTC().Add(24 * time.Hour).Format(time.RFC3339)

	rows, err := q.QueryContext(ctx, `
		SELECT start_at FROM bookings
		WHERE event_type_id = ?
		  AND status != 'cancelled'
		  AND datetime(start_at) >= datetime(?)
		  AND datetime(start_at) < datetime(?)`,
		eventTypeID, padStart, padEnd)
	if err != nil {
		return counts, err
	}
	defer rows.Close()

	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return counts, err
		}
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			continue
		}
		local := t.In(loc)
		counts.ByDay[local.Format("2006-01-02")]++
		y, w := local.ISOWeek()
		counts.ByWeek[ISOWeek{Year: y, Week: w}]++
	}
	return counts, rows.Err()
}

func enforceFrequencyCaps(ctx context.Context, tx *sql.Tx, p CreateParams) error {
	var dbDay, dbWeek sql.NullInt64
	var dbTZ string
	err := tx.QueryRowContext(ctx, `
		SELECT et.max_per_day, et.max_per_week, COALESCE(u.iana_timezone, 'UTC')
		FROM event_types et
		LEFT JOIN users u ON u.id = et.user_id
		WHERE et.id = ?`, p.EventTypeID).Scan(&dbDay, &dbWeek, &dbTZ)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("booking: load frequency caps: %w", err)
	}

	maxPerDay := p.MaxPerDay
	if maxPerDay == nil && dbDay.Valid {
		v := int(dbDay.Int64)
		maxPerDay = &v
	}
	maxPerWeek := p.MaxPerWeek
	if maxPerWeek == nil && dbWeek.Valid {
		v := int(dbWeek.Int64)
		maxPerWeek = &v
	}
	if (maxPerDay == nil || *maxPerDay <= 0) && (maxPerWeek == nil || *maxPerWeek <= 0) {
		return nil
	}

	tzName := p.EventTimezone
	if tzName == "" {
		tzName = dbTZ
	}
	loc := LoadLocationOrUTC(tzName)
	weekStart, weekEnd := ISOWeekWindow(p.StartAt, loc)
	counts, err := CountFrequencyBookings(ctx, tx, p.EventTypeID, weekStart, weekEnd, loc)
	if err != nil {
		return fmt.Errorf("booking: frequency cap check: %w", err)
	}
	if counts.Exceeded(p.StartAt, loc, maxPerDay, maxPerWeek) {
		return ErrFrequencyCapReached
	}
	return nil
}
