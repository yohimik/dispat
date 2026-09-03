"""What a release left behind, read from the three places a release writes:
the local clone, the origin, and the registry. Emits one JSON object on one
line; the experiments call it after every step, so observations.jsonl is real
JSON Lines and the transcript is a sequence of states rather than a reading of
tool output.

    observe.py <repo> [--mark name=sha ...] [--label text]

Nothing is written into the clone under test. An earlier version fetched the
origin's refs into it so ancestry could be asked locally, which put remote
refs and fetched objects into the very repository whose state the run is
about: the next `git pull --rebase` of a recovery then had a fetch it did not
perform, and the tools' own plans read a repository the observer had touched.
Instead a scratch bare repository is built beside the fixture at each
observation and both sides are fetched into it under separate ref namespaces,
so origin's refs and the clone's refs are distinguishable and neither
repository under test is written to at all.

Per package the three answers are joined into one state:

  consistent   the registry's version is tagged on origin and the tag is
               reachable from origin/main
  orphan       a tag exists for a version the registry does not hold
  unpushed     the tag exists in the local clone only
  dangling     the tag is on origin but outside origin/main's ancestry
  unrecorded   the registry serves a version no tag names
  baseline     nothing beyond the fixture's baseline version
"""
import json
import os
import shutil
import subprocess
import sys
import urllib.error
import urllib.request

PKGS = os.environ.get("EXPERIMENT_PACKAGES", "core cli ui api theme docs").split()
BASELINE = os.environ.get("EXPERIMENT_BASELINE", "1.0.0")
REG = os.environ.get("REGISTRY", "http://127.0.0.1:4873")

# The namespaces the two sides are fetched into. Separate on purpose: a tag
# that exists on origin and a tag that exists only in the clone are the whole
# difference between `consistent` and `unpushed`, and one namespace would
# merge them into a single answer that is neither.
ORIGIN_NS = "refs/observed/origin"
CLONE_NS = "refs/observed/clone"


def git(repo, *args):
    """One git command, with its failure kept. A return code nobody reads is
    how an observation of nothing comes to look like an observation of an
    empty repository."""
    r = subprocess.run(["git", "-C", repo, *args], capture_output=True, text=True)
    if r.returncode != 0:
        raise RuntimeError(
            f"git -C {repo} {' '.join(args)} exited {r.returncode}\n"
            f"stdout: {r.stdout.strip()}\nstderr: {r.stderr.strip()}"
        )
    return r.stdout.strip()


def git_ok(repo, *args):
    """A git command asked as a question: true or false, never an exception.
    Used only where a non-zero exit is the answer (`merge-base --is-ancestor`)
    rather than a failure."""
    r = subprocess.run(["git", "-C", repo, *args], capture_output=True, text=True)
    return r.returncode == 0


def observer(repo):
    """A scratch bare repository holding both sides' refs, rebuilt at every
    observation so nothing survives from the last one."""
    path = repo + "-observer"
    shutil.rmtree(path, ignore_errors=True)
    subprocess.run(["git", "init", "-q", "--bare", path], check=True)
    origin = repo + "-origin.git"
    if os.path.isdir(origin):
        git(path, "fetch", "-q", "--no-tags", origin,
            f"+refs/heads/*:{ORIGIN_NS}/heads/*", f"+refs/tags/*:{ORIGIN_NS}/tags/*")
    git(path, "fetch", "-q", "--no-tags", os.path.join(repo, ".git"),
        f"+refs/heads/*:{CLONE_NS}/heads/*", f"+refs/tags/*:{CLONE_NS}/tags/*",
        f"+HEAD:{CLONE_NS}/HEAD")
    return path


def refs(obs, namespace):
    """Every ref under a namespace, peeled: an annotated tag answers with the
    commit it names, which is what every question here is about."""
    out = {}
    listing = git(obs, "for-each-ref", "--format=%(refname)%00%(objectname)%00%(*objectname)",
                  namespace)
    for line in listing.splitlines():
        name, obj, peeled = line.split("\0")
        out[name[len(namespace) + 1:]] = peeled or obj
    return out


