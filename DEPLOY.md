# Deploying Calnode

Calnode is a **single Go binary** that embeds the admin SPA and serves the public
booking pages, with **SQLite** on a persistent volume. No separate database,
cache, or web server. The official image is built from the repo `Dockerfile`
(multi-stage: build the SvelteKit admin → compile Go → ~small Alpine runtime).

This guide covers a generic Docker deploy and a step-by-step **Railway** deploy
(the current reference host).

---

## 1. Configuration (environment variables)

| Variable | Required | Default | Notes |
|---|---|---|---|
| `CALNODE_ENCRYPTION_KEY` | **prod: yes** | — | KEK input (Argon2id). **Required when `BASE_URL` is https** — the app refuses to start without it. Use a long random string: `openssl rand -hex 32`. **Losing it makes encrypted data unrecoverable** unless you set the recovery secret below. |
| `CALNODE_RECOVERY_SECRET` | recommended | — | Escrow secret so the data key can be recovered if the encryption key is rotated/lost. Store it somewhere separate. |
| `BASE_URL` | **yes (prod)** | `http://localhost:3000` | Identity host — admin UI, OAuth callbacks, invite links. **Must include the scheme** (`https://booking.example.com`). The `https://` prefix flips the app into production mode (secure cookies, encryption-key enforcement). |
| `PUBLIC_BASE_URL` | no | = `BASE_URL` | Booker-facing host for booking links/emails, if different from the identity host. |
| `DATABASE_URL` | no | `sqlite://./data/calnode.db` | Point at the persistent volume, e.g. `sqlite:///data/calnode.db`. |
| `PORT` | no | `3000` | The app listens on `$PORT`. Many platforms inject their own (Railway injects `8080`) — let them. |
| `EMAIL_SMTP_HOST` / `_PORT` / `_USER` / `_PASS` | no¹ | — / `587` | SMTP. Can also be set later in Settings → Email (DB-stored, encrypted). |
| `EMAIL_SMTP_TLS` / `_STARTTLS` | no | `false` | `STARTTLS` for 587, implicit `TLS` for 465. |
| `EMAIL_FROM_ADDRESS` / `EMAIL_FROM_NAME` | no | `bookings@localhost` / `Calnode` | The From identity. |
| `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET` | no | — | Google sign-in + calendar. Can also be set in Settings → Google OAuth. |
| `LITESTREAM_REPLICA_URL` | recommended | — | Enables continuous SQLite backup (see §6). |
| `COOKIE_SECURE` | no | https→true | Override cookie Secure flag; defaults from `BASE_URL` scheme. |
| `LOG_LEVEL` | no | `info` | `debug`/`info`/`warn`/`error`. |

¹ Email is optional to boot, but bookings won't send confirmations until SMTP is configured (env **or** the admin UI). Precedence is **env var > DB setting > default**.

---

## 2. Generic Docker

```bash
docker run -d -p 3000:3000 \
  -e BASE_URL=https://booking.example.com \
  -e CALNODE_ENCRYPTION_KEY="$(openssl rand -hex 32)" \
  -e CALNODE_RECOVERY_SECRET="$(openssl rand -hex 32)" \
  -e DATABASE_URL=sqlite:///data/calnode.db \
  -v calnode-data:/data \
  <your-image>
```

Put a TLS-terminating reverse proxy in front (Caddy/nginx/Traefik). **The proxy
must forward the original `Host` header** — Calnode's CSRF check compares
`Origin`/`Referer` against `Host`, so a rewritten Host causes 403s on admin writes.

---

## 3. Railway (reference deploy)

Railway auto-detects the `Dockerfile` and builds it. Steps:

1. **Create a project + service** from the repo (or `railway up` from the repo dir).
2. **Add a volume** mounted at **`/data`** (Service → Settings → Volumes).
3. **Set variables** (Service → Variables):
   - `BASE_URL=https://<your-domain>` (with scheme)
   - `CALNODE_ENCRYPTION_KEY`, `CALNODE_RECOVERY_SECRET` (generate, keep safe)
   - `DATABASE_URL=sqlite:///data/calnode.db`
   - (email / Google / Litestream as needed)
   - **Don't** set `PORT` — Railway injects `8080`, and the app honours it.
