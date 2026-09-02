"""What a release left behind, read from the three places a release writes:
the local clone, the origin, and the registry. Emits one JSON object; the
experiments call it after every step so the transcript is a sequence of
these rather than a reading of tool output.

    observe.py <repo> [--mark name=sha ...] [--label text]

Per package the three answers are joined into one state:

  consistent   the registry's version is tagged on origin and the tag is
               reachable from origin/main
  orphan       a tag exists for a version the registry does not hold
  unpushed     the tag exists in the local clone only
  dangling     the tag is on origin but outside origin/main's ancestry
  unrecorded   the registry serves a version no tag names
  baseline     nothing beyond the fixture's 1.0.0
"""
import json
import os
import subprocess
import sys
import urllib.request

PKGS = ["core", "cli", "ui", "api", "theme", "docs"]
REG = os.environ.get("REGISTRY", "http://127.0.0.1:4873")


def git(repo, *args, ok=False):
    r = subprocess.run(["/usr/bin/git", "-C", repo, *args],
                       capture_output=True, text=True)
    if ok:
        return r.returncode == 0
    return r.stdout.strip()


def registry_info(name):
    """The latest version the registry serves, and every version it holds."""
    try:
        with urllib.request.urlopen(f"{REG}/{name}", timeout=5) as r:
            data = json.load(r)
        return data.get("dist-tags", {}).get("latest", "absent"), set(data.get("versions", {}))
    except Exception:
        return "absent", set()


def commit(repo, rev):
    if not rev:
        return None
    line = git(repo, "log", "-1", "--format=%H%x00%P%x00%s", rev)
    if not line:
        return None
    sha, parents, subject = line.split("\0")
    return {"sha": sha[:12], "parents": len(parents.split()) if parents else 0,
            "subject": subject}


def main():
    repo = sys.argv[1]
    marks, label = {}, ""
    args = sys.argv[2:]
    while args:
        a = args.pop(0)
        if a == "--mark":
            k, v = args.pop(0).split("=", 1)
            marks[k] = v
        elif a == "--label":
            label = args.pop(0)

    # origin's refs, with the objects behind them fetched so ancestry can be
    # asked locally. Tags are fetched under a separate namespace so the
    # local tags stay exactly what the tool left.
    git(repo, "fetch", "-q", "origin", "+refs/heads/*:refs/remotes/origin/*",
        "+refs/tags/*:refs/remote-tags/*", "--no-tags")
    remote_main = git(repo, "rev-parse", "--verify", "-q", "refs/remotes/origin/main")
    remote_tags = {}
    for line in git(repo, "ls-remote", "--tags", "origin").splitlines():
        sha, ref = line.split()
        if ref.endswith("^{}") or "dispat-release-lock" in ref:
            continue
        remote_tags[ref[len("refs/tags/"):]] = git(repo, "rev-list", "-n1", sha) or sha
    remote_heads = {}
    for line in git(repo, "ls-remote", "--heads", "origin").splitlines():
        sha, ref = line.split()
        remote_heads[ref[len("refs/heads/"):]] = sha[:12]
    local_tags = {}
    for line in git(repo, "for-each-ref", "--format=%(refname:short) %(*objectname) %(objectname)",
                    "refs/tags").splitlines():
        parts = line.split()
        name, sha = parts[0], (parts[1] if len(parts) == 3 else parts[-1])
        if "dispat-release-lock" in name:
            continue
        local_tags[name] = sha

    def reachable(sha):
        return bool(remote_main) and git(repo, "merge-base", "--is-ancestor", sha, remote_main, ok=True)

    packages = {}
    for p in PKGS:
        reg, served = registry_info(p)
        versions = set()
        for t in list(local_tags) + list(remote_tags):
            if t.startswith(p + "@"):
                versions.add(t.split("@", 1)[1])
        row = {"registry": reg, "tags": {}}
        for v in sorted(versions):
            t = f"{p}@{v}"
            row["tags"][v] = {
                "local": local_tags.get(t, "")[:12] or None,
                "origin": remote_tags.get(t, "")[:12] or None,
                "onMain": reachable(remote_tags[t]) if t in remote_tags else None,
            }
        states = set()
        for v, info in row["tags"].items():
            if v == "1.0.0":
                continue
            if v not in served:
                states.add("orphan")
            elif info["origin"] is None:
                states.add("unpushed")
            elif not info["onMain"]:
                states.add("dangling")
            else:
                states.add("consistent")
        if reg not in ("absent", "1.0.0") and reg not in row["tags"]:
            states.add("unrecorded")
        row["state"] = "+".join(sorted(states)) if states else "baseline"
        packages[p] = row

    counts = git(repo, "rev-list", "--left-right", "--count", "HEAD...refs/remotes/origin/main") \
        if remote_main else ""
    ahead, behind = (counts.split() + ["?", "?"])[:2]
    out = {
        "label": label,
        "local": {"head": commit(repo, "HEAD"), "aheadOfOrigin": ahead, "behindOrigin": behind,
                  "mergeInProgress": os.path.exists(os.path.join(repo, ".git", "MERGE_HEAD")),
                  "rebaseInProgress": os.path.exists(os.path.join(repo, ".git", "rebase-merge"))
                  or os.path.exists(os.path.join(repo, ".git", "rebase-apply")),
                  "dirty": bool(git(repo, "status", "--porcelain"))},
        "origin": {"main": commit(repo, remote_main), "heads": remote_heads,
                   "tags": {t: s[:12] for t, s in sorted(remote_tags.items())}},
        "marks": {},
        "packages": packages,
    }
    for name, sha in marks.items():
        out["marks"][name] = {
            "sha": sha[:12],
            "onOriginMain": reachable(sha),
            "onFirstParentChain": bool(remote_main) and sha[:12] in
                [s[:12] for s in git(repo, "rev-list", "--first-parent", remote_main).split()],
        }
    print(json.dumps(out, indent=1, sort_keys=True))


if __name__ == "__main__":
    main()
