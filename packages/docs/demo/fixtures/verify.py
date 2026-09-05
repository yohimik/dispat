#!/usr/bin/env python3
"""Exercise the Compute and For demo claims in disposable Git repositories."""
import json, os, shlex, shutil, subprocess, tempfile
from pathlib import Path

HERE = Path(__file__).resolve().parent
DISPAT = Path(os.environ.get("DISPAT_DEMO_BIN") or shutil.which("dispat") or "dispat").resolve()

def call(cwd: Path, argv: list[str]) -> str:
    p = subprocess.run(argv, cwd=cwd, text=True, capture_output=True)
    if p.returncode: raise RuntimeError(f"{' '.join(argv)} exited {p.returncode}\n{p.stdout}{p.stderr}")
    return p.stdout

def git(repo: Path, *args: str) -> str: return call(repo, ["git", *args])
def dispat(repo: Path, *args: str) -> str: return call(repo, [str(DISPAT), *args])

def init(repo: Path) -> None:
    git(repo, "init", "-q"); git(repo, "config", "user.email", "demo@example.test")
    git(repo, "config", "user.name", "Dispat demo")

def verify_compute(root: Path) -> None:
    expected = json.loads((HERE / "compute/expected.json").read_text())
    repo = root / "compute"
    for name in ("core", "web"): (repo / f"packages/{name}").mkdir(parents=True)
    init(repo)
    (repo / "dispat.yaml").write_text("scripts:\n  build: echo build-$DISPAT_ITEM\nspaces:\n  apps:\n    path: packages\n    flow:\n      build: [build]\n")
    (repo / "packages/core/package.json").write_text('{"name":"@acme/core","version":"1.4.2"}\n')
    (repo / "packages/web/package.json").write_text('{"name":"@acme/web","version":"2.1.0","dependencies":{"@acme/core":"workspace:*"}}\n')
    git(repo, "add", "."); git(repo, "commit", "-qm", "feat(core,web): bootstrap")
    config_before = (repo / "dispat.yaml").read_bytes()
    preview = dispat(repo, *shlex.split(expected["command"])[1:])
    assert (repo / "dispat.yaml").read_bytes() == config_before, "compute preview wrote configuration"
    for line in expected["previewContains"]: assert line in preview, (line, preview)
    written = dispat(repo, *shlex.split(expected["writeCommand"])[1:])
    assert expected["writeContains"] in written, written
    print("COMPUTE START\n" + git(repo, "log", "-1", "--oneline").strip())
    print("$ " + expected["command"] + "\n" + preview.rstrip())
    print("$ " + expected["writeCommand"] + "\n" + written.rstrip())

def verify_for(root: Path) -> None:
    expected = json.loads((HERE / "for/expected.json").read_text())
    repo = root / "for"
    for name in ("engine", "game", "native"): (repo / f"packages/{name}").mkdir(parents=True)
    init(repo)
    (repo / "dispat.yaml").write_text("scripts:\n  build: echo build-$DISPAT_ITEM\nspaces:\n  apps:\n    path: packages\n    flow:\n      build: [build]\n    packages:\n      game:\n        dependencies: [engine]\n      native:\n        dependencies: [game]\n")
    for name in ("engine", "game", "native"):
        (repo / f"packages/{name}/package.json").write_text(json.dumps({"name": f"@demo/{name}", "version": "1.0.0"}) + "\n")
    git(repo, "add", "."); git(repo, "commit", "-qm", "chore: baseline")
    (repo / "packages/engine/renderer.txt").write_text("faster frame scheduling\n")
    git(repo, "add", "."); git(repo, "commit", "-qm", "feat(engine): optimize renderer")
    output = dispat(repo, *shlex.split(expected["command"])[1:])
    lines = [line for line in output.splitlines() if line]
    assert lines == expected["output"], (lines, expected["output"])
    print("FOR START\n" + git(repo, "log", "-2", "--oneline").strip())
    print("$ " + expected["command"] + "\n" + output.rstrip())

