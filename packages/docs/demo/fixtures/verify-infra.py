#!/usr/bin/env python3
"""Verify the scheduling and recorded-progress facts illustrated by Infra."""

from __future__ import annotations

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


def run(argv: list[str], cwd: Path) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(argv, cwd=cwd, text=True, capture_output=True, check=False)
    if result.returncode != 0:
        raise AssertionError(
            f"{argv!r} exited {result.returncode}\nstdout:\n{result.stdout}\nstderr:\n{result.stderr}"
        )
    return result


def git(repo: Path, *args: str) -> subprocess.CompletedProcess[str]:
    return run(["git", *args], repo)


def reports(path: Path) -> list[dict[str, object]]:
    return [json.loads(line) for line in path.read_text().splitlines() if line.strip()]


def summary(items: list[dict[str, object]]) -> dict[str, object]:
    matches = [item for item in items if item.get("message") == "done"]
    if len(matches) != 1:
        raise AssertionError(f"expected one summary, got {matches!r}")
    return matches[0]


def assert_before(events: list[str], earlier: str, later: str) -> None:
    if events.index(earlier) >= events.index(later):
        raise AssertionError(f"expected {earlier!r} before {later!r}: {events!r}")


def invoke(binary: Path, repo: Path, destination: Path, environment: dict[str, str]) -> None:
    with destination.open("w") as output:
        result = subprocess.run(
            [str(binary), "--log-format", "json"], cwd=repo, env=environment,
            text=True, stdout=output, stderr=subprocess.PIPE, check=False,
        )
    if result.returncode != 0:
        raise AssertionError(f"release exited {result.returncode}: {result.stderr}")


