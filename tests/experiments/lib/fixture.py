"""The six-package fixture the experiments share, in one flavour per tool.

Every flavour has the same graph: cli, ui and api depend on core; theme and
docs depend on ui. They share
the same baseline (all six at 1.0.0, tagged, published) and the same bare
origin the release is pushed to, so the tools differ only in how they are
told about the change and what they do about it. Dependencies are declared
as tilde ranges, so a minor of core is outside its consumers' ranges and
every tool has a reason to release them.

Every package carries one `build` script, and the propagation experiment runs
it where each tool documents a build: dispat's build stage through the
flavour's `dispat.yaml`, nx's `release.version.preVersionCommand`, and the
`prepack` lifecycle script that `lerna publish` and `changeset publish` run
for every package they publish. A build failure is therefore one fault seen
through four tools rather than four faults sharing a name. The orphan and
midrelease fixtures run no build under lerna, nx or changesets.

    fixture.py <root> lerna|nx|changesets|dispat
    fixture.py <root> <flavour> --feature      also commit the minor to core
    fixture.py <root> dispat --held           also hold cli's release first
    fixture.py <root> <flavour> --propagation  also commit the patch to core
    fixture.py <root> dispat --deferred       also commit own cli and provider fixes
    fixture.py <root> <flavour> --colleague    also clone the origin a second time

EXPERIMENT and SCENARIO are read from the environment, as the harness exports
them.

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

# The build every package carries. It is a stage rather than a name: it reads
# the package's source and writes an artifact, so a build that did not run
# leaves nothing behind. Every tool's propagation run reaches this same script,
# which is what makes a build failure one fault observed through four tools
# rather than four faults.
BUILD = ('node -e \'const fs=require("fs");fs.mkdirSync("dist",{recursive:true});'
         'fs.writeFileSync("dist/index.js",fs.readFileSync("index.js"))\'')

# The propagation experiment's build fault, armed by a sentinel file the
# protocol creates before the first run. It is the first statement of the
# consumer's build script, so a run that reaches it has reached the build: a
# fault anywhere else would fail some other step and the build and publish
# scenarios would be one fault under two names. When it fires it leaves
# /fault-fired behind, which is how the harness knows the run it records met
# the fault at all.
BUILD_FAULT = ('test ! -f /fault-consumer-build || '
               '{ touch /fault-fired; echo "injected cli build failure" >&2; exit 42; }')

# The scenarios whose fault is the consumer's build. The others leave the
# build script as every other package has it.
BUILD_FAULT_SCENARIOS = ("build", "build-distributed")

# Where lerna and changesets document a build: `lerna publish` and
# `changeset publish` pack every package they publish, and packing runs the
# package's `prepack` script.
PREPACK = "npm run --silent build"

# Where nx documents a build: a command nx release runs before it versions.
PRE_VERSION = "npx nx run-many -t build"


def experiment():
    return os.environ.get("EXPERIMENT")


def scenario():
    return os.environ.get("SCENARIO")


def build_script(package):
    """The package's build, with the build scenarios' fault in the
    consumer's."""
    if package == "cli" and experiment() == "propagation" and scenario() in BUILD_FAULT_SCENARIOS:
        return f"{BUILD_FAULT}; {BUILD}"
    return BUILD


def package_scripts(package, flavour):
    """The package's scripts: the build, and in the propagation experiment the
    lifecycle hook through which lerna and changesets run it."""
    scripts = {"build": build_script(package)}
    if experiment() == "propagation" and flavour in ("lerna", "changesets"):
        scripts["prepack"] = PREPACK
    return scripts


def dispat_packages():
    """The dispat flavour's `packages:` block: the graph, plus the options one
    experiment or another needs. A package with nothing to say is left out,
    because the space already discovers it.

    isBuildWaitingPublish is the provider's setting: it says that consumers of
    this package may only start building once it has been published. The
    propagation experiment sets it on core, so cli's build begins after core's
    publication rather than beside it, and a consumer failure is one that
    followed a provider success rather than one that raced it.
    """
    block = ""
    for p in PKGS:
        options = []
        if DEPS.get(p):
            options.append(f"    dependencies: [{', '.join(DEPS[p])}]")
        if p == "cli" and experiment() in ("orphan", "propagation"):
            options.append("    revertOnFail: true")
        if p == "cli" and experiment() == "propagation" and scenario() == "deferred":
            # With no build, its own release may reconcile from the provider's
            # planned version to the actually published baseline after a
            # provider failure. A built artifact could already embed the
            # planned version and must be skipped instead.
            options.append("    flow: {build: []}")
        if p == "core" and experiment() == "propagation":
            if scenario() in ("deferred", "deferred-build"):
                options.append("    revertOnFail: true")
            else:
                options.append("    isBuildWaitingPublish: true")
        if options:
            block += f"  {p}:\n" + "".join(line + "\n" for line in options)
    return block


