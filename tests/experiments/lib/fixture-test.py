#!/usr/bin/env python3
"""Protocol-level checks for the propagation fixture."""
import json
import os
import pathlib
import subprocess
import tempfile
import unittest


FIXTURE = pathlib.Path(__file__).with_name("fixture.py")


class PropagationFixtureTest(unittest.TestCase):
    def build(self, flavour, experiment="propagation", flag="--propagation"):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = pathlib.Path(temporary.name) / flavour
        env = {**os.environ, "EXPERIMENT": experiment}
        subprocess.run(
            ["python3", str(FIXTURE), str(root), flavour, flag],
            check=True, env=env, capture_output=True, text=True,
        )
        return root

    def manifest(self, root, package):
        return json.loads((root / "packages" / package / "package.json").read_text())

    def test_patch_intent_and_compatible_range(self):
        for flavour, subject in (
            ("dispat", "fix(core)^: correct reader"),
            ("lerna", "fix(core): correct reader"),
            ("changesets", "fix(core): correct reader"),
        ):
            with self.subTest(flavour=flavour):
                root = self.build(flavour)
                self.assertEqual(self.manifest(root, "cli")["dependencies"]["core"], "~1.0.0")
                got = subprocess.run(
                    ["git", "log", "-1", "--format=%s"], cwd=root,
                    check=True, capture_output=True, text=True,
                ).stdout.strip()
                self.assertEqual(got, subject)

    def test_changesets_explicitly_selects_direct_consumers(self):
        root = self.build("changesets")
        changeset = (root / ".changeset" / "correct-reader.md").read_text()
        for package in ("core", "cli", "ui", "api"):
            self.assertIn(f'"{package}": patch', changeset)
        for package in ("theme", "docs"):
            self.assertNotIn(f'"{package}": patch', changeset)

    def test_the_fault_is_in_the_consumer_build_script(self):
        """The fault fires from the build script and from nothing else.

        A fault in a publish lifecycle hook would fail the publication and be
        recorded as a build failure, which is what the build and publish
        scenarios exist to tell apart."""
        for flavour in ("dispat", "lerna"):
            with self.subTest(flavour=flavour):
                root = self.build(flavour)
                cli = self.manifest(root, "cli")
                self.assertIn("fault-consumer-build", cli["scripts"]["build"])
                self.assertEqual(["build"], list(cli["scripts"]))
                for package in ("core", "ui", "api", "theme", "docs"):
                    scripts = self.manifest(root, package)["scripts"]
                    self.assertEqual(["build"], list(scripts))
                    self.assertNotIn("fault-consumer-build", scripts["build"])

    def test_the_fault_is_absent_from_the_other_experiments(self):
        root = self.build("dispat", experiment="orphan", flag="--feature")
        self.assertNotIn("fault-consumer-build", self.manifest(root, "cli")["scripts"]["build"])

    def test_dispat_stages_run_the_package_build(self):
        root = self.build("dispat")
        config = (root / "dispat.yaml").read_text()
        self.assertIn("build: npm run --silent build", config)
        # --ignore-scripts, so nothing the build stage owns can fire from a
        # publication instead.
        self.assertIn("publish: npm publish --ignore-scripts", config)
        self.assertIn("flow:\n      build: build\n      publish: publish", config)
        self.assertIn("cli:\n    dependencies: [core]\n    revertOnFail: true\n", config)

    def test_the_provider_holds_its_consumers_builds(self):
        """isBuildWaitingPublish is read from the provider, so it belongs on
        core. On cli it would order nothing, and cli's build would run beside
        core's publication instead of after it."""
        config = (self.build("dispat") / "dispat.yaml").read_text()
        self.assertIn("core:\n    isBuildWaitingPublish: true\n", config)
        self.assertNotIn("cli:\n    dependencies: [core]\n    revertOnFail: true\n    isBuildWaitingPublish", config)

    def test_deferred_fixture_lets_consumer_publish_own_work(self):
        previous = os.environ.get("SCENARIO")
        os.environ["SCENARIO"] = "deferred"
        try:
            root = self.build("dispat", flag="--deferred")
        finally:
            if previous is None:
                os.environ.pop("SCENARIO", None)
            else:
                os.environ["SCENARIO"] = previous
        config = (root / "dispat.yaml").read_text()
        self.assertNotIn("isBuildWaitingPublish: true", config)
        self.assertIn("cli:\n    dependencies: [core]\n    revertOnFail: true\n    flow: {build: []}\n", config)
        self.assertIn("core:\n    revertOnFail: true\n", config)
        subjects = subprocess.run(
            ["git", "log", "-2", "--format=%s"], cwd=root,
            check=True, capture_output=True, text=True,
        ).stdout.splitlines()
        self.assertEqual(subjects, ["fix(core)^: correct reader", "fix(cli): repair command"])

    def test_deferred_build_fixture_keeps_consumer_build(self):
        previous = os.environ.get("SCENARIO")
        os.environ["SCENARIO"] = "deferred-build"
        try:
            root = self.build("dispat", flag="--deferred")
        finally:
            if previous is None:
                os.environ.pop("SCENARIO", None)
            else:
                os.environ["SCENARIO"] = previous
        config = (root / "dispat.yaml").read_text()
        self.assertNotIn("flow: {build: []}", config)
        self.assertIn("build: npm run --silent build", config)

    def test_the_baseline_tags_are_annotated(self):
        """`git describe` ignores a lightweight tag, and lerna reads its
        baseline through describe: a lightly tagged fixture makes it assume
        every package changed."""
        root = self.build("lerna")
        for package in ("core", "cli", "ui", "api", "theme", "docs"):
            kind = subprocess.run(
                ["git", "cat-file", "-t", f"{package}@1.0.0"], cwd=root,
                check=True, capture_output=True, text=True,
            ).stdout.strip()
            self.assertEqual("tag", kind, f"{package}@1.0.0 is not annotated")
        described = subprocess.run(
            ["git", "describe", "--always", "--long", "--first-parent", "--match", "*@*"],
            cwd=root, check=True, capture_output=True, text=True,
        ).stdout.strip()
        self.assertIn("@1.0.0", described)

    def test_the_build_output_is_ignored(self):
        """A build stage that dirtied the worktree would fire a release's own
        safety check on the harness."""
        root = self.build("dispat")
        self.assertIn("dist", (root / ".gitignore").read_text().split())


if __name__ == "__main__":
    unittest.main()
