#!/bin/bash

#
# Copyright (C) 2023 Nethesis S.r.l.
# SPDX-License-Identifier: GPL-3.0-or-later
#

# Assemble the per-arch module images built by build-images.sh into one
# multi-arch tag.
#
# NS8 runs each module as a rootless container on the node, so the module
# image must exist for every node architecture. A single-arch image would
# install fine on amd64 and fail on arm64, so the per-arch tags are never
# published as-is: only the assembled tags are public.
#
# Run from this directory (packaging/ns8), after pushing each arch with
# build-images.sh. Environment:
#   REPOBASE       module image namespace, default ghcr.io/calnode
#   TARGETARCHES   space-separated arches to assemble, required
#   IMAGETAGS      space-separated tags to publish, default "latest"
#
# Example:
#   TARGETARCHES="amd64 arm64" IMAGETAGS="1.2.3 1.2 latest" bash push-manifest.sh

set -e

repobase="${REPOBASE:-ghcr.io/calnode}"
repobase="${repobase,,}"
reponame="ns8-calnode"
read -r -a arches <<< "${TARGETARCHES:-}"
read -r -a imagetags <<< "${IMAGETAGS:-latest}"

if [ "${#arches[@]}" -eq 0 ]; then
    echo "TARGETARCHES is required, e.g. TARGETARCHES=\"amd64 arm64\"" >&2
    exit 1
fi

for arch in "${arches[@]}"; do
    if ! buildah image exists "${repobase}/${reponame}:build-${arch}"; then
        echo "Missing local image for ${arch}. Run build-images.sh with" \
             "TARGETARCH=${arch} PUSH=1 first." >&2
        exit 1
    fi
done

for imagetag in "${imagetags[@]}"; do
    manifest="${repobase}/${reponame}:${imagetag}"
    # Drop any stale manifest so a retry cannot inherit old entries.
    buildah manifest rm --all "${manifest}" 2>/dev/null || true
    buildah manifest create "${manifest}"
    for arch in "${arches[@]}"; do
        buildah manifest add \
            "${manifest}" \
            "${repobase}/${reponame}:build-${arch}" \
            "docker.io/calnode/ns8-calnode:${imagetag}-${arch}"
    done
    buildah manifest push --all "${manifest}" "docker://${manifest}"
    buildah manifest rm "${manifest}"
    echo "Published ${manifest} (${arches[*]})"
done