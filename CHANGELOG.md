# Changelog

All notable changes to Calnode are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/), and versions follow
[Semantic Versioning](https://semver.org/).

**Pre-1.0 note:** while Calnode is in the `0.x` series, a **minor** bump (e.g.
`0.1` → `0.2`) may include breaking changes to the API, schema, or config. Pin an
exact tag (`ghcr.io/calnode/calnode:0.1.0`) if you need stability between upgrades.
`1.0.0` will mark the point at which the API and schema are declared stable.

## [Unreleased]

## [0.8.0] - 2026-09-03

### Added
- **Booked times can be shown struck through instead of hidden.** Off by default, and
  enabled per event type under Visibility. Requested in
  [#14](https://github.com/Calnode/calnode/discussions/14), tracked as
  [#19](https://github.com/Calnode/calnode/issues/19).

  For a public-hours use case - an intro call, a clinic, a tutor - a visibly busy
  calendar communicates demand, and an empty-looking list reads as "nothing here". It
  stays off by default because the slots endpoint is public and unauthenticated, so
  turning it on makes a host's booked hours legible to anyone with the link. That is a
  fair trade when the hours are already public and a privacy regression when they are
  not, so it is never inherited by upgrading.

  Only starts a booking or calendar conflict removed are shown. Times outside the host's
  working hours are never rendered, and times withheld by the minimum-notice rule are
  never shown as taken - nobody booked those, and saying so would corrupt the signal the
  feature exists to send. Booked times cannot be selected on any surface, and agents
  using the MCP tools or the booking assistant continue to see only bookable times.

  `GET /v1/event-types/{slug}/slots` gains a `taken` array for opted-in event types,
  absent otherwise. Event types gain `show_taken_slots` (migration 00057).

## [0.7.0] - 2026-09-03

### Added
- **Filter and page the bookings list.** The bookings page now filters by event type,
  host, team and status alongside the existing Upcoming/Past and Mine/All toggles, and
  pages through results 25 at a time instead of rendering everything at once. Requested
  in [#15](https://github.com/Calnode/calnode/discussions/15), tracked as
  [#18](https://github.com/Calnode/calnode/issues/18).

  `GET /v1/bookings` gained `event_type`, `host`, `team`, `status`, `when`, `from`,
  `to`, `order`, `limit` and `offset` query parameters, and its response now carries
  `total`, `counts` and the active `limit`/`offset` beside `items`. MCP `list_bookings`
  gained `team_id`, `limit` and `offset`, and returns `total`.

### Fixed
- **A running instance now reports which commit it is.** `/version` reported
  `commit: unknown` on every container, because the image is built from a copied
  source tree with no `.git` for the Go toolchain to read VCS metadata from. That was
  survivable while only tagged releases were deployed; it is not now that branch
  images can be, since those report `version: dev` and nothing else identified the
  build. The commit is stamped explicitly at build time instead.
- **Webhook deliveries are no longer kept forever.** Nothing ever purged
  `webhook_deliveries`, so on a busy instance the table grew for the life of the
  deployment, inside the SQLite file Litestream replicates offsite. The worker now
  sweeps finished deliveries after 30 days, alongside the five other tables it already
  purged. Only rows that reached `success` or `failed` are removed: a pending delivery
  still has a job pointing at it, and deleting one would turn a deliverable webhook
  into a permanent failure. The deliveries view only ever showed the 50 most recent, so
  nothing visible changes.
- **`status=cancelled` returned nothing, on every surface.** Both booking list queries
  hardcoded an exclusion of cancelled bookings and then filtered on top of that result,
  so asking for cancelled bookings could never match anything - including through the
  MCP tool whose own schema advertises `cancelled` as a valid value. There was no way at
  all to view a cancelled booking. An explicit status now replaces the default exclusion
  instead of being applied after it; omitting it still hides cancelled bookings.
- **Filtering by host missed the meetings that person attends but doesn't lead.** The
  host filter compared `bookings.host_id` only, while visibility has always counted a
  user as hosting a booking if they are the primary host *or* an assigned host. Group
  meetings someone was on were therefore invisible when filtering to them.

### Changed
- **Bookings are selected in SQL rather than in the browser.** `GET /v1/bookings` and
  MCP `list_bookings` previously loaded every booking the caller could see and then
  filtered and sorted the result in Go or in Svelte, running follow-up queries whose
  `IN` clause held every booking id returned, against a single-connection pool. Both now
  share one filtered, ordered, paginated query.
- **Indexed the bookings list.** The only indexes on `bookings` both led on `host_id`
  and were partial, so every listing planned as a full scan plus a temporary B-tree
  sort of the whole matching set to return one page. Paginating the API alone would
  have made the response smaller without making the work smaller. `(start_at, id)` and
  `(event_type_id, start_at, id)` (migration 00056) turn the page query into an index
  walk that stops at the limit. The Upcoming/Past counts are an aggregate and still
  scan by design.

## [0.6.0] - 2026-08-30

### Fixed
- **Slot interval is now configurable, and defaults to the meeting length.** Reported as
  "bookable timeslots are always 30 minutes apart regardless of duration" (#13). Interval
  and duration are deliberately separate settings - interval is how often a booking may
  *start*, duration is how long it *runs* - but the interval was not exposed anywhere in
  the admin UI, so every event type was stuck on the schema default of 30 unless you drove
  the REST API by hand. It now appears in the event-type editor, and new event types
  default it to their duration instead of a fixed 30, which was the wrong guess in both
  directions: a 15-minute event offered slots every 30 minutes, and a 90-minute one offered
  starts it could not honour. **Existing event types keep their stored value** until edited.
- `slot_interval_minutes` is validated on create and update. Slot generation refuses a
  non-positive interval, so a `0` previously left an event type with no bookable times and
  nothing explaining why.

### Added
- **The Connected apps page now shows the MCP connector URL, with a copy button.** It
  listed what was connected but never said how to connect anything: the only guidance was
  in the empty state, referred to "its URL" without showing one, and vanished once the
  first app was approved.

## [0.5.0] - 2026-08-26

### Fixed
- **"calendar connection not found" when choosing where bookings are written.** The
  destination endpoint looked the account up by its `calendar_connections` row id, but that
  id is recreated on every OAuth token refresh - and opening the calendar picker can trigger
  one - so a page loaded moments earlier held a dead id. Now keyed on the account identity,
  as the calendar endpoints already were.
- **Disconnecting a calendar could silently do nothing.** The same stale-id lookup, but its
  miss branch returned success, so the API answered `204` having deleted nothing and the
  account simply stayed on the page with no error. Now keyed on account identity, and a
  genuinely unknown account is reported rather than swallowed.
- **Disconnecting left the account's calendar selections behind.** `connection_calendars`
  has no foreign key on purpose (one would cascade-delete a user's selections on every token
  refresh), so disconnect flows have to clear the rows themselves - and none did, despite
  migration 00049 stating they did. Reconnecting the same address silently inherited stale
  picks, including a write target pointing at a calendar the user may no longer have.
- **The public booking page rendered blank for any event type with a dropdown
  question.** `book.html` built the dropdown's placeholder with `.T` inside the questions
  range, where the dot is the question rather than the page, so the template aborted
  partway through writing the response. The result was a **200 with correct headers and a
  truncated body**: everything up to the dropdown was present and the calendar, the slot
  picker and every script were silently missing, so the event type could not be booked at
  all. Introduced in 0.3.0 with the i18n work and not caught because no test rendered a
  select question.

## [0.4.0] - 2026-08-24

### Added
- **Email can now be delivered over Resend's HTTPS API instead of SMTP.** Set a Resend
  API key under Settings → Email and mail goes out over port 443. This exists because
  **several hosting platforms block outbound SMTP on their cheaper plans** (Railway below
  Pro among them) by dropping the packets rather than refusing the connection - which
  looks like a hang, then like a wrong password, and cannot be fixed by changing any SMTP
  setting. Ports 25/465/587/2525 are all affected and it is not provider-specific.
- The transport follows the credentials you supply: an API key selects HTTPS, otherwise
  SMTP, otherwise nothing. It does **not** probe and silently switch. Settings → Email
  badges which path is actually live, so filled-in SMTP fields are never mistaken for SMTP
  delivery, and "Remove key" switches back.

### Security
- **All three image uploads now check dimensions before decoding.** The 5 MB body limit
  bounds bytes on the wire, not pixels: a highly compressed PNG of 30000x30000 is a few
  hundred KB and decodes to gigabytes. Both the logo and banner endpoints now read the
  image header first and reject anything over 25 megapixels. The branding logo and banner
  are admin-only, but **the user avatar upload is not** - any authenticated member could
  send a ~160 KB file that decoded to hundreds of megabytes, and an out-of-memory kill
  takes down the process holding the single SQLite connection. It did not need a malicious
  user either: a genuine large camera photo is well under 5 MB compressed.

### Fixed
- Checkbox answers in the admin bookings list are matched liberally. Answers are
  canonicalised to `yes`/`no` on the way in, but rows created before that landed hold
  whatever the surface sent (the embed widget sent `Yes`), and a strict comparison rendered
  those as **No** - the opposite of what the guest ticked, which matters for consent
  checkboxes. Historic rows now display correctly without rewriting stored data.
- Branding uploads read the content-type sniff buffer with `io.ReadFull`. A short read
  could hand the sniffer a truncated prefix and reject a valid image.
- **A failed SMTP dial could hang for ~2 minutes.** `defaultSMTPTimeout` was applied only
  after the connection was established, so the dial itself fell back to the OS SYN-retry
  limit. Against a host that drops SMTP packets this stalled the background job queue,
  which shares a single SQLite connection, delaying every queued email behind it; the
  email test button also appeared to hang rather than fail.
- The email test button now explains failures instead of reporting "failed to send test
  email". An unreachable server names the platform-block possibility and points at the API
  key; a timeout after connecting points at the port/TLS mode; provider rejections are
  shown verbatim.

## [0.3.0] - 2026-08-20

### Added
- **Multi-language public surfaces (8 locales).** The booking page, the manage
  (reschedule/cancel) page, the embed widget, all four emails, the calendar invite
  title/description, and the conversational booking assistant are now translated into
  **English, Spanish, French, German, Italian, Portuguese, Dutch and Swedish**. The
  locale is negotiated from `Accept-Language` (so `de-AT` resolves to `de`), overridable
  by a footer language switcher (`?lang=`), with an operator-configurable fallback
  language in Settings → Branding for visitors whose language is not shipped.
- **The booker's locale is stored on the booking** (migration 00051), so later emails -
  reminders, cancellations, reschedule notices - arrive in the language they booked in
  rather than the language of whoever triggered the send. Host-facing sends stay English.
- **Editable assistant greeting** (migration 00052) and **fallback-language setting**
  (migration 00053).
- Adding a language requires **no code change** - dropping
  `internal/i18n/locales/<code>.json` in place is the entire task; the switcher, the
  fallback dropdown and the public API payload all read `SupportedLocales()`. See
  `docs/ARCHITECTURE.md` §23.

### Fixed
- Paid (Stripe) bookings always sent English email regardless of the language the booker
  used, because the confirmation query did not select the stored attendee locale.
- **Required checkboxes were not enforced.** A custom question of type `checkbox` marked
  required could be submitted unticked, on both the booking page and the embed widget.
  Now enforced client- and server-side, and the stored answer is canonicalised to
  `yes`/`no` instead of varying by surface.
- The public event-type endpoint returned language-dependent content without a `Vary`
  header, so a shared cache could serve one visitor's language to another.

### Notes
- **Non-English translations are LLM drafts without native review.** Structure is
  verified in CI (key parity, printf-verb parity, and a CLDR cross-check of the date
  tables against `Intl`); wording is not. Corrections via PR are welcome.
- The **built-in video room and the admin UI remain English-only.**

## [0.2.3] - 2026-08-18

### Security
- Bumped the Go toolchain from 1.26.5 to 1.26.6, closing 8 known stdlib CVEs
  (`net/http`, `encoding/xml`, `encoding/asn1`, `golang.org/x/net/idna`) that were
  reachable from Calnode's own code paths (CalDAV free/busy parsing, DB schema
  version checks, Zoom/Google HTTP clients).
- Bumped `golang.org/x/image` to v0.45.0, closing a VP8L (WebP) decode
  memory-exhaustion CVE (GO-2026-6222) reachable through the branding logo/banner
  upload endpoints, which accept WebP images.

### Added
- **Banner option on the Branding settings page.** Same upload/crop/opacity flow
  as the logo, shown full width below the logo (matching the email content
  container and the public booking form's width) on the booking page, manage
  page, and confirmation emails. Hidden entirely when not set; independent of the
  logo (either, both, or neither can be shown).
- A small link to the GitHub releases page in the admin sidebar footer, so
  self-hosted operators always have an easy way to check what version they're
  running against. The released Docker image now stamps its actual version at
  build time (`-ldflags -X buildinfo.Version=...`), which it previously didn't -
  every image, including past tagged releases, reported "dev".

## [0.2.2] - 2026-08-12

### Security
- **Fixed a LiveKit host-control leak.** For a booking held on a host's connected Google or
  Microsoft calendar, the calendar event added the attendee as a guest — and the provider then
  sent its own native invite email using that event's Location, which was the host's
  *privileged* join link. An attendee opening that invite (not Calnode's own confirmation email,
  which was never affected) got instant host controls in the room. CalDAV bookings were not
  exposed (its ICS never listed the attendee as a scheduling participant, so no native invite
  was ever sent). If you've run LiveKit bookings with a Google- or Microsoft-connected host
  before this release, treat any prior host links as having been shared more widely than
  intended.

### Fixed
- The SMTP mailer had no timeout past the initial connection — a stalled or misconfigured
  server (e.g. a port/TLS-mode mismatch) could hang a send indefinitely, surfacing in the admin
  UI as "Send test email" stuck on **Sending…** forever with no error. Now bounded to 30s (or
  the caller's own deadline, if shorter).
- `Settings → Google OAuth` now warns when the page is being viewed at a different domain than
  the server's configured `BASE_URL` — the usual cause of `redirect_uri_mismatch` after moving
  to a custom domain without updating `BASE_URL` to match.

### Added
- **Storage setup instructions.** `Settings → Storage` had a status badge but no real
  instructions for configuring the recording/backups bucket; now shows a full numbered guide
  (provider suggestions, exact env vars, including `LITESTREAM_ENDPOINT`/`REGION` which weren't
  documented anywhere before). `.env.example` documents the full `LITESTREAM_*` set for the
  first time, and the previously-undocumented `MICROSOFT_CLIENT_ID`/`SECRET`/`TENANT` set.
- `Settings → Video` now explains when meeting recordings need the storage bucket set up, with
  a link straight to `Settings → Storage`.
- The Recordings page's "no notes yet" message now says precisely which of the notetaker's three
  requirements (recording on, a Deepgram key, an LLM configured) is missing, instead of a
  generic message that only ever mentioned the first.

[0.2.2]: https://github.com/Calnode/calnode/releases/tag/v0.2.2

## [0.2.1] - 2026-08-12

Compliance and admin-UX polish.

### Added
- **AI-disclosure notice** on the booking-assistant chat panel ("Book by chat"), pinned above
  the conversation and visible before the first message, satisfying the EU AI Act's Article
  50(1) requirement that a person be told they're talking to an AI. Shown on both surfaces
  the assistant appears on: the hosted booking page and the embeddable widget.
- **Google and Microsoft now show up on the Calendar page even when unconfigured.** Previously
  an instance with no OAuth credentials for a provider simply omitted it from "Connect a
  calendar," with no indication it was ever an option. Each now renders a clearly-labelled
  "Not set up on this instance" row with a next step — a link to Settings → Google OAuth, or
  to the Microsoft setup docs.

[0.2.1]: https://github.com/Calnode/calnode/releases/tag/v0.2.1

## [0.2.0] - 2026-07-24

Adds per-account calendar selection and a set of admin-UX refinements from early user feedback.

### Added
- **Per-account sub-calendar selection.** Each connected account (Google, Microsoft 365, CalDAV)
  can expose several calendars; a per-connection **Manage calendars** picker chooses which are
  checked for conflicts, and free/busy honours the selection. Accounts connected before upgrading
  keep their existing behaviour (their bound calendar stays checked).
- **Out-of-office date ranges** in availability — block a multi-day span in one step.
- **Event-type archiving** with an Active / Archived filter, replacing outright deletion for
  event types you want to keep but hide.
- **Upcoming / Past filter** for bookings, keyed on the booking end time.
- Users can edit their own display name from the profile page.
- Calendar connections whose OAuth grant has been revoked or expired are now flagged
  **"Reconnect needed"** instead of surfacing a generic provider error.

### Changed
- Simplified the favicon to the plain logomark (dropping the rounded-square badge), matching
  the sign-in and invite marks.

### Fixed
- Corrected the Google OAuth redirect path in `.env.example`.

[Unreleased]: https://github.com/Calnode/calnode/compare/v0.2.2...HEAD
[0.2.0]: https://github.com/Calnode/calnode/releases/tag/v0.2.0

## [0.1.0] - 2026-07-23

First tagged, pinnable release. Calnode had already been running in production before
this tag — `0.1.0` marks the start of versioned releases and published, immutable
image tags (previously only `:latest` and commit SHAs existed).

Highlights of what ships in `0.1.0` (see the [README](README.md) for the full list):

- Event types, DST-correct availability, team routing (fixed / round-robin / collective / priority)
- Google Calendar, Microsoft 365 / Outlook, and CalDAV (iCloud / Fastmail / Nextcloud) — native free/busy + event write-back
- Sign in with Google / Microsoft, email + password, or passwordless magic-link
- REST API (88 endpoints) + API keys, HMAC-signed webhooks configured via API
- Native **MCP server** compiled into the binary (stdio + Streamable HTTP; OAuth 2.1)
- **Conversational booking** ("Book by chat"), BYO-LLM, off by default
- **Paid bookings** via Stripe Checkout (pay-then-book, auto-refund on cancel)
- **Zoom** (per-host OAuth) and **built-in video meetings (LiveKit)** — in-browser rooms, host controls, recording to your Litestream backup bucket, recording consent, and an AI notetaker (Deepgram transcript → LLM notes), consumable via MCP tools + webhooks
- Embeddable Shadow-DOM booking widget
- Envelope encryption at rest; SQLite WAL + optional Litestream point-in-time backup
- Multi-arch image (`linux/amd64` + `linux/arm64`)

[0.1.0]: https://github.com/Calnode/calnode/releases/tag/v0.1.0
