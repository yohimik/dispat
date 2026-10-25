"""One timing cell's figures, in the vocabulary of the dispat theorem (CCME
SPEC.md §13.11): the makespan of the release, its total work Wk, its longest
precedence chain Lk, the parallelism s it was seen to use, the bound
max(Lk, Wk / s) and the makespan's ratio to that bound, with each task's own
duration and every edge of the precedence the records contradict.

    timing.py --steps steps.jsonl --records timing.jsonl --repo <clone>
              --observation <observation> --relation publish|build|none

The inputs are records, never a tool's log:

  steps.jsonl   the protocol's steps with their wall-clock start and end. The
                planning time is the `plan` step. The release is every step
                named `run` or `run-<part>`, and its makespan runs from the
                first one's start to the last one's end.
  timing.jsonl  what the fixture recorded: a `build` line per package build,
                an `upload` line per upload the registry's proxy forwarded,
                and a `publish` line per `npm publish` the npm shim forwarded.
  <clone>       the manifests, whose dependencies are the plan's graph.

The tasks are the builds and the uploads, the same two kinds for every tool.
A publication is timed as its upload at the registry, the one point every
tool's publication passes through: lerna publishes in-process and runs no
`npm publish` to time. The `npm publish` processes, where a tool runs them,
are reported beside the tasks with their own package's build cut out, and are
no part of Wk or Lk. No tool's versioning, commit, tags or push is a task, so
all of them fall outside the tasks under every tool alike.

The precedence is the plan's over the fixture's dependency graph (§19.2,
§19.2a): every package builds before it publishes, a provider publishes
before its consumer, and the relation the protocol declares says what a
consumer's build waits for: its providers' publications (`publish`), their
builds (`build`) or nothing (`none`). A package that ran no task of a kind
passes its providers through. Every edge is checked against the records, and
the ones a release did not honour are listed, because the bound holds only for
an execution that honours them.
"""
import argparse
import glob
import json
import os
import statistics
import sys

BASELINE = os.environ.get("EXPERIMENT_BASELINE", "1.0.0")
RELEASE_STEP = "run"
PLAN_STEP = "plan"
RELATIONS = ("publish", "build", "none")
# The slack a record may stand outside a span by, and an edge's successor may
# start before its predecessor's end by: the steps' clock and the records'
# clocks are readings of one wall clock taken a few milliseconds apart.
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


def plan_span(steps):
    for s in steps:
        if s["step"] == PLAN_STEP:
            return s["started"], s["ended"]
    raise SystemExit("timing.py: no plan step")


def placed(record, span):
    """inside, outside or straddling: where a record stands against a span."""
    if record["start"] >= span[0] - SLACK and record["end"] <= span[1] + SLACK:
        return "inside"
    if record["end"] <= span[0] + SLACK or record["start"] >= span[1] - SLACK:
        return "outside"
    return "straddling"


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


def length(segments):
    return sum(end - start for start, end in segments)


def accepted(record):
    return 200 <= record.get("status", 0) < 300


def tasks_of(records):
    """The release's tasks as (package, kind, start, end, ok): every build
    and every upload, accepted or not, since a refused upload still held a
    slot for as long as it took."""
    kinds = {"build": "build", "upload": "publish"}
    return sorted(((r["package"], kinds[r["task"]], r["start"], r["end"],
                    r["task"] == "build" or accepted(r))
                   for r in records if r["task"] in kinds), key=lambda t: (t[2], t[0], t[1]))


def npm_publishes(records):
    """Each `npm publish` the shim timed, as (seconds, ok), with its own
    package's builds cut out: `prepack` runs inside the process."""
    builds = {}
    for r in records:
        if r["task"] == "build":
            builds.setdefault(r["package"], []).append((r["start"], r["end"]))
    return [(length(subtract((r["start"], r["end"]), builds.get(r["package"], []))), r.get("exit", 0) == 0)
            for r in records if r["task"] == "publish"]


def parallelism(tasks):
    """The most tasks running at one instant. An end and a start at the same
    instant do not overlap."""
    events = sorted((t, delta) for _, _, start, end, _ in tasks for t, delta in ((start, 1), (end, -1)))
    running = most = 0
    for _, delta in events:
        running += delta
        most = max(most, running)
    return most


def busy(tasks, span):
    """How long at least one task was running inside the span."""
    covered, reach = 0.0, span[0]
    for start, end in sorted((max(t[2], span[0]), min(t[3], span[1])) for t in tasks):
        if end > reach:
            covered += end - max(start, reach)
            reach = end
    return covered


def stats(durations, failed=0):
    return {"count": len(durations), "seconds": round(sum(durations), 3),
            "median": round(statistics.median(durations), 3) if durations else 0,
            "max": round(max(durations), 3) if durations else 0, "failed": failed}


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


class Timed:
    """Each (package, kind)'s summed duration and its first start and last
    end over every record of it."""

    def __init__(self, tasks):
        self.duration, self.start, self.end = {}, {}, {}
        for package, kind, start, end, _ in tasks:
            node = (package, kind)
            self.duration[node] = self.duration.get(node, 0) + end - start
            self.start[node] = min(self.start.get(node, start), start)
            self.end[node] = max(self.end.get(node, end), end)

    def __contains__(self, node):
        return node in self.duration


def nearest(graph, timed, package, kind):
    """The providers of a package that ran a task of the kind, reached
    through those that ran none (§19.2a)."""
    found = set()
    for provider in graph.get(package, []):
        if (provider, kind) in timed:
            found.add(provider)
        else:
            found |= nearest(graph, timed, provider, kind)
    return found


