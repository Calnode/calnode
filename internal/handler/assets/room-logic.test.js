// Run: node --test internal/handler/assets/room-logic.test.js
const test = require('node:test');
const assert = require('node:assert');
const RL = require('./room-logic.js');

test('amHost — live flag OR current host metadata', () => {
  assert.equal(RL.amHost({ isHost: true }), true);
  assert.equal(RL.amHost({ hostMeta: true }), true);
  assert.equal(RL.amHost({ isHost: false, hostMeta: false }), false);
  assert.equal(RL.amHost({}), false);
});

test('nextIsHost — upgrade on host, downgrade only on explicit attendee', () => {
  assert.equal(RL.nextIsHost(false, 'host'), true);      // promoted/reassigned to host
  assert.equal(RL.nextIsHost(true, 'attendee'), false);  // explicit single-host demote
  assert.equal(RL.nextIsHost(true, ''), true);           // transient/empty must NOT strip host
  assert.equal(RL.nextIsHost(true, undefined), true);
  assert.equal(RL.nextIsHost(false, 'attendee'), false);
});

test('hostUi — durable host sees everything but reclaim', () => {
  const ui = RL.hostUi({ isHost: true, recordingAvailable: true, allowShare: false, hostCapable: true });
  assert.equal(ui.host, true);
  assert.equal(ui.recordVisible, true);
  assert.equal(ui.screenVisible, true);   // host always
  assert.equal(ui.gearVisible, true);
  assert.equal(ui.hostActions, true);
  assert.equal(ui.reclaimVisible, false); // is host, hasn't stepped down
});

test('hostUi — attendee with sharing OFF', () => {
  const ui = RL.hostUi({ isHost: false, hostMeta: false, recordingAvailable: true, allowShare: false, recording: true, consentDecided: false });
  assert.equal(ui.host, false);
  assert.equal(ui.recordVisible, false);
  assert.equal(ui.screenVisible, false);  // attendee + sharing off → no screen button
  assert.equal(ui.gearVisible, false);
  assert.equal(ui.hostActions, false);
  assert.equal(ui.consentPrompt, true);   // recording + not decided + not host
});

test('hostUi — attendee with sharing ON sees the screen button', () => {
  assert.equal(RL.hostUi({ isHost: false, allowShare: true }).screenVisible, true);
});

test('hostUi — recordVisible needs recording AVAILABLE, not just host', () => {
  assert.equal(RL.hostUi({ isHost: true, recordingAvailable: false }).recordVisible, false);
});

test('hostUi — stepped-down owner: gear + reclaim, no active actions', () => {
  const ui = RL.hostUi({ isHost: false, hostMeta: false, hostCapable: true });
  assert.equal(ui.host, false);
  assert.equal(ui.gearVisible, true);
  assert.equal(ui.reclaimVisible, true);
  assert.equal(ui.hostActions, false);
});

test('hostUi — consent not re-prompted once decided', () => {
  assert.equal(RL.hostUi({ isHost: false, recording: true, consentDecided: true }).consentPrompt, false);
});

test('hostUi — host is never consent-prompted (their record click IS consent)', () => {
  assert.equal(RL.hostUi({ isHost: true, recording: true, consentDecided: false }).consentPrompt, false);
});
