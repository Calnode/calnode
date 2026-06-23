# Contributing to Calnode

Thanks for considering a contribution! This guide covers getting the app building,
running, and tested locally. For *how the system fits together*, read
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) first — it's the map.

## Prerequisites

- **Go 1.26+** (the binary is pure-Go SQLite — no CGO, no C toolchain needed)
- **Node 20+** and **pnpm** (the admin UI is SvelteKit; we use `pnpm`, not npm)

## Layout

- `cmd/calnode/` — entry point + CLI tools (key recovery/rotation, admin reset)
- `internal/` — the backend (handlers, booking engine, calendar providers, slots,
  mailer, worker, crypto, db). This is where most changes live.
- `frontend/` — the SvelteKit admin SPA, embedded into the Go binary at compile time
- `internal/handler/templates/*.html` — the public, server-rendered booking pages
  (separate from the Svelte app — see ARCHITECTURE §2)
- `internal/db/migrations/` — goose SQL migrations, run automatically on startup

## Build

The frontend is embedded into the binary via `go:embed`, so a build is two steps —
the `Makefile` chains them:

```bash
make build      # pnpm build in frontend/, then go build -o calnode ./cmd/calnode
```

> **Gotcha:** restarting the Go server alone won't pick up frontend changes — the
> assets are baked in at compile time. After editing anything under `frontend/`,
> re-run `make build` (or `pnpm build` then rebuild Go).

## Run locally

Minimum env to boot (a dev box can omit the encryption key — see the caveat below):

```bash
BASE_URL=http://localhost:3000 \
DATABASE_URL=sqlite://./data/calnode.db \
./calnode
```

Open `http://localhost:3000` → it redirects to `/admin/` and walks first-run setup
(create the owner, optionally connect a calendar, add an event type). Full env-var
reference and integration setup (Google/Microsoft OAuth, SMTP, Litestream) live in
[DEPLOY.md](DEPLOY.md).

> Without `CALNODE_ENCRYPTION_KEY` on a non-https `BASE_URL`, the vault uses an
> ephemeral per-process key, so encrypted data (OAuth tokens, SMTP password) won't
> survive a restart — fine for local dev. Production (https) hard-fails without it.

## Tests

```bash
go test ./...                       # backend — keep these green
cd frontend && pnpm test:visual     # real-browser computed-style checks
```

**Run `pnpm test:visual` after touching** `frontend/src/lib/components/ui/**`,
`frontend/src/app.css`, or the theme. shadcn-svelte styles state via Tailwind
`data-*` variants that need `@custom-variant` remaps in `app.css`; miss one and the
component renders **silently unstyled** (logic works, visuals don't). Unit tests
don't catch this class of bug — only the browser assertions do. See
`frontend/TESTING.md`.

## Migrations

Add a goose SQL file in `internal/db/migrations/` with the next number
(`000NN_short_name.sql`, `-- +goose Up` / `-- +goose Down`). They run on startup.
SQLite can't easily drop columns, so `ADD COLUMN` is reversible-by-convention only —
prefer additive, nullable/defaulted columns.

## Conventions

- **`gofmt`** all Go; standard library style. The codebase favours small, clear
  functions with comments that explain *why*, not *what*.
- **`pnpm` only**, and `pnpm exec <tool>` for local binaries (`pnpm dlx` has a
  Windows manifest bug).
- **Read [ARCHITECTURE.md §17](docs/ARCHITECTURE.md)** (cross-cutting gotchas) before
  editing — especially the SQLite single-connection rule (never query inside an open
  cursor), all-times-UTC, and "calendar side effects are best-effort."
- **Keep the docs honest.** If your change alters behaviour described in
  ARCHITECTURE.md, update the matching section in the same PR.

## Pull requests

1. Branch off `main`.
2. `go test ./...` green; `make build` succeeds; run `pnpm test:visual` if you
   touched UI/theme.
3. Keep the change focused; explain the *why* in the PR description.
4. **Contributor License Agreement.** First-time contributors are asked to sign our
   [CLA](CLA.md) — a bot comments on your PR with a one-line phrase to post. You keep the
   copyright to your work; the CLA just lets the project distribute it and license future
   versions under different terms if needed (e.g. a hosted edition). It's a one-time signature.
   *(This is a CLA, not a DCO: a DCO only certifies origin, whereas retaining relicensing
   rights requires a CLA.)*

The project's distributed code remains [Apache-2.0](LICENSE). "Calnode" is a project name/mark —
see [TRADEMARK.md](TRADEMARK.md) for how you may (and may not) use it.
