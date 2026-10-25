#!/usr/bin/env python3
"""Each tool's plan, read from the output its plan command printed in a
recorded cell. The Plans table compares these readings, so a reading that
missed a package would show as a difference between two tools that agree.
"""
import importlib.util
import json
import pathlib
import unittest

HERE = pathlib.Path(__file__).parent
spec = importlib.util.spec_from_file_location("plan", HERE / "plan.py")
plan = importlib.util.module_from_spec(spec)
spec.loader.exec_module(plan)


def lines(text):
    return text.splitlines(keepends=True)


class PlanTest(unittest.TestCase):
    def test_dispat(self):
        output = "\n".join(json.dumps(entry) for entry in (
            {"package": "core", "bump": "minor", "version": "1.0.0 -> 1.1.0", "message": "● changed"},
            {"package": "cli", "bump": "patch", "version": "1.0.0 -> 1.0.1", "message": "● changed"},
            {"package": "docs", "version": "1.0.0", "message": "unchanged"},
            {"packages": 6, "releasing": 2, "message": "release plan ready"},
        )) + "\nnot a JSON line\n"
        self.assertEqual(plan.read_plan("dispat", lines(output)), {"core": "1.1.0", "cli": "1.0.1"})

    def test_lerna(self):
        output = ("lerna notice cli v10.0.1\nlerna info current version independent\n\nChanges:\n"
                  " - api: 1.0.0 => 1.0.1\n - core: 1.0.0 => 1.1.0\n - leaf01: 1.0.0 => 1.0.1\n\n"
                  "lerna info auto-confirmed\n")
        self.assertEqual(plan.read_plan("lerna", lines(output)), {"api": "1.0.1", "core": "1.1.0", "leaf01": "1.0.1"})

    def test_nx(self):
        output = (" NX   Running release version for project: core\n\n"
                  "core 🏷️  Resolved the current version as 1.0.0 from git tag \"core@1.0.0\"\n"
                  "core ❓ Applied semver relative bump \"minor\", to get new version 1.1.0\n"
                  "\x1b[36mcore\x1b[39m ✍️  New version 1.1.0 written to manifest: packages/core/package.json\n"
                  "chain1 ✍️  New version 1.0.1 written to manifest: packages/chain1/package.json\n"
                  "chain1 ✍️  New version 1.0.1 written to manifest: packages/chain1/package.json\n")
        self.assertEqual(plan.read_plan("nx", lines(output)), {"core": "1.1.0", "chain1": "1.0.1"})

    def test_changesets(self):
        output = ("🦋 changeset v3.0.1\n\nPackages to be bumped:\n- minor\n  - core -> 1.1.0\n"
                  "    - .changeset/streaming-reader.md\n- patch\n  - \x1b[36mapi\x1b[39m -> \x1b[32m1.0.1\x1b[39m\n")
        self.assertEqual(plan.read_plan("changesets", lines(output)), {"core": "1.1.0", "api": "1.0.1"})

    def test_an_empty_plan(self):
        self.assertEqual(plan.read_plan("changesets", lines("🦋 changeset v3.0.1\n\nNo packages to be bumped\n")), {})

    def test_an_unknown_tool(self):
        with self.assertRaises(SystemExit):
            plan.read_plan("rush", [])


if __name__ == "__main__":
    unittest.main()
