# Calnode — agent notes

Go backend (SQLite, `go:embed`s the built SvelteKit SPA) + SvelteKit 5 admin UI
served under `/admin/`. Public booking pages are server-rendered Go templates
(`internal/handler/templates/*.html`), distinct from the Svelte admin app.

## Frontend toolchain

- Vite 8 (Rolldown) · SvelteKit 2 · Svelte 5 · Tailwind v4 (`@tailwindcss/vite`)
  · bits-ui / shadcn-svelte · Vitest 4 browser mode.
- The frontend is embedded at Go compile time (`frontend/embed.go` →
  `//go:embed all:build`). To see frontend changes in the running app: rebuild
  the frontend (`pnpm build` in `frontend/`) **and** rebuild/restart the Go
  binary — restarting Go alone won't pick up new assets.

## UI styling — required check

**Default to shadcn-svelte in the admin UI.** In the SvelteKit admin app (`frontend/`),
build from the existing shadcn-svelte components — `Button`/`buttonVariants`,
`ConfirmDialog` (**never** `window.confirm`/`alert`), `Dialog`, `Input`, `Switch`,
`Tooltip`, etc. Don't hand-roll buttons, modals, or browser-native dialogs. Destructive
actions use `ConfirmDialog` with `destructive`; row actions use a ghost icon button +
`Tooltip` (see `event-types`, `members`, `recordings`). **If shadcn genuinely doesn't fit,
flag it (and the reason) before deviating — don't silently hand-roll.** This does NOT apply
to the public booking templates (`internal/handler/templates/*.html`), `embed.js`, or the
LiveKit room — those are intentionally framework-free (Go templates / vanilla JS, own CSS).

