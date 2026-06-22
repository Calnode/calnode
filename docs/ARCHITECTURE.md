# Calnode — Architecture

Status: living doc. The source of truth is the code; this explains how the pieces
fit and *why*. File references point at packages/symbols (`internal/...`). New to the
codebase? Start here, then see [CONTRIBUTING.md](../CONTRIBUTING.md) for build/test.

---

## 1. What Calnode is

A self-hostable scheduling/booking app (Calendly-style) shipped as a **single Go
binary**. The Go server embeds the built SvelteKit admin SPA at compile time and
also serves the public, server-rendered booking pages. Persistence is **SQLite**.
Primary focus: **self-hosting**; the longer-term direction is instance-per-tenant
managed hosting (foundational pieces — envelope crypto, host split, readiness gate —
are already in place; see §16).

One process, one file (`data/calnode.db`), no external services required to run
(SMTP and Google are optional integrations configured at runtime).

---

## 2. Topology — two frontends, one binary

Deliberate split (see CLAUDE.md):

| Surface | Tech | Served at | Why |
|---|---|---|---|
| **Admin app** | SvelteKit 2 SPA (Svelte 5, Vite 8, Tailwind 4, shadcn-svelte/bits-ui) | `/admin/*` | Rich, authenticated, many interactive pages → a framework SPA earns its weight. Embedded via `//go:embed all:build` (`frontend/embed.go`). |
| **Public pages** | Server-rendered Go `html/template` + vanilla JS | `/book/{slug}`, `/manage/{token}` | A booker clicking an email link should get instant first paint and a tiny payload — hand-written HTML beats shipping a framework runtime. |
| **Favicon** | single embedded `favicon.svg` | `/favicon.svg`, `/favicon.ico`, `/admin/favicon.svg` | One source (`frontend.FaviconHandler`) shared by all pages. |

Consequence/debt: two styling systems (Tailwind/shadcn in the SPA; hand-written
CSS in the Go templates). The two Go templates (`book.html`, `manage.html`) are
kept visually in sync by hand — `manage.html` mirrors `book.html`'s styles +
calendar/slot JS. **If you change one calendar, change both.**

Frontend is embedded at **compile time**. To see frontend changes in the running
app you must `pnpm build` in `frontend/` **and** rebuild/restart the Go binary
(`make build` does both). Restarting Go alone won't pick up new assets.

---

## 3. Process, config, startup

- Entry: `cmd/calnode/main.go`. Sibling CLI tools: `recover_key.go`, `rotate_key.go`,
  `reset_admin.go` (key escrow recovery, KEK rotation, admin reset).
- Config: `internal/config` — env-driven. Key vars:
  - `PORT` (3000), `DATABASE_URL` (`sqlite://./data/calnode.db`)
  - `BASE_URL` — identity host (OAuth callbacks, admin UI, invites)
  - `PUBLIC_BASE_URL` — booker-facing host (booking links, emails); defaults to BASE_URL. The split lets a tenant put the team on a custom domain (`book.acme.com`) while OAuth/admin stay on the identity host (see §16).
  - `CALNODE_ENCRYPTION_KEY` (platform secret / KEK input), `CALNODE_RECOVERY_SECRET` (escrow)
  - `SMTP_*` and `GOOGLE_CLIENT_ID/SECRET` (also settable at runtime in DB settings, which take priority)
  - `MICROSOFT_CLIENT_ID/SECRET` and `MICROSOFT_TENANT` (default `common`; use the
    multi-tenant `common` so any work/personal Microsoft account can connect/sign in)
  - `COOKIE_SECURE` (defaults true when BASE_URL is https)
- Startup (`internal/server/server.go: New`): open DB → run goose migrations →
  open keyvault (unwrap DEK) → configure mailer (DB settings override env) → start
  webhook/reminder **worker** → load Google creds (DB > env) → build one
  `calendar.Service` and **register every configured provider** (Google, Microsoft) →
  wire up the matching **OAuth login** providers (Google and/or Microsoft) → if any
  calendar is configured, **start the calendar reconciler** → register routes →
  return handler + a `drain` func.
- Ops endpoints: `GET /healthz`, `GET /readyz` (readiness gate), `GET /version`
  (build stamp from `internal/buildinfo`).
- Graceful shutdown drains the worker and in-flight requests before `db.Close()`.

---

## 4. Persistence (SQLite) — and the single-connection rule

- `internal/db`: opens SQLite with **`SetMaxOpenConns(1)`** + `SetMaxIdleConns(1)`,
  **WAL** journal mode, `busy_timeout=5000`. One writer connection by design.
