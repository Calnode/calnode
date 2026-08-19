package i18n

import (
	"fmt"
	"time"
)

// Go's time.Format has no locale support — month/weekday names are hardcoded English in
// the stdlib (there's no locale parameter anywhere in the time package). These tables +
// FormatDate/FormatTimeOfDay are the fix: short weekday/month names come from the locale's
// own string table (dowKeys/monthKeys below just name the lookup keys, not the words
// themselves), and the numeric parts (day, year, hour, minute) come from time.Format, which
// is locale-neutral for digits. See internal-docs/i18n-plan.md's "Go has no locale-aware
// date formatting" finding.

// dowKeys indexes by time.Weekday (Sunday=0 .. Saturday=6).
var dowKeys = [7]string{
	"dow_short_sun", "dow_short_mon", "dow_short_tue", "dow_short_wed",
	"dow_short_thu", "dow_short_fri", "dow_short_sat",
}

// monthKeys indexes by time.Month (January=1 .. December=12); index 0 is unused.
var monthKeys = [13]string{
	"", "month_short_jan", "month_short_feb", "month_short_mar", "month_short_apr",
	"month_short_may", "month_short_jun", "month_short_jul", "month_short_aug",
	"month_short_sep", "month_short_oct", "month_short_nov", "month_short_dec",
}

// FormatDate returns t's short weekday, day, month, and year in this locale — e.g.
// "Mon 22 Jun 2026" (English) or "lun 22 jun 2026" (Spanish). Callers convert t into the
// desired zone first (t.In(loc)) — this only handles the locale, not the timezone.
func (l *Locale) FormatDate(t time.Time) string {
	return fmt.Sprintf("%s %d %s %d", l.T(dowKeys[t.Weekday()]), t.Day(), l.T(monthKeys[t.Month()]), t.Year())
}

// FormatTimeOfDay returns just the clock time, honoring the locale's 12h/24h preference
// (the "clock_format" key) — e.g. "9:00 AM" (English) or "21:00" (Spanish). This mirrors
// what Intl.DateTimeFormat already renders client-side for these locales on book.html/
// manage.html/embed.js, so server-rendered emails agree with the page instead of defaulting
// to a hardcoded 12-hour clock regardless of locale.
func (l *Locale) FormatTimeOfDay(t time.Time) string {
	if l.T("clock_format") == "24h" {
		return t.Format("15:04")
	}
	return t.Format("3:04 PM")
}

// FormatDateTime combines FormatDate and FormatTimeOfDay — e.g. "Mon 22 Jun 2026, 9:00 AM".
func (l *Locale) FormatDateTime(t time.Time) string {
	return l.FormatDate(t) + ", " + l.FormatTimeOfDay(t)
}
