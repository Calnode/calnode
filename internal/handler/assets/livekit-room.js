/* Calnode built-in video room — vanilla JS over the LiveKit browser SDK (global LivekitClient).
 *
 * Flow: read the opaque room token (?t) from the URL → prejoin (name + camera/mic preview +
 * device pick) → POST /v1/livekit/token to exchange it for a real LiveKit access token →
 * connect, publish, and render a participant grid with mic/cam/screen-share/leave controls.
 * Scope is the solid core: no chat/recording (added later). */
(function () {
  'use strict';
  var LK = window.LivekitClient;
  var $ = function (id) { return document.getElementById(id); };

  var ICON = {
    mic: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 1a3 3 0 0 0-3 3v8a3 3 0 0 0 6 0V4a3 3 0 0 0-3-3z"/><path d="M19 10v2a7 7 0 0 1-14 0v-2"/><line x1="12" y1="19" x2="12" y2="23"/></svg>',
    micOff: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="1" y1="1" x2="23" y2="23"/><path d="M9 9v3a3 3 0 0 0 5.12 2.12M15 9.34V4a3 3 0 0 0-5.94-.6"/><path d="M17 16.95A7 7 0 0 1 5 12v-2m14 0v2a7 7 0 0 1-.11 1.23"/><line x1="12" y1="19" x2="12" y2="23"/></svg>',
    cam: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M23 7l-7 5 7 5V7z"/><rect x="1" y="5" width="15" height="14" rx="2" ry="2"/></svg>',
    camOff: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M16 16v1a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2h2m5.66 0H14a2 2 0 0 1 2 2v3.34l1 1L23 7v10"/><line x1="1" y1="1" x2="23" y2="23"/></svg>',
    screen: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="3" width="20" height="14" rx="2"/><line x1="8" y1="21" x2="16" y2="21"/><line x1="12" y1="17" x2="12" y2="21"/></svg>',
    layout: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2"/><rect x="3" y="15" width="6" height="6" rx="1" fill="currentColor"/><rect x="11" y="15" width="6" height="6" rx="1" fill="currentColor"/></svg>',
    chat: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z"/></svg>',
    record: '<svg viewBox="0 0 24 24" fill="currentColor"><circle cx="12" cy="12" r="7"/></svg>',
    shareLock: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="3" width="20" height="13" rx="2"/><line x1="8" y1="21" x2="16" y2="21"/><circle cx="12" cy="9.5" r="2"/><path d="M12 11.5v1.5"/></svg>',
  };

  function showOnly(id) {
    ['lk-prejoin', 'lk-room', 'lk-left', 'lk-error'].forEach(function (s) {
      $(s).classList.toggle('hidden', s !== id);
    });
  }
  function fail(msg) {
    if (msg) $('lk-error-msg').textContent = msg;
    showOnly('lk-error');
  }
  function status(msg) {
    var el = $('lk-status');
    if (!msg) { el.classList.add('hidden'); return; }
    el.textContent = msg; el.classList.remove('hidden');
  }
  function initial(name) { return (name || '?').trim().charAt(0).toUpperCase() || '?'; }

  // ----- Prejoin -----
  var roomToken = new URLSearchParams(location.search).get('t');
  var previewTrack = null;
  var camOn = true, micOn = true;

  async function listDevices() {
    try {
      var devices = await navigator.mediaDevices.enumerateDevices();
      fillSelect($('lk-cam'), devices.filter(function (d) { return d.kind === 'videoinput'; }));
      fillSelect($('lk-mic'), devices.filter(function (d) { return d.kind === 'audioinput'; }));
    } catch (e) { /* labels need permission; ignore */ }
  }
  function fillSelect(sel, devs) {
    sel.innerHTML = '';
    devs.forEach(function (d, i) {
      var o = document.createElement('option');
      o.value = d.deviceId; o.textContent = cleanLabel(d.label) || ('Device ' + (i + 1));
      sel.appendChild(o);
    });
  }
  // Browsers tack on noisy hardware detail — USB vendor:product IDs, Windows "Default - " /
  // "Communications - " role prefixes, and enumeration indices like "(3- …)". Strip them for a
  // clean picker (e.g. "Default - Microphone (3- AT2020 USB ) (17a0:0002)" → "AT2020 USB").
  function cleanLabel(label) {
    var s = (label || '')
      .replace(/\s*\([0-9a-fA-F]{4}:[0-9a-fA-F]{4}\)\s*$/, '') // trailing USB vendor:product id
      .replace(/^(Default|Communications)\s*-\s*/i, '')         // Windows audio role prefix
      .replace(/^(Microphone|Camera|Speaker)\s*\(\s*\d*-?\s*(.+?)\s*\)\s*$/i, '$2') // "Microphone (3- AT2020 USB )" → "AT2020 USB"
      .replace(/\s{2,}/g, ' ')
      .trim();
    return s;
  }

  async function startPreview() {
    stopPreview();
    if (!camOn) { $('lk-preview-off').classList.remove('hidden'); return; }
    $('lk-preview-off').classList.add('hidden');
    try {
      var opts = {};
      if ($('lk-cam').value) opts.deviceId = $('lk-cam').value;
      previewTrack = await LK.createLocalVideoTrack(opts);
      previewTrack.attach($('lk-preview'));
      await listDevices(); // labels now available
    } catch (e) {
      camOn = false; syncToggle($('lk-pre-cam'), camOn, 'Camera');
      $('lk-preview-off').textContent = 'Camera unavailable';
      $('lk-preview-off').classList.remove('hidden');
    }
  }
  function stopPreview() {
    if (previewTrack) { previewTrack.detach(); previewTrack.stop(); previewTrack = null; }
  }
  function syncToggle(btn, on, label) {
    btn.textContent = label + (on ? ' on' : ' off');
    btn.classList.toggle('off', !on);
  }

  function initPrejoin() {
    if (!roomToken) { fail('This meeting link is missing its access token.'); return; }
    syncToggle($('lk-pre-cam'), camOn, 'Camera');
    syncToggle($('lk-pre-mic'), micOn, 'Mic');
    $('lk-pre-cam').onclick = function () { camOn = !camOn; syncToggle($('lk-pre-cam'), camOn, 'Camera'); startPreview(); };
    $('lk-pre-mic').onclick = function () { micOn = !micOn; syncToggle($('lk-pre-mic'), micOn, 'Mic'); };
    $('lk-cam').onchange = startPreview;
    $('lk-join').onclick = join;
    try { $('lk-name').value = localStorage.getItem('calnode_name') || ''; } catch (e) {}
    startPreview();
  }

  // ----- Room -----
  var room = null;
  var tiles = {}; // identity -> { el, video, camoff }
  var myName = 'Guest';
  var layoutMode = 'grid';   // 'grid' | 'speaker'
  var pinnedId = null;       // identity manually pinned to the stage (speaker mode)
  var activeSpeakerId = null;
  var chatOpen = false, unread = 0;
  var isHost = false; // set from the token role; can change if host is reassigned to us
  var canRecord = false, recording = false;
  var canScreenShare = true, allowShare = true; // me / attendees-in-general

  // applyRoomMeta reflects shared room state (recording + screen-share permission) to everyone:
  // the recording banner + button, and whether non-hosts may see the screen-share button.
  function applyRoomMeta() {
    if (!room) return;
    var meta = {};
    try { meta = JSON.parse(room.metadata || '{}'); } catch (e) {}
    recording = !!meta.recording;
    $('lk-rec-banner').classList.toggle('hidden', !recording);
    var btn = $('lk-record-btn');
    if (btn) { btn.classList.toggle('recording', recording); btn.title = recording ? 'Stop recording' : 'Record meeting'; }

    allowShare = meta.allowShare !== false; // default allowed
    var host = amHost();
    var sc = $('lk-screen');
    if (sc && !host) sc.classList.toggle('hidden', !allowShare); // non-hosts lose the button when off
    var sp = $('lk-shareperm-btn');
    if (sp) {
      sp.classList.toggle('off', !allowShare);
      sp.title = allowShare ? 'Attendee screen-share: on' : 'Attendee screen-share: off';
    }
  }
  async function toggleSharePerm() {
    await postLK('room/screenshare', { allow: !allowShare });
    // The room-metadata change drives applyRoomMeta; reflect optimistically too.
    allowShare = !allowShare; applyRoomMeta();
  }
  async function toggleRecord() {
    var btn = $('lk-record-btn'); if (btn) btn.disabled = true;
    await postLK(recording ? 'record/stop' : 'record/start');
    if (btn) btn.disabled = false;
    // The room-metadata change will drive the banner/button via applyRoomMeta.
  }

  // relayout places tiles into the grid, or (speaker mode) a big stage + a filmstrip. Moving a
  // tile's element between containers via appendChild preserves its playing <video>, so no track
  // re-attach is needed. The stage shows the pinned tile, else the active speaker, else the first.
  function relayout() {
    var grid = $('lk-grid'), stage = $('lk-stage'), strip = $('lk-strip');
    var ids = Object.keys(tiles);
    if (layoutMode === 'grid' || !ids.length) {
      grid.classList.remove('hidden'); stage.classList.add('hidden'); strip.classList.add('hidden');
      ids.forEach(function (id) { grid.appendChild(tiles[id].el); });
      return;
    }
    grid.classList.add('hidden'); stage.classList.remove('hidden'); strip.classList.remove('hidden');
    var focus = (pinnedId && tiles[pinnedId]) ? pinnedId
      : (activeSpeakerId && tiles[activeSpeakerId]) ? activeSpeakerId : ids[0];
    ids.forEach(function (id) { (id === focus ? stage : strip).appendChild(tiles[id].el); });
  }

  // togglePin: click a tile → spotlight it; click the spotlighted tile again → back to grid.
  function togglePin(id) {
    if (layoutMode === 'grid') { layoutMode = 'speaker'; pinnedId = id; }
    else if (pinnedId === id) { layoutMode = 'grid'; pinnedId = null; }
    else { pinnedId = id; }
    paintLayoutBtn(); relayout();
  }
  function toggleLayout() {
    layoutMode = layoutMode === 'grid' ? 'speaker' : 'grid';
    pinnedId = null;
    paintLayoutBtn(); relayout();
  }
  function paintLayoutBtn() {
    var b = $('lk-layout-btn'); if (!b) return;
    b.innerHTML = ICON.layout;
    b.classList.toggle('active', layoutMode === 'speaker');
  }

  // ----- Ephemeral chat (LiveKit data channel — peer-to-peer, never stored) -----
  function onData(payload, participant) {
    try {
      var msg = JSON.parse(new TextDecoder().decode(payload));
      if (msg && msg.t === 'chat' && msg.text) {
        addMsg(msg.name || (participant && participant.name) || 'Guest', String(msg.text), false);
      }
    } catch (e) { /* ignore non-chat data */ }
  }
  function addMsg(who, text, mine) {
    var empty = $('lk-chat-empty'); if (empty) empty.remove();
    var el = document.createElement('div'); el.className = 'msg' + (mine ? ' me' : '');
    var w = document.createElement('div'); w.className = 'who'; w.textContent = mine ? 'You' : who;
    var t = document.createElement('div'); t.textContent = text;
    el.appendChild(w); el.appendChild(t);
    var box = $('lk-chat-msgs'); box.appendChild(el); box.scrollTop = box.scrollHeight;
    if (!chatOpen && !mine) { unread++; paintChatBadge(); }
  }
  function sendChat(text) {
    text = (text || '').trim();
    if (!text || !room) return;
    var data = new TextEncoder().encode(JSON.stringify({ t: 'chat', name: myName, text: text }));
    try { room.localParticipant.publishData(data, { reliable: true }); } catch (e) {}
    addMsg('You', text, true);
  }
  function setChat(open) {
    chatOpen = open;
    $('lk-chat').classList.toggle('hidden', !open);
    $('lk-chat-btn').classList.toggle('active', open);
    if (open) { unread = 0; paintChatBadge(); $('lk-chat-input').focus(); }
  }
  function paintChatBadge() {
    var btn = $('lk-chat-btn'); if (!btn) return;
    var b = btn.querySelector('.badge'); if (!b) return;
    // A simple red dot — presence of unread, not a count.
    b.classList.toggle('hidden', unread === 0);
  }

  // ----- Host controls: leave / end-for-all / reassign -----
  function amHost() {
    return isHost || (room && room.localParticipant && room.localParticipant.metadata === 'host');
  }
  function leaveOrPrompt() {
    // Non-hosts just leave. Hosts always get the modal (end / pass host / just leave); the
    // pass-host option only appears when there's someone else to hand off to.
    if (!amHost()) { if (room) room.disconnect(); return; }
    var others = room ? Array.from(room.remoteParticipants.values()) : [];
    var sel = $('lk-reassign-sel'); sel.innerHTML = '';
    others.forEach(function (p) {
      var o = document.createElement('option'); o.value = p.identity; o.textContent = p.name || 'Participant';
      sel.appendChild(o);
    });
    $('lk-reassign-wrap').classList.toggle('hidden', others.length === 0);
    $('lk-leave-modal').classList.remove('hidden');
  }
  function closeLeaveModal() { $('lk-leave-modal').classList.add('hidden'); }
  // postLK POSTs to /v1/livekit/<path> with the opaque room token (the host capability).
  async function postLK(path, extra) {
    try {
      var res = await fetch('/v1/livekit/' + path, {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(Object.assign({ t: roomToken }, extra || {}))
      });
      return res.ok;
    } catch (e) { return false; }
  }
  async function endForAll() { closeLeaveModal(); await postLK('room/end'); if (room) room.disconnect(); }
  async function reassignAndLeave() {
    closeLeaveModal();
    var id = $('lk-reassign-sel').value;
    if (id) await postLK('room/reassign-host', { identity: id });
    if (room) room.disconnect();
  }

  function tileFor(identity, name, isLocal) {
    if (tiles[identity]) return tiles[identity];
    var el = document.createElement('div');
    el.className = 'tile' + (isLocal ? ' local' : '');
    var video = document.createElement('video');
    video.autoplay = true; video.playsInline = true; if (isLocal) video.muted = true;
    var camoff = document.createElement('div');
    camoff.className = 'camoff';
    camoff.innerHTML = '<div class="avatar">' + initial(name) + '</div>';
    var label = document.createElement('div');
    label.className = 'label';
    label.innerHTML = '<span class="host-badge hidden">Host</span><span class="name"></span><span class="mic-off hidden">' + ICON.micOff + '</span>';
    label.querySelector('.name').textContent = name + (isLocal ? ' (you)' : '');
    el.appendChild(video); el.appendChild(camoff); el.appendChild(label);
    el.addEventListener('click', function () { togglePin(identity); });
    $('lk-grid').appendChild(el);
    tiles[identity] = { el: el, video: video, camoff: camoff, label: label, hasVideo: false };
    setCamOff(identity, true);
    return tiles[identity];
  }
  function removeTile(identity) {
    var t = tiles[identity];
    if (t) { t.el.remove(); delete tiles[identity]; }
    if (pinnedId === identity) { pinnedId = null; }
    relayout();
  }
  function setCamOff(identity, off) {
    var t = tiles[identity]; if (!t) return;
    t.camoff.classList.toggle('hidden', !off);
    t.video.classList.toggle('hidden', off);
  }
  function setMicOff(identity, off) {
    var t = tiles[identity]; if (!t) return;
    t.label.querySelector('.mic-off').classList.toggle('hidden', !off);
  }
  function setHostBadge(identity, on) {
    var t = tiles[identity]; if (!t) return;
    t.label.querySelector('.host-badge').classList.toggle('hidden', !on);
  }

  function attachVideo(identity, track) {
    var t = tiles[identity]; if (!t) return;
    track.attach(t.video); t.hasVideo = true; setCamOff(identity, false);
    // A screen share must not be mirrored (the .local tile mirrors the selfie camera).
    t.el.classList.toggle('screen', track.source === LK.Track.Source.ScreenShare);
  }
  function detachVideo(identity, track) {
    var t = tiles[identity]; if (!t) return;
    if (track) track.detach(t.video);
    t.hasVideo = false; setCamOff(identity, true);
  }

  function wireParticipant(p) {
    var t = tileFor(p.identity, p.name || p.identity, false);
    setMicOff(p.identity, !p.isMicrophoneEnabled);
    setHostBadge(p.identity, p.metadata === 'host');
    // Attach any already-subscribed tracks.
    p.trackPublications.forEach(function (pub) {
      if (pub.track) handleTrack(pub.track, pub, p);
    });
    relayout();
    return t;
  }

  function handleTrack(track, pub, participant) {
    if (track.kind === 'video') {
      attachVideo(participant.identity, track);
    } else if (track.kind === 'audio' && !participant.isLocal) {
      var a = document.createElement('audio');
      a.autoplay = true; track.attach(a); document.body.appendChild(a);
    }
  }

  async function join() {
    var name = ($('lk-name').value || '').trim();
    if (!name) { $('lk-name').focus(); return; }
    myName = name;
    try { localStorage.setItem('calnode_name', name); } catch (e) {}
    $('lk-join').disabled = true; $('lk-join').textContent = 'Joining…';

    var data;
    try {
      var res = await fetch('/v1/livekit/token', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ t: roomToken, name: name })
      });
      data = await res.json();
      if (!res.ok) throw new Error(data && data.error ? data.error : 'Could not get a meeting token.');
    } catch (e) {
      stopPreview(); fail(e.message); return;
    }
    stopPreview();
    isHost = !!(data && data.role === 'host');
    canRecord = !!(data && data.can_record);
    canScreenShare = !(data && data.can_screenshare === false); // default true
    allowShare = !(data && data.allow_share === false);

    room = new LK.Room({ adaptiveStream: true, dynacast: true });
    var RE = LK.RoomEvent;
    room
      .on(RE.TrackSubscribed, handleTrack)
      .on(RE.TrackUnsubscribed, function (track, pub, p) { if (track.kind === 'video') detachVideo(p.identity, track); })
      .on(RE.TrackMuted, function (pub, p) { if (pub.kind === 'video') setCamOff(p.identity, true); if (pub.kind === 'audio') setMicOff(p.identity, true); })
      .on(RE.TrackUnmuted, function (pub, p) { if (pub.kind === 'video') setCamOff(p.identity, false); if (pub.kind === 'audio') setMicOff(p.identity, false); })
      .on(RE.LocalTrackPublished, function (pub) { if (pub.track && pub.track.kind === 'video') attachVideo(room.localParticipant.identity, pub.track); })
      .on(RE.LocalTrackUnpublished, function (pub) { if (pub.source === LK.Track.Source.ScreenShare) reattachLocalCamera(); })
      .on(RE.ParticipantConnected, wireParticipant)
      .on(RE.ParticipantDisconnected, function (p) { removeTile(p.identity); })
      .on(RE.ActiveSpeakersChanged, function (speakers) {
        var ids = {}; speakers.forEach(function (s) { ids[s.identity] = true; });
        Object.keys(tiles).forEach(function (id) { tiles[id].el.classList.toggle('speaking', !!ids[id]); });
        if (speakers.length) activeSpeakerId = speakers[0].identity;
        // In speaker mode with nothing pinned, follow whoever's talking.
        if (layoutMode === 'speaker' && !pinnedId) relayout();
      })
      .on(RE.DataReceived, onData)
      .on(RE.RoomMetadataChanged, applyRoomMeta)
      .on(RE.ParticipantMetadataChanged, function (prev, participant) {
        if (!room || !participant) return;
        var m = participant.metadata;
        // Reflect the host badge for whoever this is (newly-promoted host, or a demoted one).
        setHostBadge(participant.identity, m === 'host');
        if (participant.identity === room.localParticipant.identity) {
          // Upgrade on "host" (reassigned/promoted); downgrade only on the explicit "attendee"
          // demote (single-host takeover). A transient/empty value must NOT strip our controls.
          if (m === 'host') isHost = true;
          else if (m === 'attendee') isHost = false;
          var rb = $('lk-record-btn');
          if (rb) rb.classList.toggle('hidden', !(isHost && canRecord));
        }
      })
      .on(RE.Disconnected, function () { closeLeaveModal(); showOnly('lk-left'); });

    try {
      await room.connect(data.url, data.token);
    } catch (e) {
      fail('Could not connect to the meeting server.'); return;
    }
    showOnly('lk-room');
    tileFor(room.localParticipant.identity, name, true);
    setMicOff(room.localParticipant.identity, !micOn);
    setHostBadge(room.localParticipant.identity, amHost());

    var camOpts = $('lk-cam').value ? { deviceId: $('lk-cam').value } : undefined;
    var micOpts = $('lk-mic').value ? { deviceId: $('lk-mic').value } : undefined;
    try { await room.localParticipant.setMicrophoneEnabled(micOn, micOpts); } catch (e) {}
    try { await room.localParticipant.setCameraEnabled(camOn, camOpts); } catch (e) {}

    // Existing participants already in the room.
    room.remoteParticipants.forEach(wireParticipant);
    relayout();
    setupControls();
  }

  function reattachLocalCamera() {
    if (!room) return;
    var pub = room.localParticipant.getTrackPublication(LK.Track.Source.Camera);
    if (pub && pub.track) attachVideo(room.localParticipant.identity, pub.track);
    else detachVideo(room.localParticipant.identity, null);
  }

  function setupControls() {
    var lp = room.localParticipant;
    var micBtn = $('lk-mic-btn'), camBtn = $('lk-cam-btn'), screenBtn = $('lk-screen');
    var paint = function () {
      micBtn.innerHTML = lp.isMicrophoneEnabled ? ICON.mic : ICON.micOff;
      micBtn.classList.toggle('off', !lp.isMicrophoneEnabled);
      camBtn.innerHTML = lp.isCameraEnabled ? ICON.cam : ICON.camOff;
      camBtn.classList.toggle('off', !lp.isCameraEnabled);
      screenBtn.innerHTML = ICON.screen;
      screenBtn.classList.toggle('active', lp.isScreenShareEnabled);
      setCamOff(lp.identity, !lp.isCameraEnabled);
      setMicOff(lp.identity, !lp.isMicrophoneEnabled);
    };
    micBtn.onclick = async function () { await lp.setMicrophoneEnabled(!lp.isMicrophoneEnabled); paint(); };
    camBtn.onclick = async function () { await lp.setCameraEnabled(!lp.isCameraEnabled); paint(); };
    screenBtn.onclick = async function () {
      try { await lp.setScreenShareEnabled(!lp.isScreenShareEnabled); } catch (e) {}
      paint();
    };
    $('lk-layout-btn').onclick = toggleLayout;
    $('lk-chat-btn').innerHTML = ICON.chat + '<span class="badge hidden"></span>';
    paintChatBadge();
    $('lk-chat-btn').onclick = function () { setChat(!chatOpen); };
    $('lk-chat-close').onclick = function () { setChat(false); };
    $('lk-chat-form').onsubmit = function (e) { e.preventDefault(); var inp = $('lk-chat-input'); sendChat(inp.value); inp.value = ''; };
    $('lk-leave').onclick = leaveOrPrompt;
    $('lk-end-all').onclick = endForAll;
    $('lk-reassign-leave').onclick = reassignAndLeave;
    $('lk-just-leave').onclick = function () { if (room) room.disconnect(); };
    $('lk-leave-cancel').onclick = closeLeaveModal;
    if (canRecord) {
      var rb = $('lk-record-btn');
      rb.innerHTML = ICON.record; rb.classList.remove('hidden'); rb.onclick = toggleRecord;
    }
    if (isHost) {
      var sp = $('lk-shareperm-btn');
      sp.innerHTML = ICON.shareLock; sp.classList.remove('hidden'); sp.onclick = toggleSharePerm;
    }
    applyRoomMeta(); // reflect recording + screen-share state already set
    paintLayoutBtn();
    paint();
  }

  if (!LK) { fail('Video library failed to load.'); }
  else if (document.readyState !== 'loading') initPrejoin();
  else document.addEventListener('DOMContentLoaded', initPrejoin);
})();
