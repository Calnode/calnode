# Zoom integration

One Zoom OAuth app per Calnode instance (entered in **Settings → Zoom**), then each
host connects their **own** Zoom account through that app from the **Calendar** page.
Bookings with a Zoom location get a real meeting link minted under the assigned host's
account. The settings page shows the exact Redirect URL and required scope
(`meeting:write`); the app can stay unpublished for a single-account team.

## Multi-member teams: read this before inviting people

Zoom — not Calnode — decides who may authorize your app. An **unpublished** app can
only be authorized by users on the **same Zoom account** as the app owner. A member on
a different Zoom account clicks Connect and Zoom itself refuses; no callback, token,
or error ever reaches Calnode, so there is nothing to retry or work around in settings.

Your options, ranked honestly:

1. **Use the video already in the box.** Built-in LiveKit needs no per-user OAuth, no
   Zoom app, and no publication — see [VIDEO.md](VIDEO.md). For most teams hitting this
   wall, this is the answer: it converts "Zoom won't let us" into "we don't need Zoom".
2. **Work within Zoom's distribution rules.** Same-account joining, beta sharing, or
   publishing — with their real costs, spelled out in [DEPLOY.md](../DEPLOY.md) under
   "Zoom meeting links". That section is the reference for the Zoom-side mechanics;
   this file's recommendation is step 1 above.

What does **not** work: re-entering credentials, reinstalling, different hosting
(Docker vs Railway vs Render all behave identically), or having the member confirm
they own an active Zoom account. The refusal happens on Zoom's consent page under all
of them.

## Note for contributors

Per-member BYO Zoom apps (each host registers their own app) would technically work —
a member always authorizes their own app — and a PR doing it would be welcome if built
within these bounds: the instance app stays the default and fallback; per-user
credentials live entirely inside `internal/zoom` (storage + client routing) with no
changes to booking or meeting-mint flows; secrets use the existing envelope encryption.
That said, it is explicitly **not on the maintainers' roadmap**: it is a big chunk of
work that asks the least technical users to do the most technical thing (register a
Zoom Marketplace app each), while LiveKit already covers the need with zero
configuration. If you build it, build it within the bounds above.
