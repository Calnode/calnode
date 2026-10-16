#!/usr/bin/env python3
"""Assert that published NS8 module tags are genuinely multi-arch and coherent.

Two modes, because two things have very different lifetimes:

  module-only (default)
      Checks the module image itself: that the tag is an image index, that every
      required architecture is present, that each architecture's config really
      declares it, and that it carries a well-formed `org.nethserver.images`
      label. These are intrinsic properties of what the module workflow just
      published, so this is safe to run there.

  --pair
      Additionally resolves the app image named by each label and asserts it
      exists, is multi-arch, and contains each architecture. This must NOT run
      in the module workflow: `ns8-module.yml` and `docker-publish.yml` are
      triggered by the same tag push and run CONCURRENTLY, so on a fresh release
      the app image does not exist yet and the check races the app publish. It
      belongs in release-verify.yml, which runs after both have completed.

Usage:
    assert-multiarc.py [--pair] [--expect-app-tag TAG] <module-repo> <tag> [<tag>...]

    --pair             resolve and check the app image named by each label
    --expect-app-tag T assert every label names exactly this app tag, so the
                       module release and the app release are the same version

There is deliberately NO option to override the app repository. The app image a
module runs is whatever its org.nethserver.images label names, so the label is
the only authoritative source and it is also what a fork needs: a module built
under someone else's namespace names their app image, and deriving from the
label handles that with no configuration. An override here would be worse than
useless, because it would let a pair check be asserted against an app repository
the module does not actually reference, hiding a mislabelled module.

Exit status is 0 only if every tag passes every applicable check.

Why the distinction between a slow app image and a missing one is not a guess:
in --pair mode both publish workflows have already finished, so an app tag that
is still absent is genuinely wrong, not merely late. The retry here only absorbs
registry read-after-write propagation, it is bounded, and it then fails.
"""
import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

REGISTRY = "https://ghcr.io"
INDEX_TYPES = {
    "application/vnd.oci.image.index.v1+json",
    "application/vnd.docker.distribution.manifest.list.v2+json",
}
ACCEPT = ", ".join(INDEX_TYPES | {
    "application/vnd.oci.image.manifest.v1+json",
    "application/vnd.docker.distribution.manifest.v2+json",
})

# Read-after-write propagation only. Deliberately short: in --pair mode the app
# publish has already finished, so anything still missing after this is a real
# fault rather than a race, and must fail rather than wait.
ATTEMPTS = 4
RETRY_SECONDS = 10

_tokens = {}


class Fail(Exception):
    """A check failed in a way worth reporting verbatim."""


def token(repo):
    # ghcr's token endpoint requires the repository path LOWERCASE and answers
    # HTTP 400 otherwise. The owner arrives from GitHub as `github.repository_owner`
    # verbatim, which is `Calnode` for this repo, so normalise here rather than
    # trusting every caller to remember: a case slip here fails a correct release.
    repo = repo.lower()
    if repo not in _tokens:
        url = f"{REGISTRY}/token?scope=repository:{repo}:pull&service=ghcr.io"
        with urllib.request.urlopen(url, timeout=30) as f:
            _tokens[repo] = json.load(f)["token"]
    return _tokens[repo]


def get(repo, reference):
    """GET a manifest, returning (body, mediaType)."""
    req = urllib.request.Request(
        f"{REGISTRY}/v2/{repo}/manifests/{reference}",
        headers={"Authorization": f"Bearer {token(repo)}", "Accept": ACCEPT},
    )
    with urllib.request.urlopen(req, timeout=30) as f:
        return json.load(f), (f.headers.get("Content-Type") or "").split(";")[0].strip()


def get_blob(repo, digest):
    req = urllib.request.Request(
        f"{REGISTRY}/v2/{repo}/blobs/{digest}",
        headers={"Authorization": f"Bearer {token(repo)}"},
    )
    with urllib.request.urlopen(req, timeout=30) as f:
        return json.load(f)


def split_ref(ref):
    """ghcr.io/calnode/calnode:0.11.1 -> ('calnode/calnode', '0.11.1')"""
    body = ref.split("/", 1)[1] if ref.count("/") > 1 else ref
    name, _, tag = body.rpartition(":")
    return (name or body), tag


def arches(body):
    return {
        (m.get("platform", {}).get("os"), m.get("platform", {}).get("architecture"))
        for m in body.get("manifests", [])
        if m.get("platform", {}).get("architecture") not in (None, "unknown")
    }


def fetch_with_retry(repo, reference, what):
    """Absorb registry propagation, then fail loudly and quickly."""
    last = None
    for attempt in range(1, ATTEMPTS + 1):
        try:
            return get(repo, reference)
        except urllib.error.HTTPError as exc:
            last = exc
            if attempt < ATTEMPTS:
                print(f"    retrying {what} (HTTP {exc.code}, attempt {attempt}/{ATTEMPTS})")
                time.sleep(RETRY_SECONDS)
    raise Fail(f"cannot fetch {what}: HTTP {last.code}")


