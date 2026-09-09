// Run: node --test internal/handler/assets/booking-logic.test.js
const test = require('node:test');
const assert = require('node:assert');
const B = require('./booking-logic.js');

test('dateKeyFromISO uses the SELECTED tz, not the host/browser tz', () => {
  // 02:00 UTC lands on different calendar days depending on the viewer's timezone.
  const iso = '2026-06-15T02:00:00Z';
  assert.equal(B.dateKeyFromISO(iso, 'Pacific/Auckland'), '2026-06-15'); // UTC+12 → 14:00 same day
  assert.equal(B.dateKeyFromISO(iso, 'America/New_York'), '2026-06-14'); // UTC-4 → 22:00 prev day
  assert.equal(B.dateKeyFromISO(iso, 'UTC'), '2026-06-15');
});

test('groupSlotsByDay buckets by tz-correct day and can exclude one slot', () => {
  const slots = [
    { start: '2026-06-15T02:00:00Z' }, // NY → 06-14
    { start: '2026-06-15T20:00:00Z' }, // NY → 06-15
    { start: '2026-06-15T21:00:00Z' }  // NY → 06-15
  ];
  const ny = B.groupSlotsByDay(slots, 'America/New_York');
  assert.deepEqual(Object.keys(ny).sort(), ['2026-06-14', '2026-06-15']);
  assert.equal(ny['2026-06-15'].length, 2);

  const excl = B.groupSlotsByDay(slots, 'America/New_York', '2026-06-15T20:00:00Z');
  assert.equal(excl['2026-06-15'].length, 1); // the excluded current-booking slot is dropped
});

test('dowIndex is Monday-first (0=Mon … 6=Sun)', () => {
  assert.equal(B.dowIndex(new Date(2026, 5, 15)), 0); // 2026-06-15 is a Monday
  assert.equal(B.dowIndex(new Date(2026, 5, 21)), 6); // Sunday
});

test('dowLabels is Monday-first and locale-aware', () => {
  assert.deepEqual(B.dowLabels('en'), ['Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa', 'Su']);
  assert.deepEqual(B.dowLabels('es'), ['lu', 'ma', 'mi', 'ju', 'vi', 'sá', 'do']);
  // No locale passed → falls back to the runtime default rather than throwing.
  assert.equal(B.dowLabels().length, 7);
});

test('month helpers', () => {
  const d = new Date(2026, 5, 15); // June 2026
  assert.equal(B.startOfMonth(d).getDate(), 1);
  assert.equal(B.endOfMonth(d).getDate(), 30);
  assert.equal(B.addMonths(d, 1).getMonth(), 6);  // July
  assert.equal(B.addMonths(d, -6).getMonth(), 11); // prev Dec
  assert.equal(B.addMonths(d, -6).getFullYear(), 2025);
  assert.equal(B.daysInMonth(2024, 1), 29); // leap Feb
  assert.equal(B.daysInMonth(2026, 1), 28);
});


test('formatTime / formatDay respect tz', () => {
  const iso = '2026-06-15T02:00:00Z';
  assert.equal(B.formatTime(iso, 'UTC', 'en-US'), '2:00 AM');
  // NY (UTC-4) → prev day, June 14
  assert.match(B.formatDay(iso, 'America/New_York', 'short', 'en-US'), /Jun 14/);
  assert.match(B.formatDay(iso, 'America/New_York', 'long', 'en-US'), /June 14/);
});

test('mergeDaySlots interleaves taken slots in time order and tags them', () => {
  const free = [{ start: '2026-06-15T09:00:00Z' }, { start: '2026-06-15T11:00:00Z' }];
  const taken = [{ start: '2026-06-15T10:00:00Z' }];

  const merged = B.mergeDaySlots(free, taken);
  assert.deepEqual(merged.map((s) => s.start.slice(11, 16)), ['09:00', '10:00', '11:00']);
  assert.deepEqual(merged.map((s) => s.taken), [false, true, false]);
});

test('mergeDaySlots does not mutate the arrays it was given', () => {
  const free = [{ start: '2026-06-15T09:00:00Z' }];
  const taken = [{ start: '2026-06-15T10:00:00Z' }];
  B.mergeDaySlots(free, taken);
  assert.equal('taken' in free[0], false, 'the caller still holds the API response');
  assert.equal('taken' in taken[0], false);
});

test('mergeDaySlots handles a missing taken array (the opt-in is off)', () => {
  const free = [{ start: '2026-06-15T09:00:00Z' }];
  assert.deepEqual(B.mergeDaySlots(free, undefined).map((s) => s.taken), [false]);
  assert.deepEqual(B.mergeDaySlots(undefined, undefined), []);
});

test('bookableDayKeys omits a day whose slots are all taken', () => {
  // The trap this exists for: grouping taken slots too means a fully booked day still
  // produces a key, and using those keys for the calendar would show it as clickable
  // with something available on it.
  const freeByDay = { '2026-06-15': [{ start: 'x' }], '2026-06-16': [] };
  assert.deepEqual(B.bookableDayKeys(freeByDay).sort(), ['2026-06-15']);
});

test('fmt substitutes %s in order', () => {
  assert.equal(B.fmt('No available times on %s.', ['Monday, 15 June']), 'No available times on Monday, 15 June.');
  assert.equal(B.fmt('%s has no available times on %s.', ['Alex', 'Monday']), 'Alex has no available times on Monday.');
  assert.equal(B.fmt('Bookings must be made at least %s in advance.', ['4 hours']),
    'Bookings must be made at least 4 hours in advance.');
});

test('fmt honours indexed %[n]s, so a translation can reorder its arguments', () => {
  // German and Swedish put the date before the verb; the locale files are allowed to
  // reorder as long as the verbs match English (internal/i18n's parity test).
  assert.equal(B.fmt('%[2]s: %[1]s hat keine Termine.', ['Alex', 'Montag']), 'Montag: Alex hat keine Termine.');
  // An index may repeat an argument, and mixing forms keeps the sequential counter
  // independent of the indexed reads.
  assert.equal(B.fmt('%[1]s / %[1]s / %s', ['a', 'b']), 'a / a / a');
});

test('fmt leaves no format verb on screen when an argument is missing', () => {
  assert.equal(B.fmt('No available times on %s.', []), 'No available times on .');
  assert.equal(B.fmt('No available times on %s.'), 'No available times on .');
  assert.equal(B.fmt('%[3]s missing', ['a']), ' missing');
});

test('fmt leaves a string with no verbs untouched', () => {
  assert.equal(B.fmt('No available times.', ['unused']), 'No available times.');
  assert.equal(B.fmt('Inga lediga tider.'), 'Inga lediga tider.');
});
