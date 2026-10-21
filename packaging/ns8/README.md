# ns8-calnode

NethServer 8 community module wrapping [Calnode](https://github.com/calnode/calnode),
the self-hostable scheduling and booking platform. Install it on your cluster
with one command and get Calnode on your own domain. No Calnode docs required.

## How it works

This module is a thin wrapper around the published Calnode container image.
Nothing is ported or rebuilt: the module image only carries configuration
scripts and the settings UI. At install time the cluster pulls the Calnode
image named by the module's `org.nethserver.images` label and runs it as a
rootless container. A module release always names the matching app release, so
the two cannot drift apart.

Key facts:

- App listens on port 3000 inside the container, published on 127.0.0.1 only.
  There is no inbound port beyond the app itself.
- TLS is handled by the cluster Traefik proxy, not the module. Set the
  hostname and the module registers a host-based route with Let's Encrypt.
- The whole database is one SQLite file on the `calnode-data` named volume.
  The volume survives module updates. Never delete it unless you mean to
  wipe all bookings, users and settings.
- First configuration generates a `CALNODE_ENCRYPTION_KEY` that unlocks the
  database key vault. It is stored in the module state and covered by cluster
  backup. If you lose both the volume and this key, bookings become
  unreadable, so keep backups working.

## Install

On the cluster leader, as root:

```
add-module ghcr.io/calnode/ns8-calnode:0.11.1 1
```

Replace `calnode` with the actual registry namespace if you built your own
image (see Development). The command prints the instance name, usually
`ns8-calnode1`.

> **Pin a version, don't use `:edge`.** `0.11.1`, `0.11` and `:latest` are all
> the same release and are all multi-arch, so `:latest` is now safe to install.
> A pinned tag is still the better choice: it tells you exactly what you are
> running, and an update is then a deliberate act rather than whatever `main`
> happened to publish. Use `:edge` only if you are deliberately testing the
> unreleased build.
>
> The first release (0.10.1) was amd64-only and failed on arm64 nodes. That is
> fixed, and CI now reads every published tag back out of the registry and
> refuses the release unless it really is a two-arch index, so it cannot recur
> silently.

Then configure it with your public hostname:

```
api-cli run module/ns8-calnode1/configure-module --data '{"host": "book.example.com", "lets_encrypt": true, "http2https": true}'
```

Or open the cluster admin UI, find the instance, and fill in the Settings page:
hostname, Let's Encrypt toggle, HTTP to HTTPS redirect toggle.

Calnode is then live at `https://book.example.com`. Sign up the first admin
account in the web UI. Mail delivery (invites, reminders) is configured
inside Calnode under Settings, using your SMTP smarthost values.

## Update

Update from the Software Center like any other module. The container image is
swapped and the service restarts; the `calnode-data` volume is untouched, so
bookings, users and settings carry over. New module releases track new
Calnode app releases.

To move an existing install to a new hostname, just run `configure-module`
again (or save the Settings page) with the new host. The encryption key and
the database are preserved.

## Backup and restore

Backups run through the standard cluster backup in the cluster admin UI.
Each backup of the instance contains:

- the `calnode-data` volume (the SQLite database file, the whole app state)
- the module state, including `calnode.env` with `CALNODE_ENCRYPTION_KEY`

This only works because the module ships `imageroot/etc/state-include.conf`.
NS8 hands that file to restic as `--files-from`, so it backs up **only** the
paths listed there: a module with named volumes and no such file is backed up
successfully while silently omitting the data, and a module that lists only its
volume silently omits the encryption key. Both lines are required:

- `volumes/calnode-data` - the SQLite database, the whole app state
- `state/calnode.env` - holds `CALNODE_ENCRYPTION_KEY`; without it a restored
  database cannot be decrypted, and the app fails to start with
  "keyvault: failed to unwrap DEK"

Measured on a live cluster, same instance, same data:

| state-include.conf | files | size |
|---|---|---|
| absent | 2 | 476 bytes (no database) |
| volume only | 7 | 2.16 MB (database, no key) |
| volume + `state/calnode.env` | 8 | 2.16 MB |

Restore to the same or a replacement node from the cluster admin backup
section, then re-run `configure-module` with the same hostname if the route
needs recreating. Keep at least one tested backup: the encryption key and
the database are only useful together.

Adopting data from a non-NS8 install is CLI-only: import the SQLite file
into the `calnode-data` volume, then pass its key explicitly, e.g.
`api-cli run module/<id>/configure-module --data '{"host": "...",
"encryption_key": "<key from the old install>"}'`. The Settings page does
not expose the key field; cluster backup/restore never needs it (the key
travels inside the backed-up module state).

Live-tested 2026-10-13/14 on a single-node Rocky 9 cluster against the
`:edge` build: install, configure, host-based route (root 302, `/admin/` 200
through Traefik), SQLite on the `calnode-data` volume, encryption key
byte-identical across reconfigures, `remove-module --no-preserve` with
automatic route cleanup, and a full cluster backup → destroy → restore cycle:
after restoring, the app served 200, logged "keyvault: DEK unwrapped from
keystore", the encryption-key fingerprint was unchanged, and an event type
created before the backup was still there.

The `tests/` robot suite has now been run against a live cluster: 4/4 without
the browser and 5/5 with `RUN_UI_TESTS=true`, on a single-node NS8 built from
`ns8-core@ns8-stable` on Rocky 9.8. Running it is what turned up the
route-registration race described in [Testing](#testing).

## Data and privacy

This section is what the module's `terms_url` in `ui/public/metadata.json`
points at, so it is worth keeping accurate.

**Everything stays on your cluster.** Calnode runs as a container on your node
and stores everything in a local SQLite file on the `calnode-data` volume. The
module and the app phone home to nothing. There is no Calnode-operated server,
no telemetry, no analytics, and no licence check that calls out.

**What is stored.** The booking data you enter: event types, availability,
bookings, attendee names and email addresses, any notes you attach to a booking,
and your user accounts. Plus the module's own configuration: the hostname,
the Let's Encrypt and HTTP-to-HTTPS toggles, and `CALNODE_ENCRYPTION_KEY`.

**The encryption key.** On first configuration the module generates
`CALNODE_ENCRYPTION_KEY`, which unlocks the database key vault so secrets
stored inside the database are not readable in the raw file. It lives in the
module state at `/var/lib/nethserver/calnode/state/calnode.env`, with the same
file permissions as the rest of the cluster state, and it is covered by cluster
backup. It is never transmitted anywhere.

**Who can send mail.** Invites and reminders go out through whatever SMTP server
you configure inside Calnode under Settings. If you point that at a third-party
smarthost, that provider necessarily sees the recipient addresses and message
content. Nothing is sent until you configure it.

**TLS.** Terminated by the cluster's own Traefik proxy using a Let's Encrypt
certificate for the hostname you set. Traffic does not pass through Calnode or
any Calnode infrastructure. If you enable Let's Encrypt, the ACME challenge
requires your domain to resolve publicly, which is a property of your DNS, not
a disclosure to us.

**Backup.** Cluster backup copies the `calnode-data` volume and the module
state, including the encryption key, to whatever destination you configure. The
operator of that destination, which may be a third-party storage provider, holds
an encrypted copy of your database and the key that unlocks it. Configure the
backup accordingly.

## Uninstall

To uninstall the instance, keeping its data for a later reinstall:

```
remove-module --preserve ns8-calnode1
```

To wipe everything including bookings:

```
remove-module --no-preserve ns8-calnode1
```

## Development

This module lives in the Calnode repo under `packaging/ns8/` and ships from
the same CI as the app. The root workflow `.github/workflows/ns8-module.yml`
builds the module image on every PR (no push) and publishes on branch and
tag pushes, mirroring the app tags in `docker-publish.yml`:

- push to `main` pushes module `:edge`, running app `:edge`
- push to `dev` pushes module `:dev`, running app `:dev`
- push a `vX.Y.Z` tag pushes module `:X.Y.Z`, `:X.Y` and `:latest`, running
  app `:X.Y.Z`, so a module release always runs the matching app release

`:latest` moves only when a release tag is pushed, so it always tracks a real
release rather than whatever is on `main`. `:edge` tracks `main` and is the
unreleased build.

Every published module tag is a **multi-arch** manifest (amd64 + arm64). NS8
runs modules as rootless containers on the node, so a single-arch image would
install on amd64 and fail on arm64. CI builds each arch to a private
`:build-<arch>` tag and only then assembles the public tags, so an interrupted
release cannot publish a single-arch image under a version tag. After
assembling, CI reads the published tag back out of the registry and asserts it
really is a two-arch index with coherent per-arch labels, so a publish that
silently produced a single-arch tag fails the release rather than shipping.

**arm64 status, stated honestly.** The image is built for both architectures and
the manifest lists both, so an arm64 node can install it. The arm64 app binary
has been executed and works: it generated its encryption key, applied all
database migrations, and served HTTP. That was under CPU emulation, though, so
**arm64 is not yet verified on real hardware.** Nothing here has run on a
physical arm64 machine or on an arm64 NS8 node, and emulation cannot show native
kernel behaviour or performance. Treat arm64 as untested until it has been
exercised on a device.

To build locally on a machine with podman and buildah, from this directory:

```
PUSH=1 TARGETARCH=amd64 bash build-images.sh
PUSH=1 TARGETARCH=arm64 bash build-images.sh
TARGETARCHES="amd64 arm64" IMAGETAGS="latest" bash push-manifest.sh
```

Override the app reference per build with e.g. `APP_IMAGE=ghcr.io/calnode/calnode:1.2.3
IMAGETAGS="1.2.3 1.2 latest"`. `APP_IMAGE` only sets the
`org.nethserver.images` label: at install the cluster writes that same
reference into the module environment as `CALNODE_IMAGE`, which is what the
systemd unit and configure-module actually run. The label is the single
source of truth; nothing is stamped at build time.

## Testing

The suite in `tests/` is Robot Framework. It installs the module on a real NS8
cluster, configures it, checks the route responds, screenshots the settings UI
and removes the module again:

| file | what it does |
|---|---|
| `10__calnode.robot` | install, configure, route responds |
| `15__ui.robot` | screenshots of the Status and Settings pages, tagged `ui` |
| `20__remove.robot` | removal |

Locally, against a cluster you already have (see the
[NS8 testing docs](https://github.com/NethServer/ns8-github-actions/blob/v1/README.md#running-tests-locally)):

```
run-ns8-tests <NODE_ADDR> ghcr.io/calnode/ns8-calnode:edge
```

`NODE_ADDR` must be reachable by SSH as root, using the key at `~/.ssh/id_ecdsa`
or wherever `SSH_KEYFILE` points. Add `RUN_UI_TESTS=true` to include the `ui`
tests; without it they are skipped and the runner uses a lighter image.

In CI, `.github/workflows/test-module.yml` does the same thing and provisions
the cluster on DigitalOcean. It is manual-only for now because it needs a
`do_token` repository secret this repo does not have yet. It also deliberately
does not use the stock `test-module.yml@v1`, which derives the image under test
from the repository name and would resolve it to `ghcr.io/calnode/calnode:latest`
instead of `ghcr.io/calnode/ns8-calnode:edge`; the workflow header explains the
full reasoning.

Output lands in `tests/outputs/` and is git-ignored.

## UI translation

Translated with [Weblate](https://hosted.weblate.org/projects/ns8/).

To setup the translation process:

- add [GitHub Weblate app](https://docs.weblate.org/en/latest/admin/code-hosting.html#code-hosting-github-notifications) to your repository
- add your repository to [hosted.weblate.org](https://hosted.weblate.org) or ask a NethServer developer to add it to ns8 Weblate project
