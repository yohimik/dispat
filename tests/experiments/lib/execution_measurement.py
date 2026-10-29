"""Capture new standard timing evidence; the Go CLI owns formula derivation."""
import argparse
import hashlib
import json
import os
import pathlib
import platform
import re
import shutil
import subprocess
import time
import uuid

from measurement_clock import NANOSECONDS_PER_SECOND, monotonic_sample

SCHEMA = "ccme-execution-measurement/v1"
HISTORY_DIRECTORY = "execution-measurements"
CAPACITY_REASON = "The fixture records configured policy but does not export an admitted compatible unit-task capacity throughout this interval."
POOL_REASON = "Build commands and registry uploads are different resource classes; a fixed interchangeable available pool is not established."
CLOCK_REASON = "The build, proxy and coordinator clock origins have no established mapping."
RATE_REASON = "No quantitative whole-trial relative clock-rate error bound was established."
OMITTED_WORK = ["tool startup, planning and version edits", "packing outside the recorded registry upload", "signing, release records, Git commits, tags and pushes", "transport, scheduling and verification outside the named operations"]
TOOL_COMMANDS = {"dispat": "dispat", "nx": "nx", "lerna": "lerna", "changesets": "changeset"}
CONFIG_NAMES = ("package.json", "package-lock.json", "dispat.yaml", "nx.json", "lerna.json", ".changeset/config.json")
MODELED_TASKS = {"build": "build", "upload": "publish"}
INSTALLED_TOOL_LOCK = pathlib.Path("/opt/tools/package-lock.json")


def digest(body):
    return hashlib.sha256(body).hexdigest()


def encode(value):
    return (json.dumps(value, indent=2, sort_keys=True, allow_nan=False) + "\n").encode()


def write_new(path, value):
    with pathlib.Path(path).open("xb") as target:
        target.write(encode(value))


def read_json(path):
    return json.loads(pathlib.Path(path).read_text())


def read_lines(path):
    source = pathlib.Path(path)
    return [json.loads(line) for line in source.read_text().splitlines() if line.strip()] if source.exists() else []


def optional_text(path):
    try:
        return pathlib.Path(path).read_text().strip()
    except OSError:
        return None


def command_output(command, cwd=None):
    return subprocess.check_output(command, cwd=cwd, text=True, stderr=subprocess.STDOUT).strip()


def reference(identity, pointer=""):
    return {"source": "evidence", "evidenceId": identity, "pointer": pointer}


def unknown(reason):
    return {"value": None, "evidence": [], "reason": reason}


def executor_source(tool):
    if tool != "dispat":
        return {"revision": None, "reason": "The pinned npm installation identifies the executor; its upstream source commit is not independently captured."}
    identity = os.environ.get("EXPERIMENTS_DISPAT_BINARY_ID", "unknown")
    revision = os.environ.get("EXPERIMENTS_DISPAT_SOURCE_REVISION")
    if revision and not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise ValueError("EXPERIMENTS_DISPAT_SOURCE_REVISION must be an exact lowercase commit identity")
    candidate = re.search(r"candidate[.:]([0-9a-f]{40})(?:\b|$)", identity)
    if revision and candidate and revision != candidate.group(1):
        raise ValueError("Dispat source revision differs from its candidate binary identity")
    revision = revision or (candidate.group(1) if candidate else None)
    return {"buildIdentity": identity, "revision": revision,
            "reason": None if revision else "The supplied executable identity does not identify an exact source commit."}


def initialize(output, tool, scenario, repetition):
    if repetition < 1:
        raise ValueError("measurement repetition must be positive")
    trial_id = str(uuid.uuid4())
    directory = output.parent / HISTORY_DIRECTORY / trial_id
    directory.mkdir(parents=True, exist_ok=False)
    boot = optional_text("/proc/sys/kernel/random/boot_id")
    host_id = "linux-boot:" + digest(boot.encode()) if boot else None
    metadata = {"id": trial_id, "repetition": repetition, "tool": tool, "scenario": scenario,
                "createdWallSeconds": time.time(), "hostId": host_id,
                "hostIdentityReason": None if boot else "No independently reported kernel boot identity is available.",
                "hostname": platform.node(), "kernel": platform.release(), "architecture": platform.machine(),
                "cpuCount": os.cpu_count(), "cpuQuota": optional_text("/sys/fs/cgroup/cpu.max"),
                "memoryLimit": optional_text("/sys/fs/cgroup/memory.max"),
                "pythonMonotonicImplementation": time.get_clock_info("monotonic").implementation,
                "pythonMonotonicResolution": time.get_clock_info("monotonic").resolution}
    write_new(directory / "trial.json", metadata)
    for name in ("steps.jsonl", "timing.jsonl"):
        (directory / name).touch(exist_ok=False)
        legacy = output / name
        if legacy.exists() and legacy.stat().st_size:
            raise ValueError("measurement initialization cannot replace existing records: " + str(legacy))
        legacy.unlink(missing_ok=True)
        legacy.symlink_to(directory / name)
    return {"directory": str(directory), "trialId": trial_id, "phaseFile": str(directory / "active-step.json"),
            "timingFile": str(directory / "timing.jsonl"), "hostId": host_id}