def dispat_execution():
    """The root keys a distributed release adds: the name of the variable the
    signing secret is read from, never the secret itself, a preflight wait
    sized for a worker started beside the run, and every build placed on a
    worker while every publication stays here. The link to the worker is
    named on the command line, and with no endpoint it reaches the fixture's
    own origin."""
    if experiment() != "propagation" or scenario() != "build-distributed":
        return ""
    return """\
execution:
  secretEnv: EXPERIMENT_EXECUTION_SECRET
  timeouts:
    preflight: 60
runOnly: [worker, orchestrator]
"""


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


def commit(root, message, *paragraphs, empty=False):
    """One commit at the fixture's clock. Each paragraph is a message
    paragraph of its own, which is where a footer such as Release-As goes."""
    global COMMITS
    COMMITS += 1
    args = ["git", "commit", "-q", "-m", message]
    for paragraph in paragraphs:
        args += ["-m", paragraph]
    if empty:
        args.append("--allow-empty")
    sh(args, root)


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
        # dist is the build's output. A build stage that dirtied the worktree
        # would be a release safety check firing on the harness rather than on
        # anything the experiment is about. .nx is where nx keeps its cache
        # and workspace data, which an nx workspace ignores the way `nx init`
        # sets one up; without it every nx run leaves the clone dirty.
        f.write("node_modules\ndist\n" + (".nx\n" if flavour == "nx" else ""))
    for p in PKGS:
        d = os.path.join(root, "packages", p)
        os.makedirs(d, exist_ok=True)
        write_json(os.path.join(d, "package.json"),
                   {"name": p, "version": BASELINE,
                    "scripts": package_scripts(p, flavour),
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
        version = {"conventionalCommits": True,
                   "preserveMatchingDependencyRanges": False}
        if experiment() == "propagation":
            version["preVersionCommand"] = PRE_VERSION
        write_json(os.path.join(root, "nx.json"),
                   {"release": {
                        "projects": ["*"],
                        "projectsRelationship": "independent",
                        "version": version,
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
      build: npm run --silent build
      publish: npm publish --ignore-scripts --registry {REG} > /tmp/publish-$DISPAT_PACKAGE.log 2>&1
    flow:
      build: build
      publish: publish
packages:
""" + dispat_packages() + dispat_execution() + """\
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
        # Annotated, which is what every one of these tools writes for a
        # release of its own. `git describe` ignores a lightweight tag, so a
        # baseline tagged lightly is a baseline lerna cannot see: its change
        # detection then reports no previous release and assumes every
        # package changed, and the fixture rather than the tool decides what
        # the run releases.
        sh(["git", "tag", "-a", "-m", f"{p}@{BASELINE}", f"{p}@{BASELINE}"], root)
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


def held(root, flavour):
    """cli's release held before the provider's fix: an empty commit
    `release(cli): hold` with the footer `Release-As: none`, which is how an
    operator withholds one package. The provider's release then ships without
    the consumer, as a release its consumer sat out."""
    if flavour != "dispat":
        raise SystemExit("a held consumer is a dispat-only protocol")
    commit(root, "release(cli): hold", "Release-As: none", empty=True)
    sh(["git", "push", "-q", "origin", "main"], root)


def propagation(root, flavour):
    """A patch to core whose ~1.0.0 consumer range remains compatible.

    dispat's caret marks propagation intent; lerna and nx receive the closest
    conventional-commit equivalent without an invented propagation syntax.
    """
    with open(os.path.join(root, "packages", "core", "index.js"), "a") as f:
        f.write("// corrected reader\n")
    if flavour == "changesets":
        # Changesets has no CCME caret. Give it the same requested direct
        # consumer releases as explicit changeset entries.
        with open(os.path.join(root, ".changeset", "correct-reader.md"), "w") as f:
            f.write('---\n"core": patch\n"cli": patch\n"ui": patch\n"api": patch\n---\n\ncorrect reader and rebuild direct consumers\n')
    sh(["git", "add", "-A"], root)
    commit(root, "fix(core)^: correct reader" if flavour == "dispat"
           else "fix(core): correct reader")
    sh(["git", "push", "-q", "origin", "main"], root)


def deferred(root, flavour):
    """The consumer has its own pending work when its provider first fails.

    It publishes that work at the old provider baseline. The later protocol
    releases the provider alone and asks whether the next full run remembers
    the consumer that missed the provider's release.
    """
    if flavour != "dispat":
        raise SystemExit("deferred propagation is a dispat-only protocol")
    with open(os.path.join(root, "packages", "cli", "index.js"), "a") as f:
        f.write("// own fix\n")
    sh(["git", "add", "-A"], root)
    commit(root, "fix(cli): repair command")
    with open(os.path.join(root, "packages", "core", "index.js"), "a") as f:
        f.write("// corrected reader\n")
    sh(["git", "add", "-A"], root)
    commit(root, "fix(core)^: correct reader")
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
    if "--held" in sys.argv[3:]:
        held(root, flavour)
    if "--propagation" in sys.argv[3:]:
        propagation(root, flavour)
    if "--deferred" in sys.argv[3:]:
        deferred(root, flavour)
    if "--colleague" in sys.argv[3:]:
        colleague(root)
    print("fixture ready:", root, flavour)
