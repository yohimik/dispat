"""One timing cell's figures, in the vocabulary of the dispat theorem (CCME
SPEC.md §13.11): the makespan of the release, its total work Wk, its longest
precedence chain Lk, the parallelism s it used, the lower bound max(Lk, Wk / s)
no execution of the plan beats, and the makespan's ratio to that bound.

    timing.py --steps steps.jsonl --records timing.jsonl --repo <clone>
              --observation <observation> --relation build|publish

The inputs are records, never a tool's log:

  steps.jsonl   the protocol's steps with their wall-clock start and end. The
                planning time is the `plan` step. The release is every step
                named `run` or `run-<part>`, and its makespan runs from the
                first one's start to the last one's end. A `run-version` step
                is one task of the plan: no tool exposes per-package
                versioning, so the version is timed as a whole.
  timing.jsonl  what the fixture recorded: a `build` line per package build,
                a `publish` line per `npm publish` the npm shim forwarded, and
                an `upload` line per upload the registry's proxy accepted.
                Only the lines inside the release's span are the release's.
  <clone>       the manifests, whose dependencies are the plan's graph.

A package's publish is its `npm publish` when the shim saw one, and the
registry's upload otherwise, which is how a tool that publishes in-process is
timed. A build that runs inside its own package's publish, as `prepack` does,
is cut out of that publish, so no second of it is counted twice.

The precedence is the plan's, the same for every tool, over the fixture's
dependency graph. Every package builds before it publishes, and a provider
publishes before its consumer (§19.2). The relation says what a consumer's
build waits for (§19.2a): its providers' builds (`build`) or their
publications (`publish`). The version task, where there is one, precedes all
of them.
"""
import argparse
import glob
import json
import os
import sys

BASELINE = os.environ.get("EXPERIMENT_BASELINE", "1.0.0")
RELEASE_STEP = "run"
VERSION_STEP = "run-version"
PLAN_STEP = "plan"
# The slack around the release's span a record may stand outside it by: the
# step's clock and the records' clocks are two readings of one wall clock,
# taken microseconds apart.
SLACK = 0.05


def read_lines(path):
    with open(path) as f:
        return [json.loads(line) for line in f if line.strip()]


def is_release_step(name):
    return name == RELEASE_STEP or name.startswith(RELEASE_STEP + "-")


def release_span(steps):
    release = [s for s in steps if is_release_step(s["step"])]
    if not release:
        raise SystemExit("timing.py: no release step named run or run-<part>")
    return min(s["started"] for s in release), max(s["ended"] for s in release)


def planning_seconds(steps):
    for s in steps:
        if s["step"] == PLAN_STEP:
            return s["ended"] - s["started"]
    raise SystemExit("timing.py: no plan step")


def within(record, span):
    return record["start"] >= span[0] - SLACK and record["end"] <= span[1] + SLACK


def subtract(interval, holes):
    """The parts of an interval that none of the holes cover."""
    pieces = [interval]
    for hole_start, hole_end in sorted(holes):
        kept = []
        for start, end in pieces:
            if hole_end <= start or hole_start >= end:
                kept.append((start, end))
                continue
            if start < hole_start:
                kept.append((start, hole_start))
            if hole_end < end:
                kept.append((hole_end, end))
        pieces = kept
    return pieces


def tasks_of(records, version_steps):
    """Every task of the release as (package, kind, segments). A package's
    publish is its npm publishes when there are any, else its accepted
    uploads, each with the package's own builds cut out of it."""
    builds, npm, uploads = {}, {}, {}
    for r in records:
        interval = (r["start"], r["end"])
        if r["task"] == "build":
            builds.setdefault(r["package"], []).append(interval)
        elif r["task"] == "publish":
            npm.setdefault(r["package"], []).append(interval)
        elif r["task"] == "upload" and 200 <= r.get("status", 0) < 300:
            uploads.setdefault(r["package"], []).append(interval)
    tasks = [(None, "version", [(s["started"], s["ended"])]) for s in version_steps]
    for package, intervals in builds.items():
        tasks += [(package, "build", [interval]) for interval in intervals]
    sources = {}
    for package in sorted(set(npm) | set(uploads)):
        sources[package] = "npm" if package in npm else "registry"
        for interval in npm.get(package) or uploads[package]:
            tasks.append((package, "publish", subtract(interval, builds.get(package, []))))
    return tasks, sources


def length(segments):
    return sum(end - start for start, end in segments)


