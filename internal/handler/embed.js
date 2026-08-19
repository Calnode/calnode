/* Calnode embeddable booking widget.
 *
 * A dependency-free Web Component that renders the booking flow into a Shadow DOM —
 * real HTML in the host page (no iframe), styles encapsulated. It reuses the SAME
 * stylesheet and class names as the server-rendered /book page (loaded via
 * <link href="<base>/booking.css">) so the two never drift; only the responsive
 * pane layout (container-query driven) and a :host reset are widget-specific.
 *
 * Calls the instance's public, CORS-enabled endpoints: /public, /slots, /questions,
 * POST /bookings.
 *
 * Usage:
 *   <script src="https://booking.example.com/embed.js" async></script>
 *   <calnode-booking slug="intro-call"></calnode-booking>        <!-- inline -->
 *   <button data-calnode-popup="intro-call">Book a call</button>  <!-- popup  -->
 */
(function () {
  'use strict';
  if (window.customElements && customElements.get('calnode-booking')) return;

  var SELF = document.currentScript;
  var BASE = SELF ? new URL(SELF.src).origin : window.location.origin;

  var TZ = (Intl.DateTimeFormat().resolvedOptions().timeZone) || 'UTC';
  var STEP_BP = 560; // below this width → step-flow (one view at a time)

  // i18n — the server resolves locale from the browser's own Accept-Language header
  // automatically (fetch() always sends it; not CORS-blocked), so no client-side
  // detection is needed for the default auto-detect path. An explicit lang="" attribute
  // on <calnode-booking> (a host page choosing to force a language) is sent as ?lang= on
  // the /public call, matching the ?lang= override semantics on book.html/manage.html.
  // /public returns the resolved {locale, i18n} once; cached on the instance and reused
  // by every subsequent render, not refetched per-request.
  function langOverride(el) {
    var v = el.getAttribute('lang');
    return v ? v.split('-')[0] : '';
  }
  // t: pure lookup, not a method — mirrors internal/i18n.Locale.T (falls back to the
  // key itself if the string table hasn't loaded yet or the key is missing).
  function t(i18n, key) { return (i18n && i18n[key]) || key; }
  // dowLabels: Monday-first weekday header labels via Intl, matching
  // BookingLogic.dowLabels in booking-logic.js (not literally shared code — this widget
  // doesn't import that module — but the same approach, replacing what used to be a
  // hardcoded English DOW array).
  function dowLabels(locale) {
    var out = [];
    var monday = new Date(Date.UTC(2024, 0, 1));
    for (var i = 0; i < 7; i++) {
      var day = new Date(monday.getTime() + i * 86400000);
      out.push(new Intl.DateTimeFormat(locale || [], { weekday: 'short', timeZone: 'UTC' }).format(day).slice(0, 2));
    }
    return out;
  }

  var SVG_CLOCK = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>';
  var SVG_PIN = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 10c0 7-9 13-9 13s-9-6-9-13a9 9 0 0 1 18 0z"/><circle cx="12" cy="10" r="3"/></svg>';
  var SVG_CARD = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="2" y="5" width="20" height="14" rx="2"/><line x1="2" y1="10" x2="22" y2="10"/></svg>';
  var SVG_PREV = '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="15 18 9 12 15 6"/></svg>';
  var SVG_NEXT = '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="9 18 15 12 9 6"/></svg>';
  var SVG_BACK = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="15 18 9 12 15 6"/></svg>';
  var SVG_CHECK = '<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="#16a34a" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>';
  var SVG_X = '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="6" y1="6" x2="18" y2="18"/><line x1="18" y1="6" x2="6" y2="18"/></svg>';
  var SVG_SPARK = '<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3l1.9 4.8L18.7 9.7l-4.8 1.9L12 16.4l-1.9-4.8L5.3 9.7l4.8-1.9L12 3z"/></svg>';
  var SVG_CHEV2 = '<svg class="asst-link-arrow" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="13 17 18 12 13 7"/><polyline points="6 17 11 12 6 7"/></svg>';

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

  function dayKey(iso) { return new Intl.DateTimeFormat('en-CA', { timeZone: TZ, year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date(iso)); }
  // locale is the resolved server-side locale (this.locale), not the browser's own
  // ([]) — see the matching fix/comment in book.html / internal-docs/i18n-plan.md.
  function timeLabel(iso, locale) { return new Intl.DateTimeFormat(locale || [], { timeZone: TZ, hour: 'numeric', minute: '2-digit' }).format(new Date(iso)); }
  function shortDay(iso, locale) { return new Intl.DateTimeFormat(locale || [], { timeZone: TZ, weekday: 'short', month: 'short', day: 'numeric' }).format(new Date(iso)); }
  function ymd(d) { return d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0') + '-' + String(d.getDate()).padStart(2, '0'); }
  function startOfMonth(d) { return new Date(d.getFullYear(), d.getMonth(), 1); }
  function endOfMonth(d) { return new Date(d.getFullYear(), d.getMonth() + 1, 0); }
  function addMonths(d, n) { return new Date(d.getFullYear(), d.getMonth() + n, 1); }
  function mondayIndex(d) { return (d.getDay() + 6) % 7; }
  function esc(s) { return String(s).replace(/[&<>"]/g, function (c) { return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]; }); }
  // Group host label: "Alex", "Alex & Sam", "Alex, Sam & Jo", "A, B, C & 2 others".
  function hostsLabel(hosts, i18n) {
    function fn(h) { return String(h.name || '').split(' ')[0]; }
    var n = hosts.length;
    if (n === 0) return '';
    if (n === 1) return hosts[0].name || '';
    if (n === 2) return fn(hosts[0]) + ' & ' + fn(hosts[1]);
    if (n === 3) return fn(hosts[0]) + ', ' + fn(hosts[1]) + ' & ' + fn(hosts[2]);
    return fn(hosts[0]) + ', ' + fn(hosts[1]) + ', ' + fn(hosts[2]) + ' & ' + (n - 3) + ' ' + (n - 3 === 1 ? t(i18n, 'other') : t(i18n, 'others'));
  }
  function money(cents, cur) {
    var amt = (cents / 100).toFixed(2);
    var c = (cur || 'usd').toUpperCase();
    var sym = { USD: '$', EUR: '€', GBP: '£', AUD: 'A$', CAD: 'C$', NZD: 'NZ$' }[c];
    return sym ? sym + amt : amt + ' ' + c;
  }

  // Widget-only layer: :host reset, container-query responsive layout (3-pane →
  // letterbox banner → stacked), step-flow visibility, powered footer. The visual
  // primitives all come from the shared booking.css <link>.
  var STYLE = '' +
    ':host{all:initial;display:block;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;color:#111827;line-height:1.5;}' +
    '.wrap{container-type:inline-size;}' +
    '.card{box-shadow:0 1px 3px rgba(0,0,0,.06);}' +
    // Constrained widths: info becomes a compact horizontal header bar (avatar left,
    // host name + event + inline meta right) spanning the top; calendar + right below.
    '@container (max-width:719px){' +
      '.card{flex-wrap:wrap;}' +
      // min-width:0 lets the info pane shrink to the card width so its text wraps
      // instead of overflowing to the right.
      '.info{width:100%;flex-basis:100%;min-width:0;border-right:none;border-bottom:1px solid #e5e7eb;}' +
      '.info-head{display:flex;align-items:center;gap:14px;}' +
      '.info .host-faces{margin-bottom:0;flex-shrink:0;}' +
      '.info .avatar-img,.info .avatar-initials{width:46px;height:46px;margin-bottom:0;font-size:1.05rem;}' +
      '.titlewrap{min-width:0;}' +
      '.info .host-name{margin-bottom:1px;}' +
      '.info .event-name{margin-bottom:0;}' +
      // meta + description align to the pane's left edge (under the avatar). The
      // 2-line clamp is applied by JS (the shared .clamp class) only when overflowing.
      '.info .meta{flex-direction:row;flex-wrap:wrap;gap:5px 14px;margin-top:12px;}' +
      '.info .description{margin-top:6px;overflow-wrap:break-word;}' +
      '.cal-col{border-right:1px solid #e5e7eb;}' +
    '}' +
    // Narrow / mobile: stack the panes; JS shows one at a time (step-flow). The info
    // header stays the horizontal bar (flex-basis reset so it sizes to content).
    '@container (max-width:559px){' +
      '.card{flex-direction:column;flex-wrap:nowrap;}' +
      '.info{flex-basis:auto;}' +
      '.cal-col{border-right:none;border-bottom:1px solid #e5e7eb;}' +
      '.cal-grid{grid-template-columns:repeat(7,1fr);width:100%;}' +
      '.ch,.cd{width:100%;}' +
    '}' +
    // Step-flow: when narrow, show one step at a time. Calendar step keeps the info
    // banner (so you see what you are booking); the slot/form/confirm step shows just
    // the right pane with a back button.
    '.card.step-cal .right-col{display:none;}' +
    '.card.step-right .cal-col{display:none;}' +
    '.card.step-right .info{display:none;}' +
    '.powered{text-align:center;font-size:.6875rem;color:#9ca3af;padding:10px;}' +
    '.powered a{color:#6b7280;text-decoration:none;font-weight:600;}' +
    '.powered a:hover{text-decoration:underline;}' +
    '.loading{padding:48px 24px;color:#6b7280;font-size:.875rem;text-align:center;}' +
    '.infotext{display:block;}' +
    '@media (max-width:560px){:host([data-modal]) .card{min-height:100dvh;border-radius:0;}}';

  function api(path) {
    return fetch(BASE + path, { headers: { 'Accept': 'application/json' } }).then(function (r) {
      if (!r.ok) throw new Error('HTTP ' + r.status);
      return r.json();
    });
  }

  class CalnodeBooking extends HTMLElement {
    connectedCallback() {
      if (this._mounted) return;
      this._mounted = true;
      this.slug = this.getAttribute('slug');
      this.root = this.attachShadow({ mode: 'open' });
      var cssLink = el('link', { rel: 'stylesheet', href: BASE + '/booking.css' });
      // .clamp styling arrives with the stylesheet, so re-measure the description
      // overflow once it loads.
      cssLink.addEventListener('load', this.syncDesc.bind(this));
      this.root.appendChild(cssLink);
      this.root.appendChild(el('style', { text: STYLE }));
      this.wrap = el('div', { class: 'wrap' });
      this.root.appendChild(this.wrap);
      this.state = { month: startOfMonth(new Date()), slotsByDay: {}, day: null, view: 'pick', slot: null };
      this.narrow = false;
      this.cw = 9999;
      this.descExpanded = false;
      // Drive step-flow + description clamp off the widget's own width (not viewport).
      if (window.ResizeObserver) {
        this._ro = new ResizeObserver(function (entries) {
          this.cw = entries[0].contentRect.width;
          var n = this.cw < STEP_BP;
          if (n !== this.narrow) { this.narrow = n; this.applyStep(); }
          this.syncDesc();
        }.bind(this));
        this._ro.observe(this.wrap);
      }
      this.load();
    }
    disconnectedCallback() { if (this._ro) this._ro.disconnect(); }

    async load() {
      this.wrap.innerHTML = '';
      // Not translated: no string table exists yet until /public resolves below — the
      // widget can't know the language before its first network request completes.
      this.wrap.appendChild(el('div', { class: 'loading', text: 'Loading…' }));
      try {
        var lang = langOverride(this);
        var publicPath = '/v1/event-types/' + encodeURIComponent(this.slug) + '/public' + (lang ? '?lang=' + encodeURIComponent(lang) : '');
        var r = await Promise.all([
          api(publicPath),
          api('/v1/event-types/' + encodeURIComponent(this.slug) + '/questions'),
        ]);
        this.info = r[0];
        this.locale = this.info.locale || '';
        this.i18n = this.info.i18n || {};
        this.dow = dowLabels(this.locale);
        this.setAttribute('lang', this.locale || 'en'); // accessibility: announce the resolved language
        this.questions = (r[1] && r[1].items) || [];
        this.ensureAsstDrawer();
        await this.loadMonth();
        this.render();
      } catch (e) {
        this.wrap.innerHTML = '';
        // Same bootstrapping limitation as the "Loading…" text above — if /public itself
        // failed, there's no string table to translate this with.
        this.wrap.appendChild(el('div', { class: 'loading', text: 'Could not load this booking page.' }));
      }
    }

    async loadMonth() {
      var first = this.state.month, last = endOfMonth(first);
      var today = new Date(); today.setHours(0, 0, 0, 0);
      var from = first < today ? today : first;
      try {
        var r = await api('/v1/event-types/' + encodeURIComponent(this.slug) + '/slots?from=' + ymd(from) + '&to=' + ymd(last) + '&tz=' + encodeURIComponent(TZ));
        var by = {};
        (r.slots || []).forEach(function (s) { (by[dayKey(s.start)] = by[dayKey(s.start)] || []).push(s); });
        this.state.slotsByDay = by;
        // Capture the id→host map so the header can narrow to a slot's actual host once
        // one is picked. Avatar URLs come back relative; make them absolute (the widget
        // runs cross-origin to the Calnode instance).
        this.hostMeta = this.hostMeta || {};
        var hm = this.hostMeta;
        Object.keys(r.hosts || {}).forEach(function (id) {
          var m = r.hosts[id] || {}, av = m.avatar_url || '';
          hm[id] = { name: m.name || '', avatar_url: av && av.charAt(0) === '/' ? BASE + av : av };
        });
      } catch (e) { this.state.slotsByDay = {}; }
    }

    infoPane() {
      // Default: show every host the endpoint returns (round-robin: the whole rotation
      // team; fixed/group: the required set), stacked via .face + z-index — same as the
      // native page. Showing only hosts[0] surfaced one person (often one with no
      // availability) over slots that belong to someone else. Once a slot is picked,
      // narrow to that slot's actual assigned host(s), resolved from the id→host map.
      var hosts, sel = this.state.slot;
      if ((this.state.view === 'form' || this.state.view === 'confirm') && sel && sel.host_ids && this.hostMeta) {
        var hm = this.hostMeta;
        hosts = sel.host_ids.map(function (id) { return hm[id]; }).filter(Boolean);
      }
      if (!hosts || !hosts.length) hosts = (this.info.hosts && this.info.hosts.length) ? this.info.hosts : [];
      var faceKids = hosts.map(function (host, i) {
        var z = (hosts.length - i) * 10;
        var inner = host.avatar_url
          ? el('img', { class: 'avatar-img', src: host.avatar_url, alt: host.name || '' })
          : el('span', { class: 'avatar-initials', text: ((host.name || '?')[0] || '?').toUpperCase() });
        return el('span', { class: 'face', style: 'z-index:' + z }, [inner]);
      });
      // info-head = avatar + title (host name + event name). On compact widths the
      // avatar centers against this title only; meta + description sit below, indented
      // to line up under the title. On desktop these wrappers are plain blocks, so the
      // vertical column is unchanged.
      var titleKids = [];
      var label = hostsLabel(hosts);
      if (label) titleKids.push(el('p', { class: 'host-name', text: label }));
      titleKids.push(el('h1', { class: 'event-name', text: this.info.name }));
      var head = el('div', { class: 'info-head' }, [
        el('div', { class: 'host-faces' }, faceKids),
        el('div', { class: 'titlewrap' }, titleKids),
      ]);
      var meta = el('ul', { class: 'meta' }, [
        el('li', { html: SVG_CLOCK + ' ' + this.info.duration_minutes + ' min' }),
        this.info.location_label ? el('li', { html: SVG_PIN + ' ' + esc(this.info.location_label) }) : null,
        this.info.price_cents > 0 ? el('li', { html: SVG_CARD + ' ' + money(this.info.price_cents, this.info.currency) }) : null,
      ]);
      var kids = [head, meta];
      if (this.info.assistant_enabled) {
        var self = this;
        var asstLink = el('button', { class: 'asst-link', type: 'button', html: SVG_SPARK + ' ' + t(this.i18n, 'book_by_chat') + ' ' + SVG_CHEV2 });
        asstLink.addEventListener('click', function () { self.toggleAsst(); });
        kids.push(asstLink);
      }
      if (this.info.description) {
        kids.push(el('div', { class: 'description', text: this.info.description }));
        kids.push(el('button', { class: 'desc-toggle', type: 'button', text: t(this.i18n, 'show_more') }));
      }
      return el('aside', { class: 'info' }, kids);
    }

    // Conversational booking — a drawer appended to the shadow root (so it survives
    // re-renders, which wipe .wrap), opened by the inline "Book by chat" link. No global
    // floating button, to avoid colliding with the host site's own widgets. Uses the same
    // assistant endpoint + shared .asst-* styles as the hosted booking page.
    ensureAsstDrawer() {
      if (this.asstPanel || !this.info || !this.info.assistant_enabled) return;
      var self = this;
      var log = el('div', { class: 'asst-log' }, [
        el('div', { class: 'asst-msg bot', text: t(this.i18n, 'assistant_greeting') }),
      ]);
      var input = el('input', { class: 'asst-input', type: 'text', placeholder: t(this.i18n, 'assistant_input_placeholder'), maxlength: '500', 'aria-label': t(this.i18n, 'assistant_input_aria') });
      var sendBtn = el('button', { class: 'asst-send', type: 'submit', text: t(this.i18n, 'send') });
      var form = el('form', { class: 'asst-row', autocomplete: 'off' }, [input, sendBtn]);
      var closeBtn = el('button', { class: 'asst-close', type: 'button', 'aria-label': t(this.i18n, 'close'), html: SVG_X });
      var headRow = el('div', { class: 'asst-head-row' }, [el('span', { class: 'asst-title', html: SVG_SPARK + ' ' + t(this.i18n, 'book_by_chat') }), closeBtn]);
      // Persistent AI-disclosure notice (EU AI Act Art. 50(1)) — text must match the
      // "assistant_disclosure" key in internal/i18n/locales/en.json; keep in sync on edit.
      var disclosure = el('p', { class: 'asst-disclosure', role: 'note', text: t(this.i18n, 'assistant_disclosure') });
      var head = el('div', { class: 'asst-head' }, [headRow, disclosure]);
      var panel = el('div', { class: 'asst-panel', role: 'dialog', 'aria-label': t(this.i18n, 'book_by_chat') }, [head, log, form]);
      panel.hidden = true;
      this.root.appendChild(panel);
      this.asstPanel = panel; this.asstLog = log; this.asstInput = input; this.asstSend = sendBtn;
      this.asstMessages = []; this.asstBusy = false;
      closeBtn.addEventListener('click', function () { self.toggleAsst(false); });
      form.addEventListener('submit', function (e) { e.preventDefault(); self.asstSubmit(); });
    }

    toggleAsst(force) {
      if (!this.asstPanel) return;
      var show = (force === undefined) ? this.asstPanel.hidden : force;
      this.asstPanel.hidden = !show;
      if (show) this.asstInput.focus();
    }

    asstAdd(text, cls) {
      var d = el('div', { class: 'asst-msg ' + cls, text: text });
      this.asstLog.appendChild(d);
      this.asstLog.scrollTop = this.asstLog.scrollHeight;
      return d;
    }

    async asstSubmit() {
      var self = this;
      var text = (this.asstInput.value || '').trim();
      if (!text || this.asstBusy) return;
      this.asstMessages.push({ role: 'user', content: text });
      this.asstAdd(text, 'user');
      this.asstInput.value = ''; this.asstBusy = true; this.asstSend.disabled = true;
      var typing = this.asstAdd('…', 'asst-typing');
      var botEl = null, booking = null;
      var onEvent = function (obj) {
        if (obj.type === 'token') {
          if (!botEl) { typing.remove(); botEl = self.asstAdd('', 'bot'); }
          botEl.textContent += obj.text;
          self.asstLog.scrollTop = self.asstLog.scrollHeight;
        } else if (obj.type === 'status') {
          typing.textContent = obj.text;
        } else if (obj.type === 'fallback') {
          if (typing.parentNode) typing.remove();
          self.asstAdd(obj.text, 'note');
        } else if (obj.type === 'done') {
          booking = obj.booking || null;
        }
      };
      try {
        var res = await fetch(BASE + '/v1/event-types/' + encodeURIComponent(this.slug) + '/assistant', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', 'Accept': 'text/event-stream' },
          body: JSON.stringify({ messages: this.asstMessages, timezone: TZ, language: this.locale }),
        });
        if (!res.ok || !res.body) throw new Error('http ' + res.status);
        var reader = res.body.getReader(), dec = new TextDecoder(), buf = '';
        while (true) {
          var chunk = await reader.read();
          if (chunk.done) break;
          buf += dec.decode(chunk.value, { stream: true });
          var parts = buf.split('\n\n'); buf = parts.pop();
          for (var i = 0; i < parts.length; i++) {
            var line = parts[i].trim();
            if (line.indexOf('data:') !== 0) continue;
            try { onEvent(JSON.parse(line.slice(5).trim())); } catch (e) {}
          }
        }
        if (typing.parentNode) typing.remove();
        if (botEl && botEl.textContent) {
          this.asstMessages.push({ role: 'assistant', content: botEl.textContent });
          if (booking) botEl.className = 'asst-msg ok';
        }
        if (booking) this.dispatchEvent(new CustomEvent('calnode:booked', { bubbles: true, composed: true, detail: booking }));
      } catch (e) {
        if (typing.parentNode) typing.remove();
        this.asstAdd(t(this.i18n, 'assistant_error'), 'note');
      } finally {
        this.asstBusy = false; this.asstSend.disabled = false; this.asstInput.focus();
      }
    }

    // syncDesc clamps the description to 2 lines (via the shared .clamp class) only
    // when the widget is too narrow for the 3-column layout; the toggle shows only
    // when the clamped text overflows. (Local var renamed toggle, not t — t is the
    // module-level i18n lookup function and would otherwise be shadowed here.)
    syncDesc() {
      var d = this.card && this.card.querySelector('.description');
      var toggle = this.card && this.card.querySelector('.desc-toggle');
      if (!d || !toggle) return;
      if (this.cw > 719) { d.classList.remove('clamp'); toggle.classList.remove('visible'); return; }
      if (this.descExpanded) { d.classList.remove('clamp'); toggle.textContent = t(this.i18n, 'show_less'); toggle.classList.add('visible'); return; }
      d.classList.add('clamp');
      toggle.textContent = t(this.i18n, 'show_more');
      toggle.classList.toggle('visible', d.scrollHeight > d.clientHeight + 1);
    }

    calPane() {
      var self = this, st = this.state, first = st.month;
      var grid = el('div', { class: 'cal-grid' });
      this.dow.forEach(function (d) { grid.appendChild(el('div', { class: 'ch', text: d })); });
      for (var i = 0; i < mondayIndex(first); i++) grid.appendChild(el('div', { class: 'cd', text: '' }));
      var days = endOfMonth(first).getDate(), todayKey = ymd(new Date());
      for (var d = 1; d <= days; d++) {
        var key = ymd(new Date(first.getFullYear(), first.getMonth(), d));
        var has = !!st.slotsByDay[key] && key >= todayKey;
        var cls = 'cd' + (has ? ' available' : '') + (st.day === key ? ' sel' : '') + (key === todayKey ? ' today' : '');
        var btn = el('button', { class: cls, text: String(d) });
        if (!has) btn.disabled = true;
        else btn.addEventListener('click', (function (k) { return function () { self.state.day = k; self.state.view = 'pick'; self.render(); }; })(key));
        grid.appendChild(btn);
      }
      var prev = el('button', { 'aria-label': t(this.i18n, 'prev_month_aria'), html: SVG_PREV });
      prev.disabled = !(startOfMonth(first) > startOfMonth(new Date()));
      prev.addEventListener('click', function () { self.nav(-1); });
      var next = el('button', { 'aria-label': t(this.i18n, 'next_month_aria'), html: SVG_NEXT });
      next.addEventListener('click', function () { self.nav(1); });
      var nav = el('div', { class: 'cal-nav' }, [
        el('span', { class: 'month-label', text: first.toLocaleDateString(this.locale, { month: 'long', year: 'numeric' }) }),
        prev, next,
      ]);
      return el('section', { class: 'cal-col' }, [nav, grid, el('p', { class: 'tz-label', text: t(this.i18n, 'times_shown_in') + TZ })]);
    }

    rightPane() {
      var self = this, st = this.state;
      var inner;
      if (st.view === 'form') inner = this.formView(st.slot);
      else if (st.view === 'confirm') inner = this.confirmView(st.slot);
      else if (st.day) {
        var list = (st.slotsByDay[st.day] || []).slice().sort(function (a, b) { return a.start < b.start ? -1 : 1; });
        var listEl = el('div', { class: 'slots-list' });
        list.forEach(function (s) {
          var b = el('button', { class: 'slot-btn', text: timeLabel(s.start, self.locale) });
          b.addEventListener('click', function () { self.state.slot = s; self.state.view = 'form'; self.render(); });
          listEl.appendChild(b);
        });
        inner = el('div', {}, [el('p', { class: 'slots-header', text: list[0] ? shortDay(list[0].start, self.locale) : '' }), listEl]);
      } else {
        inner = el('p', { class: 'hint', text: t(this.i18n, 'select_day_hint') });
      }
      return el('section', { class: 'right-col' }, [inner]);
    }

    formView(slot) {
      var self = this;
      var back = el('button', { class: 'back-btn', html: SVG_BACK + ' ' + t(this.i18n, 'back') });
      back.addEventListener('click', function () { self.state.view = 'pick'; self.render(); });
      var form = el('form', { novalidate: 'novalidate' });
      var hp = el('input', { type: 'text', tabindex: '-1', autocomplete: 'off' });
      form.appendChild(el('div', { 'aria-hidden': 'true', style: 'position:absolute;left:-5000px;height:0;width:0;overflow:hidden;' }, [hp]));
      var name = el('input', { type: 'text', required: 'required', autocomplete: 'name', placeholder: t(this.i18n, 'name_placeholder') });
      var email = el('input', { type: 'email', required: 'required', autocomplete: 'email', placeholder: t(this.i18n, 'email_placeholder') });
      form.appendChild(el('div', { class: 'field' }, [el('label', { text: t(this.i18n, 'name_label') }), name]));
      form.appendChild(el('div', { class: 'field' }, [el('label', { text: t(this.i18n, 'email_label') }), email]));
      var qInputs = [];
      this.questions.forEach(function (q) {
        var inp, field;
        if (q.type === 'checkbox') {
          inp = el('input', { type: 'checkbox' });
          field = el('div', { class: 'field' }, [el('div', { class: 'field-checkbox' }, [inp, el('label', { html: esc(q.label) + (q.required ? ' <span class="required-star">*</span>' : '') })])]);
        } else if (q.type === 'select') {
          inp = el('select', {}, [el('option', { value: '', text: t(self.i18n, 'choose_option') })].concat((q.options || []).map(function (o) { return el('option', { value: o, text: o }); })));
          if (q.required) inp.required = true;
          field = el('div', { class: 'field' }, [el('label', { html: esc(q.label) + (q.required ? ' <span class="required-star">*</span>' : '') }), inp]);
        } else {
          inp = el('textarea', { rows: '3' });
          if (q.required) inp.required = true;
          field = el('div', { class: 'field' }, [el('label', { html: esc(q.label) + (q.required ? ' <span class="required-star">*</span>' : '') }), inp]);
        }
        form.appendChild(field);
        qInputs.push({ q: q, inp: inp });
      });
      var errBox = el('p', { class: 'form-error' });
      var cta = el('button', { class: 'btn-primary', type: 'submit', text: t(this.i18n, 'confirm_booking') });
      form.appendChild(errBox); form.appendChild(cta);
      form.addEventListener('submit', function (e) {
        e.preventDefault();
        errBox.textContent = '';
        cta.disabled = true; cta.textContent = t(self.i18n, 'confirming');
        var answers = [];
        qInputs.forEach(function (x) {
          var v = x.inp.type === 'checkbox' ? (x.inp.checked ? 'Yes' : '') : x.inp.value;
          if (v) answers.push({ question_id: x.q.id, value: v });
        });
        fetch(BASE + '/v1/bookings', {
          method: 'POST', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ event_type_slug: self.slug, start_at: slot.start, name: name.value.trim(), email: email.value.trim().toLowerCase(), timezone: TZ, language: self.locale, company: hp.value, answers: answers }),
        }).then(function (r) {
          return r.json().then(function (data) { return { ok: r.ok, data: data }; });
        }).then(function (res) {
          // res.data.error (when present) is a raw message from the booking API, not
          // translated yet — see the matching gap noted in book.html.
          if (!res.ok) throw new Error(res.data && res.data.error ? res.data.error : t(self.i18n, 'booking_failed_error'));
          // Paid event types: the server returns a Stripe Checkout URL. Send the visitor
          // there (top window, so it isn't trapped in the host page's frame).
          if (res.data && res.data.checkout_url) { (window.top || window).location.href = res.data.checkout_url; return; }
          self.state.view = 'confirm'; self.render();
          self.dispatchEvent(new CustomEvent('calnode:booked', { bubbles: true, composed: true, detail: res.data }));
        }).catch(function (err) {
          errBox.textContent = err.message || t(self.i18n, 'booking_failed_error');
          cta.disabled = false; cta.textContent = t(self.i18n, 'confirm_booking');
        });
      });
      return el('div', {}, [back, el('p', { class: 'slot-label', text: shortDay(slot.start, this.locale) + ' · ' + timeLabel(slot.start, this.locale) }), form]);
    }

    confirmView(slot) {
      return el('div', {}, [
        el('div', { class: 'confirm-icon', html: SVG_CHECK }),
        el('div', { class: 'confirm-view' }, [
          el('h3', { text: t(this.i18n, 'booking_confirmed') }),
          el('p', { class: 'when', text: shortDay(slot.start, this.locale) + ' · ' + timeLabel(slot.start, this.locale) }),
          el('p', { class: 'sub', text: t(this.i18n, 'confirmation_email_sent') }),
        ]),
      ]);
    }

    nav(delta) {
      this.state.month = addMonths(this.state.month, delta);
      this.state.day = null; this.state.view = 'pick';
      var self = this;
      this.loadMonth().then(function () { self.render(); });
    }

    // applyStep toggles which panes show when narrow (step-flow). Wide = all visible.
    applyStep() {
      if (!this.card) return;
      this.card.classList.remove('step', 'step-cal', 'step-right');
      if (!this.narrow) return;
      this.card.classList.add('step');
      // calendar step = day not yet chosen and not in form/confirm; else right pane.
      var onRight = this.state.view === 'form' || this.state.view === 'confirm' || (this.state.view === 'pick' && this.state.day);
      this.card.classList.add(onRight ? 'step-right' : 'step-cal');
    }

    render() {
      var self = this;
      this.wrap.innerHTML = '';
      this.card = el('div', { class: 'card' }, [this.infoPane(), this.calPane(), this.rightPane()]);
      // In step-flow, a slots/form view needs a back-to-calendar affordance.
      if (this.narrow && (this.state.view === 'pick' && this.state.day)) {
        var rc = this.card.querySelector('.right-col');
        var back = el('button', { class: 'back-btn', html: SVG_BACK + ' ' + t(this.i18n, 'back') });
        back.addEventListener('click', function () { self.state.day = null; self.render(); });
        rc.insertBefore(back, rc.firstChild);
      }
      var toggle = this.card.querySelector('.desc-toggle');
      if (toggle) toggle.addEventListener('click', function () { self.descExpanded = !self.descExpanded; self.syncDesc(); });
      this.wrap.appendChild(this.card);
      this.wrap.appendChild(el('div', { class: 'powered', html: t(this.i18n, 'powered_by') + ' <a href="https://calnode.com" target="_blank" rel="noopener">Calnode</a>' }));
      this.applyStep();
      this.cw = this.wrap.getBoundingClientRect().width || this.cw;
      requestAnimationFrame(function () { self.syncDesc(); });
    }
  }

  customElements.define('calnode-booking', CalnodeBooking);

  // ── popup mode (isolated in its own Shadow DOM so host CSS can't break it) ──
  var POPUP_STYLE = '' +
    ':host{all:initial;}' +
    '*{box-sizing:border-box;}' +
    '.ovl{position:fixed;inset:0;background:rgba(15,23,42,.55);display:flex;align-items:flex-start;justify-content:center;overflow:auto;padding:5vh 16px;}' +
    '.wrap{position:relative;width:100%;max-width:860px;}' +
    '.x{position:absolute;top:14px;right:14px;z-index:2;width:32px;height:32px;border-radius:50%;border:none;background:#fff;box-shadow:0 1px 5px rgba(15,23,42,.2);cursor:pointer;color:#334155;display:flex;align-items:center;justify-content:center;}' +
    '.x:hover{background:#f1f5f9;}' +
    '@media (max-width:560px){.ovl{padding:0;}.wrap{max-width:none;min-height:100%;}}';

  // lang: optional explicit language override, same semantics as the inline
  // <calnode-booking lang=""> attribute — for popup mode there's no persistent element
  // to put it on ahead of time, so it's read off the trigger button instead (see
  // wirePopups) and forwarded here. window.Calnode.openPopup(slug, lang) also accepts
  // it directly for callers driving the popup from their own JS rather than a
  // data-calnode-popup button.
  function openPopup(slug, lang) {
    var hostEl = el('div', {});
    hostEl.setAttribute('style', 'position:fixed;inset:0;z-index:2147483647;');
    var sr = hostEl.attachShadow({ mode: 'open' });
    sr.appendChild(el('style', { text: POPUP_STYLE }));
    var widget = document.createElement('calnode-booking');
    widget.setAttribute('slug', slug);
    widget.setAttribute('data-modal', '');
    if (lang) widget.setAttribute('lang', lang);
    // Not translated: this popup-chrome close button is created synchronously, before
    // the inner <calnode-booking> has loaded and resolved a locale (same bootstrapping
    // gap as the widget's own initial "Loading…" state).
    var close = el('button', { class: 'x', html: SVG_X, 'aria-label': 'Close' });
    var overlay = el('div', { class: 'ovl' }, [el('div', { class: 'wrap' }, [close, widget])]);
    function shut() { hostEl.remove(); document.removeEventListener('keydown', onKey); }
    function onKey(e) { if (e.key === 'Escape') shut(); }
    overlay.addEventListener('click', function (e) { if (e.target === overlay) shut(); });
    close.addEventListener('click', shut);
    document.addEventListener('keydown', onKey);
    sr.appendChild(overlay);
    document.body.appendChild(hostEl);
  }

  function wirePopups(scope) {
    (scope || document).querySelectorAll('[data-calnode-popup]:not([data-calnode-wired])').forEach(function (b) {
      b.setAttribute('data-calnode-wired', '1');
      b.addEventListener('click', function (e) { e.preventDefault(); openPopup(b.getAttribute('data-calnode-popup'), b.getAttribute('lang')); });
    });
  }
  if (document.readyState !== 'loading') wirePopups();
  else document.addEventListener('DOMContentLoaded', function () { wirePopups(); });
  window.Calnode = { openPopup: openPopup, wirePopups: wirePopups };
})();
