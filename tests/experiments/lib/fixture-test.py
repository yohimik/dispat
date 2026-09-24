#!/usr/bin/env python3
"""Protocol-level checks for the fixture: what each flavour of each experiment
is told, where each tool runs the build, and where the fault lives.

Every fixture is built in a temporary folder with EXPERIMENT and SCENARIO
passed explicitly, so a test never reads the scenario of whatever shell ran
it."""
import json
import os
import pathlib
import subprocess
import tempfile
import unittest


FIXTURE = pathlib.Path(__file__).with_name("fixture.py")
PACKAGES = ("core", "cli", "ui", "api", "theme", "docs")


class FixtureTest(unittest.TestCase):
    def build(self, flavour, experiment="propagation", scenario="build", flags=("--propagation",), env=None):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = pathlib.Path(temporary.name) / flavour
        environment = {k: v for k, v in os.environ.items() if k not in ("EXPERIMENT", "SCENARIO")}
        environment["EXPERIMENT"] = experiment
        if scenario is not None:
            environment["SCENARIO"] = scenario
        environment.update(env or {})
        subprocess.run(
            ["python3", str(FIXTURE), str(root), flavour, *flags],
            check=True, env=environment, capture_output=True, text=True,
        )
        return root

    def manifest(self, root, package):
        return json.loads((root / "packages" / package / "package.json").read_text())

    def git(self, root, *args):
        return subprocess.run(["git", *args], cwd=root, check=True, capture_output=True, text=True).stdout


class PropagationIntentTest(FixtureTest):
    def test_patch_intent_and_compatible_range(self):
        for flavour, subject in (
            ("dispat", "fix(core)^: correct reader"),
            ("lerna", "fix(core): correct reader"),
            ("nx", "fix(core): correct reader"),
            ("changesets", "fix(core): correct reader"),
        ):
            with self.subTest(flavour=flavour):
                root = self.build(flavour)
                self.assertEqual(self.manifest(root, "cli")["dependencies"]["core"], "~1.0.0")
                self.assertEqual(self.git(root, "log", "-1", "--format=%s").strip(), subject)

    def test_changesets_explicitly_selects_direct_consumers(self):
        root = self.build("changesets")
        changeset = (root / ".changeset" / "correct-reader.md").read_text()
        for package in ("core", "cli", "ui", "api"):
            self.assertIn(f'"{package}": patch', changeset)
        for package in ("theme", "docs"):
            self.assertNotIn(f'"{package}": patch', changeset)


class BuildPlacementTest(FixtureTest):
    """Each tool runs the same build script where it documents a build, and
    only in the propagation experiment."""

    def test_lerna_and_changesets_build_through_prepack(self):
        for flavour in ("lerna", "changesets"):
            for scenario in ("build", "publish"):
                with self.subTest(flavour=flavour, scenario=scenario):
                    root = self.build(flavour, scenario=scenario)
                    for package in PACKAGES:
                        scripts = self.manifest(root, package)["scripts"]
                        self.assertEqual(scripts["prepack"], "npm run --silent build")

    def test_nx_builds_before_it_versions(self):
        root = self.build("nx")
        version = json.loads((root / "nx.json").read_text())["release"]["version"]
        self.assertEqual(version["preVersionCommand"], "npx nx run-many -t build")
        for package in PACKAGES:
            self.assertNotIn("prepack", self.manifest(root, package)["scripts"])

    def test_nx_ignores_its_own_workspace_data(self):
        for experiment, flags in (("propagation", ("--propagation",)), ("orphan", ("--feature",))):
            with self.subTest(experiment=experiment):
                root = self.build("nx", experiment=experiment, scenario=None, flags=flags)
                self.assertIn(".nx", (root / ".gitignore").read_text().split())

    def test_the_other_experiments_run_no_build(self):
        for flavour in ("lerna", "nx", "changesets"):
            with self.subTest(flavour=flavour):
                root = self.build(flavour, experiment="orphan", scenario=None, flags=("--feature",))
                for package in PACKAGES:
                    self.assertEqual(["build"], list(self.manifest(root, package)["scripts"]))
                if flavour == "nx":
                    version = json.loads((root / "nx.json").read_text())["release"]["version"]
                    self.assertNotIn("preVersionCommand", version)

    def test_dispat_stages_run_the_package_build(self):
        root = self.build("dispat")
        config = (root / "dispat.yaml").read_text()
        self.assertIn("build: npm run --silent build", config)
        # --ignore-scripts, so nothing the build stage owns can fire from a
        # publication instead.
        self.assertIn("publish: npm publish --ignore-scripts", config)
        self.assertIn("flow:\n      build: build\n      publish: publish", config)
        self.assertIn("cli:\n    dependencies: [core]\n    revertOnFail: true\n", config)
        for package in PACKAGES:
            self.assertEqual(["build"], list(self.manifest(root, package)["scripts"]))

    def test_the_provider_holds_its_consumers_builds(self):
        """isBuildWaitingPublish is read from the provider, so it belongs on
        core. On cli it would order nothing, and cli's build would run beside
        core's publication instead of after it."""
        for scenario in ("build", "publish", "build-distributed", "deselected"):
            with self.subTest(scenario=scenario):
                config = (self.build("dispat", scenario=scenario) / "dispat.yaml").read_text()
                self.assertIn("core:\n    isBuildWaitingPublish: true\n", config)
                self.assertNotIn("cli:\n    dependencies: [core]\n    revertOnFail: true\n    isBuildWaitingPublish", config)