def parallelism(tasks):
    """The most task segments running at one instant. An end and a start at
    the same instant do not overlap."""
    events = sorted((t, delta) for _, _, segments in tasks for start, end in segments
                    for t, delta in ((start, 1), (end, -1)))
    running = most = 0
    for _, delta in events:
        running += delta
        most = max(most, running)
    return most


def read_graph(repo):
    """Each workspace package and the workspace packages it depends on."""
    manifests = {}
    for path in glob.glob(os.path.join(repo, "packages", "*", "package.json")):
        with open(path) as f:
            manifest = json.load(f)
        manifests[manifest["name"]] = manifest
    return {name: sorted(dep for field in ("dependencies", "devDependencies")
                         for dep in manifest.get(field, {}) if dep in manifests)
            for name, manifest in manifests.items()}


def topological(graph):
    order, seen = [], set()

    def visit(name):
        if name in seen:
            return
        seen.add(name)
        for provider in graph.get(name, []):
            visit(provider)
        order.append(name)

    for name in sorted(graph):
        visit(name)
    return order


def critical_path(tasks, graph, relation):
    """The longest precedence chain through the plan's task graph, and the
    tasks on it. A package without a task of a kind passes its providers'
    finish through, so a chain is never cut by a package that did not run."""
    duration = {}
    for package, kind, segments in tasks:
        if package is not None:
            duration[(package, kind)] = duration.get((package, kind), 0) + length(segments)
    version = sum(length(segments) for package, kind, segments in tasks if package is None)
    finish, previous = {}, {}

    def latest(nodes):
        best = None
        for node in nodes:
            if best is None or finish[node] > finish[best]:
                best = node
        return best

    for package in topological(graph):
        providers = graph.get(package, [])
        build, publish = (package, "build"), (package, "publish")
        waits = [(p, relation) for p in providers]
        before_build = latest(waits)
        finish[build] = (finish[before_build] if before_build else 0) + duration.get(build, 0)
        previous[build] = before_build
        before_publish = latest([build] + [(p, "publish") for p in providers])
        finish[publish] = finish[before_publish] + duration.get(publish, 0)
        previous[publish] = before_publish
    end = latest(finish)
    chain = []
    while end is not None:
        if duration.get(end, 0) > 0:
            chain.append(f"{end[0]} {end[1]}")
        end = previous.get(end)
    chain.reverse()
    if version > 0:
        chain.insert(0, "version")
    return version + (finish[latest(finish)] if finish else 0), chain


def shipped(observation):
    return sorted(name for name, row in observation["packages"].items()
                  if row["registry"] not in (BASELINE, "absent", "error"))


def measure(steps, records, graph, observation, relation):
    span = release_span(steps)
    release_records = [r for r in records if within(r, span)]
    tasks, sources = tasks_of(release_records, [s for s in steps if s["step"] == VERSION_STEP])
    makespan = span[1] - span[0]
    work = sum(length(segments) for _, _, segments in tasks)
    slots = max(parallelism(tasks), 1)
    longest, chain = critical_path(tasks, graph, relation)
    bound = max(longest, work / slots)
    timed = {(package, kind) for package, kind, _ in tasks}
    return {
        "relation": relation,
        "planningSeconds": round(planning_seconds(steps), 3),
        "makespanSeconds": round(makespan, 3),
        "work": round(work, 3),
        "criticalPath": round(longest, 3),
        "parallelism": slots,
        "bound": round(bound, 3),
        "ratio": round(makespan / bound, 3) if bound > 0 else None,
        "criticalChain": chain,
        "tasks": {kind: sum(1 for _, k, _ in tasks if k == kind) for kind in ("version", "build", "publish")},
        "publishTimedAt": {source: sum(1 for s in sources.values() if s == source) for source in ("npm", "registry")},
        "untimed": [p for p in shipped(observation)
                    if (p, "build") not in timed or (p, "publish") not in timed],
    }


def main(argv):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--steps", required=True)
    parser.add_argument("--records", required=True)
    parser.add_argument("--repo", required=True)
    parser.add_argument("--observation", required=True)
    parser.add_argument("--relation", required=True, choices=("build", "publish"))
    args = parser.parse_args(argv)
    records = read_lines(args.records) if os.path.exists(args.records) else []
    with open(args.observation) as f:
        observation = json.load(f)
    print(json.dumps(measure(read_lines(args.steps), records, read_graph(args.repo),
                             observation, args.relation), sort_keys=True))


if __name__ == "__main__":
    main(sys.argv[1:])
