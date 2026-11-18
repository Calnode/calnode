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

# A network blip and a missing image both come back as a failed `manifest add`,
# and telling them apart matters in both directions. Retrying a
# genuine 404 hides a build that never ran. Reporting a TCP reset as a missing
# tag sends the reader to rebuild and push an image that already exists, which
# is real pointless work: that is exactly what a reset during this script's CI
# selftest used to say.
#
# So: classify, retry only transport failures, and let the message name the case.
retry_attempts="${RETRY_ATTEMPTS:-3}"
retry_delay="${RETRY_DELAY:-5}"

# Transport failures. Deliberately a specific list rather than a broad "error"
# match, because the fallback has to stay fail-fast: anything we do not recognise
# is a real failure, not something to retry until the job times out.
is_network_error() {
    grep -qiE 'connection reset|connection refused|i/o timeout|context deadline exceeded|timed out|temporary failure in name resolution|no such host|network is unreachable|tls handshake|unexpected eof|(^|[^0-9])(502|503|504)([^0-9]|$)|bad gateway|service unavailable|gateway timeout|internal server error'
}

# add_arch RETRIES the add, then records why it gave up in ADD_OUTPUT/ADD_KIND
# so the caller can print the right message. Returns 0 on success.
add_arch() {
    local src="$1" attempt=1 delay="$retry_delay" output
    while :; do
        if output=$(buildah manifest add "${manifest}" "${src}" 2>&1); then
            return 0
        fi
        if printf '%s' "$output" | is_network_error; then
            if [ "$attempt" -lt "$retry_attempts" ]; then
                echo "Registry unreachable (attempt ${attempt}/${retry_attempts})," \
                     "retrying in ${delay}s: ${src}" >&2
                sleep "$delay"
                attempt=$((attempt + 1))
                delay=$((delay * 2))
                continue
            fi
            ADD_KIND=network
        else
            ADD_KIND=missing
        fi
        ADD_OUTPUT="$output"
        return 1
    done
}

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
        if ! add_arch "${source_prefix}${repobase}/${reponame}:build-${arch}"; then
            src="${source_prefix}${repobase}/${reponame}:build-${arch}"
            # Show what the tool actually said, then say which case it is.
            printf '%s\n' "${ADD_OUTPUT}" | tail -n 4 | sed 's/^/  /' >&2
            if [ "${ADD_KIND}" = "network" ]; then
                echo "The registry was unreachable while reading ${src}." >&2
                echo "This is a TRANSPORT failure, not a missing image: the tag was" >&2
                echo "never reported absent and nothing is wrong with the build. Do" >&2
                echo "not rebuild or push the image. Re-run the job; if it keeps" >&2
                echo "failing, check connectivity to the registry from this host." >&2
            else
                echo "Failed to add ${src}" "to ${manifest}." >&2
                echo "If that tag does not exist in the registry, run build-images.sh" \
                     "with TARGETARCH=${arch} PUSH=1 first." >&2
            fi
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