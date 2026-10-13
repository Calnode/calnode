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

# Add imageroot directory to the container image
container=$(buildah from scratch)
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
buildah commit "${container}" "${repobase}/${reponame}:build"

if [ "${PUSH:-0}" = "1" ]; then
    for imagetag in "${imagetags[@]}"; do
        buildah tag "${repobase}/${reponame}:build" "${repobase}/${reponame}:${imagetag}"
        buildah push "${repobase}/${reponame}:${imagetag}" "docker://${repobase}/${reponame}:${imagetag}"
    done
else
    printf "Built %s:%s (app %s).\nPublish with:\n\n" "${repobase}/${reponame}" "build" "${app_image}"
    for imagetag in "${imagetags[@]}"; do
        printf "  buildah tag %s:build %s:%s\n" "${repobase}/${reponame}" "${repobase}/${reponame}" "${imagetag}"
        printf "  buildah push %s:%s docker://%s:%s\n" "${repobase}/${reponame}" "${imagetag}" "${repobase}/${reponame}" "${imagetag}"
    done
    printf "\nOr rerun with PUSH=1 IMAGETAGS=\"...\" to push directly.\n"
fi
