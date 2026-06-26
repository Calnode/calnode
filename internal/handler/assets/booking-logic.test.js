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

test('formatHostsLabel — 1/2/3/overflow', () => {
  assert.equal(B.formatHostsLabel([]), '');
  assert.equal(B.formatHostsLabel([{ name: 'Alex' }]), 'Alex');
  assert.equal(B.formatHostsLabel([{ name: 'Alex' }, { name: 'Sam' }]), 'Alex & Sam');
  assert.equal(B.formatHostsLabel([{ name: 'Alex' }, { name: 'Sam' }, { name: 'Jo' }]), 'Alex, Sam & Jo');
  assert.equal(
    B.formatHostsLabel([{ name: 'A' }, { name: 'B' }, { name: 'C' }, { name: 'D' }, { name: 'E' }]),
    'A, B, C & 2 others'
  );
  assert.equal(B.formatHostsLabel(['Alex', 'Sam']), 'Alex & Sam'); // plain strings too
});

test('formatTime / formatDay respect tz', () => {
  const iso = '2026-06-15T02:00:00Z';
  assert.equal(B.formatTime(iso, 'UTC', 'en-US'), '2:00 AM');
  // NY (UTC-4) → prev day, June 14
  assert.match(B.formatDay(iso, 'America/New_York', 'short', 'en-US'), /Jun 14/);
  assert.match(B.formatDay(iso, 'America/New_York', 'long', 'en-US'), /June 14/);
});