4. **Deploy:** `railway up` (or push-to-deploy). Migrations run automatically on boot.
5. **Custom domain** (Service → Settings → Networking → Custom Domain):
   - **Target port = the port the app is *actually* listening on.** On Railway that's
     **`8080`** (the injected `PORT`), **not** 3000. A wrong target port → 502 /
     "Application failed to respond". Check the `server listening port=…` startup log.
   - Add the CNAME (+ TXT verify) record Railway shows at your DNS provider. On
     **Cloudflare, set it to "DNS only" (grey cloud)** — proxying (orange cloud)
     blocks Railway's cert issuance and can cause redirect loops. You may re-enable
     the proxy after the cert issues, with SSL mode = Full (strict).

### Railway-specific build notes (already handled in this repo)
- **No `VOLUME` directive in the Dockerfile** — Railway's builder rejects it; storage
  comes from the managed volume.
- **pnpm under CI:** the build pins `pnpm` and lists `pnpm.onlyBuiltDependencies`, so
  `pnpm install --frozen-lockfile` doesn't hard-fail under `CI=true`.

---

## 4. Email (Resend recommended for production)

Gmail/Workspace SMTP **rewrites the From** to the authenticated account unless you
use a verified "Send mail as" alias — so for a branded From, use a transactional
provider. With **Resend**:

1. Verify your domain in Resend (add the SPF/DKIM/MX records it shows; **DNS-only** on Cloudflare).
2. Create an API key.
3. Settings → Email (or env): host `smtp.resend.com`, port `587`, username `resend`,
   password = the API key, **STARTTLS on**, From `bookings@yourdomain`.
4. Send a test email.

> Email settings are stored **per instance** in that instance's DB — staging/prod/local each need their own.

---

## 5. Google OAuth (sign-in + calendar)

In Google Cloud Console → Credentials → your OAuth client, add **Authorized
redirect URIs** (derived from `BASE_URL`):

```
https://<your-domain>/v1/auth/callback
https://<your-domain>/v1/calendar/callback
```

Set `GOOGLE_CLIENT_ID`/`GOOGLE_CLIENT_SECRET` (env or Settings → Google OAuth — the
page shows the exact redirect URIs for the running instance). Calendar is a
sensitive scope, so submit the app for verification before wide public use
(unverified = warning screen + 100-user cap).

---

## 6. Backups (Litestream)

Set `LITESTREAM_REPLICA_URL` to stream the SQLite DB continuously to S3-compatible
storage (S3, Cloudflare R2, Backblaze B2). The entrypoint then **restores on boot**
if the local DB is missing and replicates while running. Example:

```
LITESTREAM_REPLICA_URL=s3://my-bucket/calnode
# plus the provider's credentials, e.g. AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY
```

A single volume with no replica is the biggest data-loss risk — configure this
before real bookings exist.

---

## 7. First run

Open `https://<your-domain>/` → it redirects to `/admin/`. On a fresh database
you'll be guided through **first-run setup** (create the owner account). Then:
Settings → Email, → Google OAuth, → Branding (logo, business name), and create your
first event type + availability.

---

## 8. Troubleshooting

| Symptom | Likely cause |
|---|---|
| App won't start (prod) | Missing `CALNODE_ENCRYPTION_KEY` with an https `BASE_URL`. |
| Custom domain 502 / "Application failed to respond" | Custom-domain **target port ≠ the listening port** (use 8080 on Railway). |
| `ERR_CERT_COMMON_NAME_INVALID` on a new domain | Cert not issued yet — wait; ensure the DNS record is **DNS-only**, not proxied. |
| 403 on admin actions behind a proxy | Proxy not forwarding the original `Host` header (CSRF same-origin check). |
| OAuth `redirect_uri_mismatch` | Registered URI doesn't match `BASE_URL` + `/v1/...callback` exactly. |
| Email `550 domain not verified` | From address domain isn't verified with your email provider. |
| Logo broken in email when testing locally | Gmail's image proxy can't reach `localhost` — only loads from a public URL. |
