#!/bin/bash

#
# Copyright (C) 2023 Nethesis S.r.l.
# SPDX-License-Identifier: GPL-3.0-or-later
#

# Build the ns8-calnode module image and optionally push it.
#
# Run from this directory (packaging/ns8). Environment:
#   REPOBASE   module image namespace, default ghcr.io/calnode
#   APP_IMAGE  Calnode app image the module runs, default
#              ghcr.io/calnode/calnode:latest. Release CI passes the app
#              tag matching the release, so module 1.2.3 always runs app
#              1.2.3 and the two can never drift apart. APP_IMAGE is only
#              used for the org.nethserver.images label: the systemd unit
#              reads the exact reference from ${CALNODE_IMAGE}, which the
#              cluster writes into the module environment at install time
#              from that same label. One source of truth, no stamping.
#   TARGETARCH  architecture to build for, default amd64. NS8 runs modules
#              as rootless containers on the node, so an amd64-only module
#              image cannot be installed on an arm64 node: build each arch
#              and assemble them into one manifest tag (see push-manifest.sh).
#   IMAGETAGS  space-separated module tags to push, default "latest"
#   PUSH=1     push after building. Default 0 prints the push commands.
#
# The module image only carries configuration scripts and the settings UI.
# The Calnode application itself is pulled from APP_IMAGE at install time.

# Terminate on error
set -e

# Must run from packaging/ns8 (uses relative imageroot and ui paths)
if [ ! -d imageroot ] || [ ! -d ui ]; then
    echo "Run build-images.sh from the packaging/ns8 directory." >&2
    exit 1
fi

# Prepare variables for later use. Registry namespaces must be lowercase
# (docker:// rejects uppercase); normalize up front so every use below is safe.
repobase="${REPOBASE:-ghcr.io/calnode}"
repobase="${repobase,,}"
reponame="ns8-calnode"
app_image="${APP_IMAGE:-ghcr.io/calnode/calnode:latest}"
read -r -a imagetags <<< "${IMAGETAGS:-latest}"
targetarch="${TARGETARCH:-amd64}"

# Per-arch build tag. push-manifest.sh stitches these into the public tags.
buildtag="build-${targetarch}"

# Add imageroot directory to the container image, pinned to this arch.
container=$(buildah from --arch "${targetarch}" scratch)
buildah add "${container}" imageroot /imageroot

# Reuse existing nodebuilder-ns8-calnode container, to speed up builds
if ! buildah containers --format "{{.ContainerName}}" | grep -q nodebuilder-ns8-calnode; then
    echo "Pulling NodeJS runtime..."
    buildah from --name nodebuilder-ns8-calnode -v "${PWD}:/usr/src:Z" docker.io/library/node:24.21.0-slim
fi

echo "Build static UI files with node..."
buildah run \
    --workingdir=/usr/src/ui \
    --env="NODE_OPTIONS=--openssl-legacy-provider" \
    nodebuilder-ns8-calnode \
    sh -c "corepack enable && yarn install && yarn build"

# Add the UI files to the container image
buildah add "${container}" ui/dist /ui
# Setup the entrypoint, ask to reserve one TCP port with the label and set a rootless container
buildah config --entrypoint=/ \
    --label="org.nethserver.authorizations=traefik@node:routeadm" \
    --label="org.nethserver.tcp-ports-demand=1" \
    --label="org.nethserver.rootfull=0" \
    --label="org.nethserver.volumes=calnode-data" \
    --label="org.nethserver.images=${app_image}" \
    "${container}"
# Commit the image (repobase already lowercased at variable prep above).
buildah commit "${container}" "${repobase}/${reponame}:${buildtag}"

if [ "${PUSH:-0}" = "1" ]; then
    # Push only the per-arch tag. The public tags are assembled from these by
    # push-manifest.sh, so a half-finished release can never publish a
    # single-arch image under a version tag.
    buildah push "${repobase}/${reponame}:${buildtag}" \
        "docker://${repobase}/${reponame}:${buildtag}"
else
    printf "Built %s:%s (arch %s, app %s).\n\n" "${repobase}/${reponame}" "${buildtag}" "${targetarch}" "${app_image}"
    printf "To publish this arch and assemble the multi-arch tags:\n\n"
    printf "  PUSH=1 TARGETARCH=%s IMAGETAGS=\"%s\" bash build-images.sh\n" \
        "${targetarch}" "${imagetags[*]}"
    printf "  TARGETARCHES=\"%s\" IMAGETAGS=\"%s\" bash push-manifest.sh\n" \
        "${targetarch}" "${imagetags[*]}"
fi
