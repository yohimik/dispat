#!/usr/bin/env python3
"""The timing figures over hand-made records: which records are the
release's, how a publish is timed, what overlaps, and the longest chain under
each build readiness relation. The Timing table on the page is these numbers.
"""
import importlib.util
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import unittest

HERE = pathlib.Path(__file__).parent
spec = importlib.util.spec_from_file_location("timing", HERE / "timing.py")
timing = importlib.util.module_from_spec(spec)
spec.loader.exec_module(timing)

# core is the provider of cli and of chain, and chain of tail.
GRAPH = {"core": [], "cli": ["core"], "chain": ["core"], "tail": ["chain"]}


def step(name, started, ended, kind="release"):
    return {"step": name, "exit": 0, "kind": kind, "phase": "initial",
            "seconds": round(ended - started, 2), "started": started, "ended": ended}


def record(package, task, start, end, **extra):
    return {"package": package, "task": task, "start": start, "end": end, **extra}


def observation(**registry):
    return {"packages": {name: {"registry": version} for name, version in registry.items()}}


STEPS = [step("plan", 0.0, 0.5, kind="query"), step("run", 1.0, 11.0)]


class WindowTest(unittest.TestCase):
    def test_the_release_spans_every_run_step(self):
        steps = [step("plan", 0, 1, "query"), step("run-version", 2, 3), step("run-commit", 3, 3.1, "manual"),
                 step("run-publish", 3.2, 9), step("final", 9, 10, "query")]
        self.assertEqual(timing.release_span(steps), (2, 9))

    def test_a_record_outside_the_release_is_not_the_release_s(self):
        records = [record("core", "upload", 0.1, 0.2, status=201),
                   record("core", "build", 2, 3), record("core", "publish", 3, 4)]
        figures = timing.measure(STEPS, records, GRAPH, observation(core="1.1.0"), "build")
        self.assertEqual(figures["tasks"], {"version": 0, "build": 1, "publish": 1})
        self.assertEqual(figures["work"], 2)

    def test_no_release_step_is_refused(self):
        with self.assertRaises(SystemExit):
            timing.release_span([step("plan", 0, 1, "query")])


class PublishTest(unittest.TestCase):
    def test_an_npm_publish_is_preferred_to_the_upload_inside_it(self):
        records = [record("core", "publish", 2, 4), record("core", "upload", 3.5, 3.9, status=201)]
        tasks, sources = timing.tasks_of(records, [])
        self.assertEqual([(p, k, s) for p, k, s in tasks], [("core", "publish", [(2, 4)])])
        self.assertEqual(sources, {"core": "npm"})

    def test_an_upload_alone_is_the_publish(self):
        records = [record("core", "upload", 3, 3.5, status=201), record("cli", "upload", 4, 5, status=502)]
        tasks, sources = timing.tasks_of(records, [])
        self.assertEqual([(p, k, s) for p, k, s in tasks], [("core", "publish", [(3, 3.5)])])
        self.assertEqual(sources, {"core": "registry"})

    def test_the_package_s_own_build_is_cut_out_of_its_publish(self):
        records = [record("core", "publish", 2, 6), record("core", "build", 3, 4), record("cli", "build", 4, 5)]
        tasks, _ = timing.tasks_of(records, [])
        publish = [s for p, k, s in tasks if k == "publish"]
        self.assertEqual(publish, [[(2, 3), (4, 6)]])
        self.assertEqual(timing.length(publish[0]), 3)

    def test_the_version_step_is_one_task(self):
        tasks, _ = timing.tasks_of([], [step("run-version", 1, 3)])
        self.assertEqual(tasks, [(None, "version", [(1, 3)])])


class ParallelismTest(unittest.TestCase):
    def test_the_most_segments_at_one_instant(self):
        tasks = [("a", "build", [(0, 4)]), ("b", "build", [(1, 2)]), ("c", "build", [(1.5, 3)]),
                 ("d", "build", [(3.5, 5)])]
        self.assertEqual(timing.parallelism(tasks), 3)

    def test_an_end_and_a_start_at_one_instant_do_not_overlap(self):
        self.assertEqual(timing.parallelism([("a", "build", [(0, 1)]), ("b", "build", [(1, 2)])]), 1)

    def test_a_publish_does_not_overlap_the_build_cut_out_of_it(self):
        tasks = [("core", "publish", [(2, 3), (4, 6)]), ("core", "build", [(3, 4)])]
        self.assertEqual(timing.parallelism(tasks), 1)