- Migrations: **goose** SQL files in `internal/db/migrations/` (00001→00029). Run
  automatically on startup. `ALTER TABLE ADD COLUMN` is reversible-by-convention
  only (SQLite can't easily drop columns).

### ⚠️ The single-connection gotcha (bit us once)

With one connection, **never run a query while a `rows` cursor from the same pool
is still open** (i.e. inside a `for rows.Next()` loop). The open cursor holds the
only connection; the inner query waits for a connection that never frees →
deadlock until context deadline, surfacing as a confusing `context deadline
exceeded` (not "database is locked"). **Pattern:** read the cursor fully into a
slice, close it, then loop. See `Handler.assignedHosts`, the calendar reconciler,
`Reschedule`. (Memory: `sqlite-single-connection`.)

Bonus property: because booking transactions serialize on the single connection,
the app-level overlap check reliably guards **all** hosts (not just the one the
partial unique index covers) — no TOCTOU between concurrent bookings.

### Data model (key tables)

- **Identity/authz:** `users` (+ `is_admin`, `is_owner`, `archived_at`,
  `archived_by`, plus prefs `time_format`/`week_start`/`date_format`, seven
  `notify_*` toggles, and auth columns `email_login`/`password_hash`/`provider`/
  `provider_id`), `sessions` (cookie auth), `api_keys` (SHA-256 hashed),
  `invite_tokens` (hashed, single-use). **There is no `auth_providers` table** —
  OAuth identity is columns on `users` (migration 00012).
- **Teams:** `teams`, `team_members` (with `routing_priority`).
- **Event types:** `event_types` (+ `routing_mode` — CHECK has **four** values:
  fixed | round_robin | collective | priority; `rr_strategy`; `max_active_bookings`
  (per-invitee cap, enforced); `seat_limit` (group/class — column exists but is
  **not enforced**); `team_id` — vestigial for routing), `event_type_hosts`
  (the host-roles table), `event_type_questions` (intake form),
  `event_type_reminders` (per-ET `hours_before`, UNIQUE).
- **Availability:** `availability_rules` (weekly), `availability_overrides` (dated).
- **Bookings:** `bookings` (primary `host_id`, `external_event_id`, status),
  `booking_hosts` (every attending host + `is_primary` + per-host
  `external_event_id`), `booking_attendees` (organizer + invitees),
  `booking_answers`, `booking_manage_tokens` (PK is the hashed token; TTL is
  app-level, set by `IssueManageToken`, not a schema constraint).
  Note: `bookings.host_id` and `booking_hosts.user_id` carry **no ON DELETE
  clause** (NO ACTION), and `users.archived_by` is a bare TEXT column (no FK) —
  which is *why* member offboarding is archive, not delete.
- **Integrations/secrets:** `calendar_connections` (per-user OAuth tokens, encrypted;
  `provider` = `google`|`microsoft`, `is_destination`/`check_conflicts` roles, and
  `account_kind` work|personal from migration 00032),
  `server_settings` (a **singleton** row id=1 holding SMTP + Google creds —
  encrypted `smtp_pass_enc`, `google_client_secret_enc`), `crypto_keystore` (wrapped
  DEK + recovery escrow), `webhooks` + `webhook_deliveries`, `jobs` (worker queue).

All timestamps are **UTC**. Times are rendered in the host's / booker's local tz
at the edges only.

---

## 5. Security & crypto (envelope encryption)

`internal/keyvault` + `internal/secret`:

- A random **DEK** (AES-256) encrypts secret columns via **AES-256-GCM** (random
  12-byte nonce). The encrypted columns are **five values across four tables**:
  `server_settings.smtp_pass_enc`, `server_settings.google_client_secret_enc`,
  `calendar_connections.access_token_enc` + `refresh_token_enc`, and
  `webhooks.secret_enc` (webhook signing secret).
- The DEK is stored **wrapped** in `crypto_keystore`, encrypted by a **KEK** =
  `Argon2id(CALNODE_ENCRYPTION_KEY)` — params **64 MiB / 3 iterations / 2 lanes**,
  16-byte salt; the wrap is AES-256-GCM. A second wrapped copy is escrowed under
  `CALNODE_RECOVERY_SECRET` — but **only if that env was set at first boot**.
- On startup the vault unwraps the DEK ("DEK unwrapped from keystore (primary)").
- CLI tools: `rotate_key` re-wraps the **same DEK** under a new platform secret
  (it does **not** rotate the DEK or re-encrypt columns); `recover_key` unwraps via
  the recovery escrow; `reset_admin` is a bcrypt password reset (not part of the
  crypto subsystem).
- **Dev caveat:** with no `CALNODE_ENCRYPTION_KEY` and a non-https BASE_URL, the
  vault generates an **ephemeral per-process DEK** (so any persisted `*_enc` data is
  unreadable across restarts). Production (https BASE_URL) hard-fails without the key.

So the DB at rest contains only ciphertext + a wrapped key; losing the DB without
the platform/recovery secret doesn't expose secrets.

---

## 6. Auth, sessions, roles

- **API keys** (`cno_…`, **SHA-256**-hashed in `api_keys`) and **browser sessions**
  (cookie `calnode_session`, HttpOnly/SameSite=Lax/Secure-when-https, **30-day**,
  stored in `sessions`) both satisfy `RequireAuth` (`internal/handler/auth.go`).
  Note: a *present-but-invalid* API key is rejected outright — it does NOT fall
  through to the session path (confused-deputy guard).
- **Login paths:** **Google OAuth** (`/v1/auth/login` → `/v1/auth/callback`),
  **Microsoft OAuth** (`/v1/auth/microsoft/login` → `/v1/auth/microsoft/callback`),
  and **email+password** (`email_login` + bcrypt). Both OAuth flows are identity-only
  (Google `openid email profile`; Microsoft `openid email profile User.Read` — calendar
  access is a *separate* connection with its own scopes), share helpers in
  `auth_oauth.go` (`newOAuthState`/`verifyOAuthState`/`finishOAuthLogin`), map a user
  **by email**, and **cannot create users** (unknown email → `no_account`). Microsoft
  needs its own redirect URI registered in Azure (`/v1/auth/microsoft/callback`),
  distinct from the calendar one. (Magic-link/OTP is a planned alternative; the
  shipped fallback is password auth.) The actual user-creation path for non-first
  users is **invites** (`invite_tokens`), not OAuth self-registration.
- **CSRF:** cookie sessions are `SameSite=Lax` (blocks the classic cross-site write),
  plus a `SameOriginCheck` middleware (`internal/server`) that rejects a state-changing
  request whose Origin/Referer host ≠ the request Host — but only when the
  `calnode_session` cookie is present, so public booking, API-key, and manage-token
  requests are untouched. API-key auth isn't CSRF-able (custom header).
- **Roles:** Member / Admin / Owner. `is_owner` is additive over `is_admin`.
  Owner-gated actions: grant/revoke admin, transfer ownership. Admins can cancel
  any booking, see all bookings, manage teams/members. Safe-removal + archive
  guards prevent orphaning.
- **Offboarding = archive** (`users.archived_at`), never hard-delete — preserves
  bookings, event-type ownership, team links. Archived ⇒ no login, hidden from
  lists, skipped in routing/slots, event types deactivated. Reversible (restore).
  Archiving is blocked while the member has upcoming (primary-host) bookings; a
  resolve-meetings flow makes the admin reassign/cancel each first.

---

## 7. Routing — the host-roles model

An event type owns a **host list** (`event_type_hosts`): each row = (user, role,
priority), role ∈ **required | rotation | optional**. The editor authors these
roles through **two plain questions** rather than a mode picker — *who can host?*
(just me / specific people) and, for people, *how are they staffed?* (rotate /
everyone attends). `routing_mode` is **derived** from the two answers, never set
directly (`frontend/src/routes/event-types/[slug]/+page.svelte`):

| Q1 | Q2 | `routing_mode` | Roles written |
|---|---|---|---|
| Just me | — | `fixed` | owner → `required` |
| Specific people | Rotate | `round_robin` | each → `rotation` (+ `rr_strategy`) |
| Specific people | Everyone attends | `collective` | each → `required`; per-person **Optional** toggle → `optional` (join-if-free) |

Everyone is `required` by default in the "Everyone attends" branch, so the common
case has no extra knobs; flipping a person to **Optional** is the only refinement.
The old "fixed host inside a rotation" combo (a `required` host alongside a
rotation pool) is no longer authorable from the UI — the engine still supports it,
but no editor path writes it.

**The one rule** — a slot is offered when: all `required` hosts free **AND** (if a
rotation pool exists) ≥1 rotation host free. At booking time the assignment is: all
required + one rotation pick (by strategy) + every free optional; always ≥1 attendee.

`rr_strategy`: `even` (least-loaded — fewest upcoming confirmed via this event
type; `leastLoadedHost`), `priority` (lowest priority number free first), `soonest`
(falls back to even at assignment — the slot is already fixed). Free rotation
candidates stay in priority order; `pickRotationHost` switches on strategy
(`internal/booking/service.go`).

Note: the `routing_mode` column actually carries **four** values — the schema also
defines a distinct `priority` mode alongside `fixed`/`round_robin`/`collective`
(ranked single-host) — though the two-question editor only ever writes the three in
the table; ranked selection within a rotation is expressed via `rr_strategy =
priority`.

Teams are just a one-click way to populate the host list; `event_types.team_id` is
vestigial for routing. The resolver (`resolveEventTypeHosts`) excludes archived
members.

---

## 8. Slot generation

- Engine: `internal/slots/generate.go`. Input: `[]HostAvailability` (rules,
  overrides, busy intervals, **Role**), `EventConfig` (duration, interval, buffers,
  min-notice, max-future, `RoutingMode`), date range, booker tz, injectable `Now`.
- Computes per-host free windows per UTC day, intersects/subtracts busy, aligns to
  the slot interval, then `pickHosts` decides per-slot which hosts to surface:
  - `collective`: all hosts must be free → return all.
  - `round_robin`: every **required** (fixed) host free, **and** one free rotation
    host (priority order) → return required + the pick. No rotation pick available
    ⇒ not offered (kept consistent with booking-time assignment, which always needs
    a rotation pick).
  - `fixed`/default: the single host if free.
- Handler: `internal/handler/slots_handler.go: GetSlots`. Resolves the host pool by
  mode (role-tagged), then loads each host's availability **concurrently**
  (goroutines) — the slow part is one Google free/busy round-trip per host, so
  parallelizing turns N sequential calls into ~one call's latency. DB queries
  serialize on the single connection (fast); only the network overlaps. Response
  includes a `hosts` metadata map (id→name/avatar) for rendering faces.
- **Busy** for a host = every non-cancelled booking they attend, via
  `booking_hosts` (NOT just `bookings.host_id`) — so a non-primary Group/fixed seat
  also blocks their availability and prevents cross-event double-booking.
- **Own-event exclusion (§6.2):** Calnode's own events also surface in Google
  free/busy, so the handler subtracts them (`slots.SubtractIntervals`) — the DB is
  the source of truth for Calnode bookings. The cut set is this host's bookings with
  a non-empty `booking_hosts.external_event_id` (any status). This de-duplicates
  confirmed bookings and, crucially, frees a slot whose booking was *cancelled* but
  whose Google event hasn't been deleted yet — without waiting for the reconciler.
- **Timezone boundary:** slots are generated per host-local day but bookings are
  stored UTC; a morning slot for a +UTC host maps to the previous UTC day. The busy
  fetch window is widened ±1–2 days so it isn't missed (regression-tested in
  `slots_busy_test.go`).

