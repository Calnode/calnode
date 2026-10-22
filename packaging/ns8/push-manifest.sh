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
#   SOURCE_PREFIX  transport prefix used when reading each per-arch image,
#                  default "docker://". Set it to the empty string to read from
#                  local containers-storage instead, which only the hermetic
#                  self-test wants. Do not do that for a real release: see the
#                  note on the manifest add below.
#   PUSH_DESTINATION  push target override, default docker://<tag>. Set to an
#                     oci: path to exercise the whole script with no registry,
#                     which is what the CI selftest does.
#
# Example:
#   TARGETARCHES="amd64 arm64" IMAGETAGS="1.2.3 1.2 latest" bash push-manifest.sh

set -e

repobase="${REPOBASE:-ghcr.io/calnode}"
repobase="${repobase,,}"
reponame="ns8-calnode"
read -r -a arches <<< "${TARGETARCHES:-}"
read -r -a imagetags <<< "${IMAGETAGS:-latest}"
# ${x-default}, not ${x:-default}: an empty SOURCE_PREFIX is a deliberate
# request to read locally, and ${x:-} would silently overwrite it with the
# default and defeat the hermetic self-test.
source_prefix="${SOURCE_PREFIX-docker://}"

if [ "${#arches[@]}" -eq 0 ]; then
    echo "TARGETARCHES is required, e.g. TARGETARCHES=\"amd64 arm64\"" >&2
    exit 1
fi

for imagetag in "${imagetags[@]}"; do
    manifest="${repobase}/${reponame}:${imagetag}"
    # Drop any stale manifest so a retry cannot inherit old entries.
    buildah manifest rm "${manifest}" 2>/dev/null || true
    buildah manifest create "${manifest}"
    for arch in "${arches[@]}"; do
        # Read each architecture from the REGISTRY, not from local
        # containers-storage. Reading locally is what broke the v0.11.0 release:
        # buildah(1) there had two graph roots merged into a single store, so the
        # local image record existed while the manifest blob it pointed at did
        # not, and the add failed with
        #
        #   reading manifest for instance "sha256:...": file does not exist
        #
        # build-images.sh has already pushed the per-arch tags by the time this
        # runs, so docker:// is both the documented form (the example in
        # buildah-manifest-add(1) is `buildah manifest add mylist:v1.11
        # docker://fedora`) and the source of truth for what actually ships.
        #
        # It also removes the need for the old `buildah images -q` pre-check,
        # which could not answer the question reliably: it inspects the local
        # store, so it happily reported a present image whose manifest was
        # missing, and would equally have reported a published image as absent.
        # A failed add is now the check, and it is authoritative.
        #
        # Exactly two arguments: buildah 5.x takes only the list and the image,
        # with no instance-name argument.
        if ! buildah manifest add "${manifest}" \
             "${source_prefix}${repobase}/${reponame}:build-${arch}"; then
            echo "Failed to add ${source_prefix}${repobase}/${reponame}:build-${arch}" \
                 "to ${manifest}." >&2
            echo "If that tag does not exist in the registry, run build-images.sh" \
                 "with TARGETARCH=${arch} PUSH=1 first." >&2
            buildah manifest rm "${manifest}" 2>/dev/null || true
            exit 1
        fi
    done
    # Refuse to publish a partial manifest. If any arch silently failed to
    # join the list, an arm64 node would get an image it cannot run.
    count=$(buildah manifest inspect "${manifest}" | grep -c '"architecture"')
    if [ "$count" -ne "${#arches[@]}" ]; then
        echo "Refusing to publish ${manifest}: it holds ${count} arches," \
             "expected ${#arches[*]}" >&2
        buildah manifest rm "${manifest}" 2>/dev/null || true
        exit 1
    fi
    buildah manifest push --all "${manifest}" "${PUSH_DESTINATION:-docker://${manifest}}"
    buildah manifest rm "${manifest}"
    echo "Published ${manifest} (${arches[*]})"
done