shadcn-svelte components style state via Tailwind `data-*` variants. Bits-ui
states exposed as `data-state="…"` (checked/unchecked/open/closed) need an
`@custom-variant` remap in `frontend/src/app.css` or they render **silently
unstyled** (logic works, visuals don't). See `frontend/TESTING.md`.

**After changing `frontend/src/lib/components/ui/**`, `frontend/src/app.css`, or
the theme — run `pnpm test:visual`** (Vitest browser smoke). Unit tests do NOT
catch this class of bug; only the real-browser computed-style assertions do.

## Booking calendar — THREE surfaces, keep them aligned

The date/time-slot booking calendar exists in **three** places. A change to its
behaviour or markup must usually be made in all three, or they drift:

1. **Booking page** — `internal/handler/templates/book.html` (server-rendered Go template + vanilla JS)
2. **Manage page** — `internal/handler/templates/manage.html` (reschedule flow; same calendar/slots)
3. **Embed widget** — `internal/handler/embed.js` (Shadow-DOM Web Component on customer sites)

- **Styling is shared:** all three load `internal/handler/templates/booking.css`
  (served at `GET /booking.css`; the widget injects it into its shadow root). Change
  visuals **there**, once — don't re-style per surface.
- **Markup + JS are NOT shared** (Go template vs web component): the calendar render,
  slot picking, and the **mobile step-flow** (calendar → slots → form, with Back) are
  implemented separately in each. If you change calendar *behaviour*, update all three.
- Verify on **desktop and mobile** for each surface after touching the calendar.
- **Shared slot logic lives in `internal/handler/assets/booking-logic.js`** (tested with
  `node --test`), not per-surface: day grouping, time formatting, and the taken-slot
  merge. Put anything all three need there rather than writing it three times.
- **Taken/booked slots** (`show_taken_slots`, off by default) are computed by
  `slots.GenerateWithTaken` as the *difference* between a normal pass and one ignoring
  busy, which is what keeps out-of-hours and min-notice starts from being mislabelled as
  booked. Never feed them to MCP or the assistant - `computeSlots(..., includeTaken)`
  makes each caller say. See ARCHITECTURE §8.

## Conversational booking assistant (optional LLM layer)

The "Book by chat" assistant lives on **two** of those surfaces — `book.html` (floating
drawer + inline link) and `embed.js` (inline link only; no floating button, to avoid
colliding with host-site widgets). **Not** on the manage page (reschedule context — a
reschedule chat is deliberately deferred). Server side is one endpoint,
`POST /v1/event-types/{slug}/assistant` (`booking_assistant.go`): an LLM tool-loop that
drives `find_available_slots`/`book` over the **shared deterministic cores** (`computeSlots`,
`createBookingForSlug`) — never re-implement booking logic in the assistant. Invariants:
the LLM does NL→constraints only (never time math), sees only computed availability (never
raw calendar data), and `<think>` reasoning is stripped. Shared `.asst-*` styles are in
`booking.css`; the base prompt (`assistantBaseRules`) is code-owned, admins only append
"Additional instructions". Off by default — `getLLM()` nil → the picker is the fallback.

## Built-in video meetings (LiveKit)

Self-hostable video as a booking location type (`location_type = "livekit"`). **No LiveKit SDK
server-side** — all tokens are hand-signed. The browser room app is **vanilla JS + a vendored
client SDK**, not Svelte.

- **Where it lives:** room UI = `internal/handler/templates/livekit-room.html` +
  `assets/livekit-room.js` (+ vendored `assets/livekit-client.umd.min.js`). Server =
  `internal/livekit/` (`livekit.go` token signing, `admin.go` Twirp/egress) and
  `internal/handler/livekit_room.go` + `livekit_recording.go`. Settings UI =
  `frontend/src/routes/settings/video/`.
- **Three token kinds (don't conflate):** (1) **room token** — opaque HMAC blob in the join URL
  (`{r,e,role}`), carries no LiveKit grant; (2) **access token** — the real LiveKit HS256 JWT the
  SDK joins with (`AccessToken`/`VerifyAccessToken`); (3) **admin token** — short-lived JWT for
  Twirp server APIs.
- **Host authority — `authorizeHost`, NOT just the room token.** A host action is allowed if the
  caller is the **durable host** (`hostRoomOrOwner`: holds a host room token OR is the signed-in
  booking owner) **OR** the **current reassigned host** — proven by verifying their *access token*
  and confirming that identity has `metadata="host"` right now (`ListParticipants`). Clients send
  **both** `t` (room token) and `at` (access token) on every host call. **Reclaim host is
  durable-host-only.** Reassigning only flips metadata, so without the access-token path a temp
  host has the badge but no real power — that gap is exactly what `authorizeHost` closes.
- **Single host:** any host join demotes prior hosts (`demoteOtherHosts` → metadata `"attendee"`);
  the client downgrades only on explicit `"attendee"`, never on a transient/empty metadata event.
- **Recording (Egress):** room-composite → the **Litestream backups bucket** (`LITESTREAM_*` env),
  `recordings/` prefix. **Finalize on stop/end (`finalizeActiveRecording`), do NOT depend on the
  webhook** — `object_key` is set at start so downloads work without it; a startup sweep closes
  orphaned `active` rows. Idempotent guard keys on an `active` row per room.
- **Webhook = single sink** `POST /v1/livekit/webhook` (legacy alias `/v1/livekit/egress-webhook`,
  keep it). LiveKit allows one URL per project, so it receives **all** events; we verify the
  signature and act only on `egress_started/ended/failed` + `room_finished` (everything else is
  200-ACKed and dropped). The **egress lifecycle is the source of truth** for the recording flag.
- **Recording banner** is driven by room **metadata** (`{recording, allowShare}`) + the
  `RoomMetadataChanged` realtime event — it's an in-room overlay, so `showOnly` hides it off the
  room view. **Always `mergeRoomMeta` (read-merge-write), never overwrite** — recording and
  screen-share flags would clobber each other.
- **Attendee screen-share defaults OFF**; host opts in (gear menu). Enforced server-side via
  `canPublishSources` at token mint + live `UpdateParticipant`, not just hidden in the UI.
- Room HTML is served `no-store` and injects `?v=<content-hash>` onto the room JS/SDK assets —
  bump-free cache-busting. After changing the room JS/HTML you still need a frontend-independent
  Go rebuild (these assets are `go:embed`-ed in the handler package, not the SPA).
- **Watch the room JS complexity.** `livekit-room.js` has grown large + stateful (host model,
  single-host, consent, chat, layout, recording) with state in scattered module flags + manual
  DOM updates — several bugs traced to that (stale state, the metadata up/down-grade logic). It's
  fine now, but if it keeps growing the move is NOT "shadcn-ify it" (it's deliberately
  framework-free) — it's tidy-in-place: one state object + a single derive/render, extract the
  pure logic (host/consent state machines) into testable functions. A dedicated tiny Svelte build
  is the last resort, not the first.

## Languages (i18n) - data-driven, adding one touches no code

Public surfaces (book.html, manage.html, embed.js, the four emails, calendar invite
title/description) are translated. **NOT translated: the admin SPA, the LiveKit room**
(`livekit-room.html`/`.js`, ~45 hardcoded strings - it has its own asset pipeline and shares
no string plumbing with the Go templates), and admin-authored content (event names,
descriptions, questions, custom email copy). Locale is resolved per request from
`Accept-Language` + a `?lang=` override + the operator's fallback setting
(`internal/handler/i18n.go`), and the booker's locale is stored on the booking so later
reminders match. Ships `en es fr de it pt nl sv`. Full detail: ARCHITECTURE §23.

**Adding a locale = adding `internal/i18n/locales/<code>.json`.** Nothing else. `init()`
globs the directory; the switcher, the fallback dropdown and the public API payload all read
`SupportedLocales()`. Name the file **BCP-47 canonical** (`pt-BR.json`, never `pt-br.json`).

- Go has no locale date data, so `date_format`, `clock_format`, `dow_short_*` and
  `month_short_*` are **keys in the JSON** (`internal/i18n/datetime.go`), not stdlib calls.
- Three guards police a new file: same-keys, printf-verb parity (uses `fmt` as the oracle),
  and `TestDateTablesMatchCLDR`, which cross-checks the date tables against `Intl` via node.
  Run `go test ./internal/i18n/`.
- Tests use `ja`/`ko` to mean "a language we don't ship". If you add either,
  `assertUnsupported`/`requireUnsupported` fail loudly and tell you what to change.
- **Every non-English locale is an LLM draft with no native review.** Structure is verified;
  wording is not. Say so before anyone markets a language.

## Email - two transports, and the SMTP trap

`internal/mailer` has **two** real transports behind one `Mailer` interface: `smtp.go` and
`resend.go` (HTTPS to `api.resend.com`). **`handler.BuildMailer` is the ONLY place the
choice is made** - boot (`server.go`) and settings-save both call it, so they cannot drift.
The rule: a Resend API key selects HTTPS, else an SMTP host selects SMTP, else `Noop`.

- **Do not turn this into probe-and-fallback.** It was considered and rejected: a probe
  tests reachability at boot rather than at send time, an open TCP port is not a working
  delivery path, and silent switching masks a broken SMTP config while making "which path
  sent this?" unanswerable. Credentials state intent.
- **Why the HTTPS path exists at all:** several platforms (Railway below Pro) block
  outbound SMTP by *dropping* packets. It presents as a hang, then as a credentials
  problem, on every SMTP port, for every provider. Nothing at the SMTP layer fixes it.
- **Keep the dial bounded.** `defaultSMTPTimeout` must be on the dialers (`newDialers`),
  not only on `conn.SetDeadline`, which runs after the dial. Unbounded, a packet-dropping
  host hangs ~2 min and stalls the job queue, which shares the single SQLite connection.
- **`.ics` invites carry `method=REQUEST` in their Content-Type** - that parameter is what
  makes clients show RSVP instead of a file. The Resend path sets `content_type`
  explicitly; if invites ever arrive as plain attachments, look there first
  (`TestICSAttachmentKeepsItsMethodParameter`).
- Adding a secret to email settings: use `storeEmailSecret`, and make the JSON field a
  **pointer** so "omitted" (keep) stays distinguishable from `""` (clear).

## Event types: slot interval is not the duration

`slot_interval_minutes` = how often a booking may **start**. `duration_minutes` = how long it
**runs**. Deliberately independent (a 45-min meeting offered on the hour is valid). New event
types default the interval to the duration; existing ones keep what is stored, so a change of
default is never retroactive. `slots.Generate` refuses a non-positive interval, so create and
update both validate it - an unvalidated `0` yields an event type with no bookable times and
no explanation. Reported as issue #13, where the setting being absent from the admin UI looked
exactly like duration being ignored.

Keep the editor's floor aligned with the API's (`>= 1`). A stricter client-side minimum makes
an event type configured below it via the API unsaveable from the editor, even when the person
is editing an unrelated field.

## Conventions

- `pnpm` (not npm). Use `pnpm exec <tool>` for local binaries.
- Verify changes against the real app, not just builds — this codebase has been
  bitten by CSS that compiles fine but renders wrong.
