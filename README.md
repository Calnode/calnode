# Calnode

**A lean, self-hostable scheduling engine that lives in your AI stack.**

Calnode is a Calendly-style booking app shipped as a **single Go binary** with an
embedded **SQLite** database — no Redis, no Postgres, no separate API server, no
multi-gigabyte image. It's API-first, webhook-native, and built for a world where
agents do the booking. Self-host the whole thing on a $5 box; nothing is paywalled.

> Our production instance bills **~$0.09/month** of compute. It's a single static
> binary serving a SQLite file — it costs almost nothing to run.

`Apache-2.0` · `Go 1.26` · `single static binary` · `SQLite + Litestream`

---

## Why Calnode

- **One binary, one file.** Pure-Go SQLite (no CGO) compiles to a fully static
  binary. `docker run` it, or drop it on a VPS. No external services to orchestrate.
- **API-first, agent-ready.** A full REST API (88 endpoints) with API keys and
  **HMAC-signed webhooks configured *via API*** — script every booking action from
  Claude, ChatGPT, n8n, or curl. Plus a native **MCP server** built into the binary
  (official Go SDK; stdio + Streamable HTTP) so agents get first-class booking tools.
- **Modern, no bloat.** Go backend + a SvelteKit 5 admin app; public booking pages
  are server-rendered Go templates for instant first paint and a tiny payload.
- **Correct by construction.** DST-safe time handling (UTC instant + IANA name),
  a transactional double-booking guard, and native-API calendar free/busy (never
  stale `.ics` feeds).
- **Yours.** Instance-per-tenant by design — one deployment is one isolated
  workspace, your data, your calendar credentials. No shared multi-tenant database.
- **Easy to extend.** A clean Go codebase with `sqlc`-generated queries — not a
  100-package monorepo. Add an endpoint without spelunking.

---

## Calnode vs cal.com

The default open-source scheduler is a SaaS monolith. Calnode is the opposite.

| | cal.com | Calnode |
|---|---|---|
| **Codebase** | 500k+ LOC TS across ~100 packages | Lean Go + one SvelteKit app |
| **Runtime deps** | 4 GB+ image; needs Redis **+** Postgres **+** API server | One static binary + a SQLite file |
| **Database** | Postgres (+ Redis) | SQLite (WAL) + Litestream point-in-time backup |
| **Webhooks** | UI-only | **API-first**, HMAC-signed, per-webhook payloads |
| **AI / agents** | None | REST API + webhooks **+ a native MCP server** (stdio + HTTP) |
| **Deploy** | Orchestrate several services | `docker run` one container |
| **Isolation** | Shared multi-tenant DB (`org_id` everywhere) | Instance-per-tenant — isolation is the default |
| **Licence** | AGPL-3.0 | **Apache-2.0**, nothing paywalled for self-host |

*(cal.com figures reflect its public footprint; see the the design docs for the full rationale.)*

If you're weighing self-hosted schedulers and you want **small, fast, scriptable,
and AI-ready** over feature-maximal, Calnode is built for you.

---

## AI-native

Everything a human can do, an agent can do — over the API today:

```bash
# Find slots and book, with an API key
curl -s "$BASE/v1/event-types/intro-call/slots?from=2026-06-16&to=2026-06-20&tz=Pacific/Auckland" \
  -H "Authorization: Bearer $API_KEY"

curl -s -X POST "$BASE/v1/bookings" -H "Authorization: Bearer $API_KEY" \
  -H 'Idempotency-Key: 9f3c…' -H 'Content-Type: application/json' \
  -d '{"event_type_slug":"intro-call","start_at":"2026-06-17T21:00:00Z","name":"Alex","email":"alex@example.com","timezone":"Pacific/Auckland"}'
```

Wire booking lifecycle events (`booking.created` / `.rescheduled` / `.cancelled`)
to n8n / Make / your own service with HMAC-signed webhooks — all configured through
the API, not buried in a UI.

**Native MCP server.** A Model Context Protocol server is compiled *into* the binary
(official Go SDK), exposing seven first-class tools — `list_event_types`,
`get_available_slots`, `create_booking`, `get_booking`, `reschedule_booking`,
`cancel_booking`, `list_bookings`. The MCP tools call the same internal services as
the REST API (no parallel code path), so booking side effects — calendar events,
confirmation emails, webhooks, reminders — fire identically.

