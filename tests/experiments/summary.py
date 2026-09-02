"""One table over a results folder: what every cell recorded.

    summary.py <results-dir> [--markdown]

A row per cell with the final state of each package the run touched, the
step exit codes, and the expectations that held. The verdict counts for the
tool under test only; the compared tools' rows are records.
"""
import glob
import json
import os
import sys


def load(path):
    with open(path) as f:
        return json.load(f)


def last_observation(cell):
    """The observations file holds pretty-printed objects back to back; the
    last one is the state the run ended in."""
    try:
        with open(os.path.join(cell, "observations.jsonl")) as f:
            raw = f.read()
    except OSError:
        return None
    objs, depth, start = [], 0, None
    for i, ch in enumerate(raw):
        if ch == "{":
            if depth == 0:
                start = i
            depth += 1
        elif ch == "}":
            depth -= 1
            if depth == 0 and start is not None:
                objs.append(json.loads(raw[start:i + 1]))
    return objs[-1] if objs else None


def main():
    root = sys.argv[1]
    md = "--markdown" in sys.argv
    rows = []
    for cell in sorted(glob.glob(os.path.join(root, "*"))):
        v = os.path.join(cell, "verdict.json")
        if not os.path.isfile(v):
            continue
        verdict = load(v)
        obs = last_observation(cell)
        state = ""
        if obs:
            state = " ".join(f"{p}={r['registry']}/{r['state']}"
                             for p, r in obs["packages"].items() if r["state"] != "baseline")
        steps = " ".join(f"{s['step']}={s['exit']}" for s in verdict["steps"])
        held = sum(1 for c in verdict["checks"] if c["ok"])
        rows.append((os.path.basename(cell), verdict["tool"], verdict.get("dispat", ""),
                     steps, f"{held}/{len(verdict['checks'])}",
                     "holds" if verdict["passed"] else "; ".join(
                         c["check"] for c in verdict["checks"] if not c["ok"]),
                     state))
    if md:
        print("| cell | tool | dispat | steps | checks | outcome | final state |")
        print("|---|---|---|---|---|---|---|")
        for cell, tool, dispat, steps, checks, outcome, state in rows:
            print(f"| {cell} | {tool} | {dispat} | `{steps}` | {checks} | {outcome} | `{state}` |")
    else:
        for cell, tool, dispat, steps, checks, outcome, state in rows:
            print(f"{cell}\n  {tool}  {dispat}\n  steps: {steps}\n  checks: {checks}  {outcome}\n  state: {state}")


if __name__ == "__main__":
    main()
