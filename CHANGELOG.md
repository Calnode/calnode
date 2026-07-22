# Changelog

All notable changes to Calnode are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/), and versions follow
[Semantic Versioning](https://semver.org/).

**Pre-1.0 note:** while Calnode is in the `0.x` series, a **minor** bump (e.g.
`0.1` → `0.2`) may include breaking changes to the API, schema, or config. Pin an
exact tag (`ghcr.io/calnode/calnode:0.1.0`) if you need stability between upgrades.
`1.0.0` will mark the point at which the API and schema are declared stable.

## [Unreleased]

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

[Unreleased]: https://github.com/Calnode/calnode/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/Calnode/calnode/releases/tag/v0.1.0
