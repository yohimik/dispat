"""The six-package fixture the experiments share, in one flavour per tool.

Every flavour has the same graph, cli, ui, api -> core and theme, docs -> ui,
the same baseline (all six at 1.0.0, tagged, published) and the same bare
origin the release is pushed to, so the tools differ only in how they are
told about the change and what they do about it. Dependencies are declared
as tilde ranges, so a minor of core is outside its consumers' ranges and
every tool has a reason to release them.

    fixture.py <root> lerna|nx|changesets|dispat
    fixture.py <root> <flavour> --feature    also commit the change to core
    fixture.py <root> <flavour> --colleague  also clone the origin a second time

Every commit is made at a pinned date, so two runs of the same cell produce
the same commit shas and two transcripts can be diffed against each other.
The folder must not exist: a fixture built over a previous run's leftovers is
a fixture nobody can reason about, and `git init` over an existing repository
succeeds quietly.
"""
import json
import os
import subprocess
import sys

PKGS = os.environ.get("EXPERIMENT_PACKAGES", "core cli ui api theme docs").split()
DEPS = {"cli": ["core"], "ui": ["core"], "api": ["core"],
        "theme": ["ui"], "docs": ["ui"]}
REG = os.environ.get("REGISTRY", "http://127.0.0.1:4873")
TOKEN = os.environ.get("NPM_TOKEN", "anonymous")
BASELINE = os.environ.get("EXPERIMENT_BASELINE", "1.0.0")

# The clock every commit is dated by: fixed, and advanced one minute per
# commit so history reads in the order it was written. 2025-01-01T00:00:00Z.
EPOCH = int(os.environ.get("EXPERIMENT_EPOCH", "1735689600"))
COMMITS = 0


def git_env():
    stamp = f"{EPOCH + 60 * COMMITS} +0000"
    return {**os.environ, "GIT_AUTHOR_DATE": stamp, "GIT_COMMITTER_DATE": stamp}


def sh(args, cwd):
    """One command, with everything it said kept on failure. Truncated output
    is how a fixture that could not build comes to fail on a later step for a
    reason that names something else."""
    r = subprocess.run(args, cwd=cwd, capture_output=True, text=True, env=git_env())
    if r.returncode != 0:
        raise RuntimeError(f"{args} in {cwd} exited {r.returncode}\n"
                           f"stdout:\n{r.stdout}\nstderr:\n{r.stderr}")
    return r.stdout


def commit(root, message):
    global COMMITS
    COMMITS += 1
    sh(["git", "commit", "-qm", message], root)


def npmrc(path):
    host = REG.split("//", 1)[1]
    with open(path, "w") as f:
        f.write(f"registry={REG}\n//{host}/:_authToken={TOKEN}\n")


def write_json(path, data):
    with open(path, "w") as f:
        json.dump(data, f, indent=1)
        f.write("\n")


