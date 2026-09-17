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
2. **Same Zoom account.** If everyone already belongs to one Zoom organization, join the
   accounts and the unpublished app works for all members instantly. The price is real:
   joined accounts lose independence (the admin can see usage and enforce settings) and
   paid features need a license per head from the account owner.
3. **Publish the app.** Zoom Marketplace review (technical design docs, security
   controls, vulnerability testing). Built for vendors shipping a product, not for a
   team running its own scheduler. Only take this path if you are productizing Calnode.
4. **Stopgap: Zoom's beta share link.** Lets up to 100 external users authorize for up
   to 90 days (4 weeks + extensions). A bridge, not a home — afterwards, publish or
   lose access.

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
