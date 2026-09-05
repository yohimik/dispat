#!/usr/bin/env python3
"""Verify the scheduling and recovery facts illustrated by Order and Heal."""

from __future__ import annotations

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


def run(argv: list[str], cwd: Path, *, ok: tuple[int, ...] = (0,)) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(argv, cwd=cwd, text=True, capture_output=True, check=False)
    if result.returncode not in ok:
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


def main() -> None:
    requested = os.environ.get("DISPAT_DEMO_BIN")
    binary = Path(requested).expanduser().resolve() if requested else None
    if binary is None:
        found = shutil.which("dispat")
        if found:
            binary = Path(found).resolve()
    if binary is None or not binary.is_file():
        raise SystemExit("set DISPAT_DEMO_BIN to a built dispat binary, or put dispat on PATH")

    with tempfile.TemporaryDirectory(prefix="dispat-demo-release-") as temporary:
        base = Path(temporary)
        repo, remote = base / "work", base / "origin.git"
        repo.mkdir()
        run(["git", "init", "--bare", str(remote)], base)
        git(repo, "init", "-b", "main")
        git(repo, "config", "user.name", "Dispat demo")
        git(repo, "config", "user.email", "demo@example.invalid")

        fixtures = Path(__file__).resolve().parent / "release"
        shutil.copytree(fixtures, repo, dirs_exist_ok=True)
        for name in ("docs", "mobile"):
            (repo / name).mkdir()
        (repo / "docs/file").write_text("unchanged\n")
        (repo / "mobile/file").write_text("unchanged\n")
        (repo / "dispat.yaml").write_text(
            """logLevel: debug
concurrency: [4, 4]
initials: {core: 1.4.2, api: 0.8.2, utils: 2.0.3, sdk: 0.3.1, web: 2.1.0, docs: 1.1.0, mobile: 3.1.0}
scripts:
  build: 'root=$(git rev-parse --show-toplevel); printf "build-start %s\\n" "$DISPAT_PACKAGE" >> "$root/events"; case "$DISPAT_PACKAGE" in api) test -f "$root/.published/core" && test "$DISPAT_UPDATED_CORE_NEW_VERSION" = 1.5.0 && grep -q "go mod edit -require=example.invalid/core@v" Dockerfile && grep -q "example.invalid/core" main.go;; web) test -f "$root/.published/api" && test -f "$root/.built/sdk" && test "$DISPAT_UPDATED_API_NEW_VERSION" = 0.8.3 && grep -q "FROM acme/api" Dockerfile && grep -q "sdk/dist/sdk.js" Dockerfile && test -f webassets/index.html && test -f "$root/sdk/dist/sdk.js";; sdk) grep -q "workspace:\\*" package.json && test -f "$root/utils/package.json" && test -f src/client.js && mkdir -p dist && cp src/client.js dist/sdk.js;; esac; if [ "$DISPAT_PACKAGE" = api ] && grep -q fail check; then exit 1; fi; sleep 0.12; mkdir -p "$root/.built"; touch "$root/.built/$DISPAT_PACKAGE"; printf "build-end %s\\n" "$DISPAT_PACKAGE" >> "$root/events"'
  publish: 'root=$(git rev-parse --show-toplevel); printf "publish-start %s\\n" "$DISPAT_PACKAGE" >> "$root/events"; sleep 0.18; mkdir -p "$root/.published"; touch "$root/.published/$DISPAT_PACKAGE"; printf "publish-end %s\\n" "$DISPAT_PACKAGE" >> "$root/events"'
flow: {build: build, publish: publish}
packages:
  core: {path: core, isBuildWaitingPublish: true}
  api: {path: api, dependencies: [core], isBuildWaitingPublish: true}
  utils: {path: utils}
  sdk: {path: sdk, dependencies: [utils]}
  web: {path: web, dependencies: [api, sdk], isBuildWaitingPublish: true}
  docs: {path: docs}
  mobile: {path: mobile}
commit: {enabled: false}
changelog: {enabled: false}
github: {enabled: false}
"""
        )
        git(repo, "add", ".")
        git(repo, "commit", "-m", "chore: initialize fixture")
        git(repo, "remote", "add", "origin", str(remote))
        git(repo, "push", "-u", "origin", "main")

        (repo / "core/core.go").write_text('package core\n\nconst Version = "1.5.0"\n')
        git(repo, "add", "core")
        git(repo, "commit", "-m", "feat(core)^^: add streaming api")
        (repo / "utils/package.json").write_text('{"name":"utils","version":"2.0.3","fixed":true}\n')
        git(repo, "add", "utils")
        git(repo, "commit", "-m", "fix(utils)^: close file handle leak")

        environment = os.environ.copy()
        environment["DISPAT_UPDATE_CHECK"] = "false"
        first_path = repo / "first.jsonl"
        with first_path.open("w") as output:
            first = subprocess.run(
                [str(binary), "--log-format", "json"], cwd=repo, env=environment,
                text=True, stdout=output, stderr=subprocess.PIPE, check=False,
            )
        if first.returncode != 1:
            raise AssertionError(f"first release exited {first.returncode}, expected 1: {first.stderr}")

        events = (repo / "events").read_text().splitlines()
        for provider in ("core", "utils"):
            assert_before(events, f"build-start {provider}", "build-end core")
            assert_before(events, f"build-start {provider}", "build-end utils")
        assert_before(events, "build-end utils", "build-start sdk")
        assert_before(events, "build-start sdk", "publish-end utils")
        assert_before(events, "publish-end core", "build-start api")
        if "build-start web" in events:
            raise AssertionError(f"web built before api published in the failed run: {events!r}")

        first_summary = summary(reports(first_path))
        expected_first = {"published": 3, "failed": 1, "skipped": 1, "unchanged": 2}
        for key, expected in expected_first.items():
            if first_summary.get(key) != expected:
                raise AssertionError(f"first summary {key}={first_summary.get(key)!r}, expected {expected}")

        (repo / "api/check").write_text("pass\n")
        git(repo, "commit", "-am", "chore(api): repair failing test")
        second_path = repo / "second.jsonl"
        with second_path.open("w") as output:
            second = subprocess.run(
                [str(binary), "--log-format", "json"], cwd=repo, env=environment,
                text=True, stdout=output, stderr=subprocess.PIPE, check=False,
            )
        if second.returncode != 0:
            raise AssertionError(f"second release exited {second.returncode}: {second.stderr}")
        all_events = (repo / "events").read_text().splitlines()
        second_events = all_events[len(events):]
        assert_before(second_events, "publish-end api", "build-start web")
        assert_before(all_events, "build-end sdk", "build-start web")
        second_items = reports(second_path)
        expected_second = {"published": 2, "failed": 0, "skipped": 0, "unchanged": 5}
        second_summary = summary(second_items)
        for key, expected in expected_second.items():
            if second_summary.get(key) != expected:
                raise AssertionError(f"second summary {key}={second_summary.get(key)!r}, expected {expected}")

        catchups = {
            (item.get("package"), item.get("version"))
            for item in second_items
            if item.get("message") == "↻ catch-up"
        }
        if catchups != {("api", "0.8.2 -> 0.8.3"), ("web", "2.1.0 -> 2.1.1")}:
            raise AssertionError(f"unexpected catch-up releases: {catchups!r}")
        tags = set(git(repo, "tag", "--list").stdout.splitlines())
        expected_tags = {"core@1.5.0", "utils@2.0.4", "sdk@0.3.2", "api@0.8.3", "web@2.1.1"}
        if not expected_tags.issubset(tags):
            raise AssertionError(f"missing release tags: {expected_tags - tags}")

        print("verified independent providers, npm local build, two publish-gated Docker consumers, and catch-up recovery")


if __name__ == "__main__":
    main()