### Client calendar perf (book.html / manage.html)

- **Optimistic render:** the calendar paints immediately treating every in-window
  day as available/clickable; when the month's availability returns it re-renders
  to grey out only days with zero slots. No blocking on the network for first paint.
- **Month-slot cache:** the month-availability request already returns *every* slot
  for the month — it's cached grouped by day (`slotsByDay`), so clicking a day reads
  from memory (instant, zero extra requests). Falls back to a single-day fetch if
  clicked before the month load lands. Timezone change rebuilds the cache.

---

## 9. Booking lifecycle

`internal/booking/service.go` (transactions) + `internal/handler/booking_handler.go`
(HTTP + async side effects).

- **Create:** in one transaction — overlap-check each candidate (required all free;
  rotation pick one free; optional join if free) via the `booking_hosts`-join
  predicate; enforce the per-invitee active-booking cap (by email, inside the txn);
  insert `bookings` (host_id = primary) + a `booking_hosts` row per attendee
  (`is_primary` flag) + attendee + answers. Double-book is guarded by both the
  app-level check (serialized) and a partial unique index on (host_id, start_at).
  HTTP 201 returns immediately; **side effects run in a background goroutine**:
  per-host Google Calendar event creation (store `external_event_id` per host),
  per-host confirmation emails (by prefs), attendee confirmation, webhook, reminders.
  **Idempotency (opt-in):** an `Idempotency-Key` header reserves the request
  (`idempotency_keys`, migration 00024); a retry with the same key + body replays
  the original 201 verbatim instead of creating a duplicate (agent/retry safety),
  a key reused with a *different* body → 422, and the key is released on any failure
  so a genuine retry can proceed. Worker purges keys after 24h (§13). The public
  booking page sends no key, so this path is inert for normal bookings.
