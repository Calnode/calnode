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

## Translations

Calnode ships 11 locales (`en es fr fr-CA de el it nl pl pt sv`) on the booker-facing surfaces:
booking page, manage/reschedule page, embed widget, the four emails, and the calendar
invite. The admin SPA is translated separately (`en` + `fr` today). The built-in video
room is still English-only.

**Translation PRs are very welcome** - both new languages and corrections to existing
ones. **Most non-English locales are machine drafts with no native review, and none has
had a full native read** (Polish was contributed and grammar-checked by a native
speaker), so if you speak one of them, fixes are genuinely valuable and will be merged
readily. Small, single-language PRs are easier to review than sweeping ones. The admin
French catalog was written by a native speaker.

**Adding a booker-facing language is adding one file, and it does not have to be complete:**
`internal/i18n/locales/<code>.json`. No Go, template, or frontend change is needed -
`init()` globs the directory. The language switcher, the fallback-language setting and the
public API payload all read `SupportedLocales()`, which only lists locales at or above the
coverage floor (see below).

1. Copy `internal/i18n/locales/en.json` to `internal/i18n/locales/<code>.json` and translate
   the values. Delete the keys you have not got to yet if that helps; a missing key falls
   back to English at lookup time, so a partial file is mergeable and useful.
2. Keep the keys spelled exactly as English has them. A key that is not in `en.json` fails
   the build (`TestLocalesHaveNoUnknownKeys`), because it is always a typo and renders for
   nobody.
3. Keep printf verbs intact: `%s`, `%d`, `%q`. Reordering is fine with indexed verbs
   (`%[2]s`); dropping one is not. In the admin catalogue the placeholders are `{name}`.
4. Name the file **BCP-47 canonical**: `pt-BR.json`, never `pt-br.json`.
5. `dow_short_*`, `month_short_*`, `date_format` and `clock_format` are data, not code -
   Go has no locale date tables. Match CLDR; the test below checks you against `Intl`.
6. Run `go test ./internal/i18n/` and `make i18n-coverage` to see where you stand.
7. Eyeball the result: `go run ./cmd/calnode`, then
   `http://localhost:3000/book/<slug>?lang=<code>` and the manage page. Long words
   overflowing buttons is the usual surprise; check mobile width too.

**When a language is offered.** A language appears in the switcher, and is matched from
`Accept-Language`, only at or above 80% of the English keys (`i18n.MinCoverage`). Below the
floor it still resolves with `?lang=<code>`, so you can check your work in place, and
startup logs which languages are withheld. The floor exists because a mostly-English
booking page is worse for a booker than English is.

**What passing tests does not prove.** They check structure, not wording. Before a language
is advertised, a speaker should read the booking page, the manage page and the four emails
end to end. The usual misses are literal calques, agreement when two translated sentences
are spliced together, and plurals written as flat strings.

Terminology is agreed in [`docs/i18n-glossary.md`](docs/i18n-glossary.md). Add your
language's column there rather than inventing a second word for something that already has
one.

**Adding an admin-UI language** is a small frontend change (the catalogs are not globbed):

1. Copy `frontend/src/lib/i18n/en.json` to `<code>.json` and translate the values. Keep
   every key and any `{placeholders}` (including `{console}` / `{email}`-style link
   slots rendered by `LinkedText` — do not turn catalog values into HTML). English is
   the source of truth; missing keys fall back to English at runtime.
2. Register the new file in `frontend/src/lib/i18n/catalog.ts` (`catalogs` + `ADMIN_LOCALES`).
3. Name the code the same way as booker locales: BCP-47 primary tag (`fr`, `de`).
4. The operator picks the language in Settings → Profile → Preferences (stored in
   `localStorage`, with `navigator.languages` as the first-visit fallback).
5. Run the catalog tests (`node --experimental-strip-types --test src/lib/i18n/catalog.test.ts`
   from `frontend/`). `pnpm test:visual` also runs the same assertions via Vitest.

Background and the full list of limitations (2-form plurals, no RTL) are in
[ARCHITECTURE.md §23](docs/ARCHITECTURE.md).

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
