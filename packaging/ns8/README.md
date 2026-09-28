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
add-module ghcr.io/calnode/ns8-calnode:latest 1
```

Replace `calnode` with the actual registry namespace if you built your own
image (see Development). The command prints the instance name, usually
`ns8-calnode1`.

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

Restore to the same or a replacement node from the cluster admin backup
section, then re-run `configure-module` with the same hostname if the route
needs recreating. Keep at least one tested backup: the encryption key and
the database are only useful together.

UNTESTED: no live NS8 node was reachable while writing this, so install,
configure, update, backup and restore steps above are documented from the
NS8 module layout but have not been run against a real cluster. The
automated test suite in `tests/` covers install, configure, HTTP check and
removal once a node is available.

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

- push to `main` pushes module `:edge` and `:latest`, running app `:edge`
- push to `dev` pushes module `:dev`, running app `:dev`
- push a `vX.Y.Z` tag pushes module `:X.Y.Z`, `:X.Y` and `:latest`, running
  app `:X.Y.Z`, so a module release always runs the matching app release

To build locally on a machine with podman and buildah, from this directory:

```
bash build-images.sh
```

It prints the `buildah push` commands for your registry. Override the app
reference per build with e.g. `APP_IMAGE=ghcr.io/calnode/calnode:1.2.3
IMAGETAGS="1.2.3 latest" PUSH=1 bash build-images.sh`. The systemd unit in
the tree keeps a `__CALNODE_APP_IMAGE__` placeholder that the script stamps
at build time, so the committed files never name an app tag.

Running the test suite needs an NS8 test cluster; see the
[NS8 testing docs](https://github.com/NethServer/ns8-github-actions/blob/v1/README.md#running-tests-locally).
UNTESTED here: no test cluster was available.

## UI translation

Translated with [Weblate](https://hosted.weblate.org/projects/ns8/).

To setup the translation process:

- add [GitHub Weblate app](https://docs.weblate.org/en/latest/admin/code-hosting.html#code-hosting-github-notifications) to your repository
- add your repository to [hosted.weblate.org](https://hosted.weblate.org) or ask a NethServer developer to add it to ns8 Weblate project
