# Domains & base URLs

Calnode has **two** base-URL settings. This doc explains what each one does,
why there are two, and how to configure them for self-hosting (today) and for
managed hosting (later). If you only ever serve one domain, you can stop after
the TL;DR.

## TL;DR

| You want… | Set | Result |
|-----------|-----|--------|
| One domain for everything | `BASE_URL=https://cal.example.com` | Team logs in there, OAuth runs there, bookers see it, emails link to it. Leave `PUBLIC_BASE_URL` unset. |
| Bookings on a different host than admin | `BASE_URL=https://admin.example.com` **and** `PUBLIC_BASE_URL=https://book.example.com` | Team/admin/OAuth on `admin.…`; public booking links + emails on `book.…` |

**For self-hosting, the common case is the first row: set `BASE_URL` to your
domain and you're done.** `PUBLIC_BASE_URL` is an optional knob you only reach
for when you deliberately want two hosts.

## The two settings

### `BASE_URL` — the identity host

This is *what the server thinks it is*. It drives everything an
**administrator** touches and everything tied to authentication:

- Google OAuth redirect URIs (`/v1/auth/callback`, `/v1/calendar/callback`)
- The admin UI and post-login redirects
- Team-member invite links (`/admin/invite/…`)
- The `Secure` flag on session cookies (auto-enabled when `BASE_URL` is `https://`)

Your team logs in at `BASE_URL`. Your Google OAuth app's redirect URIs must
point at `BASE_URL`.

### `PUBLIC_BASE_URL` — the booker-facing host

This is *what your customers see*. It drives everything a **booker** touches:

- Absolute links on the public booking page
- Links inside confirmation / cancellation / reschedule emails
- The "manage your booking" links (`/manage/{token}`)

**`PUBLIC_BASE_URL` defaults to `BASE_URL` when unset.** So a single-domain
deploy never has to set it.

## Why two settings at all?

For a single-domain deploy they'd be identical, and one variable would do. The
split exists so a custom/vanity domain can take over the *public* face
(`book.acme.com`) without necessarily moving *admin + authentication*.

The subtlety is **Google OAuth redirect URIs are exact-match (no wildcards)**.
Every redirect URI has to be pre-registered in the Google Cloud console. So the
host that authentication runs on is "sticky" — moving it means registering a new
redirect URI.

Crucially, **Calnode uses per-tenant Google OAuth**: each instance stores its
*own* `google_client_id` / `google_client_secret` (configured in
Settings → Google OAuth). There is no shared platform OAuth app. That means
adding a redirect URI for a new domain is the operator's own one-time,
self-service action in their own Google console — not a platform-wide
bottleneck. So you have a free choice:

- **Keep auth where it is, brand only the public side.** Set `PUBLIC_BASE_URL`
  to the vanity domain, leave `BASE_URL` alone. Zero Google-console changes.
  Bookers see the vanity domain; admins keep logging in at the original host.
- **Move everything to the new domain.** Point `BASE_URL` at it (and register
  that redirect URI in your Google app). `PUBLIC_BASE_URL` inherits it, so the
  whole experience — admin, login, bookings, emails — is on the one domain.

## Self-hosting (today)

There is no "canonical" host in self-hosting — `BASE_URL` is simply *your*
domain, whatever you set it to. The recommended setup:

```
BASE_URL=https://cal.yourcompany.com
# PUBLIC_BASE_URL unset → inherits BASE_URL
```

Your team logs in at `cal.yourcompany.com/admin/login`, bookers book at
`cal.yourcompany.com/book/…`, and emails link to the same host. Register
`https://cal.yourcompany.com/v1/auth/callback` and `…/v1/calendar/callback`
in your Google OAuth app.

Only split the two if you specifically want admin and bookings on different
hosts — e.g. internal admin at `admin.yourcompany.com`, public bookings at
`book.yourcompany.com`.

## A note on managed hosting

This two-variable split isn't just a self-hosting nicety — it's designed so a
future Calnode-managed hosting option can reuse the exact same plumbing
(assigning a default host per tenant, then optionally moving a custom domain to
either variable depending on how much the tenant wants to change). Nothing
about that changes what you do today as a self-hoster; it's just why the
split exists rather than being a single `BASE_URL`.

## See also

- `.env.example` — the committed, copy-paste config reference.