def app_refs_for(repo, tag, arch_names):
    """Check the module image and return the app refs its labels name."""
    print(f"\n=== {repo}:{tag}  (require {arch_names})")
    body, ctype = fetch_with_retry(repo, tag, f"{repo}:{tag}")
    mtype = body.get("mediaType") or ctype

    if mtype not in INDEX_TYPES:
        raise Fail(
            f"tag is a single-arch manifest ({mtype}), not a multi-arch index; "
            "an arm64 node cannot pull it"
        )
    have = arches(body)
    print(f"  ok    image index, arches present: {sorted(a for _, a in have)}")

    found = {}
    for arch in arch_names:
        if ("linux", arch) not in have:
            raise Fail(f"index is missing linux/{arch}")

        entry = next(
            m for m in body["manifests"]
            if m.get("platform", {}).get("os") == "linux"
            and m.get("platform", {}).get("architecture") == arch
        )
        sub, _ = get(repo, entry["digest"])
        cfg = get_blob(repo, sub["config"]["digest"])

        if cfg.get("architecture") != arch:
            raise Fail(
                f"linux/{arch} entry carries a {cfg.get('architecture')} config, "
                "so the entry is mislabelled"
            )
        print(f"  ok    linux/{arch} config really declares {arch}")

        labels = (cfg.get("config") or {}).get("Labels") or {}
        app_ref = labels.get("org.nethserver.images")
        if not app_ref:
            raise Fail(f"linux/{arch} has no org.nethserver.images label")
        app_repo, app_tag = split_ref(app_ref)
        if not app_tag:
            raise Fail(f"linux/{arch} label org.nethserver.images={app_ref} has no tag")
        print(f"  ok    linux/{arch} org.nethserver.images = {app_ref}")
        found[arch] = (app_repo, app_tag)

    # Every architecture must agree on the app image, or different nodes would
    # get different applications from the same module tag.
    distinct = set(found.values())
    if len(distinct) > 1:
        raise Fail(f"architectures disagree on the app image: {sorted(distinct)}")
    return found


def check_app_image(app_repo, app_tag, arch_names, what):
    abody, actype = fetch_with_retry(app_repo, app_tag, what)
    amtype = abody.get("mediaType") or actype
    if amtype not in INDEX_TYPES:
        raise Fail(f"{what} is single-arch ({amtype}); an arm64 node cannot run it")
    for arch in arch_names:
        if ("linux", arch) not in arches(abody):
            raise Fail(f"{what} does not contain linux/{arch}")
    print(f"  ok    {what} is multi-arch and contains {sorted(arch_names)}")


def check_tag(repo, tag, arch_names, pair, expect_app_tag):
    found = app_refs_for(repo, tag, arch_names)
    if not pair:
        print("  --    not checking the app image (module-only mode)")
        return

    for app_repo, app_tag in sorted(set(found.values())):
        if expect_app_tag and app_tag != expect_app_tag:
            raise Fail(
                f"label points at app tag '{app_tag}' but this release publishes "
                f"app tag '{expect_app_tag}'; the module and the app would be "
                "different versions"
            )
        check_app_image(
            app_repo,
            app_tag,
            arch_names,
            f"app image {app_repo}:{app_tag}",
        )
    if expect_app_tag:
        print(f"  ok    module {tag} and app {expect_app_tag} are the same release")


def main(argv=None):
    p = argparse.ArgumentParser(add_help=True, description=__doc__)
    p.add_argument("repo", help="module image repository, e.g. calnode/ns8-calnode")
    p.add_argument("tags", nargs="+", help="published module tags to check")
    p.add_argument("--pair", action="store_true",
                   help="also resolve the app image named by each label")
    p.add_argument("--expect-app-tag", default=None,
                   help="assert every label names exactly this app tag")
    p.add_argument("--required-arches",
                   default=os.environ.get("REQUIRED_ARCHES", "amd64 arm64"))
    args = p.parse_args(argv)

    arch_names = args.required_arches.split()
    failures = []
    for tag in args.tags:
        try:
            check_tag(args.repo, tag, arch_names, args.pair, args.expect_app_tag)
        except Fail as exc:
            print(f"  FAIL  {exc}")
            failures.append(f"{args.repo}:{tag}: {exc}")
        except Exception as exc:  # noqa: BLE001 - report, never mask
            print(f"  ERROR {type(exc).__name__}: {exc}")
            failures.append(f"{args.repo}:{tag}: {type(exc).__name__}: {exc}")

    print("\n" + "=" * 62)
    for f in failures:
        print(f"  FAIL  {f}")
    if failures:
        print(f"\n{len(failures)} of {len(args.tags)} tag(s) are not usable.")
        return 1
    mode = "multi-arch and coherent" if args.pair else "multi-arch with coherent labels"
    print(f"  PASS  all {len(args.tags)} tag(s) are {mode}")
    return 0


if __name__ == "__main__":
    sys.exit(main())