class FaultTest(FixtureTest):
    """The fault fires from the consumer's build script and from nothing else,
    and only in the scenarios whose fault is a build."""

    def test_the_fault_is_in_the_consumer_build_script(self):
        for flavour in ("dispat", "lerna", "nx", "changesets"):
            for scenario in ("build", "build-distributed") if flavour == "dispat" else ("build",):
                with self.subTest(flavour=flavour, scenario=scenario):
                    root = self.build(flavour, scenario=scenario)
                    self.assertIn("fault-consumer-build", self.manifest(root, "cli")["scripts"]["build"])
                    for package in ("core", "ui", "api", "theme", "docs"):
                        scripts = self.manifest(root, package)["scripts"]
                        self.assertNotIn("fault-consumer-build", scripts["build"])
                        self.assertNotIn("fault-consumer-build", scripts.get("prepack", ""))

    def test_the_fault_is_absent_from_every_other_scenario(self):
        for flavour, scenario in (
            ("dispat", "publish"), ("lerna", "publish"), ("nx", "publish"), ("changesets", "publish"),
            ("dispat", "deselected"), ("dispat", "held"),
        ):
            with self.subTest(flavour=flavour, scenario=scenario):
                root = self.build(flavour, scenario=scenario)
                self.assertNotIn("fault-consumer-build", self.manifest(root, "cli")["scripts"]["build"])

    def test_the_fault_is_absent_from_the_other_experiments(self):
        root = self.build("dispat", experiment="orphan", scenario=None, flags=("--feature",))
        self.assertNotIn("fault-consumer-build", self.manifest(root, "cli")["scripts"]["build"])

    def test_the_fault_leaves_a_trace(self):
        """The harness refuses a run whose fault never fired, and the trace
        is how it knows a build fault did. The fault's clause runs here with
        its two absolute paths moved into a temporary folder; it exits before
        the build itself, which needs node."""
        script = self.manifest(self.build("dispat"), "cli")["scripts"]["build"]
        clause = script.split("; node ", 1)[0]
        with tempfile.TemporaryDirectory() as folder:
            sentinel, trace = os.path.join(folder, "armed"), os.path.join(folder, "fired")
            clause = clause.replace("/fault-consumer-build", sentinel).replace("/fault-fired", trace)
            unarmed = subprocess.run(["sh", "-c", clause], capture_output=True, text=True)
            self.assertEqual(unarmed.returncode, 0)
            self.assertFalse(os.path.exists(trace), "an unarmed fault left a trace")
            pathlib.Path(sentinel).touch()
            armed = subprocess.run(["sh", "-c", clause], capture_output=True, text=True)
            self.assertEqual(armed.returncode, 42)
            self.assertIn("injected cli build failure", armed.stderr)
            self.assertTrue(os.path.exists(trace), "the fault fired without a trace")


