#!/usr/bin/env python3
"""Protocol-level checks for the fixture: what each flavour of each experiment
is told, where each tool runs the build, and where the fault lives.

Every fixture is built in a temporary folder with EXPERIMENT and SCENARIO
passed explicitly, so a test never reads the scenario of whatever shell ran
it."""
import json
import os
import pathlib
import shutil
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

    def test_changesets_splits_the_entry_of_a_consumer_that_sits_out(self):
        """A changeset naming an ignored package beside others is refused, so
        where cli sits the release out its entry is a file of its own. The
        four entries are the ones every other scenario asks for."""
        for scenario, flags in (("deselected", ("--propagation",)), ("held", ("--held", "--propagation"))):
            with self.subTest(scenario=scenario):
                folder = self.build("changesets", scenario=scenario, flags=flags) / ".changeset"
                self.assertEqual((folder / "correct-reader.md").read_text(),
                                 '---\n"core": patch\n"ui": patch\n"api": patch\n---\n\n'
                                 'correct reader and rebuild direct consumers\n')
                self.assertEqual((folder / "rebuild-cli.md").read_text(),
                                 '---\n"cli": patch\n---\n\nrebuild against the corrected reader\n')

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
            for scenario in ("build", "build-distributed"):
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
            ("lerna", "deselected"), ("nx", "held"), ("changesets", "deferred-build"),
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


class CompetitorScenarioTest(FixtureTest):
    """lerna, nx and changesets run every propagation scenario, each told in
    the way it reads."""

    HOLDS = {
        "lerna": lambda root: json.loads((root / "packages" / "cli" / "package.json").read_text())["private"],
        "nx": lambda root: json.loads((root / "nx.json").read_text())["release"]["projects"],
        "changesets": lambda root: json.loads((root / ".changeset" / "config.json").read_text())["ignore"],
    }
    HELD = {"lerna": True, "nx": ["*", "!cli"], "changesets": ["cli"]}

    def test_held_commits_the_tools_own_hold_before_the_fix(self):
        for flavour in ("lerna", "nx", "changesets"):
            with self.subTest(flavour=flavour):
                root = self.build(flavour, scenario="held", flags=("--held", "--propagation"))
                subjects = self.git(root, "log", "--format=%s").splitlines()
                self.assertEqual(subjects, ["fix(core): correct reader", "chore(release): hold cli", "chore: baseline"])
                self.assertEqual(self.HOLDS[flavour](root), self.HELD[flavour])
                self.assertEqual(self.git(root, "rev-parse", "HEAD").strip(),
                                 self.git(root, "rev-parse", "origin/main").strip(), "the hold was not pushed")

    def test_a_held_baseline_is_published_from_its_tag(self):
        """npm skips a private package and exits 0, so a baseline published
        from the held working tree left lerna's cli off the registry. The
        harness publishes a package whose manifest moved since its baseline
        from that tag instead, and the tag must hold the public manifest."""
        root = self.build("lerna", scenario="held", flags=("--held", "--propagation"))
        moved = subprocess.run(["git", "diff", "--quiet", "cli@1.0.0", "--", "packages/cli/package.json"], cwd=root)
        self.assertEqual(1, moved.returncode, "the hold did not move cli's manifest")
        tagged = json.loads(self.git(root, "show", "cli@1.0.0:packages/cli/package.json"))
        self.assertNotIn("private", tagged)
        self.assertEqual("1.0.0", tagged["version"])
        for package in PACKAGES:
            if package == "cli":
                continue
            unmoved = subprocess.run(["git", "diff", "--quiet", f"{package}@1.0.0", "--",
                                      f"packages/{package}/package.json"], cwd=root)
            self.assertEqual(0, unmoved.returncode, f"{package}'s manifest moved")

    def test_the_hold_is_absent_from_every_other_scenario(self):
        root = self.build("lerna", scenario="deselected")
        self.assertNotIn("private", self.manifest(root, "cli"))
        nx = json.loads((self.build("nx", scenario="deselected") / "nx.json").read_text())
        self.assertEqual(nx["release"]["projects"], ["*"])
        config = json.loads((self.build("changesets", scenario="deselected") / ".changeset" / "config.json").read_text())
        self.assertEqual(config["ignore"], [])

    def test_deferred_commits_the_consumers_fix_then_the_providers(self):
        for flavour in ("lerna", "nx", "changesets"):
            for scenario in ("deferred", "deferred-build"):
                with self.subTest(flavour=flavour, scenario=scenario):
                    root = self.build(flavour, scenario=scenario, flags=("--deferred",))
                    subjects = self.git(root, "log", "-2", "--format=%s").splitlines()
                    self.assertEqual(subjects, ["fix(core): correct reader", "fix(cli): repair command"])
                    self.assertEqual(self.manifest(root, "cli")["dependencies"]["core"], "~1.0.0")

    def test_deferred_changesets_ask_for_each_fix(self):
        folder = self.build("changesets", scenario="deferred", flags=("--deferred",)) / ".changeset"
        self.assertEqual((folder / "repair-command.md").read_text(), '---\n"cli": patch\n---\n\nrepair command\n')
        self.assertEqual((folder / "correct-reader.md").read_text(),
                         '---\n"core": patch\n"cli": patch\n"ui": patch\n"api": patch\n---\n\n'
                         'correct reader and rebuild direct consumers\n')

    def test_deferred_consumer_has_no_build_and_deferred_build_keeps_it(self):
        """dispat's deferred cli opts out of its build stage; a tool that runs
        every build script it finds is given a cli without one."""
        for flavour in ("lerna", "nx", "changesets"):
            with self.subTest(flavour=flavour):
                deferred = self.build(flavour, scenario="deferred", flags=("--deferred",))
                self.assertEqual({}, self.manifest(deferred, "cli")["scripts"])
                self.assertIn("build", self.manifest(deferred, "core")["scripts"])
                kept = self.build(flavour, scenario="deferred-build", flags=("--deferred",))
                self.assertIn("build", self.manifest(kept, "cli")["scripts"])
        self.assertEqual(["build"], list(self.manifest(
            self.build("dispat", scenario="deferred", flags=("--deferred",)), "cli")["scripts"]))