- **Public-surface abuse controls:** the `/slots` endpoint is rate-limited (60/min/IP,
  `slotsRL`) and `POST /v1/bookings` (20/min/IP, `bookingRL`); a per-email hourly cap
  (`maxBookingsPerEmailPerHour`) backstops rotating-IP spam; and the booking form has a
  hidden **honeypot** (`company`) that rejects bots. Booker email *verification* is
  intentionally absent — it would need a pending-booking state (a deliberate non-goal).
- **Cancel:** `CancelBooking` (admin) and `CancelByToken` (manage link) share
  `Handler.cancelSideEffects` — loops `booking_hosts`, cancels each host's calendar
  event by its stored id, notifies each host + the attendee (attendee "With:" = the
  primary host), fires the webhook. On a successful delete it **clears
  `external_event_id`** (matching the reconciler) so the freed slot is immediately
  re-offerable and own-event exclusion stops treating it as ours.
- **Reschedule:** `RescheduleBooking` (admin) + `RescheduleByToken` share
  `moveCalendarEvents` — `Service.UpdateEvent` moves **every** assigned host's event to
  the new time. The overlap re-check covers **all** the booking's hosts, not just
  the primary.
- **Reassign:** `ReassignHost` swaps the primary host (archive/resolve flow), moves
  the calendar event old→new, and keeps `booking_hosts` in sync so later
  cancel/notify fan-out targets the right person.

Side effects are **best-effort** (a mail/calendar failure must not roll back the
committed booking) — which is why the reconciler (§11) exists.

---

## 10. Calendar integration (provider abstraction)

Calnode talks to calendars through a **provider abstraction**, not a single vendor:

- **`internal/calendar`** defines the `Provider` interface (Name, InvitesGuests,
  AuthURL/EncryptState/Exchange, Connected/Disconnect/HasDestination, FreeBusy,
  CreateEvent/UpdateEvent/CancelEvent) and a **`Service`** that dispatches per-user:
  it reads `calendar_connections.provider` for the user and routes the call to the
  right backend. Booking/slot/reconciler code talks only to `*calendar.Service` —
  never a concrete provider.