class DispatScenarioTest(FixtureTest):
    def test_held_commits_the_hold_before_the_fix(self):
        root = self.build("dispat", scenario="held", flags=("--held", "--propagation"))
        subjects = self.git(root, "log", "--format=%s").splitlines()
        self.assertEqual(subjects, ["fix(core)^: correct reader", "release(cli): hold", "chore: baseline"])
        footer = self.git(root, "log", "-1", "--skip=1", "--format=%(trailers:key=Release-As,valueonly)").strip()
        self.assertEqual(footer, "none")
        self.assertEqual(self.git(root, "rev-parse", "HEAD").strip(),
                         self.git(root, "rev-parse", "origin/main").strip(), "the hold was not pushed")

    def test_build_distributed_names_the_secret_and_never_holds_it(self):
        secret = "0123456789abcdef-not-for-the-file"
        root = self.build("dispat", scenario="build-distributed",
                          env={"EXPERIMENT_EXECUTION_SECRET": secret})
        config = (root / "dispat.yaml").read_text()
        self.assertIn("execution:\n  secretEnv: EXPERIMENT_EXECUTION_SECRET\n  timeouts:\n    preflight: 60\n", config)
        self.assertIn("runOnly: [worker, orchestrator]\n", config)
        self.assertNotIn(secret, config)
        # The link is named on the command line, so the file lists no worker.
        self.assertNotIn("workers:", config)

    def test_only_the_distributed_scenario_configures_execution(self):
        for scenario in ("build", "publish", "deselected", "held", "deferred", "deferred-build"):
            with self.subTest(scenario=scenario):
                flags = ("--deferred",) if scenario.startswith("deferred") else ("--propagation",)
                config = (self.build("dispat", scenario=scenario, flags=flags) / "dispat.yaml").read_text()
                self.assertNotIn("execution:", config)
                self.assertNotIn("runOnly:", config)

    def test_deferred_fixture_lets_consumer_publish_own_work(self):
        root = self.build("dispat", scenario="deferred", flags=("--deferred",))
        config = (root / "dispat.yaml").read_text()
        self.assertNotIn("isBuildWaitingPublish: true", config)
        self.assertIn("cli:\n    dependencies: [core]\n    revertOnFail: true\n    flow: {build: []}\n", config)
        self.assertIn("core:\n    revertOnFail: true\n", config)
        subjects = self.git(root, "log", "-2", "--format=%s").splitlines()
        self.assertEqual(subjects, ["fix(core)^: correct reader", "fix(cli): repair command"])

    def test_deferred_build_fixture_keeps_consumer_build(self):
        root = self.build("dispat", scenario="deferred-build", flags=("--deferred",))
        config = (root / "dispat.yaml").read_text()
        self.assertNotIn("flow: {build: []}", config)
        self.assertIn("build: npm run --silent build", config)


class BaselineTest(FixtureTest):
    def test_the_baseline_tags_are_annotated(self):
        """`git describe` ignores a lightweight tag, and lerna reads its
        baseline through describe: a lightly tagged fixture makes it assume
        every package changed."""
        root = self.build("lerna")
        for package in PACKAGES:
            kind = self.git(root, "cat-file", "-t", f"{package}@1.0.0").strip()
            self.assertEqual("tag", kind, f"{package}@1.0.0 is not annotated")
        described = self.git(root, "describe", "--always", "--long", "--first-parent", "--match", "*@*").strip()
        self.assertIn("@1.0.0", described)

    def test_the_build_output_is_ignored(self):
        """A build stage that dirtied the worktree would fire a release's own
        safety check on the harness."""
        root = self.build("dispat")
        self.assertIn("dist", (root / ".gitignore").read_text().split())


if __name__ == "__main__":
    unittest.main()