class TimingTest(FixtureTest):
    """The timing experiment's two shapes, and a build that records itself
    where each tool runs it."""

    def build_timing(self, flavour, wide=False):
        flags = ("--feature", "--wide") if wide else ("--feature",)
        return self.build(flavour, experiment="timing", scenario="wide" if wide else "six", flags=flags)

    def packages(self, root):
        return sorted(path.name for path in (root / "packages").iterdir())

    def test_the_wide_shape(self):
        root = self.build_timing("dispat", wide=True)
        names = self.packages(root)
        self.assertGreaterEqual(len(names), 40)
        self.assertEqual(names, sorted(subprocess.run(
            ["python3", str(FIXTURE), "--packages", "wide"], check=True, capture_output=True, text=True).stdout.split()))
        self.assertEqual(self.manifest(root, "chain1")["dependencies"], {"core": "~1.0.0"})
        self.assertEqual(self.manifest(root, "chain4")["dependencies"], {"chain3": "~1.0.0"})
        leaves = [name for name in names if name.startswith("leaf")]
        self.assertGreaterEqual(len(leaves), 30)
        for leaf in leaves:
            self.assertEqual(self.manifest(root, leaf)["dependencies"], {"core": "~1.0.0"})
        self.assertEqual(self.manifest(root, "core")["dependencies"], {})

    def test_the_wide_shape_is_deterministic_and_baselined(self):
        first, second = self.build_timing("lerna", wide=True), self.build_timing("lerna", wide=True)
        self.assertEqual(self.git(first, "rev-parse", "HEAD"), self.git(second, "rev-parse", "HEAD"))
        tags = self.git(first, "tag", "-l").split()
        self.assertEqual(sorted(tags), sorted(f"{name}@1.0.0" for name in self.packages(first)))
        for tag in tags:
            self.assertEqual("tag", self.git(first, "cat-file", "-t", tag).strip(), f"{tag} is not annotated")
        self.assertEqual(self.git(first, "rev-parse", "HEAD").strip(),
                         self.git(first, "rev-parse", "origin/main").strip(), "the feature was not pushed")

    def test_the_wide_feature_reaches_every_dependent_under_dispat(self):
        subjects = {flavour: self.git(self.build_timing(flavour, wide=True), "log", "-1", "--format=%s").strip()
                    for flavour in ("dispat", "nx")}
        self.assertEqual(subjects, {"dispat": "feat(core)^^: streaming reader", "nx": "feat(core): streaming reader"})
        six = self.git(self.build_timing("dispat"), "log", "-1", "--format=%s").strip()
        self.assertEqual(six, "feat(core)^: streaming reader")

    def test_the_package_list_must_match_the_shape(self):
        with self.assertRaises(subprocess.CalledProcessError):
            self.build("dispat", experiment="timing", scenario="wide", flags=("--feature", "--wide"),
                       env={"EXPERIMENT_PACKAGES": "core cli ui api theme docs"})

    def test_the_build_records_itself(self):
        for flavour in ("dispat", "lerna", "nx", "changesets"):
            with self.subTest(flavour=flavour):
                build = self.manifest(self.build_timing(flavour), "core")["scripts"]["build"]
                self.assertIn("EXPERIMENT_TIMING", build)
                self.assertIn('task:"build"', build)
                self.assertIn('dist/index.js', build)

    @unittest.skipUnless(shutil.which("node"), "the build is a node script")
    def test_the_build_writes_one_line_with_its_interval(self):
        root = self.build_timing("dispat")
        script = self.manifest(root, "cli")["scripts"]["build"]
        timing = root / "timing.jsonl"
        subprocess.run(["sh", "-c", script], cwd=root / "packages" / "cli", check=True,
                       env={**os.environ, "EXPERIMENT_TIMING": str(timing)})
        [line] = timing.read_text().splitlines()
        record = json.loads(line)
        self.assertEqual((record["package"], record["task"]), ("cli", "build"))
        self.assertGreaterEqual(record["end"] - record["start"], 0.5)
        self.assertTrue((root / "packages" / "cli" / "dist" / "index.js").exists())

    def test_each_tool_builds_where_it_documents_a_build(self):
        for flavour in ("lerna", "changesets"):
            with self.subTest(flavour=flavour):
                self.assertEqual(self.manifest(self.build_timing(flavour), "ui")["scripts"]["prepack"],
                                 "npm run --silent build")
        version = json.loads((self.build_timing("nx") / "nx.json").read_text())["release"]["version"]
        self.assertEqual(version["preVersionCommand"], "npx nx run-many -t build")
        config = (self.build_timing("dispat") / "dispat.yaml").read_text()
        self.assertIn("build: npm run --silent build", config)
        self.assertNotIn("isBuildWaitingPublish", config)
        self.assertNotIn("revertOnFail", config)


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
