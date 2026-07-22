# Calnode

**A lean, self-hostable scheduling engine that lives in your AI stack.**

Calnode is a Calendly-style booking app — with **first-party video meetings,
recording, and AI notetaking** built in — shipped as a **single Go binary** with an
embedded **SQLite** database: no Redis, no Postgres, no separate API server, no
multi-gigabyte image. It's API-first, webhook-native, and built for a world where
agents do the booking. Self-host the whole thing on a $5 box; nothing is paywalled.

> Calnode runs for pennies on a small VPS — it's a single static binary serving
> a SQLite file, so there's almost nothing to pay for.

`Apache-2.0` · `Go 1.26` · `single static binary` · `SQLite + Litestream`

[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/Calnode/calnode/badge)](https://securityscorecards.dev/viewer/?uri=github.com/Calnode/calnode) · [Audit it yourself](AUDIT.md)

---

## Quick start

Kick the tires — one command, no config:

```bash
docker run -p 3000:3000 -v ./data:/data ghcr.io/calnode/calnode:latest
# → open http://localhost:3000
```

The whole app — booking pages, admin UI, SQLite — in one container, with data in
`./data`. With no encryption key set it runs on an ephemeral one (fine for a look;
stored credentials won't survive a restart). **Deploying for real** — HTTPS, a
persistent encryption key, backups — see **[Deploy for real](#deploy-for-real)** below,
or the full **[DEPLOY.md](DEPLOY.md)**.

---

## Why Calnode

- **One binary, one file.** Pure-Go SQLite (no CGO) compiles to a fully static
  binary. `docker run` it, or drop it on a VPS. No external services to orchestrate
  (built-in video, if you turn it on, is the one add-on — it needs a LiveKit server).
- **Meetings built in.** Optional first-party video rooms (LiveKit) as a booking
  location — guests join in-browser, no app or account. Recording lands in your own
  Litestream backup bucket (no extra storage to provision); an AI notetaker turns each
  call into a transcript + notes, exposed as MCP tools and webhooks. The one add-on:
  video needs a LiveKit endpoint (Cloud or self-hosted).
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
| **Video & recording** | Third-party links (Zoom / Meet) | **Built-in in-browser rooms + recording + AI notes** (self-hosted LiveKit) |
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
(official Go SDK), exposing eight first-class tools — `list_event_types`,
`get_event_type`, `get_available_slots`, `create_booking`, `get_booking`,
`reschedule_booking`, `cancel_booking`, `list_bookings`. The MCP tools call the same internal services as
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

**Conversational booking — in the booking page itself.** Beyond agents, the booking page
(and the embed widget) ships an optional **"Book by chat"** assistant: a visitor types
"free Tuesday afternoon or next week" and it resolves real availability and books — the
deterministic engine still computes the slots (the model never invents times), and the
assistant only ever sees free/busy windows, never your calendar contents. **Bring your own
model** — any OpenAI-compatible endpoint (a hosted model or one you run yourself); off by
default, with the standard calendar always there as the fallback.

**Connecting Claude (remote / HTTP).** In Claude (claude.ai or Desktop) →
**Settings → Connectors → Add custom connector** → enter `https://<your-instance>/mcp`
→ **Connect** → sign in → **Allow**. Custom connectors need a paid Claude plan; the
server must be on HTTPS. *Local stdio alternative (any plan):* point an MCP client at
the `calnode mcp` subcommand via its config file — no OAuth, runs against the local DB.

**Permissions.** MCP tools are **role-scoped**, mirroring the rest of the app: an
**owner/admin** acts across the whole workspace, a **member** sees and manages only
bookings they host. (The stdio subcommand is the local operator → full access.) Booking
*creation* and availability are the public booking surface, open to all. Roles are fixed
(owner / admin / member); configurable RBAC is intentionally out of the lean core.

---

## Deploy for real

```bash
docker run -d -p 3000:3000 \
  -e BASE_URL=https://booking.example.com \
  -e CALNODE_ENCRYPTION_KEY="$(openssl rand -hex 32)" \
  -e CALNODE_RECOVERY_SECRET="$(openssl rand -hex 32)" \
  -e DATABASE_URL=sqlite:///data/calnode.db \
  -v calnode-data:/data \
  ghcr.io/calnode/calnode:latest
```

**Pinning a version.** `:latest` follows the newest tagged release; `:edge` tracks
`main`. For reproducible deploys, pin a release — `:0.1` (tracks patches within the
minor) or an exact `:0.1.0`. See [releases](https://github.com/Calnode/calnode/releases).

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
- **Sign in with Google or Microsoft** (OAuth), email + password, or **passwordless magic-link**
- **CalDAV calendars** — iCloud / Fastmail / Nextcloud via app-password (free/busy + event write-back)
- Public booking + self-serve **reschedule/cancel** via signed manage links
- HTML branded email (logo, business name, size/opacity) with add-to-calendar links
- REST API (88 endpoints) + API keys; **HMAC webhooks** with per-webhook payloads + delivery log
- **Native MCP server** (10 tools incl. meeting notes + transcript; stdio via `calnode mcp` + Streamable HTTP at `/mcp`)
- **Conversational booking** ("Book by chat" on the booking page + embed widget; BYO-LLM, off by default)
- **Paid bookings** — Stripe Checkout (pay-then-book: the slot is held, confirmed on the payment webhook, auto-refunded on cancel)
- **Zoom** — per-host OAuth; a Zoom-located booking mints a meeting under the assigned host's account
- **Built-in video meetings (LiveKit)** — in-browser rooms as a booking location (no app or account for guests); host controls (end-for-all, hand-off **and reclaim** host, attendee screen-share toggle), **meeting recording** straight to your own **Litestream backup bucket** (the same one
  you already use for DB backup — no extra storage to provision) with in-app downloads, **recording consent** (notice + consent-or-leave), and an **AI notetaker** (Deepgram transcript → LLM notes). Headless-consumable: MCP `get_meeting_notes`/`get_transcript` + `recording.completed`/`transcript.ready`/`notes.ready` webhooks. BYO LiveKit endpoint (Cloud or self-hosted); configured in Settings → Video — see [docs/VIDEO.md](docs/VIDEO.md)
- Embeddable booking widget (Shadow-DOM web component; inline + popup)
- Members, roles (owner/admin/member), email-token invitations
- `Idempotency-Key` on booking creation; transactional double-booking guard
- Envelope encryption at rest (secrets sealed with a KEK; recovery escrow)
- Optional analytics: `<head>` code injection + `window.dataLayer` events (GTM/GA4)

**On the roadmap**
- OpenAPI spec · `/metrics` · multi-domain (one instance, many hostnames)

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
  second datastore. (The one exception is optional built-in video: it talks to a
  LiveKit server — Cloud or self-hosted — only when you enable video.)
- **Instance-per-tenant.** Each install is one workspace; isolation is a feature,
  and the self-host and cloud codepaths are identical.

---

## Audit it yourself in 10 minutes

Calnode's backend is small enough to fit entirely in one LLM's context window —
most scheduling software (cal.com included) can't say that. **[AUDIT.md](AUDIT.md)**
turns that into a self-serve check: a copy-paste scanner block (govulncheck, gosec,
gitleaks across full history, SBOM, semgrep — all neutral, standard tooling you run
yourself), an adversarial LLM prompt-pack for your own coding agent, and
**[a claims → verification manifest](audit/claims.yaml)** mapping every security
claim we make to exactly how to check it in the source. Not a certification — a
due-diligence accelerator.

---

## Commercial support

Self-hosting is free forever — `docker run` it and you own the whole thing. If you'd
rather not run it yourself, **[Orchestratr](https://calnode.com/services)** — the team
behind Calnode — will deploy and operate it for you:

- **Production deployment** on your infrastructure — VPC, private cloud, or bare metal
- **SSO** (SAML / OIDC) integrated with Okta, Entra ID, or Google Workspace
- **Calendar, Zoom & CRM integration** wired into your existing stack
- **Migration** off Calendly / cal.com, plus bulk user & team provisioning
- **Training & ongoing support** for your ops and IT teams

Built for regulated and mid-to-large orgs that need Calnode on their own infrastructure,
fully integrated. → **[calnode.com/services](https://calnode.com/services)**

---

## License

[Apache-2.0](LICENSE). The full scheduler is self-hostable, and nothing previously
free is ever paywalled.

The **code** is Apache-2.0; the **"Calnode" name and logo** are not — see
[TRADEMARK.md](TRADEMARK.md) (use the code freely; name your fork something else).
Contributions are accepted under a [CLA](CLA.md) so the project can stand behind every
line and keep its future licensing options open — see [CONTRIBUTING.md](CONTRIBUTING.md).
