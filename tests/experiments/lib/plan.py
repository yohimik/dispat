"""A tool's plan, read from the output of its own plan command, and what the
tool was told, as one JSON object:

    {"packages": {package: next version}, "told": ...}

    plan.py dispat|lerna|nx|changesets <log> <clone>

Each tool states its plan its own way:

  dispat      `dispat status --log-format json`: one JSON line per package,
              those with a `bump` planned, at `version` written `old -> new`
  lerna       `lerna version` with nothing written back: ` - core: 1.0.0 => 1.1.0`
  nx          `nx release --dry-run`: `core ... New version 1.1.0 ...`
  changesets  `changeset status --verbose`: `  - core -> 1.1.0`

Colour codes are stripped first: a tool that colours its output when it
believes a terminal is attached would otherwise hide every line from the
patterns below. dispat's own count of the packages it is releasing, on its
`release plan ready` line, is held against the packages read, so a reading
that missed one is refused rather than recorded as a difference.

What the tool was told is what it reads of the pending change: the pending
changesets' front matter for changesets, which reads no commit messages, and
the subject of the commit at the clone's head for the others.
"""
import glob
import json
import os
import re
import subprocess
import sys

ANSI = re.compile(r"\x1b\[[0-9;]*m")
VERSION = r"(\d+\.\d+\.\d+[0-9A-Za-z.+-]*)"
PATTERNS = {
    "lerna": re.compile(r"^ - ([^:\s]+): \S+ => " + VERSION + r"\s*$"),
    "nx": re.compile(r"^(\S+) .*New version " + VERSION),
    "changesets": re.compile(r"- (\S+) -> " + VERSION + r"\s*$"),
}


def dispat_plan(lines):
    plan, releasing = {}, None
    for line in lines:
        try:
            entry = json.loads(line)
        except ValueError:
            continue
        if not isinstance(entry, dict):
            continue
        if entry.get("message") == "release plan ready":
            releasing = entry.get("releasing")
        if "bump" in entry and "package" in entry:
            plan[entry["package"]] = str(entry.get("version", "")).split("->")[-1].strip()
    if releasing != len(plan):
        raise SystemExit(f"plan.py: dispat says it is releasing {releasing}, and {len(plan)} were read")
    return plan


def matched_plan(pattern, lines):
    plan = {}
    for line in lines:
        found = pattern.search(ANSI.sub("", line).rstrip("\n"))
        if found:
            plan[found.group(1)] = found.group(2)
    return plan


def read_plan(tool, lines):
    if tool == "dispat":
        return dispat_plan(lines)
    if tool not in PATTERNS:
        raise SystemExit(f"plan.py: no such tool {tool}")
    return matched_plan(PATTERNS[tool], lines)


def told(tool, repo):
    if tool == "changesets":
        lines = []
        for path in sorted(glob.glob(os.path.join(repo, ".changeset", "*.md"))):
            if os.path.basename(path) == "README.md":
                continue
            with open(path) as f:
                front = f.read().split("---")
            lines += [line.strip() for line in (front[1] if len(front) > 2 else "").splitlines() if line.strip()]
        return ", ".join(lines)
    return subprocess.run(["git", "-C", repo, "log", "-1", "--format=%s"],
                          check=True, capture_output=True, text=True).stdout.strip()


if __name__ == "__main__":
    tool, log, repo = sys.argv[1:4]
    with open(log) as f:
        print(json.dumps({"packages": read_plan(tool, f.readlines()), "told": told(tool, repo)}, sort_keys=True))