Two transports:
- **stdio** for local agents — run `calnode mcp` (logs to stderr, JSON-RPC on stdout).
- **Streamable HTTP** at `POST /mcp` for remote agents. Calnode is its own **OAuth 2.1
  authorization server** (dynamic client registration + PKCE), so an agent adds the
  server by URL and clicks **Connect** → signs in with the workspace's Google/Microsoft
  login → approves a consent screen — no pre-shared key. A `cno_` API key also works
  (`Authorization: Bearer <key>`) for scripts. *(The Connect UX needs HTTPS — it shines
  on a deployed instance.)*

```
User:  "Book a 30-min call with Wynne next week — I'm in Auckland."
Agent: get_available_slots("intro-call", "2026-06-16", "2026-06-20", "Pacific/Auckland")
       → presents options → create_booking(…) → returns confirmation + meeting link
```

---

## Quick start

```bash
docker run -d -p 3000:3000 \
  -e BASE_URL=https://booking.example.com \
  -e CALNODE_ENCRYPTION_KEY="$(openssl rand -hex 32)" \
  -e CALNODE_RECOVERY_SECRET="$(openssl rand -hex 32)" \
  -e DATABASE_URL=sqlite:///data/calnode.db \
  -v calnode-data:/data \
  <your-image>
```

Open `/` → it redirects to `/admin/` and walks you through first-run setup (create
the owner account, connect a calendar, add an event type). Put a TLS-terminating
proxy in front that forwards the original `Host` header.

**Full guide → [DEPLOY.md](DEPLOY.md)** (env vars, Railway step-by-step, custom
domains, Resend email, Google & Microsoft OAuth, Litestream backups, troubleshooting).

---

## Features

**Shipped**
- Event types with per-type duration, location, custom questions, custom email copy
- DST-correct availability (working hours, day-of-week rules, date overrides)
- Team routing: **fixed · round-robin · collective · priority**
- **Google Calendar & Microsoft 365 / Outlook** — native free/busy conflict checks
  behind one provider abstraction; auto **Google Meet / Teams** links, minted only
  when the host's connected calendar matches the platform (else a manual link is used)
- **Sign in with Google or Microsoft** (OAuth) or email + password
- Public booking + self-serve **reschedule/cancel** via signed manage links
- HTML branded email (logo, business name, size/opacity) with add-to-calendar links
- REST API (88 endpoints) + API keys; **HMAC webhooks** with per-webhook payloads + delivery log
- **Native MCP server** (7 tools; stdio via `calnode mcp` + Streamable HTTP at `/mcp`)
- Embeddable booking widget (Shadow-DOM web component; inline + popup)
- Members, roles (owner/admin/member), email-token invitations
- `Idempotency-Key` on booking creation; transactional double-booking guard
- Envelope encryption at rest (secrets sealed with a KEK; recovery escrow)
- Optional analytics: `<head>` code injection + `window.dataLayer` events (GTM/GA4)

**On the roadmap**
- Apple / CalDAV calendars · Zoom OAuth (auto links) · magic-link auth ·
  optional bring-your-own-LLM natural-language layer

---

## Stack

- **Backend:** Go 1.26 · pure-Go SQLite (`modernc.org/sqlite`, CGO-free → static binary) · `goose` migrations
- **Admin UI:** SvelteKit 2 / Svelte 5 · Vite 8 · Tailwind 4 · shadcn-svelte (embedded at compile time via `go:embed`)
- **Public pages:** server-rendered Go `html/template` + vanilla JS (no framework runtime)
- **Durability:** SQLite WAL + optional Litestream replication (S3/R2) for backup & point-in-time restore
- **Background work:** in-process job queue on the same DB — reminders, webhook delivery, calendar reconciliation. No broker.

---

## Design principles

A few load-bearing decisions (full detail in the the design docs):

- **The DB is the source of truth; external calendars are a projection.** A booking
  exists once it's committed locally; syncing to Google is a retryable side effect.
- **Time = UTC instant + IANA timezone name**, never a fixed offset — availability
  resolves to UTC per-date so DST shifts never corrupt a slot.
- **Single process, no external services.** Durability comes from Litestream, not a
  second datastore.
- **Instance-per-tenant.** Each install is one workspace; isolation is a feature,
  and the self-host and cloud codepaths are identical.

---

## License

[Apache-2.0](LICENSE). The full scheduler is self-hostable, and nothing previously
free is ever paywalled.