- **Providers:** `internal/gcal` (Google) and `internal/calendar/microsoft`
  (Microsoft 365 / Outlook via Graph). Both implement `Provider`. One `Service` is
  built at startup (`internal/server`) and each configured backend is `Register`ed.
- **One calendar per user.** On a successful connect the callback calls
  `Service.RetainOnly(userID, provider)`, deleting any prior connection on a
  different provider — so connecting Microsoft replaces a previous Google connection
  and vice versa.
- `calendar_connections` holds per-user OAuth tokens (encrypted), the `provider`,
  connection roles `is_destination` / `check_conflicts`, and **`account_kind`**
  (migration 00032 — `work`|`personal`|`""`). Token refresh is automatic via an
  oauth2 `TokenSource` wrapped by a `savingTokenSource` that persists refreshed
  tokens (and preserves `account_kind`, which a refresh has no id_token to re-derive).

**Per-provider notes.**
- *Google* (`internal/gcal`): Meet via `conferenceData.createRequest` +
  `conferenceDataVersion=1` (returns `hangoutLink`); `CancelEvent` treats 410 Gone as
  success; `sendUpdates=all`. Free/busy via the freebusy API.
- *Microsoft* (`internal/calendar/microsoft`): Graph `/me/events`; Teams via
  `isOnlineMeeting:true, onlineMeetingProvider:"teamsForBusiness"` → `onlineMeeting.joinUrl`;
  free/busy via `/me/calendarView`. **`account_kind` is captured at connect** from the
  id_token `tid` claim (the consumers tenant `9188040d-…` ⇒ `personal`, else `work`) —
  added via the `openid` scope. A blanket `404 MailboxNotEnabledForRESTAPI` on every
  `/me/*` call means the account has **no Exchange Online mailbox** (not an auth error).
  Graph errors log their response body to make this self-evident.