class CriticalPathTest(unittest.TestCase):
    # Every build lasts 1 and every publish 2, except chain's build at 3.
    TASKS = [(p, "build", [(0, 3 if p == "chain" else 1)]) for p in GRAPH] + \
            [(p, "publish", [(0, 2)]) for p in GRAPH]

    def test_a_consumer_build_after_its_providers_builds(self):
        longest, chain = timing.critical_path(self.TASKS, GRAPH, "build")
        # chain builds from 1 to 4, after core's build and beside core's
        # publication, then publishes to 6; tail built by 5 waits for that
        # publication and publishes to 8.
        self.assertEqual(longest, 8)
        self.assertEqual(chain, ["core build", "chain build", "chain publish", "tail publish"])

    def test_a_consumer_build_after_its_providers_publications(self):
        longest, chain = timing.critical_path(self.TASKS, GRAPH, "publish")
        # core 1+2, chain 3+2, tail 1+2.
        self.assertEqual(longest, 11)
        self.assertEqual(chain, ["core build", "core publish", "chain build", "chain publish",
                                 "tail build", "tail publish"])

    def test_the_version_precedes_everything(self):
        longest, chain = timing.critical_path(self.TASKS + [(None, "version", [(0, 5)])], GRAPH, "publish")
        self.assertEqual(longest, 16)
        self.assertEqual(chain[0], "version")

    def test_a_package_that_did_not_run_passes_its_provider_through(self):
        tasks = [("core", "build", [(0, 1)]), ("core", "publish", [(0, 2)]),
                 ("tail", "build", [(0, 1)]), ("tail", "publish", [(0, 1)])]
        longest, chain = timing.critical_path(tasks, GRAPH, "publish")
        self.assertEqual(longest, 5)
        self.assertEqual(chain, ["core build", "core publish", "tail build", "tail publish"])


class MeasureTest(unittest.TestCase):
    def test_the_bound_and_the_ratio(self):
        # Two packages, one after the other under the publish relation:
        # Lk = 1 + 2 + 1 + 2 = 6; Wk = 6; s = 1; bound 6; makespan 10.
        records = [record("core", "build", 1, 2), record("core", "publish", 2, 4),
                   record("cli", "build", 4, 5), record("cli", "publish", 5, 7)]
        figures = timing.measure(STEPS, records, GRAPH, observation(core="1.1.0", cli="1.0.1"), "publish")
        self.assertEqual(figures["planningSeconds"], 0.5)
        self.assertEqual(figures["makespanSeconds"], 10)
        self.assertEqual((figures["work"], figures["criticalPath"], figures["parallelism"]), (6, 6, 1))
        self.assertEqual((figures["bound"], figures["ratio"]), (6, 1.667))
        self.assertEqual(figures["untimed"], [])

    def test_the_bound_is_the_work_over_the_slots_when_that_is_longer(self):
        records = [record(p, "build", 1, 3) for p in ("cli", "chain", "tail")] + \
                  [record(p, "publish", 3, 4) for p in ("cli", "chain", "tail")]
        figures = timing.measure(STEPS, records, GRAPH, observation(), "build")
        # Wk = 9 over s = 3 is 3, Lk = chain 2 + tail 2 + tail's publish 1 = 5.
        self.assertEqual((figures["work"], figures["parallelism"], figures["criticalPath"]), (9, 3, 5))
        self.assertEqual(figures["bound"], 5)

    def test_a_shipped_package_without_a_record_is_untimed(self):
        records = [record("core", "build", 1, 2), record("core", "publish", 2, 3), record("cli", "build", 3, 4)]
        figures = timing.measure(STEPS, records, GRAPH, observation(core="1.1.0", cli="1.0.1", tail="1.0.0"), "build")
        self.assertEqual(figures["untimed"], ["cli"])


class CommandLineTest(unittest.TestCase):
    def test_the_figures_from_files(self):
        with tempfile.TemporaryDirectory() as folder:
            root = pathlib.Path(folder)
            for name, deps in GRAPH.items():
                package = root / "repo" / "packages" / name
                package.mkdir(parents=True)
                (package / "package.json").write_text(json.dumps(
                    {"name": name, "version": "1.0.0", "dependencies": {d: "~1.0.0" for d in deps} | {"left-pad": "1"}}))
            (root / "steps.jsonl").write_text("".join(json.dumps(s) + "\n" for s in STEPS))
            (root / "timing.jsonl").write_text(json.dumps(record("core", "build", 1, 2)) + "\n"
                                               + json.dumps(record("core", "publish", 2, 3)) + "\n")
            (root / "observation.json").write_text(json.dumps(observation(core="1.1.0")))
            out = subprocess.run([sys.executable, str(HERE / "timing.py"), "--steps", str(root / "steps.jsonl"),
                                  "--records", str(root / "timing.jsonl"), "--repo", str(root / "repo"),
                                  "--observation", str(root / "observation.json"), "--relation", "build"],
                                 check=True, capture_output=True, text=True, env={**os.environ}).stdout
            figures = json.loads(out)
            self.assertEqual(figures["criticalChain"], ["core build", "core publish"])
            self.assertEqual(timing.read_graph(str(root / "repo")), {n: d for n, d in GRAPH.items()})


if __name__ == "__main__":
    unittest.main()