def base(root, flavour):
    for path in (root, root + "-origin.git", root + "-colleague"):
        if os.path.exists(path):
            raise SystemExit(f"{path} already exists: a fixture is built into a fresh folder")
    os.makedirs(root)
    sh(["git", "init", "-q", "."], root)
    with open(os.path.join(root, ".gitignore"), "w") as f:
        f.write("node_modules\n")
    for p in PKGS:
        d = os.path.join(root, "packages", p)
        os.makedirs(d, exist_ok=True)
        write_json(os.path.join(d, "package.json"),
                   {"name": p, "version": BASELINE,
                    "dependencies": {q: f"~{BASELINE}" for q in DEPS.get(p, [])}})
        with open(os.path.join(d, "index.js"), "w") as f:
            f.write("// v1\n")
        # npm publishes from the package folder and reads the .npmrc there,
        # not the workspace root's.
        npmrc(os.path.join(d, ".npmrc"))
    npmrc(os.path.join(root, ".npmrc"))

    if flavour == "lerna":
        write_json(os.path.join(root, "package.json"),
                   {"name": "fixture", "private": True,
                    "workspaces": ["packages/*"]})
        write_json(os.path.join(root, "lerna.json"),
                   {"version": "independent",
                    "command": {"version": {"conventionalCommits": True,
                                            "push": True},
                                "publish": {"registry": REG}}})
    elif flavour == "nx":
        write_json(os.path.join(root, "package.json"),
                   {"name": "fixture", "private": True,
                    "workspaces": ["packages/*"]})
        write_json(os.path.join(root, "nx.json"),
                   {"release": {
                        "projects": ["*"],
                        "projectsRelationship": "independent",
                        "version": {"conventionalCommits": True,
                                    "preserveMatchingDependencyRanges": False},
                        "changelog": {"workspaceChangelog": False,
                                      "projectChangelogs": False},
                        "git": {"commit": True, "tag": True, "push": True}},
                    "defaultBase": "main"})
    elif flavour == "changesets":
        write_json(os.path.join(root, "package.json"),
                   {"name": "fixture", "private": True,
                    "workspaces": ["packages/*"],
                    "packageManager": "npm@10.9.2"})
        # manypkg, changesets' workspace discovery, recognises an npm
        # workspace only when a package-lock.json exists at the root.
        with open(os.path.join(root, "package-lock.json"), "w") as f:
            f.write("{}\n")
        cs = os.path.join(root, ".changeset")
        os.makedirs(cs, exist_ok=True)
        write_json(os.path.join(cs, "config.json"),
                   {"changelog": False, "commit": False,
                    "fixed": [], "linked": [], "ignore": [],
                    "access": "public", "baseBranch": "main",
                    "updateInternalDependencies": "patch"})
    elif flavour == "dispat":
        with open(os.path.join(root, "dispat.yaml"), "w") as f:
            f.write(f"""\
spaces:
  packages:
    path: packages
    scripts:
      publish: npm publish --ignore-scripts --registry {REG} > /tmp/publish-$DISPAT_PACKAGE.log 2>&1
    flow:
      publish: publish
packages:
""" + "".join(f"  {p}:\n    dependencies: [{', '.join(d)}]\n" +
              ("    revertOnFail: true\n"
               if p == "cli" and os.environ.get("EXPERIMENT") == "orphan" else "")
              for p, d in DEPS.items()) + """\
autoVersion:
  enabled: true
commit:
  enabled: true
  push: true
changelog:
  enabled: false
github:
  enabled: false
""")
    else:
        raise SystemExit(f"unknown flavour {flavour}")

    sh(["git", "add", "-A"], root)
    commit(root, "chore: baseline")
    for p in PKGS:
        sh(["git", "tag", f"{p}@{BASELINE}"], root)
    origin = root + "-origin.git"
    subprocess.run(["git", "init", "-q", "--bare", origin], check=True)
    sh(["git", "remote", "add", "origin", origin], root)
    sh(["git", "push", "-q", "origin", "main", "--tags"], root)
    sh(["git", "branch", "-q", "--set-upstream-to=origin/main", "main"], root)


def feature(root, flavour):
    """The one pending change: a minor to core. Each tool is told in the
    way it reads: a conventional commit, or a changeset file for changesets,
    which reads no commit messages."""
    with open(os.path.join(root, "packages", "core", "index.js"), "a") as f:
        f.write("// streaming\n")
    if flavour == "changesets":
        with open(os.path.join(root, ".changeset", "streaming-reader.md"), "w") as f:
            f.write('---\n"core": minor\n---\n\nstreaming reader\n')
    sh(["git", "add", "-A"], root)
    commit(root, "feat(core)^: streaming reader" if flavour == "dispat"
           else "feat(core): streaming reader")
    sh(["git", "push", "-q", "origin", "main"], root)


def colleague(root):
    """A second clone of the origin: the colleague whose push lands while
    the release runs."""
    clone = root + "-colleague"
    sh(["git", "clone", "-q", root + "-origin.git", clone], os.path.dirname(root) or ".")
    return clone


if __name__ == "__main__":
    root, flavour = sys.argv[1], sys.argv[2]
    base(root, flavour)
    if "--feature" in sys.argv[3:]:
        feature(root, flavour)
    if "--colleague" in sys.argv[3:]:
        colleague(root)
    print("fixture ready:", root, flavour)
