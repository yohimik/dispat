"""A tool's plan, read from the output of its own plan command, as one JSON
object of {package: next version}.

    plan.py dispat|lerna|nx|changesets <log>

Each tool states its plan its own way:

  dispat      `dispat status --log-format json`: one JSON line per package,
              those with a `bump` planned, at `version` written `old -> new`
  lerna       `lerna version` with nothing written back: ` - core: 1.0.0 => 1.1.0`
  nx          `nx release --dry-run`: `core ... New version 1.1.0 ...`
  changesets  `changeset status --verbose`: `  - core -> 1.1.0`

Colour codes are stripped first: a tool that colours its output when it
believes a terminal is attached would otherwise hide every line from the
patterns below.
"""
import json
import re
import sys

ANSI = re.compile(r"\x1b\[[0-9;]*m")
VERSION = r"(\d+\.\d+\.\d+[0-9A-Za-z.+-]*)"
PATTERNS = {
    "lerna": re.compile(r"^ - ([^:\s]+): \S+ => " + VERSION + r"\s*$"),
    "nx": re.compile(r"^(\S+) .*New version " + VERSION),
    "changesets": re.compile(r"- (\S+) -> " + VERSION + r"\s*$"),
}


def dispat_plan(lines):
    plan = {}
    for line in lines:
        try:
            entry = json.loads(line)
        except ValueError:
            continue
        if not isinstance(entry, dict) or "bump" not in entry or "package" not in entry:
            continue
        plan[entry["package"]] = str(entry.get("version", "")).split("->")[-1].strip()
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


if __name__ == "__main__":
    with open(sys.argv[2]) as f:
        print(json.dumps(read_plan(sys.argv[1], f.readlines()), sort_keys=True))
