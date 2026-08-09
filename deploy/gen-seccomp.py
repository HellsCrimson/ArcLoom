#!/usr/bin/env python3
"""Regenerate deploy/seccomp-arcloom.json from Docker's current default profile.

The profile is Docker's default plus ONE rule allowing the five syscalls
bubblewrap needs to build the engine sandbox (see deploy/README.md for why each
is required and how that was established). It is a pinned copy, so it does not
follow upstream on its own — re-run this after a Docker upgrade if a new glibc
starts using a syscall the pinned copy does not list.
"""
import collections
import json
import sys
import urllib.request

SOURCE = "https://raw.githubusercontent.com/moby/profiles/main/seccomp/default.json"
OUT = "deploy/seccomp-arcloom.json"

# Determined empirically: each is necessary (removing it makes bwrap fail) and
# together they are sufficient (a real `tofu` run works). clone3 is deliberately
# absent — Docker's default answers it with ENOSYS and glibc falls back to clone.
ARCLOOM_SYSCALLS = ["unshare", "clone", "pivot_root", "mount", "umount2"]

COMMENT = (
    "ArcLoom: permit bubblewrap to build the engine sandbox (user+mount namespace). "
    "Minimal set, determined empirically; clone3 is NOT added because Docker's default "
    "already answers it with ENOSYS, which glibc handles by falling back to clone."
)


def main() -> int:
    with urllib.request.urlopen(SOURCE, timeout=30) as r:
        profile = json.load(r, object_pairs_hook=collections.OrderedDict)
    if profile.get("defaultAction") != "SCMP_ACT_ERRNO":
        print(f"refusing: upstream defaultAction is {profile.get('defaultAction')!r}, "
              "expected SCMP_ACT_ERRNO — the profile shape changed, review by hand",
              file=sys.stderr)
        return 1
    profile["syscalls"].append(collections.OrderedDict([
        ("names", ARCLOOM_SYSCALLS),
        ("action", "SCMP_ACT_ALLOW"),
        ("comment", COMMENT),
    ]))
    with open(OUT, "w") as f:
        json.dump(profile, f, indent=2)
        f.write("\n")
    print(f"wrote {OUT} ({len(profile['syscalls'])} rules)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
