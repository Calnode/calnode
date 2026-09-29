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

  // roleFromToken — read the join link's role for DISPLAY HINTS ONLY (the prejoin
  // host-link warning). The payload is signed but not encrypted; the server
  // re-verifies on /token, so a forged role buys nothing. Returns 'host' or ''.
  // Manual base64url decode (no atob/Buffer): this module runs in browsers and
  // node alike and must stay dependency-free.
  var B64URL = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_';
  function b64urlDecode(s) {
    var bits = '', out = '';
    s = String(s).replace(/=+$/, '');
    for (var i = 0; i < s.length; i++) {
      var v = B64URL.indexOf(s.charAt(i));
      if (v < 0) throw new Error('bad char');
      bits += ('000000' + v.toString(2)).slice(-6);
    }
    for (var j = 0; j + 8 <= bits.length; j += 8) {
      out += String.fromCharCode(parseInt(bits.substr(j, 8), 2));
    }
    return decodeURIComponent(escape(out));
  }
  function roleFromToken(tok) {
    if (!tok || typeof tok !== 'string') return '';
    var dot = tok.indexOf('.');
    if (dot < 0) return '';
    try {
      var payload = JSON.parse(b64urlDecode(tok.slice(0, dot)));
      return payload && payload.role === 'host' ? 'host' : '';
    } catch (e) {
      return '';
    }
  }

  // demoteNotice — what to tell ME when my host status just changed. An upgrade
  // needs no words (the controls appearing say it). A downgrade names the new
  // host when known — this is the "where did my settings cog go" moment, almost
  // always because this host link was shared and someone else opened it.
  function demoteNotice(wasHost, isHostNow, otherHostName, canReclaim) {
    if (!wasHost || isHostNow) return null;
    var msg = otherHostName
      ? otherHostName + ' took over as host — you are now attending.'
      : 'Someone else took over as host — you are now attending.';
    if (canReclaim) msg += ' Take host back from the menu if that was a mistake.';
    return msg;
  }

  return { amHost: amHost, nextIsHost: nextIsHost, hostUi: hostUi, roleFromToken: roleFromToken, demoteNotice: demoteNotice };
});
