#!/usr/bin/env python3
"""Exercise the exact commit directives and versions shown by Control.tsx."""

from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import tempfile


def run(argv: list[str], cwd: Path, env: dict[str, str], ok: tuple[int, ...] = (0,)) -> subprocess.CompletedProcess[str]:
    result = subprocess.run(argv, cwd=cwd, env=env, text=True, capture_output=True, check=False)
    if result.returncode not in ok:
        raise AssertionError(f"{argv!r} exited {result.returncode}\nstdout:\n{result.stdout}\nstderr:\n{result.stderr}")
    return result


def events(result: subprocess.CompletedProcess[str]) -> list[dict[str, object]]:
    return [json.loads(line) for line in (result.stdout + result.stderr).splitlines() if line.strip().startswith("{")]


def plan_version(items: list[dict[str, object]], message: str) -> str:
    matches = [item for item in items if item.get("message") == message and item.get("package") == "core"]
    if len(matches) != 1:
        raise AssertionError(f"expected one {message!r} plan line for core, got {matches!r}; events={items!r}")
    value = matches[0].get("version")
    if not isinstance(value, str):
        raise AssertionError(f"plan line has no version: {matches[0]!r}")
    return value


def main() -> None:
    requested = os.environ.get("DISPAT_DEMO_BIN")
    if not requested:
        raise SystemExit("set DISPAT_DEMO_BIN to the release-ready dispat binary")
    binary = Path(requested).expanduser().resolve()
    if not binary.is_file():
        raise SystemExit(f"DISPAT_DEMO_BIN is not a file: {binary}")
    env = os.environ.copy()
    env["DISPAT_UPDATE_CHECK"] = "false"

    with tempfile.TemporaryDirectory(prefix="dispat-demo-control-") as temporary:
        root = Path(temporary)
        repo, remote = root / "work", root / "origin.git"
        repo.mkdir()
        run(["git", "init", "--bare", str(remote)], root, env)
        run(["git", "init", "-b", "main"], repo, env)
        run(["git", "config", "user.name", "Dispat demo"], repo, env)
        run(["git", "config", "user.email", "demo@example.invalid"], repo, env)
        (repo / "core").mkdir()
        (repo / "core/package.json").write_text('{"name":"core","version":"1.4.2"}\n')
        (repo / "dispat.yaml").write_text(
            """initials: {core: 1.4.2}
scripts:
  build: 'printf "build %s\\n" "$DISPAT_NEW_VERSION" >> ../published'
  publish: 'printf "publish %s\\n" "$DISPAT_NEW_VERSION" >> ../published'
flow: {build: build, publish: publish}
packages: {core: {path: core}}
commit: {enabled: false}
changelog: {enabled: false}
github: {enabled: false}
"""
        )
        run(["git", "add", "."], repo, env)
        run(["git", "commit", "-m", "chore: initialize fixture"], repo, env)
        run(["git", "remote", "add", "origin", str(remote)], repo, env)
        run(["git", "push", "-u", "origin", "main"], repo, env)

        beats = [
            (["feat(core): add streaming api"], "● changed", "1.4.2 -> 1.5.0", "core@1.5.0"),
            (["feat(core)%beta: try it out"], "● changed", "1.5.0 -> 1.6.0-beta.0", "core@1.6.0-beta.0"),
            (["fix(core)%beta: tighten retries"], "● changed", "1.6.0-beta.0 -> 1.6.0-beta.1", "core@1.6.0-beta.1"),
            (["feat(core)%beta!: drop the v1 wire format"], "● changed", "1.6.0-beta.1 -> 2.0.0-beta.0", "core@2.0.0-beta.0"),
            (["release(core)%beta>stable: graduate"], "● changed", "2.0.0-beta.0 -> 2.0.0", "core@2.0.0"),
            (["feat(core): queue dashboard", "Release-As: none"], "‖ held (Release-As: none)", "2.0.0 -> 2.1.0", None),
            (["release(core): resume", "Release-As: auto"], "● changed", "2.0.0 -> 2.1.0", "core@2.1.0"),
        ]
        expected_tags: set[str] = set()
        for index, (messages, plan_message, expected_version, expected_tag) in enumerate(beats):
            marker = repo / "core" / "change.txt"
            marker.write_text(f"beat {index}\n")
            run(["git", "add", "core/change.txt"], repo, env)
            commit = ["git", "commit"]
            for message in messages:
                commit.extend(["-m", message])
            run(commit, repo, env)

            status = run([str(binary), "--log-format", "json", "status"], repo, env)
            actual_version = plan_version(events(status), plan_message)
            if actual_version != expected_version:
                raise AssertionError(f"beat {index}: planned {actual_version!r}, expected {expected_version!r}")

            before = set(run(["git", "tag", "--list"], repo, env).stdout.splitlines())
            if expected_tag is None:
                published = repo / "published"
                before_publish = published.read_text() if published.exists() else ""
                run([str(binary), "--log-format", "json"], repo, env)
                if set(run(["git", "tag", "--list"], repo, env).stdout.splitlines()) != before:
                    raise AssertionError("held beat created a tag")
                if (published.read_text() if published.exists() else "") != before_publish:
                    raise AssertionError("held beat ran a publish script")
                continue

            release = run([str(binary), "--log-format", "json"], repo, env)
            released = [item for item in events(release) if item.get("message") == "published" and item.get("package") == "core"]
            if len(released) != 1 or released[0].get("tag") != expected_tag:
                raise AssertionError(f"beat {index}: unexpected publish event {released!r}")
            after = set(run(["git", "tag", "--list"], repo, env).stdout.splitlines())
            if after - before != {expected_tag}:
                raise AssertionError(f"beat {index}: expected only tag {expected_tag}, got {after - before}")
            expected_tags.add(expected_tag)

        actual_tags = set(run(["git", "tag", "--list"], repo, env).stdout.splitlines())
        if actual_tags != expected_tags:
            raise AssertionError(f"unexpected final tags: {actual_tags ^ expected_tags}")
        print("verified Control directives, planned versions, held non-publication, and release tags")


if __name__ == "__main__":
    main()
