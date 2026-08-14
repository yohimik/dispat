#!/bin/sh
# The coverage totals and the README badge, from the profiles a full suite run
# left in coverage/.
#
# Only a `--since all` run produces a complete set, which is why this is called
# from the release workflow and nowhere else: a run selected by a `--since`
# window tests part of the monorepo, so a merged total from it would be a
# number about whichever packages happened to change.
#
# Run from the repository root, after `dispat run tests --since all`. Reads
# nothing but coverage/*.out; writes the two merged profiles and the badge JSON
# back into that folder, and appends the summary table to $GITHUB_STEP_SUMMARY
# when there is one. Pushing the badge is the caller's job — see the badges
# branch step in .github/workflows/release.yml.
set -eu

# Three views of the suite for the job summary — the unit layer (the workspace
# modules' own tests), the integration layer (the black-box suite's
# instrumented binary — CLI module scope, since that is the binary) — with only
# the combined total across every profile (go tool cover merges the overlapping
# blocks) becoming the README badge.
merge() { out=$1; shift; { head -n 1 "$1"; tail -q -n +2 "$@"; } > "$out"; }
pct() { go tool cover -func="$1" | tail -n 1 | awk '{print $3}'; }
badge() { # <label> <percentage> <file>: shields.io endpoint JSON
  COLOR=red
  BAND=$(printf '%.0f' "${2%\%}")
  if   [ "$BAND" -ge 92 ]; then COLOR=brightgreen
  elif [ "$BAND" -ge 85 ]; then COLOR=green
  elif [ "$BAND" -ge 75 ]; then COLOR=yellowgreen
  elif [ "$BAND" -ge 60 ]; then COLOR=yellow
  elif [ "$BAND" -ge 40 ]; then COLOR=orange
  fi
  printf '{"schemaVersion": 1, "label": "%s", "message": "%s", "color": "%s"}\n' \
    "$1" "$2" "$COLOR" > "$3"
  cat "$3"
}

# Whatever the run produced, rather than a list to keep in step with the
# monorepo: every package's tests script writes one profile named after it, and
# integration.out is the one that is not a unit layer. Collected into the
# positional parameters rather than a variable, so a profile name never goes
# through word splitting. The merge outputs are skipped explicitly: they land in
# this folder too, and a re-run would otherwise fold them into themselves.
cd coverage
set --
for f in *.out; do
  case "$f" in integration.out|coverage.out|coverage-unit.out) continue ;; esac
  set -- "$@" "$f"
done
echo "unit profiles: $*"
merge coverage-unit.out "$@"
merge coverage.out      "$@" integration.out
UNIT=$(pct coverage-unit.out)
INTEGRATION=$(pct integration.out)
TOTAL=$(pct coverage.out)
echo "unit ${UNIT} / integration ${INTEGRATION} / total ${TOTAL}"

# Outside Actions there is no step summary to write to, and the totals are on
# stdout already.
{
  echo "### Test coverage"
  echo ""
  echo '| Layer | Statement coverage |'
  echo '|---|---|'
  echo "| unit (all modules) | ${UNIT} |"
  echo "| integration (CLI module, via the instrumented binary) | ${INTEGRATION} |"
  echo "| **combined total** | **${TOTAL}** |"
} >> "${GITHUB_STEP_SUMMARY:-/dev/null}"

badge "coverage" "$TOTAL" coverage.json
