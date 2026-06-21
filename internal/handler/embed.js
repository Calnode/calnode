/* Calnode embeddable booking widget.
 *
 * A dependency-free Web Component that renders a booking flow into a Shadow DOM —
 * real HTML in the host page (no iframe), with encapsulated styles so neither page
 * breaks the other. It calls the instance's public booking endpoints cross-origin
 * (CORS-enabled): /public, /slots, /questions, POST /bookings.
 *
 * Usage:
 *   <script src="https://booking.example.com/embed.js" async></script>
 *   <calnode-booking slug="intro-call"></calnode-booking>        <!-- inline -->
 *   <button data-calnode-popup="intro-call">Book a call</button>  <!-- popup  -->
 */
(function () {
  'use strict';
  if (window.customElements && customElements.get('calnode-booking')) return;

  // The API base is the origin this script was served from.
  var SELF = document.currentScript;
  var BASE = SELF ? new URL(SELF.src).origin : window.location.origin;

  var TZ = (Intl.DateTimeFormat().resolvedOptions().timeZone) || 'UTC';
  var MONTH_NAMES = ['January','February','March','April','May','June','July','August','September','October','November','December'];
  var DOW = ['Su','Mo','Tu','We','Th','Fr','Sa'];

  function el(tag, attrs, kids) {
    var n = document.createElement(tag);
    if (attrs) for (var k in attrs) {
      if (k === 'class') n.className = attrs[k];
      else if (k === 'text') n.textContent = attrs[k];
      else if (k === 'html') n.innerHTML = attrs[k];
      else n.setAttribute(k, attrs[k]);
    }
    (kids || []).forEach(function (c) { if (c) n.appendChild(c); });
    return n;
  }

  // ── timezone-aware date helpers ───────────────────────────────────────────
  // Day key (YYYY-MM-DD) for a UTC instant, in the booker's timezone.
  function dayKey(iso) {
    var d = new Date(iso);
    var p = new Intl.DateTimeFormat('en-CA', { timeZone: TZ, year: 'numeric', month: '2-digit', day: '2-digit' }).format(d);
    return p; // en-CA → YYYY-MM-DD
  }
  function timeLabel(iso) {
    return new Intl.DateTimeFormat([], { timeZone: TZ, hour: 'numeric', minute: '2-digit' }).format(new Date(iso));
  }
  function ymd(d) {
    return d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0') + '-' + String(d.getDate()).padStart(2, '0');
  }

  var STYLE = '' +
    ':host{all:initial;display:block;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;color:#0f172a;line-height:1.5;}' +
    '*{box-sizing:border-box;}' +
    '.card{border:1px solid #e2e8f0;border-radius:12px;background:#fff;overflow:hidden;max-width:480px;}' +
    '.hd{padding:20px 20px 14px;border-bottom:1px solid #f1f5f9;}' +
    '.logo{max-height:28px;width:auto;margin-bottom:10px;}' +
    '.name{font-size:18px;font-weight:600;margin:0;}' +
    '.meta{color:#64748b;font-size:13px;margin-top:4px;display:flex;gap:12px;flex-wrap:wrap;}' +
    '.desc{color:#475569;font-size:13px;margin-top:8px;white-space:pre-wrap;}' +
    '.bd{padding:16px 20px 20px;}' +
    '.navrow{display:flex;align-items:center;justify-content:space-between;margin-bottom:10px;}' +
    '.navrow b{font-size:14px;}' +
    '.icobtn{border:1px solid #e2e8f0;background:#fff;border-radius:8px;width:32px;height:32px;cursor:pointer;font-size:16px;color:#334155;}' +
    '.icobtn:disabled{opacity:.4;cursor:default;}' +
    '.grid{display:grid;grid-template-columns:repeat(7,1fr);gap:4px;}' +
    '.dow{text-align:center;font-size:11px;color:#94a3b8;padding:4px 0;}' +
    '.day{aspect-ratio:1;border:none;background:#f8fafc;border-radius:8px;cursor:pointer;font-size:13px;color:#0f172a;}' +
    '.day:hover:not(:disabled){background:#eef2ff;}' +
    '.day.empty{background:transparent;cursor:default;}' +
    '.day:disabled{color:#cbd5e1;background:transparent;cursor:default;}' +
    '.day.sel{background:#6366f1;color:#fff;}' +
    '.slots{display:grid;grid-template-columns:repeat(3,1fr);gap:8px;margin-top:4px;max-height:260px;overflow:auto;}' +
    '.slot{border:1px solid #c7d2fe;background:#fff;color:#4338ca;border-radius:8px;padding:9px 4px;font-size:13px;font-weight:500;cursor:pointer;}' +
    '.slot:hover{background:#eef2ff;}' +
    '.back{background:none;border:none;color:#6366f1;cursor:pointer;font-size:13px;padding:0;margin-bottom:10px;}' +
    'label{display:block;font-size:13px;font-weight:500;margin:12px 0 4px;}' +
    'input,textarea,select{width:100%;border:1px solid #cbd5e1;border-radius:8px;padding:9px 10px;font-size:14px;font-family:inherit;color:#0f172a;}' +
    'input:focus,textarea:focus,select:focus{outline:2px solid #c7d2fe;border-color:#6366f1;}' +
    '.hp{position:absolute;left:-9999px;width:1px;height:1px;overflow:hidden;}' +
    '.cta{width:100%;margin-top:16px;background:#6366f1;color:#fff;border:none;border-radius:8px;padding:11px;font-size:14px;font-weight:600;cursor:pointer;}' +
    '.cta:disabled{opacity:.6;cursor:default;}' +
    '.muted{color:#64748b;font-size:13px;}' +
    '.err{color:#dc2626;font-size:13px;margin-top:10px;}' +
    '.ok{text-align:center;padding:14px 4px;}' +
    '.okmark{width:44px;height:44px;border-radius:50%;background:#dcfce7;color:#16a34a;display:flex;align-items:center;justify-content:center;font-size:24px;margin:0 auto 12px;}' +
    '.row{display:flex;align-items:flex-start;gap:8px;}' +
    '.row input[type=checkbox]{width:auto;margin-top:3px;}' +
    '.cl{font:inherit;}' +
    '.powered{text-align:center;font-size:11px;color:#94a3b8;padding:10px;border-top:1px solid #f1f5f9;}';

  function api(path) {
    return fetch(BASE + path, { headers: { 'Accept': 'application/json' } }).then(function (r) {
      if (!r.ok) throw new Error('HTTP ' + r.status);
      return r.json();
    });
  }

  function CalnodeBookingProto() {}

  class CalnodeBooking extends HTMLElement {
    connectedCallback() {
      if (this._mounted) return;
      this._mounted = true;
      this.slug = this.getAttribute('slug');
      this.root = this.attachShadow({ mode: 'open' });
      this.root.appendChild(el('style', { text: STYLE }));
      this.body = el('div', { class: 'card' });
      this.root.appendChild(this.body);
      this.state = { month: startOfMonth(new Date()), slotsByDay: {}, day: null };
      this.load();
    }

    async load() {
      this.render(el('div', { class: 'bd muted', text: 'Loading…' }));
      try {
        var r = await Promise.all([api('/v1/event-types/' + encodeURIComponent(this.slug) + '/public'),
                                   api('/v1/event-types/' + encodeURIComponent(this.slug) + '/questions')]);
        this.info = r[0];
        this.questions = (r[1] && r[1].items) || [];
        await this.loadMonth();
        this.renderCalendar();
      } catch (e) {
        this.render(el('div', { class: 'bd err', text: 'Could not load this booking page.' }));
      }
    }

    header() {
      var kids = [];
      if (this.info.logo_url) kids.push(el('img', { class: 'logo', src: this.info.logo_url, alt: this.info.business_name || '' }));
      kids.push(el('p', { class: 'name', text: this.info.name }));
      var meta = el('div', { class: 'meta' }, [
        el('span', { text: '⏱ ' + this.info.duration_minutes + ' min' }),
        this.info.location_label ? el('span', { text: '📍 ' + this.info.location_label }) : null,
      ]);
      kids.push(meta);
      if (this.info.description) kids.push(el('div', { class: 'desc', text: this.info.description }));
      return el('div', { class: 'hd' }, kids);
    }

    render(inner) {
      this.body.innerHTML = '';
      // The header/footer are built from fetched info; loading + error states
      // render before that's available, so guard on it.
      if (this.info) this.body.appendChild(this.header());
      this.body.appendChild(inner);
      if (this.info) this.body.appendChild(el('div', { class: 'powered', html: 'Powered by <b>' + (this.info.business_name ? esc(this.info.business_name) : 'Calnode') + '</b>' }));
    }

    async loadMonth() {
      var first = this.state.month;
      var last = endOfMonth(first);
      var today = new Date(); today.setHours(0, 0, 0, 0);
      var from = first < today ? today : first;
      try {
        var r = await api('/v1/event-types/' + encodeURIComponent(this.slug) + '/slots?from=' + ymd(from) + '&to=' + ymd(last) + '&tz=' + encodeURIComponent(TZ));
        var by = {};
        (r.slots || []).forEach(function (s) { (by[dayKey(s.start)] = by[dayKey(s.start)] || []).push(s); });
        this.state.slotsByDay = by;
      } catch (e) { this.state.slotsByDay = {}; }
    }

    renderCalendar() {
      var st = this.state, self = this;
      var first = st.month;
      var grid = el('div', { class: 'grid' });
      DOW.forEach(function (d) { grid.appendChild(el('div', { class: 'dow', text: d })); });
      var lead = first.getDay();
      for (var i = 0; i < lead; i++) grid.appendChild(el('div', { class: 'day empty' }));
      var days = endOfMonth(first).getDate();
      var todayKey = ymd(new Date());
      for (var d = 1; d <= days; d++) {
        var dt = new Date(first.getFullYear(), first.getMonth(), d);
        var key = ymd(dt);
        var has = !!st.slotsByDay[key];
        var past = key < todayKey;
        var btn = el('button', { class: 'day' + (st.day === key ? ' sel' : ''), text: String(d) });
        if (!has || past) { btn.disabled = true; }
        else btn.addEventListener('click', (function (k) { return function () { self.state.day = k; self.renderSlots(); }; })(key));
        grid.appendChild(btn);
      }
      var canPrev = startOfMonth(first) > startOfMonth(new Date());
      var prev = el('button', { class: 'icobtn', text: '‹' });
      prev.disabled = !canPrev;
      prev.addEventListener('click', function () { self.state.month = addMonths(self.state.month, -1); self.state.day = null; self.loadMonth().then(function () { self.renderCalendar(); }); });
      var next = el('button', { class: 'icobtn', text: '›' });
      next.addEventListener('click', function () { self.state.month = addMonths(self.state.month, 1); self.state.day = null; self.loadMonth().then(function () { self.renderCalendar(); }); });
      var nav = el('div', { class: 'navrow' }, [prev, el('b', { text: MONTH_NAMES[first.getMonth()] + ' ' + first.getFullYear() }), next]);
      this.render(el('div', { class: 'bd' }, [nav, grid, el('div', { class: 'muted', html: '&nbsp;' }), el('div', { class: 'muted', text: 'Times shown in ' + TZ })]));
    }

    renderSlots() {
      var self = this, list = this.state.slotsByDay[this.state.day] || [];
      list.sort(function (a, b) { return a.start < b.start ? -1 : 1; });
      var back = el('button', { class: 'back', text: '‹ Back to calendar' });
      back.addEventListener('click', function () { self.state.day = null; self.renderCalendar(); });
      var slots = el('div', { class: 'slots' });
      list.forEach(function (s) {
        var b = el('button', { class: 'slot', text: timeLabel(s.start) });
        b.addEventListener('click', function () { self.renderForm(s); });
        slots.appendChild(b);
      });
      var heading = new Intl.DateTimeFormat([], { timeZone: TZ, weekday: 'long', month: 'long', day: 'numeric' }).format(new Date(list[0] ? list[0].start : this.state.day));
      this.render(el('div', { class: 'bd' }, [back, el('b', { text: heading }), slots]));
    }

    renderForm(slot) {
      var self = this;
      var back = el('button', { class: 'back', text: '‹ Back' });
      back.addEventListener('click', function () { self.renderSlots(); });
      var when = new Intl.DateTimeFormat([], { timeZone: TZ, weekday: 'long', month: 'long', day: 'numeric', hour: 'numeric', minute: '2-digit' }).format(new Date(slot.start));
      var form = el('form', {});
      var name = el('input', { type: 'text', required: 'required', autocomplete: 'name', placeholder: 'Your name' });
      var email = el('input', { type: 'email', required: 'required', autocomplete: 'email', placeholder: 'you@example.com' });
      form.appendChild(el('label', { text: 'Name' })); form.appendChild(name);
      form.appendChild(el('label', { text: 'Email' })); form.appendChild(email);
      var qInputs = [];
      this.questions.forEach(function (q) {
        var inp;
        if (q.type === 'checkbox') {
          inp = el('input', { type: 'checkbox' });
          form.appendChild(el('div', { class: 'row' }, [inp, el('label', { class: 'cl', text: q.label + (q.required ? ' *' : '') })]));
        } else if (q.type === 'select') {
          inp = el('select', {}, [el('option', { value: '', text: 'Select…' })].concat((q.options || []).map(function (o) { return el('option', { value: o, text: o }); })));
          if (q.required) inp.required = true;
          form.appendChild(el('label', { text: q.label + (q.required ? ' *' : '') })); form.appendChild(inp);
        } else {
          inp = el('input', { type: 'text' });
          if (q.required) inp.required = true;
          form.appendChild(el('label', { text: q.label + (q.required ? ' *' : '') })); form.appendChild(inp);
        }
        qInputs.push({ q: q, inp: inp });
      });
      // honeypot
      var hp = el('input', { type: 'text', class: 'hp', tabindex: '-1', autocomplete: 'off' });
      var hpWrap = el('div', { class: 'hp' }, [hp]); hpWrap.setAttribute('aria-hidden', 'true');
      form.appendChild(hpWrap);
      var errBox = el('div', { class: 'err' });
      var cta = el('button', { class: 'cta', type: 'submit', text: 'Confirm booking' });
      form.appendChild(cta); form.appendChild(errBox);
      form.addEventListener('submit', function (e) {
        e.preventDefault();
        errBox.textContent = '';
        cta.disabled = true; cta.textContent = 'Booking…';
        var answers = [];
        qInputs.forEach(function (x) {
          var v = x.inp.type === 'checkbox' ? (x.inp.checked ? 'Yes' : '') : x.inp.value;
          if (v) answers.push({ question_id: x.q.id, value: v });
        });
        fetch(BASE + '/v1/bookings', {
          method: 'POST', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ event_type_slug: self.slug, start_at: slot.start, name: name.value.trim(), email: email.value.trim().toLowerCase(), timezone: TZ, company: hp.value, answers: answers }),
        }).then(function (r) {
          return r.json().then(function (data) { return { ok: r.ok, data: data }; });
        }).then(function (res) {
          if (!res.ok) throw new Error(res.data && res.data.error ? res.data.error : 'Could not complete booking.');
          self.renderConfirm(slot, res.data);
          self.dispatchEvent(new CustomEvent('calnode:booked', { bubbles: true, composed: true, detail: res.data }));
        }).catch(function (err) {
          errBox.textContent = err.message || 'Could not complete booking.';
          cta.disabled = false; cta.textContent = 'Confirm booking';
        });
      });
      this.render(el('div', { class: 'bd' }, [back, el('div', { class: 'muted', text: when }), form]));
    }

    renderConfirm(slot, booking) {
      var when = new Intl.DateTimeFormat([], { timeZone: TZ, weekday: 'long', month: 'long', day: 'numeric', hour: 'numeric', minute: '2-digit' }).format(new Date(slot.start));
      var loc = (booking && booking.location_value) || this.info.location_label || '';
      this.render(el('div', { class: 'bd' }, [
        el('div', { class: 'ok' }, [
          el('div', { class: 'okmark', text: '✓' }),
          el('b', { text: 'Booking confirmed' }),
          el('div', { class: 'muted', text: this.info.name }),
          el('div', { class: 'muted', text: when + ' (' + TZ + ')' }),
          loc ? el('div', { class: 'muted', text: loc }) : null,
        ]),
      ]));
    }
  }

  function startOfMonth(d) { return new Date(d.getFullYear(), d.getMonth(), 1); }
  function endOfMonth(d) { return new Date(d.getFullYear(), d.getMonth() + 1, 0); }
  function addMonths(d, n) { return new Date(d.getFullYear(), d.getMonth() + n, 1); }
  function esc(s) { return String(s).replace(/[&<>"]/g, function (c) { return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]; }); }

  customElements.define('calnode-booking', CalnodeBooking);

  // ── popup mode ──────────────────────────────────────────────────────────────
  function openPopup(slug) {
    var overlay = el('div', {});
    overlay.setAttribute('style', 'position:fixed;inset:0;background:rgba(15,23,42,.5);display:flex;align-items:flex-start;justify-content:center;padding:5vh 16px;z-index:2147483647;overflow:auto;');
    var close = el('button', { text: '✕' });
    close.setAttribute('style', 'position:absolute;top:16px;right:16px;width:36px;height:36px;border-radius:50%;border:none;background:#fff;font-size:18px;cursor:pointer;');
    var widget = document.createElement('calnode-booking');
    widget.setAttribute('slug', slug);
    function shut() { overlay.remove(); }
    overlay.addEventListener('click', function (e) { if (e.target === overlay) shut(); });
    close.addEventListener('click', shut);
    overlay.appendChild(close); overlay.appendChild(widget);
    document.body.appendChild(overlay);
  }
  function wirePopups(scope) {
    (scope || document).querySelectorAll('[data-calnode-popup]:not([data-calnode-wired])').forEach(function (b) {
      b.setAttribute('data-calnode-wired', '1');
      b.addEventListener('click', function (e) { e.preventDefault(); openPopup(b.getAttribute('data-calnode-popup')); });
    });
  }
  if (document.readyState !== 'loading') wirePopups();
  else document.addEventListener('DOMContentLoaded', function () { wirePopups(); });
  window.Calnode = { openPopup: openPopup, wirePopups: wirePopups };
})();