def capture_inputs(directory, output, repo, tool, protocol_directory):
    if (directory / "inputs.json").exists():
        return
    plan = read_json(output / "plan.json")
    paths = [repo / name for name in CONFIG_NAMES]
    paths += sorted(repo.glob("packages/*/package.json"))
    paths += sorted(repo.glob("packages/*/project.json"))
    files = {str(path.relative_to(repo)): {"sha256": digest(path.read_bytes()), "text": path.read_text()}
             for path in paths if path.is_file()}
    write_new(directory / "configuration-capture.json", {"selectedPlan": plan, "files": files,
                "capturedBeforeRelease": True, "sourceRevision": command_output(["git", "rev-parse", "HEAD"], repo)})
    manifests = {json.loads(value["text"])["name"]: json.loads(value["text"])
                 for name, value in files.items() if name.startswith("packages/") and name.endswith("/package.json")}
    dependencies = {name: sorted({provider for field in ("dependencies", "devDependencies")
                                 for provider in manifest.get(field, {}) if provider in manifests})
                    for name, manifest in manifests.items()}
    selected = set(plan["packages"])
    if not selected.issubset(manifests):
        raise ValueError("selected release plan names a package absent from the captured manifests")
    protocol = protocol_directory / "timing" / (tool + ".sh")
    policy = re.search(r"measure_timing (build|publish|none) after-release", protocol.read_text())
    if policy is None:
        raise ValueError("timing protocol has no explicit supported readiness projection")
    relation = policy.group(1)
    build_selected = set(selected)
    if tool == "dispat" and re.search(r"isBuildWaitingPublish\s*:", files["dispat.yaml"]["text"]):
        raise ValueError("Dispat readiness override differs from the timing fixture's declared default projection")
    if tool == "nx":
        config = json.loads(files["nx.json"]["text"])
        if config["release"]["version"].get("preVersionCommand") != "npx nx run-many -t build":
            raise ValueError("Nx build selection is not the fixture's declared all-project run-many command")
        build_selected = set(manifests)
        defaults = config.get("targetDefaults", {}).get("build", {}).get("dependsOn", [])
        if defaults:
            raise ValueError("Nx build dependsOn differs from the declared fixture projection")
        for name, value in files.items():
            if name.endswith("/project.json") and json.loads(value["text"]).get("targets", {}).get("build", {}).get("dependsOn"):
                raise ValueError("Nx project build dependsOn differs from the declared fixture projection")
    for package in build_selected:
        if not manifests[package].get("scripts", {}).get("build"):
            raise ValueError("a selected modeled build has no configured script: " + package)
        if tool in ("lerna", "changesets") and manifests[package]["scripts"].get("prepack") != "npm run --silent build":
            raise ValueError("the captured prepack policy differs from the declared build projection: " + package)
    vertices = []
    for package in sorted(manifests):
        for operation, selection in (("build", build_selected), ("publish", selected)):
            excluded = package not in selection
            vertices.append({"id": package + ":" + operation, "operation": operation,
                             "isVirtual": excluded, "durationInputs": [],
                             "scopeReason": "not selected for this operation; dependency relay only" if excluded else "required selected operation"})
    edges = []
    for package, providers in sorted(dependencies.items()):
        edges.append({"from": package + ":build", "to": package + ":publish", "kind": "own-build-before-publication"})
        for provider in providers:
            edges.append({"from": provider + ":publish", "to": package + ":publish", "kind": "provider-publication"})
            if relation != "none":
                edges.append({"from": provider + ":" + relation, "to": package + ":build", "kind": "provider-" + relation + "-before-build"})
    executable = pathlib.Path(shutil.which(TOOL_COMMANDS[tool])).resolve()
    protocol_files = {str(path.relative_to(protocol_directory)): digest(path.read_bytes())
                      for path in sorted(protocol_directory.rglob("*")) if path.is_file() and "__pycache__" not in path.parts and path.suffix != ".pyc"}
    sources = {str(path.relative_to(repo)): {"sha256": digest(path.read_bytes()), "text": path.read_text()}
               for path in sorted(repo.glob("packages/*/index.js"))}
    dependency_lock = {"sha256": None, "reason": "No installed comparison-tool dependency lock is available."}
    if INSTALLED_TOOL_LOCK.is_file():
        shutil.copyfile(INSTALLED_TOOL_LOCK, directory / "tool-dependencies.json")
        dependency_lock = {"sha256": digest(INSTALLED_TOOL_LOCK.read_bytes()), "file": "tool-dependencies.json"}
    inputs = {"capturedBeforeRelease": True, "sourceRevision": command_output(["git", "rev-parse", "HEAD"], repo),
              "sourceRevisionDefinition": "fixture Git HEAD before release", "executorSource": executor_source(tool),
              "installedToolDependencies": dependency_lock,
              "selectedPlan": plan, "configuration": files, "configurationSha256": digest(encode(files)),
              "sourceFiles": sources, "credentialFiles": "npmrc and environment credentials are deliberately excluded",
              "dependencies": dependencies, "readiness": {"relation": relation, "source": str(protocol.relative_to(protocol_directory)),
                  "classification": "fixture-declared policy projection; not a native executor graph export"},
              "protocolFiles": protocol_files, "protocolRevision": digest(encode(protocol_files)),
              "binaryPath": str(executable), "binarySha256": digest(executable.read_bytes()),
              "derivationBinarySha256": digest(pathlib.Path(shutil.which("execution-measurement")).read_bytes()),
              "toolchain": {"tool": command_output([TOOL_COMMANDS[tool], "--version"]),
                  "node": command_output(["node", "--version"]), "python": platform.python_version(),
                  "git": command_output(["git", "--version"])},
              "graph": {"id": "standard-build-publication-projection", "scope": "analysis-projection",
                  "provenance": "captured before execution from manifest dependencies, selected release plan and fixture-declared readiness policy; not a native task graph",
                  "vertices": vertices, "edges": edges, "omittedWork": OMITTED_WORK}}
    write_new(directory / "inputs.json", inputs)
    shutil.copyfile(output / "plan.json", directory / "selected-plan.json")


