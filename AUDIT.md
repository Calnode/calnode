# Auditing Calnode — a 10-minute self-serve security & quality check

Calnode's backend is ~24,000 lines of Go across ~105 files (~840KB, excluding
tests). That's small enough to fit entirely in one LLM context window — which most
scheduling software (cal.com included) can't do. This page is our attempt to turn
"auditable" from a slogan into something you actually run, yourself, before you
trust Calnode with your calendar or your customers' meeting data.

**This is a due-diligence accelerator, not a certification.** It doesn't replace a
real penetration test or a formal security review if your org requires one — it's
meant to give you (or your security team) a credible first pass in minutes instead
of the weeks a typical vendor security review takes, and to short-circuit the part
of procurement that usually blocks everything else.

**Everything here runs on your machine, with your own tools and your own API key.**
Nothing routes through Calnode or any Calnode-hosted service. We don't see your
results, and you don't have to trust us to interpret them.

---

## 1. Architecture & trust boundaries, in one page

- **Single Go binary + embedded SQLite.** No Postgres, MySQL, or Redis. The binary
  `go:embed`s a compiled SvelteKit admin app; public booking pages are
  server-rendered Go templates.
- **Instance-per-tenant.** One deployment (one binary + one SQLite file) is one
  workspace. There's no shared multi-tenant database in the codebase to isolate —
  the deployment boundary *is* the tenant boundary.
- **Data flow, normal path:** booker hits a public `/book/{slug}` page (Go
  template, no auth) → the deterministic slot engine checks calendar free/busy
  (Google/Microsoft/CalDAV, native APIs, never stale `.ics`) → a booking is created
  transactionally (no double-booking races) → confirmation emails + optional
  webhooks fire.
- **Data flow, video + notetaker (opt-in):** a booking with a `livekit` location
  gets a hand-signed room token (no LiveKit SDK server-side) → recording, if the
  host starts it, goes to **your own** S3-compatible bucket → if the notetaker is
  enabled, the recording is transcribed via **Deepgram** (the one third-party
  service that ever sees meeting audio) and summarized via **your configured
  BYO-LLM**.
- **Secrets at rest** use envelope encryption (a per-secret key wrapped by a root
  key), not flat single-key encryption.
- **Every claim above** is mapped to exact source locations in
  [`audit/claims.yaml`](audit/claims.yaml) — don't take this page's word for it,
  check the manifest.

## 2. Layer 1 — deterministic scanners (no LLM, run this first)

Copy-paste this block against a fresh clone. All tools are standard, neutral,
widely-used security tooling — none of it is Calnode-specific.

```bash
git clone https://github.com/Calnode/calnode && cd calnode

# Install the scanners (Go tools install cleanly with no CGO toolchain needed)
go install golang.org/x/vuln/cmd/govulncheck@latest
go install github.com/securego/gosec/v2/cmd/gosec@latest
go install github.com/zricethezav/gitleaks/v8@latest
go install github.com/anchore/syft/cmd/syft@latest
pip install semgrep   # or: brew install semgrep

# Known Go vulnerabilities reachable from your code (expect: no vulnerabilities found)
govulncheck ./...

# Go SAST — injection, crypto, unsafe patterns (expect: findings are all annotated
# inline with `#nosec` + a reason at that exact line — read a few to judge for
# yourself whether the reasoning holds up)
gosec ./...

# Secrets across the FULL git history, not just the current tree
gitleaks detect --source . --no-banner -v

# SBOM — see exactly what's in the Go dependency tree (should be lean; frontend
# npm packages are normal frontend-tooling volume, not part of this claim)
syft dir:. --exclude './frontend/node_modules/**' --exclude './.git/**' -o table

# Frontend (admin SvelteKit app) dependency vulnerabilities
cd frontend && pnpm install && pnpm audit && cd ..

# Generic security + secrets rulesets across the whole repo
semgrep scan --config p/security-audit --config p/secrets \
  --exclude frontend/node_modules --exclude frontend/build .
```

Our own latest run of this exact block: **govulncheck 0 · gosec 0 unresolved (all
findings annotated) · gitleaks 0 across 337 commits · semgrep 0**. Yours should
match — if it doesn't, that's either drift since our last run or something we need
to know about. (`govulncheck` and `gosec` are also re-run on every push to `main`
via `.github/workflows/audit.yml`, so an unannotated gosec finding or a new CVE
fails CI instead of silently accumulating — see `audit/claims.yaml`'s
`clean-security-scan` entry for the last time this actually caught something.)

We also publish an [OpenSSF Scorecard](https://github.com/Calnode/calnode) badge
and CI-generated SBOM — see the badge on [README.md](README.md) and the
`audit` workflow under **Actions** in this repo for the latest run.

## 3. Layer 2 — the adversarial LLM pass (the part a big codebase can't offer)

Deterministic scanners can't answer the questions a CISO actually asks: *where
does data really go, can tenant isolation be broken, is the auth model sound.*
Because Calnode's backend fits entirely in an LLM's context window, you can point
your **own** coding agent — Claude Code, Cursor, whatever you already use, with
**your own API key** — at the whole codebase and ask it directly.

**[→ audit/prompts/prompt-pack.md](audit/prompts/prompt-pack.md)** — six adversarial
prompts (trace all outbound egress, try to break tenant isolation, audit the auth
model, review crypto/secrets handling, hunt injection/SSRF/deserialization bugs,
attack the booking flow specifically). They're written to find problems, not to
confirm the code is fine — push your agent if it comes back vague.

## 4. The claims manifest — falsifiable, including the unflattering parts

**[→ audit/claims.yaml](audit/claims.yaml)** maps every public claim we make (single
binary/no server DB, instance-per-tenant isolation, meeting-content egress scoped to
Deepgram only, envelope encryption at rest, consent-aware recording, consent-gated
analytics, BYO-LLM with no raw calendar access, the "small enough to audit" claim
itself) to exactly how to verify it — including the caveats and exceptions we'd
rather not have to admit, like recording consent being notice-and-choice rather than
a hard gate. Feed it to your agent alongside the prompt-pack and have it report
✅/⚠️ per claim against what it actually finds in the code.

## 5. Keeping this honest over time

The deterministic half of `claims.yaml` (no Postgres driver, no new third-party
egress, no secrets in history, no unresolved vulnerabilities, no unannotated SAST
findings) is wired into `.github/workflows/audit.yml` as CI assertions — so if a
future change silently breaks one of these claims (a new dependency drags in a
Postgres driver, a new integration starts sending recordings somewhere new, a new
gosec finding ships without a `#nosec` justification), the build fails instead of
the claim quietly going false. This manifest is a regression guard, not just a
one-time document.

This gate has already caught a real gap once: `gosec` was documented in this page's
scanner block (§2) as something you should expect to come back clean, but it had
never actually been added to `audit.yml` — only `govulncheck` and `gitleaks` were.
That let 44 unannotated findings accumulate silently before a routine audit re-run
caught it (see commit `c4c067c`). `gosec` is now a CI gate too, specifically so a
manual claim in prose can't drift from what CI actually checks again. `semgrep` is
the one scanner in §2 that is still manual-only (not yet a CI gate) — if you notice
it drift, that's a known gap, not a surprise.

---

**Found something?** This whole exercise only works if you tell us when it's wrong.
Open an issue: https://github.com/Calnode/calnode/issues