**Online-meeting links are provider-matched (`booking_handler.go`).** A
`google_meet`/`teams` event type auto-mints a link **only when the primary host's
connected provider natively matches the platform** — Meet↔Google, Teams↔work-Microsoft
(`Service.CanAutoGenerate`; personal Microsoft can't mint Teams). When it can't, we
**never fabricate a link of the wrong kind** — the organizer's manually-entered
`location_value` is used instead. The minted link is stored on
`bookings.location_value`, surfaced in emails + the manage page, and passed as the
location of secondary hosts' events. The reconciler's create path applies the same
match rule; reschedule keeps the link (same event id), reassign carries the existing
link to the new host. Created async, so the link lands in the email + booking record,
not the instant 201.

**Save-time location validation (`event_type.go` + `validateLocation`).** A location
type only saves with usable join info: Teams/Meet need an auto-capable connected
calendar **or** a valid manual link (host-checked); Zoom/Video link need a valid https
URL (Zoom host-checked); Phone needs a phone number; In-person is free-form. Enforced
on create (when location is explicit) and on update **only when location actually
changes**, so editing an unrelated field on a legacy event type never trips it. New
event types get a **smart default** location from the owner's connected calendar
(Google→Meet, work-Microsoft→Teams, else Zoom) so the common case is bookable with no
manual link.

- Multi-host: each attending host gets their own event; the per-host event id lives
  in `booking_hosts.external_event_id` (migration 00023); the primary's also on
  `bookings.external_event_id` for back-compat.

**The email `.ics` gate (easy to miss).** `Handler.noConnectedDestination` (§12) lives in
the *mailer* path, not the provider packages. It must **not** attach Calnode's `.ics`
for any provider that auto-invites attendees (Google ✓, Microsoft Graph ✓) or
recipients get a *duplicate* invite; the gate keys on `Service.InvitesGuests`. Plain
CalDAV without iTIP scheduling (RFC 6638) does **not** auto-invite, so a future CalDAV
provider would want the `.ics` — the rule is "no destination whose provider
auto-delivers invites," not "no Google."

**Adding another provider (Apple/iCloud, CalDAV):** implement `calendar.Provider`,
register it in `internal/server`, set `InvitesGuests()` correctly (drives the `.ics`
gate above), and extend `providerMintsPlatform`/`CanAutoGenerate` if it offers a
native meeting platform.

---

## 11. Calendar reconciler (self-healing)

`internal/handler/calendar_reconcile.go`. Calendar side effects are best-effort and
async; a transient network failure (e.g. DNS blip) can leave the calendar diverged
from the booking with no retry. The reconciler closes that gap using `booking_hosts`
as the desired state:

- **Cancel direction:** cancelled booking with a lingering `external_event_id` →
  delete the event, null the id.
- **Create direction:** upcoming confirmed booking, host has a destination calendar,
  no event id → create it, store the id. A **5-minute grace** skips just-created
  bookings so it can't race/duplicate the inline create.
- Runs at startup, every 2 min, and on a **nudge** fired when an inline op errors.
  **Idempotent** (re-delete is a 410 no-op; create only fills gaps), so re-running
  is safe. `Service.HasDestination` avoids pointless retries for hosts with no calendar.
- **Time-drift (reschedule):** a failed inline move (`UpdateEvent`) flags the host row
  `needs_sync` (migration 00025); `reconcileReschedules` re-applies the booking's time
  to flagged events and clears the flag. So the reconciler covers create, cancel, AND
  time-drift — the last via an explicit flag, since drift can't be inferred from
  booking state without reading Google. Idempotent (re-applying the same time is a
  no-op).

---

## 12. Notifications & email

- `internal/mailer`: SMTP sender. The `From` header = `{EmailFromName} <{EmailFrom}>`
  (`smtp.go: buildRaw`). Configurable in Settings → Email (`email_from`,
  `email_from_name`) or env.
- Email types: confirmation, cancellation, reschedule, reminder — to attendee
  and/or host, gated by per-user notification prefs. Custom per-event-type notes +
  per-event "send test".
- **HTML emails (`mailer/html.go`):** every booking email is sent
  `multipart/alternative` — a styled HTML body plus the plain-text version as the
  fallback. One shared `html/template` layout (inline styles, table layout, fixed
  light palette — no CSS vars/`<style>`/external CSS, for client compatibility)
  with a per-type "content" block cloned onto it; add-to-calendar links render as
  buttons, plus a "Manage booking"/"Book again" button. The body carries **no
  brand text** (the sender already brands it): the header is a **logo-only slot**
  shown only when a logo is set, and there's no footer. Host emails set
  `HideManageLink`. `RenderBody` returns subject+text+html (test-email path).
- **Add-to-calendar:** attendee confirmation/reschedule/reminder emails carry
  Google + Outlook "Add to Calendar" *links* (`BookingData.GoogleCalURL`/`OutlookCalURL`)
  — always safe (pull-based). **Plus a gated `.ics` invite** (`mailer/ics.go`,
  `BuildICS`): attached to both the attendee's *and* each host's confirmation/
  reschedule (`METHOD:REQUEST`) and cancellation (`METHOD:CANCEL`) emails, **gated
  per-recipient on that person's host having no connected destination calendar**
  (`Handler.noConnectedDestination`, provider-agnostic via
  `Service.HasDestination`). When a host *is* connected (Google or Microsoft), that
  provider already puts the event on their calendar and invites the booker, so
  attaching our own (different-UID) `.ics` would duplicate it; when there's no
  destination, the `.ics` is how that recipient gets the meeting on their calendar.
  Stable UID `{bookingID}@calnode` + non-decreasing `SEQUENCE` (updated_at unix) lets a
  client match the REQUEST → reschedule → CANCEL to one event. `smtp.go: buildRaw`
  nests the body in `multipart/mixed` when a message has attachments; the body
  itself is `multipart/alternative` (text+HTML) or single-part text.
- Multi-host fan-out: each assigned host gets their host-notification; the attendee
  gets one. (See §9.)
- **Per-event-type customisation:** custom note bodies (`msg_*`) and custom subject
  lines (`subj_*`, migration 00026) for the four attendee emails; a blank subject
  falls back to the built-in default (`BookingData.SubjectOverride` / `subjectOr`).
- **Branding (`branding_settings.go`, migration 00029):** instance-wide
  `business_name` + `logo_url` on the singleton row. Business name is the wordmark
  fallback (defaults to "Calnode") + public-page header; the logo is the email
  header image + public-page header. `GET/PATCH /v1/settings/branding` (name only);
  the logo is an **upload** (`POST/DELETE /v1/settings/branding/logo`, public serve
  `GET /branding/logo`) reusing the avatar pipeline — `imaging.Fit` into 600×200
  preserving aspect ratio (no crop), re-encoded PNG (keeps transparency), stored on
  the `/data` volume. `logo_url` stores the relative serve path with a `?v=<ts>`
  cache-buster; `Handler.applyBranding` makes it absolute for emails (relative is
  fine on same-origin pages). Public-page CSP `img-src` allows `https:`/`data:` so
  the logo loads (`strictPublicCSP`). Brand is threaded into every send site
  (booking/cancel/reschedule/reassign + worker reminder).
- Reminders: scheduled as `jobs` and sent by the worker (§13).
- **Deliverability note (ops):** prod sends via **Resend** SMTP
  (`smtp.resend.com`, user `resend`, password = API key, STARTTLS); `orchestratr.ai`
  verified so any `@orchestratr.ai` From works. Email settings are **per-instance in
  each DB** (local ≠ prod). NB: Google/Workspace SMTP **rewrites the From** to the
  authenticated account unless it's a verified "Send mail as" alias — that's why a
  dedicated provider is used for branded From. SPF/DKIM/DMARC for the sending domain
  drive inbox placement.

---

## 13. Webhooks & background worker

- `internal/worker`: polls the `jobs` table **every 5s** (batch ≤10). Job types:
  `webhook.deliver` and `reminder.send`. Also purges expired manage tokens +
  sessions + idempotency keys (>24h old) each cycle, and reaps jobs whose 30s lock
  expired (crash recovery —
  reset to pending +1 min). Retry **backoff is a fixed two-step: 60s then 5 min**
  (not exponential), `max_attempts` 3. Atomic claim via
  `UPDATE … WHERE status='pending'` + RowsAffected.
- `internal/webhook`: enqueues `booking.created` / `.cancelled` / `.rescheduled`
  (there is **no** `booking.reminder` webhook event). Deliveries are signed
  **HMAC-SHA256**, header `X-Calnode-Signature` (+ `X-Calnode-Event`/`-Delivery`),
  secret stored encrypted. The worker's HTTP client is **SSRF-guarded** (resolves
  DNS, blocks private/loopback/CGNAT/ULA IPs, dials the resolved IP to avoid
  re-resolution) since webhook URLs are user-supplied.
