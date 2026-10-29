"""Local monotonic evidence, separate from the legacy wall-clock records."""
import json
import os
import pathlib
import time

NANOSECONDS_PER_SECOND = 1_000_000_000
MONOTONIC_CLOCK = "monotonic"
TRIAL_ENVIRONMENT = "EXPERIMENT_MEASUREMENT_TRIAL_ID"
PHASE_ENVIRONMENT = "EXPERIMENT_MEASUREMENT_PHASE_FILE"
ABSENT_IDENTITY = "unrecorded"


def monotonic_sample():
    return {"nanoseconds": str(time.monotonic_ns()), "wallSeconds": time.time()}


def local_interval(start, end, role, process_id=None):
    trial = os.environ.get(TRIAL_ENVIRONMENT, ABSENT_IDENTITY)
    process = os.getpid() if process_id is None else process_id
    return {"durationSeconds": (int(end["nanoseconds"]) - int(start["nanoseconds"])) / NANOSECONDS_PER_SECOND,
            "startNanoseconds": start["nanoseconds"], "endNanoseconds": end["nanoseconds"],
            "clockKind": MONOTONIC_CLOCK, "clockDomain": f"{trial}:{role}:{process}:python-monotonic",
            "clockAPI": "time.monotonic_ns", "clockImplementation": time.get_clock_info(MONOTONIC_CLOCK).implementation}


def active_phase():
    path = os.environ.get(PHASE_ENVIRONMENT)
    if not path:
        return None
    try:
        return json.loads(pathlib.Path(path).read_text())
    except FileNotFoundError:
        return None