def verify_run(root: Path) -> None:
    expected = json.loads((HERE / "run/expected.json").read_text())
    repo = root / "run"
    for name in ("core", "utils", "api", "web", "sdk", "docs", "mobile"):
        (repo / f"packages/{name}").mkdir(parents=True)
    init(repo)
    (repo / "dispat.yaml").write_text("scripts:\n  tests: echo tests-$DISPAT_PACKAGE\nspaces:\n  apps:\n    path: packages\n    flow:\n      build: [tests]\n    packages:\n      api:\n        dependencies: [core, utils]\n      web:\n        dependencies: [api]\n      sdk:\n        dependencies: [utils]\n")
    for name in ("core", "utils", "api", "web", "sdk", "docs", "mobile"):
        (repo / f"packages/{name}/package.json").write_text(json.dumps({"name": f"@demo/{name}", "version": "1.0.0"}) + "\n")
    git(repo, "add", "."); git(repo, "commit", "-qm", "chore: Baseline")
    for name, version in expected["baseVersions"].items(): git(repo, "tag", f"{name}@{version}")
    (repo / "packages/utils/fix.txt").write_text("closed\n")
    git(repo, "add", "."); git(repo, "commit", "-qm", expected["commit"])
    tags_before = git(repo, "tag", "--list")
    status_before = git(repo, "status", "--porcelain")
    output = dispat(repo, *shlex.split(expected["command"])[1:])
    assert git(repo, "tag", "--list") == tags_before, "run created release tags"
    assert git(repo, "status", "--porcelain") == status_before, "run rewrote package files"
    selected = [line.split("package=", 1)[1].split()[0] for line in output.splitlines() if "run script started" in line]
    assert set(selected) == set(expected["selected"]), selected
    for consumer, provider in (("api", "utils"), ("sdk", "utils"), ("web", "api")):
        assert output.index(f"tests-{provider} package={provider}") < output.index(f"run script started package={consumer}"), output
    for line in ("package=utils", "package=api", "package=sdk", "package=web", "run finished failed=0 ran=4 script=tests skipped=0"):
        assert line in output, (line, output)
    print("RUN START\n" + git(repo, "log", "-2", "--oneline").strip())
    print("$ " + expected["command"] + "\n" + output.rstrip())

def verify_glue(root: Path) -> None:
    repo = root / "glue"; (repo / "packages/core").mkdir(parents=True); (repo / "packages/api").mkdir(parents=True)
    init(repo)
    (repo / "dispat.yaml").write_text("scripts:\n  tests: echo tests\nspaces:\n  libs:\n    path: packages\n    flow:\n      build: [tests]\n    packages:\n      api:\n        dependencies: [core]\n")
    (repo / "build.gradle").write_text('dependencies { implementation "com.acme:core:1.4.2" }\n')
    (repo / "README.md").write_text("npm install @acme/core@1.4.2\n")
    (repo / "packages/core/go.mod").write_text("module github.com/acme/core\n\ngo 1.26\n")
    (repo / "packages/api/go.mod").write_text("module github.com/acme/api\n\ngo 1.26\n\nrequire github.com/acme/core v0.0.0\n")
    git(repo, "add", "."); git(repo, "commit", "-qm", "feat(core,api): Bootstrap")
    branch = subprocess.run([str(DISPAT), "if", "ENV=prod", "--then", "echo deploying to prod", "--else", "echo deploying to stage"], cwd=repo, text=True, capture_output=True, env={**os.environ, "ENV": "prod"}, check=True).stdout
    assert branch.strip() == "deploying to prod", branch
    replaced = dispat(repo, "replacer", "--replace", "com.acme:core:1.4.2=>com.acme:core:1.5.0", "--replace", "@acme/core@1.4.2=>@acme/core@1.5.0", "build.gradle", "README.md")
    assert "2 file(s), 2 occurrence(s): 2 applied" in replaced, replaced
    linked = dispat(repo, "autowriter", "--link-local", "--since", "all", "--sync-lock=false")
    assert "applied  link     github.com/acme/core  ../core" in linked, linked
    tested = dispat(repo, "tests", "--since", "all"); assert "run finished failed=0 ran=2 script=tests" in tested, tested
    unlinked = dispat(repo, "autowriter", "--unlink-local", "--since", "all", "--sync-lock=false")
    assert "github.com/acme/core  (removed)" in unlinked, unlinked
    verified = dispat(repo, "scanner", "--verify-unlinked"); assert "3 manifest(s), 1 dependency declaration(s)" in verified, verified
    print("GLUE START\n" + git(repo, "log", "-1", "--oneline").strip())
    print("$ dispat if 'ENV=prod' --then 'echo deploying to prod' --else 'echo deploying to stage'\n" + branch.rstrip())
    print("$ dispat replacer ... build.gradle README.md\n" + replaced.rstrip())
    print("$ dispat autowriter --link-local --since all --sync-lock=false\n" + linked.rstrip())
    print("$ dispat tests --since all\n" + tested.rstrip())
    print("$ dispat autowriter --unlink-local --since all --sync-lock=false\n" + unlinked.rstrip())
    print("$ dispat scanner --verify-unlinked\n" + verified.rstrip())

def main() -> None:
    if not DISPAT.is_file(): raise SystemExit(f"demo CLI not found: {DISPAT}")
    with tempfile.TemporaryDirectory(prefix="dispat-demo-") as directory:
        verify_compute(Path(directory)); verify_for(Path(directory)); verify_run(Path(directory)); verify_glue(Path(directory))

if __name__ == "__main__": main()
