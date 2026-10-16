#!/usr/bin/env python3
"""Assert that published NS8 module tags are genuinely multi-arch and coherent.

This is the release gate. It exists because a release once published an
amd64-only image as `:latest`, which installs on amd64 and hard-fails on arm64,
and nothing noticed: the publish step succeeded, and no check looked at what had
actually landed in the registry.

Run it after `push-manifest.sh`. Usage:

    assert-multiarc.py <module-repo> <tag> [<tag>...]

Exit status is 0 only if every tag passes every check for every required
architecture.

For each required architecture this verifies:

  1. the tag resolves to an image index / manifest list, not a single manifest.
     A single manifest means an arm64 node cannot pull the tag at all.
  2. the index contains that architecture.
  3. that architecture's image config really declares that architecture, so an
     entry cannot be mislabelled inside the list.
  4. that config carries an `org.nethserver.images` label. NS8 reads it at
     install time to learn which app image to run, so a missing label produces a
     module that installs and then does nothing.
  5. the app image the label names is itself multi-arch and contains that same
     architecture, so the module cannot point at an app image its own node
     cannot run.

Defaults assume the public repository; override with MODULE_REPO /
REQUIRED_ARCHES.
"""
import json
import os
import sys
import time
import urllib.error
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

# A push and an immediate read can race on the registry side. Retry briefly so a
# release is not failed by propagation latency, but fail rather than wait long
# enough to hide a real problem.
ATTEMPTS = 4
RETRY_SECONDS = 10

_module_repo = os.environ.get("MODULE_REPO", "calnode/ns8-calnode")
_required = os.environ.get("REQUIRED_ARCHES", "amd64 arm64").split()
_tokens = {}


class Fail(Exception):
    """A check failed in a way worth reporting verbatim."""


def token(repo):
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
    """Tolerate a short propagation delay after a push, then give up loudly."""
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


def check_tag(repo, tag, arch_names):
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
        print(f"  ok    linux/{arch} org.nethserver.images = {app_ref}")

        app_repo, app_tag = split_ref(app_ref)
        abody, actype = fetch_with_retry(app_repo, app_tag, f"app image {app_ref}")
        amtype = abody.get("mediaType") or actype
        if amtype not in INDEX_TYPES:
            raise Fail(f"app image {app_ref} is single-arch ({amtype})")
        if ("linux", arch) not in arches(abody):
            raise Fail(f"app image {app_ref} does not contain linux/{arch}")
        print(f"  ok    app image {app_ref} is multi-arch and contains linux/{arch}")


def main(argv):
    repo = argv[1] if len(argv) > 1 else _module_repo
    tags = argv[2:]
    if not tags:
        print(__doc__)
        return 2

    failures = []
    for tag in tags:
        try:
            check_tag(repo, tag, _required)
        except Fail as exc:
            print(f"  FAIL  {exc}")
            failures.append(f"{repo}:{tag}: {exc}")
        except Exception as exc:  # noqa: BLE001 - report, never mask
            print(f"  ERROR {type(exc).__name__}: {exc}")
            failures.append(f"{repo}:{tag}: {type(exc).__name__}: {exc}")

    print("\n" + "=" * 62)
    for f in failures:
        print(f"  FAIL  {f}")
    if failures:
        print(
            f"\n{len(failures)} of {len(tags)} published tag(s) are not usable. "
            "Refusing to call this release good."
        )
        return 1
    print(f"  PASS  all {len(tags)} published tag(s) are multi-arch and coherent")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))