def begin_step(directory, step, kind, phase):
    trial = read_json(directory / "trial.json")
    started = {"trialId": trial["id"], "step": step, "kind": kind, "phase": phase,
               "start": monotonic_sample(), "coordinatorClockDomain": trial["id"] + ":coordinator:python-monotonic"}
    write_new(directory / ("step-" + step + "-start.json"), started)
    write_new(directory / "active-step.json", started)


def end_step(directory, step, exit_code):
    ended = monotonic_sample()
    started = read_json(directory / ("step-" + step + "-start.json"))
    started["end"], started["exit"] = ended, exit_code
    started["durationSeconds"] = (int(ended["nanoseconds"]) - int(started["start"]["nanoseconds"])) / NANOSECONDS_PER_SECOND
    started["boundary"] = "coordinator monotonic sample before the shell command through sample after its completion; includes measurement bridge latency"
    with (directory / "monotonic-steps.jsonl").open("a") as target:
        target.write(json.dumps(started, allow_nan=False) + "\n")
    (directory / "active-step.json").unlink()


def evidence_record(directory, name, identity):
    path = directory / name
    return {"id": identity, "path": str(path.relative_to(directory.parent.parent)), "sha256": digest(path.read_bytes())}


def build_measurement(directory, harness_exit):
    trial, inputs = read_json(directory / "trial.json"), read_json(directory / "inputs.json")
    steps = read_lines(directory / "monotonic-steps.jsonl")
    records = read_lines(directory / "timing.jsonl")
    modeled_steps = [step for step in steps if step["step"] == "run" or step["step"].startswith("run-")]
    selected_steps = {step["step"] for step in modeled_steps}
    graph = inputs["graph"]
    graph["evidence"] = [reference("inputs", "/graph"), reference("inputs", "/readiness")]
    vertices = {vertex["id"]: vertex for vertex in graph["vertices"]}
    intervals = []
    for index, record in enumerate(records):
        measured = record.get("measurement")
        if measured is None or record["task"] not in MODELED_TASKS:
            continue
        phase = measured.get("phase") or {}
        if phase.get("trialId") != trial["id"] or phase.get("step") not in selected_steps:
            continue
        operation = MODELED_TASKS[record["task"]]
        task_id = record["package"] + ":" + operation
        if task_id not in vertices or vertices[task_id]["isVirtual"]:
            raise ValueError("measured operation differs from the retained pre-execution selection: " + task_id)
        success = record.get("exit", 0) == 0 if operation == "build" else 200 <= record.get("status", 0) < 300
        interval = {"taskId": task_id, "attemptId": trial["id"] + ":record:" + str(index),
                    "nodeId": "build-executor" if operation == "build" else "registry-proxy",
                    "status": "completed" if success else "failed", "operation": operation,
                    "boundary": measured["boundary"], "durationSeconds": measured["durationSeconds"],
                    "clockDomain": measured["clockDomain"], "clockKind": measured["clockKind"],
                    "startNanoseconds": measured["startNanoseconds"], "endNanoseconds": measured["endNanoseconds"],
                    "stepId": phase["step"], "evidence": [reference("task-records", "/" + str(index))]}
        vertices[task_id]["durationInputs"].append({"source": "measurement", "pointer": "/taskIntervals/" + str(len(intervals)) + "/durationSeconds"})
        intervals.append(interval)
    reference_domain = trial["id"] + ":coordinator:python-monotonic"
    windows = []
    if modeled_steps:
        first, last = modeled_steps[0], modeled_steps[-1]
        seconds = (int(last["end"]["nanoseconds"]) - int(first["start"]["nanoseconds"])) / NANOSECONDS_PER_SECOND
        covered = sorted({interval["taskId"] for interval in intervals})
        windows.append({"id": "coordinator", "boundary": "before first run or run-* command through completion of last run or run-* command, including gaps and named manual operations",
                        "seconds": seconds, "clockDomain": reference_domain, "clockKind": "monotonic",
                        "evidence": [reference("step-records")], "includedSteps": [step["step"] for step in modeled_steps],
                        "containsTasks": covered, "containmentEvidence": [reference("task-records"), reference("step-records")],
                        "containmentExplanation": "Build evidence is appended before the child command exits. Proxy evidence is appended before its HTTP response reaches the publishing command. Each record carries the active command identity, set before invocation and cleared after completion."})
        windows.append({"id": "step-sum", "boundary": "sum of named run/run-* coordinator command intervals; gaps excluded",
                        "seconds": sum(step["durationSeconds"] for step in modeled_steps), "clockDomain": reference_domain,
                        "clockKind": "monotonic", "evidence": [reference("step-records")],
                        "includedSteps": [step["step"] for step in modeled_steps], "aggregation": "sum of disjoint local monotonic command intervals"})
    windows.append({"id": "modeled-task-span", "boundary": "span of the modeled build and upload operations only",
                    "seconds": None, "clockDomain": reference_domain, "clockKind": "monotonic", "evidence": [], "reason": CLOCK_REASON})
    status = "completed" if harness_exit == 0 and modeled_steps and all(step["exit"] == 0 for step in modeled_steps) and all(item["status"] == "completed" for item in intervals) else "failed"
    retained = [("trial.json", "environment"), ("inputs.json", "inputs"), ("task-records.json", "task-records"), ("step-records.json", "step-records")]
    write_new(directory / "task-records.json", records)
    write_new(directory / "step-records.json", steps)
    evidence = [evidence_record(directory, name, identity) for name, identity in retained]
    if (directory / "tool-dependencies.json").exists():
        evidence.append(evidence_record(directory, "tool-dependencies.json", "tool-dependencies"))
    nodes = [{"id": role, "hostId": trial["hostId"], "hostIdentityReason": trial["hostIdentityReason"],
              "topology": "two process roles in the same experiment container", "configuredTaskSlots": None,
              "admittedTaskSlots": None, "capacityReason": CAPACITY_REASON, "hardwareEvidence": reference("environment"),
              "cpuQuota": trial["cpuQuota"], "memoryLimit": trial["memoryLimit"], "eligibility": operation}
             for role, operation in (("build-executor", "build"), ("registry-proxy", "publish"))]
    return {"schema": SCHEMA, "trial": {"id": trial["id"], "repetition": trial["repetition"], "status": status,
                "tool": trial["tool"], "mode": "local-release", "profile": trial["scenario"], "workload": "standard-release-timing",
                "sourceRevision": inputs["sourceRevision"], "binarySha256": inputs["binarySha256"],
                "sourceRevisionDefinition": inputs["sourceRevisionDefinition"], "executorSource": inputs["executorSource"],
                "protocolRevision": inputs["protocolRevision"], "configurationSha256": inputs["configurationSha256"],
                "toolchain": inputs["toolchain"], "evidence": evidence}, "graph": graph,
            "resources": {"nodes": nodes, "pools": [], "taskSlotLimit": unknown(CAPACITY_REASON),
                "usableOnePoolSlots": unknown(POOL_REASON), "nested": []}, "taskIntervals": intervals,
            "timeWindows": windows, "observedPeakOverlap": unknown(CLOCK_REASON), "analysis": [],
            "clockCompatibility": {"referenceDomain": reference_domain,
                "domains": sorted({reference_domain} | {item["clockDomain"] for item in intervals}),
                "method": "nominal-seconds", "assumptions": ["One reported second is treated as a compatible elapsed-time unit across the listed domains for this trial."],
                "uncertainty": {"relativeRateErrorBound": None, "reason": RATE_REASON}, "evidence": [reference("environment")]},
            "premises": {"precedenceValid": None, "precedenceReason": "Configuration states the modeled prerequisites, but unmapped clock coordinates do not verify observed cross-domain precedence.",
                "enclosingWindowId": "coordinator", "evidence": [reference("inputs", "/readiness")]}}


