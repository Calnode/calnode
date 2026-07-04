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

[Litestream](https://litestream.io) streams the SQLite DB continuously to
**S3-compatible** object storage (≈1-second RPO) and **restores it automatically on
boot** if the volume comes up empty (lost/replaced). It's built into the image and
supervises the app (`entrypoint.sh` runs `litestream replicate -exec /calnode`).

**Backups are OFF until you set the env vars below** — until then the container
just runs the app directly. A single volume with no replica is the biggest
data-loss risk, so enable this before real bookings exist.

### Universal config (one set of vars, any provider)

Because R2, B2, MinIO, Spaces, Wasabi, and AWS all speak the S3 API, the same five
variables cover every provider — only the endpoint/region change:

| Variable | Purpose |
|---|---|
| `LITESTREAM_REPLICA_URL` | `s3://<bucket>/calnode` — the bucket + path. Setting this turns backups ON. |
| `LITESTREAM_ENDPOINT` | Provider S3 endpoint. **Leave unset for AWS.** |
| `LITESTREAM_REGION` | Bucket region (`auto` for R2). |
| `LITESTREAM_ACCESS_KEY_ID` | Access key (use a bucket-scoped key, not a root credential). |
| `LITESTREAM_SECRET_ACCESS_KEY` | Secret key. |

Per-provider values (everything else is identical):

| Provider | `LITESTREAM_ENDPOINT` | `LITESTREAM_REGION` |
|---|---|---|
| **Cloudflare R2** | `https://<account-id>.r2.cloudflarestorage.com` | `auto` |
| **Backblaze B2** | `https://s3.<region>.backblazeb2.com` | `<region>` (e.g. `us-west-004`) |
| **AWS S3** | *(unset)* | e.g. `us-east-1` |
| **MinIO / self-host** | `https://minio.example.com` | `us-east-1` |

**Two easy mistakes:**
1. `LITESTREAM_ENDPOINT` is the **account** endpoint — do **not** append the bucket
   name. Litestream takes the bucket from `LITESTREAM_REPLICA_URL`; a bucket in both
   places fails. (R2 shows the S3 API URL *with* the bucket — drop the last path segment.)
2. For R2, `LITESTREAM_REGION` is always **`auto`**, not the bucket's physical
   location (WNAM/ENAM/etc.). A real region string makes R2 reject the signature.

If the endpoint/region are empty or wrong, Litestream silently falls back to **AWS**
and you'll see `InvalidAccessKeyId` (403) in the logs (your R2 key sent to Amazon).

### Enabling it
1. Create a **private** bucket and a **bucket-scoped** access key (read + write — read is needed for restore).
2. Set the five variables on the service (Railway → Variables, or your platform's equivalent).
3. Redeploy. On boot Litestream initialises and the first snapshot uploads within ~10s.
4. **Verify the round-trip** before relying on it (below).

> The backup contains booking PII (names, emails, intake answers) in plaintext —
> keep the bucket **private**. Calnode's encrypted secrets (SMTP/OAuth) stay sealed
> by the envelope-encryption key even inside the backup.

### Verifying / restoring
Recovery is automatic: if the volume comes up empty, `entrypoint.sh` restores the
latest replica from object storage before starting the app. To **prove the
round-trip without touching the live DB**, restore to a scratch path (never
`/data/calnode.db`) and confirm it's a valid SQLite file.

**Non-destructive drill, in the container (zero setup — litestream + creds + config are already there):**
```bash
railway ssh --service calnode sh -lc '
  rm -f /tmp/check.db
  litestream restore -config /etc/litestream.yml -o /tmp/check.db /data/calnode.db
  ls -l /tmp/check.db          # non-zero, ~ the live DB size
  head -c 16 /tmp/check.db; echo   # "SQLite format 3" = valid, replayed from the replica
  rm -f /tmp/check.db
'
```
It writes only to `/tmp` (ephemeral) and reads from the replica, so the running
app and the `/data` volume are untouched. (The runtime image has no `sqlite3` CLI,
hence the header check; for a full `PRAGMA integrity_check`, restore locally below.)

**Locally (full integrity check):** install the `litestream` binary, export the five
`LITESTREAM_*` vars, then:
```bash
litestream restore -config litestream.yml -o ./check.db /data/calnode.db
sqlite3 ./check.db "PRAGMA integrity_check;"   # expect: ok
```

Run a drill after first enabling backups, and periodically thereafter — an
unverified backup isn't a backup.

---

## 7. Built-in video & recording (LiveKit)

Optional — only if you want Calnode-hosted in-browser meetings as a booking location.
Full setup guide, including recording consent, the AI notetaker, and host controls:
[docs/VIDEO.md](docs/VIDEO.md).

- **Credentials live in the app, not env vars.** Create a project at
  [cloud.livekit.io](https://cloud.livekit.io) (or self-host LiveKit), then enter the
  **Server URL** (`wss://…`), **API Key**, and **API Secret** in **Settings → Video**.
  "Calnode Video (LiveKit)" then becomes selectable as an event-type location.
- **Recording reuses your Litestream bucket.** If `LITESTREAM_*` (§6) is configured,
  meeting recordings are written to that same bucket under a `recordings/` prefix — no
  extra storage to set up. Turn recording on in **Settings → Video / Storage**; downloads
  appear on the **Recordings** page (presigned, short-lived URLs).
- **Register the webhook (recommended).** In **Settings → Video**, copy the webhook URL
  (`https://<your-domain>/v1/livekit/webhook`) into **LiveKit Cloud → Project → Settings →
  Webhooks**, and attach the **same API key** you entered above (it signs the events).
  Recordings still finalize without it — the webhook just adds accurate duration and a
  clean "room closed" backstop. A healthy hit shows `POST /v1/livekit/webhook → 200`; a
  `403` means the webhook's API key doesn't match the one in Settings → Video.

---

## 8. First run

Open `https://<your-domain>/` → it redirects to `/admin/`. On a fresh database
you'll be guided through **first-run setup** (create the owner account). Then:
Settings → Email, → Google OAuth, → Branding (logo, business name), and create your
first event type + availability.

---

## 9. Troubleshooting

| Symptom | Likely cause |
|---|---|
| App won't start (prod) | Missing `CALNODE_ENCRYPTION_KEY` with an https `BASE_URL`. |
| Custom domain 502 / "Application failed to respond" | Custom-domain **target port ≠ the listening port** (use 8080 on Railway). |
| `ERR_CERT_COMMON_NAME_INVALID` on a new domain | Cert not issued yet — wait; ensure the DNS record is **DNS-only**, not proxied. |
| 403 on admin actions behind a proxy | Proxy not forwarding the original `Host` header (CSRF same-origin check). |
| OAuth `redirect_uri_mismatch` | Registered URI doesn't match `BASE_URL` + `/v1/...callback` exactly. |
| Email `550 domain not verified` | From address domain isn't verified with your email provider. |
| Logo broken in email when testing locally | Gmail's image proxy can't reach `localhost` — only loads from a public URL. |
| Litestream `InvalidAccessKeyId` / 403, log shows `endpoint=""` | `LITESTREAM_ENDPOINT` unset (or the running build predates the endpoint/region config) → Litestream defaults to AWS. Set the **account** endpoint (no bucket) + `region=auto` for R2, and redeploy so the config is live. |
