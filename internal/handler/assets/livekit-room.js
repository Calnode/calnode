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
      o.value = d.deviceId; o.textContent = d.label || ('Device ' + (i + 1));
      sel.appendChild(o);
    });
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
    label.innerHTML = '<span class="name"></span><span class="mic-off hidden">' + ICON.micOff + '</span>';
    label.querySelector('.name').textContent = name + (isLocal ? ' (you)' : '');
    el.appendChild(video); el.appendChild(camoff); el.appendChild(label);
    $('lk-grid').appendChild(el);
    tiles[identity] = { el: el, video: video, camoff: camoff, label: label, hasVideo: false };
    setCamOff(identity, true);
    return tiles[identity];
  }
  function removeTile(identity) {
    var t = tiles[identity];
    if (t) { t.el.remove(); delete tiles[identity]; }
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

  function attachVideo(identity, track) {
    var t = tiles[identity]; if (!t) return;
    track.attach(t.video); t.hasVideo = true; setCamOff(identity, false);
  }
  function detachVideo(identity, track) {
    var t = tiles[identity]; if (!t) return;
    if (track) track.detach(t.video);
    t.hasVideo = false; setCamOff(identity, true);
  }

  function wireParticipant(p) {
    var t = tileFor(p.identity, p.name || p.identity, false);
    setMicOff(p.identity, !p.isMicrophoneEnabled);
    // Attach any already-subscribed tracks.
    p.trackPublications.forEach(function (pub) {
      if (pub.track) handleTrack(pub.track, pub, p);
    });
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
      })
      .on(RE.Disconnected, function () { showOnly('lk-left'); });

    try {
      await room.connect(data.url, data.token);
    } catch (e) {
      fail('Could not connect to the meeting server.'); return;
    }
    showOnly('lk-room');
    tileFor(room.localParticipant.identity, name, true);
    setMicOff(room.localParticipant.identity, !micOn);

    var camOpts = $('lk-cam').value ? { deviceId: $('lk-cam').value } : undefined;
    var micOpts = $('lk-mic').value ? { deviceId: $('lk-mic').value } : undefined;
    try { await room.localParticipant.setMicrophoneEnabled(micOn, micOpts); } catch (e) {}
    try { await room.localParticipant.setCameraEnabled(camOn, camOpts); } catch (e) {}

    // Existing participants already in the room.
    room.remoteParticipants.forEach(wireParticipant);
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
    $('lk-leave').onclick = function () { if (room) room.disconnect(); };
    paint();
  }

  if (!LK) { fail('Video library failed to load.'); }
  else if (document.readyState !== 'loading') initPrejoin();
  else document.addEventListener('DOMContentLoaded', initPrejoin);
})();