def main() -> None:
    requested = os.environ.get("DISPAT_DEMO_BIN")
    binary = Path(requested).expanduser().resolve() if requested else None
    if binary is None:
        found = shutil.which("dispat")
        if found:
            binary = Path(found).resolve()
    if binary is None or not binary.is_file():
        raise SystemExit("set DISPAT_DEMO_BIN to a built dispat binary, or put dispat on PATH")

    fixture_root = Path(__file__).resolve().parent / "infra"
    expected = json.loads((fixture_root / "expected.json").read_text())

    with tempfile.TemporaryDirectory(prefix="dispat-demo-infra-") as temporary:
        base = Path(temporary)
        repo, remote = base / "work", base / "origin.git"
        repo.mkdir()
        run(["git", "init", "--bare", str(remote)], base)
        git(repo, "init", "-b", "main")
        git(repo, "config", "user.name", "Dispat demo")
        git(repo, "config", "user.email", "demo@example.invalid")
        shutil.copytree(fixture_root, repo, dirs_exist_ok=True)
        (repo / "dispat.yaml").write_text(
            """logLevel: debug
concurrency: [3, 3]
initials: {infra: 1.2.0, backend: 0.8.2, frontend: 2.1.0}
scripts:
  build: 'set -eu; root=$(git rev-parse --show-toplevel); case "$DISPAT_PACKAGE" in infra) printf "terraform-state-rebuild-start\\n" >> "$root/events"; printf "imported known cloud resources\\n" > terraform.tfstate; printf "terraform-state-rebuild-end\\nterraform-plan-start\\n" >> "$root/events"; printf "planned application database\\n" > tfplan; sleep 0.08; printf "terraform-plan-end\\n" >> "$root/events";; *) attempts=0; while test ! -f "$root/terraform-apply-start" && test "$attempts" -lt 500; do attempts=$((attempts + 1)); sleep 0.01; done; test -f "$root/terraform-apply-start"; printf "build-start %s\\n" "$DISPAT_PACKAGE" >> "$root/events"; touch "$root/build-start-$DISPAT_PACKAGE"; printf "build-end %s\\n" "$DISPAT_PACKAGE" >> "$root/events";; esac'
  publish: 'set -eu; root=$(git rev-parse --show-toplevel); case "$DISPAT_PACKAGE" in infra) test -f tfplan; printf "terraform-apply-start\\n" >> "$root/events"; touch "$root/terraform-apply-start"; attempts=0; while { test ! -f "$root/build-start-backend" || test ! -f "$root/build-start-frontend"; } && test "$attempts" -lt 500; do attempts=$((attempts + 1)); sleep 0.01; done; test -f "$root/build-start-backend"; test -f "$root/build-start-frontend"; printf "terraform-owned-state\\n" > terraform.tfstate; printf "terraform-apply-end\\n" >> "$root/events";; *) printf "publish-start %s\\n" "$DISPAT_PACKAGE" >> "$root/events"; mkdir -p "$root/.published"; touch "$root/.published/$DISPAT_PACKAGE"; printf "publish-end %s\\n" "$DISPAT_PACKAGE" >> "$root/events";; esac'
flow: {build: build, publish: publish}
packages:
  infra: {path: infra, tagFormat: 'infra/v{version}'}
  backend: {path: backend, dependencies: [infra]}
  frontend: {path: frontend, dependencies: [infra]}
commit: {enabled: false}
changelog: {enabled: false}
github: {enabled: false}
"""
        )
        git(repo, "add", ".")
        git(repo, "commit", "-m", "chore: initialize infrastructure fixture")
        git(repo, "remote", "add", "origin", str(remote))
        git(repo, "push", "-u", "origin", "main")

        with (repo / "infra/main.tf").open("a") as terraform:
            terraform.write('\n# database sizing selected for the application\n')
        git(repo, "add", "infra/main.tf")
        git(repo, "commit", "-m", expected["commit"])

        environment = os.environ.copy()
        environment["DISPAT_UPDATE_CHECK"] = "false"
        first_path = repo / "first.jsonl"
        invoke(binary, repo, first_path, environment)

        events = (repo / "events").read_text().splitlines()
        assert_before(events, "terraform-state-rebuild-start", "terraform-state-rebuild-end")
        assert_before(events, "terraform-state-rebuild-end", "terraform-plan-start")
        assert_before(events, "terraform-plan-start", "terraform-plan-end")
        assert_before(events, "terraform-plan-end", "terraform-apply-start")
        assert_before(events, "terraform-apply-start", "build-start backend")
        assert_before(events, "terraform-apply-start", "build-start frontend")
        assert_before(events, "build-start backend", "terraform-apply-end")
        assert_before(events, "build-start frontend", "terraform-apply-end")
        assert_before(events, "terraform-apply-end", "publish-start backend")
        assert_before(events, "terraform-apply-end", "publish-start frontend")
        if (repo / ".dispat").exists():
            raise AssertionError("fixture unexpectedly created a Dispat progress directory")

        first_items = reports(first_path)
        first_summary = summary(first_items)
        for key, value in expected["summary"].items():
            if first_summary.get(key) != value:
                raise AssertionError(f"first summary {key}={first_summary.get(key)!r}, expected {value}")
        plan_keys = ("package", "version", "dependsOn", "dueToProviders", "reason")
        actual_plan = [
            {key: item[key] for key in plan_keys if key in item}
            for item in first_items
            if item.get("message") == "● changed"
        ]
        if sorted(actual_plan, key=lambda item: item["package"]) != sorted(expected["plan"], key=lambda item: item["package"]):
            raise AssertionError(f"unexpected plan records: {actual_plan!r}")
        publish_keys = ("package", "version", "tag")
        actual_published = [
            {key: item[key] for key in publish_keys}
            for item in first_items
            if item.get("message") == "published"
        ]
        if sorted(actual_published, key=lambda item: item["package"]) != expected["published"]:
            raise AssertionError(f"unexpected publish records: {actual_published!r}")
        expected_tags = {item["tag"] for item in expected["published"]}
        tags = set(git(repo, "tag", "--list").stdout.splitlines())
        if not expected_tags.issubset(tags):
            raise AssertionError(f"missing release tags: {expected_tags - tags}")

        before_rerun = list(events)
        second_path = repo / "second.jsonl"
        invoke(binary, repo, second_path, environment)
        after_rerun = (repo / "events").read_text().splitlines()
        if after_rerun != before_rerun:
            raise AssertionError(f"unchanged rerun executed release stages: {after_rerun[len(before_rerun):]!r}")
        second_summary = summary(reports(second_path))
        for key, value in {"published": 0, "failed": 0, "skipped": 0, "unchanged": 3}.items():
            if second_summary.get(key) != value:
                raise AssertionError(f"rerun summary {key}={second_summary.get(key)!r}, expected {value}")
        if set(git(repo, "tag", "--list").stdout.splitlines()) != tags:
            raise AssertionError("unchanged rerun changed the durable release tags")

        print("verified builds overlap Terraform apply, deploys wait for apply, versions, tags, and empty rerun")


if __name__ == "__main__":
    main()
