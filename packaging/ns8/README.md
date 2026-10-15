# ns8-calnode

NethServer 8 community module wrapping [Calnode](https://github.com/calnode/calnode),
the self-hostable scheduling and booking platform. Install it on your cluster
with one command and get Calnode on your own domain. No Calnode docs required.

## How it works

This module is a thin wrapper around the published Calnode container image
(`ghcr.io/calnode/calnode:latest`). Nothing is ported or rebuilt: the module
image only carries configuration scripts and the settings UI. At install time
the cluster pulls the Calnode image and runs it as a rootless container.

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
add-module ghcr.io/calnode/ns8-calnode:edge 1
```

Replace `calnode` with the actual registry namespace if you built your own
image (see Development). The command prints the instance name, usually
`ns8-calnode1`.

> **Use `:edge`, not `:latest`.** This module has not had a release tag yet, so
> `:latest` still points at the first build, which predates the fixes for a
> missing `create-module` action, non-executable action scripts and an
> invalid schema pattern. That build fails at `add-module`. `:latest` becomes
> usable at the next module release, at which point a pinned version tag
> (`ghcr.io/calnode/ns8-calnode:0.10.2`, say) is the better thing to install.

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

Because `:latest` only moves on a release tag, and no module release has been
cut yet, `:latest` is currently unusable (see Install). Use `:edge` until the
first module release lands.

Every published module tag is a **multi-arch** manifest (amd64 + arm64). NS8
runs modules as rootless containers on the node, so a single-arch image would
install on amd64 and fail on arm64. CI builds each arch to a private
`:build-<arch>` tag and only then assembles the public tags, so an interrupted
release cannot publish a single-arch image under a version tag.

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
