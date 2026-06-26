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

## Conventions

- `pnpm` (not npm). Use `pnpm exec <tool>` for local binaries.
- Verify changes against the real app, not just builds — this codebase has been
  bitten by CSS that compiles fine but renders wrong.