def registry_info(name):
    """The latest version the registry serves, every version it holds, and the
    error when it answered with one.

    A registry that is down and a package that was never published are not the
    same fact, and reading the first as the second is how a broken proxy comes
    to read as a tool that published nothing. 404 is the honest absence; every
    other failure is reported as such."""
    try:
        with urllib.request.urlopen(f"{REG}/{name}", timeout=5) as r:
            data = json.load(r)
    except urllib.error.HTTPError as e:
        if e.code == 404:
            return "absent", set(), None
        return "error", set(), f"HTTP {e.code} {e.reason}"
    except Exception as e:  # noqa: BLE001 - a connection error is a reading too
        return "error", set(), str(e)
    return data.get("dist-tags", {}).get("latest", "absent"), set(data.get("versions", {})), None


def commit(obs, rev):
    if not rev:
        return None
    line = git(obs, "log", "-1", "--format=%H%x00%P%x00%s", rev)
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
            if v:
                marks[k] = v
        elif a == "--label":
            label = args.pop(0)
        else:
            raise SystemExit(f"observe.py: unknown argument {a}")

    obs = observer(repo)
    origin_heads = refs(obs, f"{ORIGIN_NS}/heads")
    origin_tags = {t: s for t, s in refs(obs, f"{ORIGIN_NS}/tags").items()
                   if "dispat-release-lock" not in t}
    clone_tags = {t: s for t, s in refs(obs, f"{CLONE_NS}/tags").items()
                  if "dispat-release-lock" not in t}
    remote_main = f"{ORIGIN_NS}/heads/main" if "main" in origin_heads else ""

    def reachable(sha):
        return bool(remote_main) and git_ok(obs, "merge-base", "--is-ancestor", sha, remote_main)

    packages = {}
    for p in PKGS:
        reg, served, error = registry_info(p)
        versions = set()
        for t in list(clone_tags) + list(origin_tags):
            if t.startswith(p + "@"):
                versions.add(t.split("@", 1)[1])
        row = {"registry": reg, "tags": {}}
        if error:
            row["error"] = error
        for v in sorted(versions):
            t = f"{p}@{v}"
            row["tags"][v] = {
                "local": clone_tags.get(t, "")[:12] or None,
                "origin": origin_tags.get(t, "")[:12] or None,
                "onMain": reachable(origin_tags[t]) if t in origin_tags else None,
            }
        states = set()
        for v, info in row["tags"].items():
            if v == BASELINE:
                continue
            if v not in served:
                states.add("orphan")
            elif info["origin"] is None:
                states.add("unpushed")
            elif not info["onMain"]:
                states.add("dangling")
            else:
                states.add("consistent")
        if reg not in ("absent", "error", BASELINE) and reg not in row["tags"]:
            states.add("unrecorded")
        row["state"] = "+".join(sorted(states)) if states else "baseline"
        packages[p] = row

    ahead, behind = 0, 0
    if remote_main:
        counts = git(obs, "rev-list", "--left-right", "--count",
                     f"{CLONE_NS}/HEAD...{remote_main}").split()
        # Integers rather than the strings a shell would have to compare as
        # text: an assertion reading `== "0"` fails the day the observer emits
        # 0, and one reading `== 0` fails the day it emits "0".
        ahead, behind = int(counts[0]), int(counts[1])

    out = {
        "label": label,
        "local": {"head": commit(obs, f"{CLONE_NS}/HEAD"),
                  "aheadOfOrigin": ahead, "behindOrigin": behind,
                  # The working tree is the clone's own and has no equivalent
                  # in a bare observer, so these three are read from the clone
                  # directly. All three are read-only.
                  "mergeInProgress": os.path.exists(os.path.join(repo, ".git", "MERGE_HEAD")),
                  "rebaseInProgress": os.path.exists(os.path.join(repo, ".git", "rebase-merge"))
                  or os.path.exists(os.path.join(repo, ".git", "rebase-apply")),
                  "dirty": bool(git(repo, "status", "--porcelain"))},
        "origin": {"main": commit(obs, remote_main),
                   "heads": {h: s[:12] for h, s in sorted(origin_heads.items())},
                   "tags": {t: s[:12] for t, s in sorted(origin_tags.items())}},
        "marks": {},
        "packages": packages,
    }
    first_parents = []
    if remote_main:
        first_parents = [s[:12] for s in
                         git(obs, "rev-list", "--first-parent", remote_main).split()]
    for name, sha in marks.items():
        out["marks"][name] = {
            "sha": sha[:12],
            "onOriginMain": reachable(sha),
            "onFirstParentChain": sha[:12] in first_parents,
        }
    print(json.dumps(out, sort_keys=True))


if __name__ == "__main__":
    main()
