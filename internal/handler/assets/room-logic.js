// room-logic.js — the PURE decision logic for the LiveKit room (no DOM, no SDK), extracted so it
// can be unit-tested (see room-logic.test.js). It's served concatenated ahead of livekit-room.js
// (so `RoomLogic` is a page global there) and is also require()-able by the node tests. This is
// the fragile host/consent/screen-share logic that previously lived inline and caused bugs.
(function (root, factory) {
  if (typeof module === 'object' && module.exports) module.exports = factory();
  else root.RoomLogic = factory();
})(typeof self !== 'undefined' ? self : this, function () {
  // amHost — I'm the host if my live flag says so, OR my room metadata is "host" right now.
  function amHost(s) {
    return !!(s.isHost || s.hostMeta);
  }

  // nextIsHost — how a ParticipantMetadataChanged updates MY host status. Upgrade on "host"
  // (reassigned/promoted), downgrade only on the explicit "attendee" demote; a transient/empty
  // value must NOT change it (that flake is what stripped a host's controls before).
  function nextIsHost(cur, metadata) {
    if (metadata === 'host') return true;
    if (metadata === 'attendee') return false;
    return cur;
  }

  // hostUi — derive EVERY host/consent/screen-share UI flag from one state snapshot. Pure: the
  // caller applies these booleans to the DOM. Keeps the "what shows when" rules in one tested place.
  function hostUi(s) {
    var host = amHost(s);
    return {
      host: host,
      recordVisible: host && !!s.recordingAvailable, // host + instance can record
      screenVisible: host || !!s.allowShare,         // host always; attendees only when allowed
      gearVisible: !!s.hostCapable || host,          // host now, OR owner who can reclaim
      hostActions: host,                             // share toggle + make-host (active host only)
      reclaimVisible: !!s.hostCapable && !host,      // stepped-down owner
      consentPrompt: !!s.recording && !s.consentDecided && !host // attendee acknowledges recording
    };
  }

  return { amHost: amHost, nextIsHost: nextIsHost, hostUi: hostUi };
});