- **Per-webhook payload fields:** each webhook chooses which fields land in the `data`
  object (`webhooks.fields` JSON, migration 00027) — incl. attendee PII + intake
  answers. NULL ⇒ the original default set (so existing webhooks are unchanged and
  never start leaking PII). The payload is enriched (`enrich`) + filtered per-webhook
  (`buildData`) at enqueue time, so each subscriber gets its own `data`. New webhooks
  default to all fields ticked (self-hoster unticks what they don't want); a
  delivery-log view (status/HTTP/attempts) is in the admin webhooks page.

---

## 14. Visibility model

- Members see **only bookings they host** — `ListByHost` matches `bookings.host_id`
  **OR** any `booking_hosts` seat, so Group/fixed non-primary attendees see meetings
  they're on (not just the ones they lead).
- Owners/admins can request the whole workspace: `GET /v1/bookings?scope=all`
  (gated on `IsAdmin`; ignored for non-admins so it can't escalate). The bookings
  page has a "My / All" toggle for admins with a Host column.

---

## 15. Frontend toolchain & conventions

- Svelte 5, SvelteKit 2 (adapter-static SPA), Vite 8 (Rolldown), Tailwind v4
  (`@tailwindcss/vite`), shadcn-svelte (nova style) + bits-ui, tailwind-variants 3,
  **tailwind-merge v3** (must match Tailwind v4 — v2 mis-merges v4 classes; memory
  `tailwind-merge-version`). `pnpm` only; `pnpm exec` for local bins (`pnpm dlx` has
  a Windows manifest bug).
- shadcn states styled via Tailwind `data-*` variants need `@custom-variant` remaps
  in `app.css` or they render silently unstyled — run `pnpm test:visual` after
  touching `ui/**`, `app.css`, or the theme (memory `shadcn-tailwind-variants`).
- Prefer a component's variant prop over a `class` override of its size default
  (e.g. button height) — overrides rely on tailwind-merge stripping the default.
- Control heights are compact `h-8` by default ("nova"); auth-screen CTAs use
  explicit `h-11`.

---

## 16. Deployment & control-plane direction

- Today: single self-hosted binary + SQLite. `make build` (frontend then backend),
  run `./calnode`.
- Direction: **instance-per-tenant** managed hosting. The foundational pieces are in
  place — envelope encryption, `PUBLIC_BASE_URL` split, version stamp, readiness gate.
  Custom domains: a tenant points `book.acme.com` at their instance;
  `PUBLIC_BASE_URL` drives booker-facing links/emails while `BASE_URL` stays the
  identity host for OAuth/admin. Managed SaaS provisioning is later/lower priority.
- **Behind a reverse proxy (Fly / Railway / nginx) — required:** forward the
  **original `Host` header**. The CSRF same-origin check (§6) compares the request's
  `Origin`/`Referer` against `Host`, so a proxy that rewrites Host would *false-block
  admin writes* (403). Fly and Railway preserve Host by default; a hand-rolled nginx
  needs `proxy_set_header Host $host;`. Related: per-IP rate limits (§8) key on the
  **TCP remote address** (proxy headers like `X-Forwarded-For` are intentionally
  ignored as forgeable), so behind a shared proxy the limit keys on the proxy's
  connection — fine for per-instance Fly/Railway, worth knowing for a fronting proxy.

---

## 17. Cross-cutting gotchas (read before editing)

1. **SQLite single connection** — never query inside an open cursor; materialize
   first (§4).
2. **All times UTC** in storage; convert at the edges. The slot busy-window must be
   widened for tz boundaries.
3. **Frontend is embedded at compile time** — `pnpm build` + rebuild Go to see
   changes.
4. **Two public templates drift** — keep `book.html` and `manage.html` in sync.
5. **tailwind-merge must track Tailwind major** (§15).
6. **Calendar side effects are best-effort** — the reconciler is the safety net;
   keep ops idempotent.
7. **Booking visibility / availability key on `booking_hosts`**, not `host_id`
   alone, or multi-host attendees become invisible / double-bookable.
8. **Public-page CSP is dynamic** — `publicCSP()` (tracking_settings.go) returns the
   strict default and relaxes only when head code-injection is configured (broad
   `https:` or the operator's `tracking_csp_allow`). Don't re-hardcode the CSP on the
   `book`/`manage` handlers — route it through `publicCSP`.

---

## 18. Known gaps / deferred

- **Multi-host archiving interplay** — archive guard / upcoming-bookings / reassign
  count only the primary `host_id`; archiving a member who is a non-primary
  required/fixed host on upcoming bookings isn't blocked. **Accepted limitation**
  (degrades gracefully; offboarding is deliberate).
- **BIMI/avatar** for outbound email — not settable via headers; needs DMARC
  enforcement + a VMC if ever wanted.
- **Teams on personal Microsoft accounts** — `teamsForBusiness` is work/school only,
  so a personal Microsoft account can't auto-mint Teams links; the organizer must
  supply a manual link (validated, and surfaced in the editor hint).
- **OAuth app verification** — the Google and Microsoft apps should go through
  publisher verification before wide public use to avoid "unverified app" warnings.

---

## 19. MCP server (agent interface)

A Model Context Protocol server is compiled into the binary on the official Go SDK
(`github.com/modelcontextprotocol/go-sdk`). `Handler.MCPServer()`
(`internal/handler/mcp.go`) builds one server instance exposing seven typed tools
(schema generated from Go structs): `list_event_types`, `get_available_slots`,
`create_booking`, `get_booking`, `reschedule_booking`, `cancel_booking`,
`list_bookings`.

- **Two transports, one server:**
  - **stdio** — the `calnode mcp` subcommand (`cmd/calnode/mcp.go`) boots the full
    stack via `server.BuildHandler` (the service-wiring half of `server.New`, factored
    out so both paths share it) and runs over `mcp.StdioTransport`. Logs go to
    **stderr** — stdout is the JSON-RPC stream.
  - **Streamable HTTP** — mounted at `POST /mcp` in `server.New` via
    `mcp.NewStreamableHTTPHandler`, wrapped in `RequireAuth` (API-key path is the
    intended caller; the session path stays same-origin-guarded). No SSE.
- **No parallel code path.** Tools call the same internal services as the REST
  handlers. The shared cores: `computeSlots` (slot generation, also behind `GetSlots`),
  `validateAnswersCore` (intake-answer validation, behind `validateAnswers`),
  `resolveBookingHostPool` (routing-mode host split), and the side-effect dispatchers
  `dispatchBookingConfirmation` / `rescheduleSideEffects` / `cancelSideEffects`. So an
  MCP booking fires calendar events, emails, webhooks, and reminders identically to a
  web booking.
- **Scope:** booking reads/mutations are workspace-scoped (instance-per-tenant); the
  tools translate between the slug they expose as `event_type_id` and the internal id
  stored on bookings. `cancel_booking` uses `CancelByID` (any booking in the
  workspace). Gap: `create_booking` does not yet honour an `idempotency_key`
  (REST-only).
- **Authorization — the "Connect" flow** (`mcp_oauth.go`, `mcp_oauth_authorize.go`):
  Calnode is its own **OAuth 2.1 authorization server** for the `/mcp` resource, so an
  agent (Claude, ChatGPT) adds the server by URL and clicks **Connect** instead of
  pasting a key.
  - `/mcp` is guarded by `auth.RequireBearerToken(VerifyMCPBearer)`; a `401` advertises
    `/.well-known/oauth-protected-resource` (RFC 9728), which points at the AS metadata
    `/.well-known/oauth-authorization-server` (RFC 8414).
  - Clients self-register at `POST /oauth/register` (RFC 7591 DCR; public PKCE clients).
    `GET/POST /oauth/authorize` checks for a Calnode session — if absent it bounces
    through the **existing Google/Microsoft login** (via a `post_login_redirect` cookie
    that `finishOAuthLogin` honours) and back to a **consent** screen. `POST /oauth/token`
    does PKCE-S256-verified `authorization_code` and rotating `refresh_token` grants.
  - Tokens are opaque, **SHA-256-hashed** in `oauth_access_tokens` (migration 00033);
    `VerifyMCPBearer` accepts either an OAuth access token or a `cno_` API key, so
    scripted callers keep working. The worker purges expired `oauth_auth_codes`.
  - The slick Connect UX needs the server on **HTTPS** with valid metadata (deployed
    instance); `http://localhost` works for stdio/manual testing but not the remote
    connector UI.
  - **Connected apps** admin page (`/connections`, `GET`/`DELETE /v1/oauth/connections`)
    lists the grants a user authorized and revokes one (deletes the token → immediate
    loss of `/mcp` access). Per-user scoped, like API keys.

---

## 20. Changelog

This doc tracks the code; when you change behaviour in an area above, update the
matching section in the same PR. Notable rounds:

- **2026-06-21 — Microsoft 365 + multi-provider calendar.** A **calendar provider
  abstraction** (`internal/calendar` `Provider` + `Service`), the **Microsoft 365 /
  Outlook** provider (Graph free/busy, create/reschedule/cancel, **Teams** links),
  **multi-tenant + personal** Microsoft support (`MICROSOFT_TENANT=common`),
  **work/personal account-kind** detection (id_token `tid`, migration 00032),
  **provider-matched** meeting-link generation with manual fallback, **save-time
  location validation** for every type + smart default + picker reorder, and
  **Microsoft OAuth sign-in** (`/v1/auth/microsoft/*`, identity-only). Touched §3,
  §4, §6, §10. Known constraint: Teams auto-links need a work account (§18).
