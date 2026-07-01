# Calnode adversarial prompt-pack

Feed these to **your own** coding agent (Claude Code, Cursor, Aider, whatever you
use), against a clone of this repo, using **your own API key**. Calnode has no
involvement in this pass — that's the point. These are written to be adversarial:
the goal is to find problems, not to confirm the codebase is fine. If your agent
comes back saying "looks secure!" without specifics, push it — ask it to try harder,
or ask each prompt again with "you missed something — look again."

Cross-reference findings against [`claims.yaml`](../claims.yaml) — if your agent finds
something that contradicts a claim there, that's exactly the kind of gap this kit
is meant to surface. Please report it: https://github.com/Calnode/calnode/issues

Suggested setup: `git clone https://github.com/Calnode/calnode && cd calnode`, then
paste the whole repo into context (it fits — that's the premise) plus one prompt at
a time. Don't run all of these in one turn; each deserves its own focused pass.

---

## 1. Trace every outbound network call

> Read through this entire codebase and list every single outbound HTTP/network
> call it makes to a third party — grep for `http.NewRequest`, `http.Client`,
> hardcoded URLs, SDK calls, everything. For each one, tell me: (a) what data does
> it send, (b) under what condition (always-on vs. optional feature vs.
> operator-configured), (c) does it ever carry meeting content (audio, transcripts,
> recordings) or calendar/PII data. Be exhaustive — I specifically want to know if
> there's an egress path that isn't documented in `audit/claims.yaml`'s
> `meeting-content-egress` claim. Don't take the claim's word for it; verify it.

**What this should surface:** Deepgram (meeting content, opt-in), and the
documented-as-expected calendar/payment/meeting-link/analytics/LLM integrations. If
it finds meeting content going anywhere else, that's a real finding — report it.

## 2. Try to break tenant isolation

> This codebase claims "instance-per-tenant" isolation — one deployment is one
> workspace, no shared multi-tenant database. Try to prove that wrong. Look for: any
> `tenant_id`/`workspace_id`/multi-tenancy scaffolding; any place a query could be
> missing a `WHERE user_id = ?` / `WHERE host_id = ?` clause and leak another user's
> data within a single instance; any admin/owner endpoint that doesn't check the
> caller actually owns the resource being modified. Assume you're a malicious member
> of the team trying to read or modify another team's or another member's data.

**What this should surface:** nothing cross-instance (there's no multi-tenant code
to break), but it's a legitimate test of *intra-instance* authorization — team
member A reading team member B's bookings, API keys, or settings. Real findings
here matter regardless of the tenant-isolation framing.

## 3. Review the auth model end-to-end

> Review every authentication and authorization path in this codebase: session
> cookies, API keys, OAuth (Google/Microsoft sign-in), the OAuth 2.1 authorization
> server for MCP connections, magic-link/email auth, and the LiveKit video
> `authorizeHost` model. For each, tell me what could go wrong: session fixation,
> missing CSRF protection on state-changing routes, API keys that don't expire or
> aren't scoped, OAuth redirect_uri validation gaps, role checks that trust
> client-supplied data instead of re-deriving from the session. Then specifically
> check: can a non-admin escalate to admin? Can a "member" role reach an "owner"
> endpoint?

## 4. Review encryption and secret handling

> Review `internal/keyvault/` and how it's used across the codebase. Is the
> envelope-encryption (KEK/DEK) implementation sound — proper key derivation, no
> hardcoded keys, no weak randomness (check for `math/rand` where `crypto/rand` is
> required)? Then grep the ENTIRE codebase (not just recent commits) for anything
> that looks like a real secret, API key, or credential accidentally committed —
> don't trust that gitleaks already caught everything; read suspicious-looking
> config/test files yourself too.

## 5. Find injection, SSRF, and unsafe deserialization

> Audit every SQL query for injection risk — specifically flag any query built via
> string concatenation and verify whether the concatenated parts are ever
> user-controlled data (not just placeholders or fixed column names). Audit every
> outbound webhook/URL fetch for SSRF — can an operator-configured webhook URL or
> calendar/CalDAV endpoint be used to reach internal/private IP ranges? Check
> `encoding/json`/`encoding/gob` usage for unsafe deserialization of untrusted
> input. Report anything you're not 100% sure is safe, even if you can't prove
> exploitability.

## 6. Find authz gaps in the booking flow specifically

> The public booking flow (`/book/{slug}`, the manage/reschedule flow, and the
> LLM-conversational booking assistant) is the most attacker-reachable surface in
> this app — it's unauthenticated by design. Walk through it as an attacker: can
> you book a slot that shouldn't be available (double-booking, race condition, past
> a cutoff)? Can you access or modify someone else's booking via the manage token
> without the real token? Can you make the LLM assistant (if configured) do
> something outside its intended scope — leak another booking's details, bypass a
> validation the deterministic booking core would otherwise enforce?

---

**After running these:** compare what your agent found against
[`claims.yaml`](../claims.yaml). Every claim there should either hold up, or you've
found something we need to know about — either way, we want to hear from you:
https://github.com/Calnode/calnode/issues