def finalize(directory, output, harness_exit):
    derived = directory / "execution-measurement.json"
    if derived.exists():
        shutil.copyfile(derived, output / derived.name)
        return
    if not (directory / "harness-outcome.json").exists():
        write_new(directory / "harness-outcome.json", {"exit": harness_exit, "finishedWallSeconds": time.time()})
    if not (directory / "inputs.json").exists():
        if not (directory / "unavailable.json").exists():
            write_new(directory / "unavailable.json", {"status": "failed", "reason": "Execution ended before pre-release graph/configuration capture.", "trial": read_json(directory / "trial.json")})
        return
    raw = directory / "raw-measurement.json"
    # EXIT may retry after a derivation failure. Keep the first raw trial
    # immutable and retry only its derivation, never recapture changed inputs.
    if not raw.exists():
        if (output / "verdict.json").exists():
            shutil.copyfile(output / "verdict.json", directory / "legacy-verdict.json")
        measurement = build_measurement(directory, harness_exit)
        if (directory / "legacy-verdict.json").exists():
            verdict = read_json(directory / "legacy-verdict.json")
            measurement["trial"]["evidence"].append(evidence_record(directory, "legacy-verdict.json", "legacy-verdict"))
            measurement["assertions"] = verdict["checks"]
            if not verdict["passed"]:
                measurement["trial"]["status"] = "failed"
        write_new(raw, measurement)
    with (directory / "derivation.log").open("a") as log:
        subprocess.run(["execution-measurement", "-input", str(raw), "-output", str(derived)], check=True, stdout=log, stderr=log)
    shutil.copyfile(derived, output / derived.name)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("init", "capture", "begin", "end", "finalize"))
    parser.add_argument("--directory", type=pathlib.Path)
    parser.add_argument("--output", type=pathlib.Path)
    parser.add_argument("--repo", type=pathlib.Path)
    parser.add_argument("--tool", choices=tuple(TOOL_COMMANDS))
    parser.add_argument("--scenario")
    parser.add_argument("--repetition", type=int, default=1)
    parser.add_argument("--step")
    parser.add_argument("--kind")
    parser.add_argument("--phase")
    parser.add_argument("--exit", type=int, default=0)
    parser.add_argument("--protocol-directory", type=pathlib.Path, default=pathlib.Path(__file__).resolve().parent.parent)
    args = parser.parse_args()
    if args.action == "init":
        print(json.dumps(initialize(args.output, args.tool, args.scenario, args.repetition)))
    elif args.action == "capture":
        capture_inputs(args.directory, args.output, args.repo, args.tool, args.protocol_directory)
    elif args.action == "begin":
        begin_step(args.directory, args.step, args.kind, args.phase)
    elif args.action == "end":
        end_step(args.directory, args.step, getattr(args, "exit"))
    else:
        finalize(args.directory, args.output, getattr(args, "exit"))


if __name__ == "__main__":
    main()