def edges(graph, timed, relation):
    """Every edge of the precedence between two tasks that ran, (before,
    after): a package's build before its publication, a provider's
    publication before its consumer's, and the relation's edge into a
    consumer's build."""
    out = []
    for package in topological(graph):
        build, publish = (package, "build"), (package, "publish")
        if build in timed and publish in timed:
            out.append((build, publish))
        if publish in timed:
            out += [((p, "publish"), publish) for p in sorted(nearest(graph, timed, package, "publish"))]
        if build in timed and relation != "none":
            out += [((p, relation), build) for p in sorted(nearest(graph, timed, package, relation))]
    return out


def honoured(timed, edge):
    before, after = edge
    return timed.start[after] >= timed.end[before] - SLACK


def name(node):
    return f"{node[0]} {node[1]}"


def consumer_builds(graph, timed):
    """How many consumer builds started after their providers' builds, and
    after their publications: the relation the records show, beside the one
    the protocol declares."""
    pairs = [(p, c) for c in graph if (c, "build") in timed for p in nearest(graph, timed, c, "build")]
    after_build = sum(honoured(timed, ((p, "build"), (c, "build"))) for p, c in pairs)
    after_publish = sum((p, "publish") in timed and honoured(timed, ((p, "publish"), (c, "build"))) for p, c in pairs)
    return {"edges": len(pairs), "afterBuild": after_build, "afterPublish": after_publish}


def observed_relation(builds):
    if builds["edges"] == 0:
        return None
    if builds["afterPublish"] == builds["edges"]:
        return "publish"
    if builds["afterBuild"] == builds["edges"]:
        return "build"
    return "none"


def critical_path(graph, timed, relation):
    """The longest precedence chain through the plan's task graph, and the
    tasks on it. A package without a task of a kind passes its providers'
    finish through, so a chain is never cut by a package that did not run."""
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
        before_build = latest([] if relation == "none" else [(p, relation) for p in providers])
        finish[build] = (finish[before_build] if before_build else 0) + timed.duration.get(build, 0)
        previous[build] = before_build
        before_publish = latest([build] + [(p, "publish") for p in providers])
        finish[publish] = finish[before_publish] + timed.duration.get(publish, 0)
        previous[publish] = before_publish
    end = latest(finish)
    longest = finish[end] if end else 0
    chain = []
    while end is not None:
        if end in timed:
            chain.append(name(end))
        end = previous.get(end)
    chain.reverse()
    return longest, chain


def shipped(observation):
    return sorted(name for name, row in observation["packages"].items()
                  if row["registry"] not in (BASELINE, "absent", "error"))


def measure(steps, records, graph, observation, relation):
    span, planning = release_span(steps), plan_span(steps)
    where = [placed(r, span) for r in records]
    release = [r for r, w in zip(records, where) if w == "inside"]
    tasks = tasks_of(release)
    if not any(kind == "build" for _, kind, *_ in tasks) or not any(kind == "publish" for _, kind, *_ in tasks):
        raise SystemExit("timing.py: the records hold no build or no publication of the release")
    timed = Timed(tasks)
    makespan = span[1] - span[0]
    work = sum(end - start for _, _, start, end, _ in tasks)
    slots = parallelism(tasks)
    longest, chain = critical_path(graph, timed, relation)
    bound = max(longest, work / slots)
    builds = consumer_builds(graph, timed)
    npm = npm_publishes(release)
    plan_builds = [r["end"] - r["start"] for r in records if r["task"] == "build" and placed(r, planning) == "inside"]
    accepted_publishes = {p for p, kind, _, _, ok in tasks if kind == "publish" and ok}
    return {
        "relation": relation,
        "observedRelation": observed_relation(builds),
        "consumerBuilds": builds,
        "violations": [{"before": name(b), "after": name(a)}
                       for b, a in edges(graph, timed, relation) if not honoured(timed, (b, a))],
        "planningSeconds": round(planning[1] - planning[0], 3),
        "planBuilds": {"count": len(plan_builds), "seconds": round(sum(plan_builds), 3)},
        "makespanSeconds": round(makespan, 3),
        "idleSeconds": round(makespan - busy(tasks, span), 3),
        "work": round(work, 3),
        "criticalPath": round(longest, 3),
        "parallelism": slots,
        "bound": round(bound, 3),
        "ratio": round(makespan / bound, 3),
        "criticalChain": chain,
        "tasks": {
            "build": stats([end - start for _, kind, start, end, _ in tasks if kind == "build"]),
            "publish": stats([end - start for _, kind, start, end, _ in tasks if kind == "publish"],
                             failed=sum(1 for _, kind, *_, ok in tasks if kind == "publish" and not ok)),
            "npmPublish": stats([seconds for seconds, _ in npm], failed=sum(1 for _, ok in npm if not ok)),
        },
        "taskTimes": [{"package": p, "task": kind, "start": round(start - span[0], 3), "seconds": round(end - start, 3)}
                      for p, kind, start, end, _ in tasks],
        "untimed": [p for p in shipped(observation) if (p, "build") not in timed or p not in accepted_publishes],
        "unknown": sorted({r["package"] for r in release if r["package"] not in graph}),
        "straddling": where.count("straddling"),
    }


def main(argv):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--steps", required=True)
    parser.add_argument("--records", required=True)
    parser.add_argument("--repo", required=True)
    parser.add_argument("--observation", required=True)
    parser.add_argument("--relation", required=True, choices=RELATIONS)
    args = parser.parse_args(argv)
    records = read_lines(args.records) if os.path.exists(args.records) else []
    with open(args.observation) as f:
        observation = json.load(f)
    print(json.dumps(measure(read_lines(args.steps), records, read_graph(args.repo),
                             observation, args.relation), sort_keys=True))


if __name__ == "__main__":
    main(sys.argv[1:])
