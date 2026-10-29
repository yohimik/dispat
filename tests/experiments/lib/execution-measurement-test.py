#!/usr/bin/env python3
"""Pre-execution graph evidence and additive monotonic measurement records."""
import json
import os
import pathlib
import shutil
import subprocess
import tempfile
import unittest
from unittest import mock

import execution_measurement as measurement


class MeasurementTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = pathlib.Path(self.temporary.name)
        self.output = self.root / "results" / "timing-six-dispat"
        self.output.mkdir(parents=True)
        self.repo = self.root / "repo"
        self.repo.mkdir()
        self.protocol = self.root / "protocol"
        (self.protocol / "timing").mkdir(parents=True)
        self.binary = self.root / "tool"
        self.binary.write_text("pinned tool executable")
        self.settings = measurement.initialize(self.output, "dispat", "six", 1)
        self.directory = pathlib.Path(self.settings["directory"])
        self.plan = {"packages": {"core": "1.1.0", "leaf": "1.1.0"}, "told": "feat(core): change"}
        self.write_fixture("dispat")

    def write_fixture(self, tool):
        (self.output / "plan.json").write_text(json.dumps(self.plan))
        (self.protocol / "timing" / (tool + ".sh")).write_text("measure_timing " + ("none" if tool == "nx" else "build") + " after-release\n")
        (self.repo / "dispat.yaml").write_text("spaces:\n  packages:\n    path: packages\n")
        (self.repo / "nx.json").write_text(json.dumps({"release": {"version": {"preVersionCommand": "npx nx run-many -t build"}}}))
        for package, providers in {"core": [], "bridge": ["core"], "leaf": ["bridge"]}.items():
            folder = self.repo / "packages" / package
            folder.mkdir(parents=True, exist_ok=True)
            (folder / "package.json").write_text(json.dumps({"name": package,
                "scripts": {"build": "fixture build", "prepack": "npm run --silent build"},
                "dependencies": {provider: "1.0.0" for provider in providers}}))
            (folder / "index.js").write_text("source for " + package)
        (self.repo / ".npmrc").write_text("credential-that-must-not-be-retained")

    def capture(self, tool="dispat"):
        with mock.patch.object(measurement, "command_output", return_value="pinned-revision-or-version"), mock.patch.object(measurement.shutil, "which", return_value=str(self.binary)):
            measurement.capture_inputs(self.directory, self.output, self.repo, tool, self.protocol)
        return measurement.read_json(self.directory / "inputs.json")

    def records(self, omit=()):
        inputs = measurement.read_json(self.directory / "inputs.json")
        trial_id = self.settings["trialId"]
        records = []
        for vertex in inputs["graph"]["vertices"]:
            if vertex["isVirtual"] or vertex["id"] in omit:
                continue
            package, operation = vertex["id"].split(":")
            records.append({"package": package, "task": "upload" if operation == "publish" else "build",
                "start": 100, "end": 100.5, "status": 201,
                "measurement": {"durationSeconds": 0.500000007, "startNanoseconds": "1000000000", "endNanoseconds": "1500000007",
                    "clockKind": "monotonic", "clockDomain": trial_id + ":" + vertex["id"], "boundary": "fixture operation",
                    "phase": {"trialId": trial_id, "step": "run"}}})
        (self.directory / "timing.jsonl").write_text("".join(json.dumps(row) + "\n" for row in records))
        step = {"step": "run", "kind": "release", "phase": "initial", "exit": 0,
                "start": {"nanoseconds": "500000000", "wallSeconds": 99},
                "end": {"nanoseconds": "2500000011", "wallSeconds": 102}, "durationSeconds": 2.000000011}
        (self.directory / "monotonic-steps.jsonl").write_text(json.dumps(step) + "\n")
        return records

    def test_projection_retains_virtual_relays_and_typed_edges_before_execution(self):
        inputs = self.capture()
        graph = inputs["graph"]
        self.assertEqual(len(graph["vertices"]), 6)
        self.assertEqual([v["id"] for v in graph["vertices"] if v["isVirtual"]], ["bridge:build", "bridge:publish"])
        self.assertEqual(len(graph["edges"]), 7)
        self.assertIn({"from": "bridge:build", "to": "leaf:build", "kind": "provider-build-before-build"}, graph["edges"])
        self.assertTrue(inputs["capturedBeforeRelease"])
        self.assertNotIn("credential-that-must-not-be-retained", (self.directory / "inputs.json").read_text())
        self.assertEqual(inputs["sourceFiles"]["packages/core/index.js"]["text"], "source for core")

    def test_post_release_manifest_change_cannot_reconstruct_or_replace_captured_graph(self):
        captured = self.capture()
        manifest = self.repo / "packages/leaf/package.json"
        manifest.write_text(json.dumps({"name": "leaf", "dependencies": {}}))
        self.assertEqual(self.capture(), captured)
        self.records()
        block = measurement.build_measurement(self.directory, 0)
        self.assertEqual(block["graph"]["edges"], captured["graph"]["edges"])

    def test_nx_build_selection_preserves_unreleased_run_many_projects(self):
        self.write_fixture("nx")
        inputs = self.capture("nx")
        virtual = [vertex["id"] for vertex in inputs["graph"]["vertices"] if vertex["isVirtual"]]
        self.assertEqual(virtual, ["bridge:publish"])
        self.assertFalse(any(edge["kind"] == "provider-build-before-build" for edge in inputs["graph"]["edges"]))

    def test_required_missing_operation_remains_real_and_capacity_is_never_peak_overlap(self):
        self.capture()
        self.records(omit=("leaf:publish",))
        block = measurement.build_measurement(self.directory, 0)
        leaf = next(vertex for vertex in block["graph"]["vertices"] if vertex["id"] == "leaf:publish")
        self.assertFalse(leaf["isVirtual"])
        self.assertEqual(leaf["durationInputs"], [])
        self.assertIsNone(block["resources"]["taskSlotLimit"]["value"])
        self.assertIsNone(block["resources"]["usableOnePoolSlots"]["value"])
        self.assertIsNone(block["observedPeakOverlap"]["value"])
        self.assertIsNone(block["clockCompatibility"]["uncertainty"]["relativeRateErrorBound"])
        self.assertEqual(block["taskIntervals"][0]["durationSeconds"], 0.500000007)
        self.assertEqual(block["timeWindows"][0]["seconds"], 2.000000011)

    def test_failed_attempt_is_retained_and_does_not_become_successful_coverage(self):
        self.capture()
        records = self.records()
        records[0]["exit"] = 42
        (self.directory / "timing.jsonl").write_text("".join(json.dumps(row) + "\n" for row in records))
        block = measurement.build_measurement(self.directory, 1)
        self.assertEqual(block["trial"]["status"], "failed")
        self.assertEqual(block["taskIntervals"][0]["status"], "failed")

    def test_unselected_operation_cannot_be_silently_dropped_or_called_virtual(self):
        self.capture()
        records = self.records()
        unexpected = json.loads(json.dumps(records[0]))
        unexpected["package"] = "bridge"
        with (self.directory / "timing.jsonl").open("a") as target:
            target.write(json.dumps(unexpected) + "\n")
        with self.assertRaisesRegex(ValueError, "pre-execution selection"):
            measurement.build_measurement(self.directory, 0)

    def test_new_repetition_keeps_old_raw_records_and_unique_identity(self):
        (self.directory / "timing.jsonl").write_text("retained first trial")
        shutil.rmtree(self.output)
        self.output.mkdir()
        second = measurement.initialize(self.output, "dispat", "six", 2)
        self.assertNotEqual(self.settings["trialId"], second["trialId"])
        self.assertEqual((self.directory / "timing.jsonl").read_text(), "retained first trial")
        self.assertEqual(measurement.read_json(pathlib.Path(second["directory"]) / "trial.json")["repetition"], 2)

    def test_legacy_raw_links_remain_readable_when_results_move_out_of_container(self):
        (self.directory / "timing.jsonl").write_text("retained timing record")
        destination = self.root / "downloaded-results"
        shutil.move(str(self.output.parent), destination)
        relocated = destination / self.output.name / "timing.jsonl"
        self.assertFalse(os.path.isabs(os.readlink(relocated)))
        self.assertEqual(relocated.read_text(), "retained timing record")

    def test_coordinator_duration_uses_monotonic_samples_when_wall_clock_moves_backwards(self):
        samples = [{"nanoseconds": "1000000000", "wallSeconds": 5}, {"nanoseconds": "1500000007", "wallSeconds": 4}]
        with mock.patch.object(measurement, "monotonic_sample", side_effect=samples):
            measurement.begin_step(self.directory, "run", "release", "initial")
            measurement.end_step(self.directory, "run", 0)
        recorded = measurement.read_lines(self.directory / "monotonic-steps.jsonl")[0]
        self.assertEqual(recorded["durationSeconds"], 0.500000007)
        self.assertFalse((self.directory / "active-step.json").exists())

    def test_changed_readiness_policy_is_refused_with_configuration_retained(self):
        (self.repo / "dispat.yaml").write_text("isBuildWaitingPublish: true\n")
        with self.assertRaisesRegex(ValueError, "readiness override"):
            self.capture()
        self.assertIn("isBuildWaitingPublish", (self.directory / "configuration-capture.json").read_text())

    def test_shared_deriver_checks_real_producer_output_when_available(self):
        executable = os.environ.get("EXECUTION_MEASUREMENT_TEST_BINARY")
        if not executable:
            self.skipTest("shared CLI integration runs in the built measurement environment")
        self.capture()
        self.records()
        raw = self.directory / "raw.json"
        measurement.write_new(raw, measurement.build_measurement(self.directory, 0))
        destination = self.directory / "derived.json"
        subprocess.run([executable, "-input", str(raw), "-output", str(destination)], check=True)
        derived = measurement.read_json(destination)
        self.assertEqual(derived["graph"]["counts"], {"vertices": 6, "edges": 7, "virtualVertices": 2, "timedVertices": 4})
        self.assertEqual(derived["analysis"][0]["status"], "computed")
        self.assertAlmostEqual(derived["analysis"][0]["value"], 2.000000028)
        self.assertIsNone(derived["analysis"][2]["value"])

    def test_finalization_retains_failure_and_retries_derivation_without_rewriting_raw_evidence(self):
        self.capture()
        self.records()
        measurement.write_new(self.output / "verdict.json", {"passed": False, "checks": [{"check": "artifact", "ok": False}]})
        with mock.patch.object(measurement.subprocess, "run", side_effect=subprocess.CalledProcessError(1, "deriver")):
            with self.assertRaises(subprocess.CalledProcessError):
                measurement.finalize(self.directory, self.output, 0)
        original = (self.directory / "raw-measurement.json").read_bytes()
        self.assertEqual(json.loads(original)["trial"]["status"], "failed")
        def derive(command, **_kwargs):
            shutil.copyfile(command[2], command[4])
        with mock.patch.object(measurement.subprocess, "run", side_effect=derive):
            measurement.finalize(self.directory, self.output, 1)
        self.assertEqual((self.directory / "raw-measurement.json").read_bytes(), original)
        self.assertEqual((self.output / "execution-measurement.json").read_bytes(), original)
        measurement.finalize(self.directory, self.output, 1)

    def test_failure_before_input_capture_is_retained_and_finalization_is_idempotent(self):
        measurement.finalize(self.directory, self.output, 42)
        measurement.finalize(self.directory, self.output, 42)
        self.assertEqual(measurement.read_json(self.directory / "harness-outcome.json")["exit"], 42)
        self.assertEqual(measurement.read_json(self.directory / "unavailable.json")["status"], "failed")

    def test_executor_source_is_separate_from_fixture_and_conflicts_are_refused(self):
        commit = "a" * 40
        with mock.patch.dict(os.environ, {"EXPERIMENTS_DISPAT_BINARY_ID": "1.11.0-candidate." + commit, "EXPERIMENTS_DISPAT_SOURCE_REVISION": commit}):
            captured = self.capture()
            self.assertEqual(captured["executorSource"]["revision"], commit)
            self.assertNotEqual(captured["sourceRevision"], commit)
        with mock.patch.dict(os.environ, {"EXPERIMENTS_DISPAT_BINARY_ID": "candidate." + commit, "EXPERIMENTS_DISPAT_SOURCE_REVISION": "b" * 40}):
            with self.assertRaisesRegex(ValueError, "differs"):
                measurement.executor_source("dispat")


if __name__ == "__main__":
    unittest.main()
