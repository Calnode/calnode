# Changelog

All notable changes to Calnode are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/), and versions follow
[Semantic Versioning](https://semver.org/).

**Pre-1.0 note:** while Calnode is in the `0.x` series, a **minor** bump (e.g.
`0.1` → `0.2`) may include breaking changes to the API, schema, or config. Pin an
exact tag (`ghcr.io/calnode/calnode:0.1.0`) if you need stability between upgrades.
`1.0.0` will mark the point at which the API and schema are declared stable.

## [Unreleased]

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
  running against.

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
