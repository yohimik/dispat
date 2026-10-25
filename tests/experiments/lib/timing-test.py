#!/usr/bin/env python3
"""The timing figures over hand-made records: which records are the
release's, what a task is, what overlaps, the longest chain under each build
readiness relation, and which edges a release did not honour. The Timing and
Tasks tables on the page are these numbers.
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
    if task == "upload":
        extra.setdefault("status", 201)
    return {"package": package, "task": task, "start": start, "end": end, **extra}


def observation(**registry):
    return {"packages": {name: {"registry": version} for name, version in registry.items()}}


def timed(*records):
    return timing.Timed(timing.tasks_of(list(records)))


STEPS = [step("plan", 0.0, 0.5, kind="query"), step("run", 1.0, 11.0)]


class WindowTest(unittest.TestCase):
    def test_the_release_spans_every_run_step(self):
        steps = [step("plan", 0, 1, "query"), step("run-version", 2, 3), step("run-commit", 3, 3.1, "manual"),
                 step("run-publish", 3.2, 9), step("final", 9, 10, "query")]
        self.assertEqual(timing.release_span(steps), (2, 9))

    def test_where_a_record_stands(self):
        span = (1, 11)
        self.assertEqual(timing.placed(record("core", "build", 1.0, 2), span), "inside")
        self.assertEqual(timing.placed(record("core", "build", 0.1, 0.2), span), "outside")
        self.assertEqual(timing.placed(record("core", "build", 0.5, 2), span), "straddling")
        self.assertEqual(timing.placed(record("core", "build", 10.99, 12), span), "outside")

    def test_a_record_outside_the_release_is_not_the_release_s_and_a_straddling_one_is_counted(self):
        records = [record("core", "upload", 0.1, 0.2), record("cli", "build", 0.6, 1.5),
                   record("core", "build", 2, 3), record("core", "upload", 3, 4)]
        figures = timing.measure(STEPS, records, GRAPH, observation(core="1.1.0"), "build")
        self.assertEqual((figures["tasks"]["build"]["count"], figures["tasks"]["publish"]["count"]), (1, 1))
        self.assertEqual((figures["work"], figures["straddling"]), (2, 1))

    def test_no_release_step_or_no_plan_step_is_refused(self):
        with self.assertRaises(SystemExit):
            timing.release_span([step("plan", 0, 1, "query")])
        with self.assertRaises(SystemExit):
            timing.plan_span([step("run", 0, 1)])


class TaskTest(unittest.TestCase):
    def test_a_publication_is_its_upload_accepted_or_not(self):
        records = [record("core", "publish", 2, 4, exit=0), record("core", "upload", 3.5, 3.9),
                   record("cli", "upload", 4, 5, status=502), record("cli", "build", 1, 2)]
        self.assertEqual(timing.tasks_of(records), [("cli", "build", 1, 2, True), ("core", "publish", 3.5, 3.9, True),
                                                    ("cli", "publish", 4, 5, False)])

    def test_an_npm_publish_has_its_own_package_s_build_cut_out(self):
        records = [record("core", "publish", 2, 6, exit=0), record("core", "build", 3, 4),
                   record("cli", "build", 4, 5), record("cli", "publish", 5, 6, exit=1)]
        self.assertEqual(timing.npm_publishes(records), [(3, True), (1, False)])

    def test_the_stats_of_one_kind(self):
        self.assertEqual(timing.stats([0.5, 0.2, 0.3], failed=1),
                         {"count": 3, "seconds": 1.0, "median": 0.3, "max": 0.5, "failed": 1})
        self.assertEqual(timing.stats([]), {"count": 0, "seconds": 0, "median": 0, "max": 0, "failed": 0})


class ParallelismTest(unittest.TestCase):
    @staticmethod
    def tasks(*intervals):
        return [("p", "build", start, end, True) for start, end in intervals]

    def test_the_most_tasks_at_one_instant(self):
        self.assertEqual(timing.parallelism(self.tasks((0, 4), (1, 2), (1.5, 3), (3.5, 5))), 3)

    def test_an_end_and_a_start_at_one_instant_do_not_overlap(self):
        self.assertEqual(timing.parallelism(self.tasks((0, 1), (1, 2))), 1)

    def test_the_time_no_task_ran(self):
        self.assertEqual(timing.busy(self.tasks((0, 2), (1, 3), (5, 6), (9, 12)), (1, 10)), 2 + 1 + 1)


class CriticalPathTest(unittest.TestCase):
    # Every build lasts 1 and every publication 2, except chain's build at 3.
    TIMED = timed(*[record(p, "build", 0, 3 if p == "chain" else 1) for p in GRAPH],
                  *[record(p, "upload", 0, 2) for p in GRAPH])

    def test_a_consumer_build_after_its_providers_builds(self):
        longest, chain = timing.critical_path(GRAPH, self.TIMED, "build")
        # chain builds from 1 to 4, after core's build and beside core's
        # publication, then publishes to 6; tail built by 5 waits for that
        # publication and publishes to 8.
        self.assertEqual(longest, 8)
        self.assertEqual(chain, ["core build", "chain build", "chain publish", "tail publish"])

    def test_a_consumer_build_after_its_providers_publications(self):
        longest, chain = timing.critical_path(GRAPH, self.TIMED, "publish")
        # core 1+2, chain 3+2, tail 1+2.
        self.assertEqual(longest, 11)
        self.assertEqual(chain, ["core build", "core publish", "chain build", "chain publish",
                                 "tail build", "tail publish"])

    def test_a_consumer_build_after_nothing(self):
        longest, chain = timing.critical_path(GRAPH, self.TIMED, "none")
        # chain's build 3 + its publication 2 + tail's 2, while core's
        # publication ends at 3.
        self.assertEqual(longest, 7)
        self.assertEqual(chain, ["chain build", "chain publish", "tail publish"])

    def test_a_package_that_did_not_run_passes_its_provider_through(self):
        ran = timed(record("core", "build", 0, 1), record("core", "upload", 0, 2),
                    record("tail", "build", 0, 1), record("tail", "upload", 0, 1))
        longest, chain = timing.critical_path(GRAPH, ran, "publish")
        self.assertEqual(longest, 5)
        self.assertEqual(chain, ["core build", "core publish", "tail build", "tail publish"])


class EdgeTest(unittest.TestCase):
    def test_the_precedence_under_each_relation(self):
        ran = timed(*[record(p, "build", 0, 1) for p in ("core", "chain", "tail")],
                    *[record(p, "upload", 1, 2) for p in ("core", "tail")])
        # chain did not publish, so tail's publication follows core's through it.
        self.assertEqual(timing.edges(GRAPH, ran, "none"), [
            (("core", "build"), ("core", "publish")),
            (("tail", "build"), ("tail", "publish")), (("core", "publish"), ("tail", "publish"))])
        self.assertIn((("chain", "build"), ("tail", "build")), timing.edges(GRAPH, ran, "build"))
        self.assertIn((("core", "publish"), ("chain", "build")), timing.edges(GRAPH, ran, "publish"))

    def test_a_consumer_built_beside_its_provider_breaks_the_build_edge(self):
        records = [record("core", "build", 1, 2), record("cli", "build", 1.01, 2), record("core", "upload", 2, 3),
                   record("cli", "upload", 3, 4)]
        figures = timing.measure(STEPS, records, GRAPH, observation(core="1.1.0", cli="1.0.1"), "build")
        self.assertEqual(figures["violations"], [{"before": "core build", "after": "cli build"}])
        self.assertEqual(figures["consumerBuilds"], {"edges": 1, "afterBuild": 0, "afterPublish": 0})
        self.assertEqual(figures["observedRelation"], "none")
        self.assertEqual(timing.measure(STEPS, records, GRAPH, observation(), "none")["violations"], [])

    def test_a_consumer_published_before_its_provider_breaks_the_publish_order(self):
        records = [record("core", "build", 1, 2), record("cli", "build", 2, 3), record("cli", "upload", 3, 4),
                   record("core", "upload", 3.5, 5)]
        figures = timing.measure(STEPS, records, GRAPH, observation(), "build")
        self.assertEqual(figures["violations"], [{"before": "core publish", "after": "cli publish"}])
        self.assertEqual(figures["observedRelation"], "build")

    def test_an_edge_within_the_clocks_slack_is_honoured(self):
        ran = timed(record("core", "build", 0, 1), record("cli", "build", 1 - timing.SLACK / 2, 2))
        self.assertTrue(timing.honoured(ran, (("core", "build"), ("cli", "build"))))

    def test_the_relation_the_records_show(self):
        self.assertEqual(timing.observed_relation({"edges": 2, "afterBuild": 2, "afterPublish": 2}), "publish")
        self.assertEqual(timing.observed_relation({"edges": 2, "afterBuild": 2, "afterPublish": 1}), "build")
        self.assertEqual(timing.observed_relation({"edges": 2, "afterBuild": 1, "afterPublish": 0}), "none")
        self.assertIsNone(timing.observed_relation({"edges": 0, "afterBuild": 0, "afterPublish": 0}))


class MeasureTest(unittest.TestCase):
    def test_the_bound_the_ratio_and_each_task(self):
        # Two packages, one after the other under the publish relation:
        # Lk = 1 + 2 + 1 + 2 = 6; Wk = 6; s = 1; bound 6; makespan 10.
        records = [record("core", "build", 1, 2), record("core", "upload", 2, 4),
                   record("cli", "build", 4, 5), record("cli", "upload", 5, 7),
                   record("core", "build", 0.1, 0.4)]
        figures = timing.measure(STEPS, records, GRAPH, observation(core="1.1.0", cli="1.0.1"), "publish")
        self.assertEqual((figures["planningSeconds"], figures["planBuilds"]), (0.5, {"count": 1, "seconds": 0.3}))
        self.assertEqual((figures["makespanSeconds"], figures["idleSeconds"]), (10, 4))
        self.assertEqual((figures["work"], figures["criticalPath"], figures["parallelism"]), (6, 6, 1))
        self.assertEqual((figures["bound"], figures["ratio"]), (6, 1.667))
        self.assertEqual(figures["tasks"]["publish"], {"count": 2, "seconds": 4, "median": 2, "max": 2, "failed": 0})
        self.assertEqual(figures["taskTimes"][:2], [{"package": "core", "task": "build", "start": 0, "seconds": 1},
                                                   {"package": "core", "task": "publish", "start": 1, "seconds": 2}])
        self.assertEqual((figures["violations"], figures["observedRelation"]), ([], "publish"))
        self.assertEqual((figures["untimed"], figures["unknown"], figures["straddling"]), ([], [], 0))

    def test_the_bound_is_the_work_over_the_slots_when_that_is_longer(self):
        records = [record(p, "build", 1, 3) for p in ("cli", "chain", "tail")] + \
                  [record(p, "upload", 3, 4) for p in ("cli", "chain", "tail")]
        figures = timing.measure(STEPS, records, GRAPH, observation(), "build")
        # Wk = 9 over s = 3 is 3, Lk = chain 2 + tail 2 + tail's publication 1 = 5.
        self.assertEqual((figures["work"], figures["parallelism"], figures["criticalPath"]), (9, 3, 5))
        self.assertEqual(figures["bound"], 5)

    def test_a_shipped_package_without_a_build_or_an_accepted_upload_is_untimed(self):
        records = [record("core", "build", 1, 2), record("core", "upload", 2, 3), record("cli", "build", 3, 4),
                   record("cli", "upload", 4, 5, status=502), record("tail", "upload", 5, 6)]
        figures = timing.measure(STEPS, records, GRAPH, observation(core="1.1.0", cli="1.0.1", tail="1.0.1"), "build")
        self.assertEqual(figures["untimed"], ["cli", "tail"])
        self.assertEqual(figures["tasks"]["publish"]["failed"], 1)

    def test_a_record_of_a_package_outside_the_graph_is_named(self):
        records = [record("core", "build", 1, 2), record("fixture", "upload", 2, 3), record("core", "upload", 3, 4)]
        self.assertEqual(timing.measure(STEPS, records, GRAPH, observation(), "build")["unknown"], ["fixture"])

    def test_a_release_without_a_build_or_a_publication_is_refused(self):
        for records in ([record("core", "build", 1, 2)], [record("core", "upload", 1, 2)]):
            with self.assertRaises(SystemExit):
                timing.measure(STEPS, records, GRAPH, observation(), "build")


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
                                               + json.dumps(record("core", "upload", 2, 3)) + "\n")
            (root / "observation.json").write_text(json.dumps(observation(core="1.1.0")))
            out = subprocess.run([sys.executable, str(HERE / "timing.py"), "--steps", str(root / "steps.jsonl"),
                                  "--records", str(root / "timing.jsonl"), "--repo", str(root / "repo"),
                                  "--observation", str(root / "observation.json"), "--relation", "none"],
                                 check=True, capture_output=True, text=True, env={**os.environ}).stdout
            figures = json.loads(out)
            self.assertEqual((figures["relation"], figures["criticalChain"]), ("none", ["core build", "core publish"]))
            self.assertEqual(timing.read_graph(str(root / "repo")), {n: d for n, d in GRAPH.items()})


if __name__ == "__main__":
    unittest.main()
