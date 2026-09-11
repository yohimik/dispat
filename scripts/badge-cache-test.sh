#!/bin/sh
# Exercise the real badge target against an isolated copy of the profiles a
# full suite just produced. Negative cases never touch the job's artifacts.
set -eu
root=${1:-$(git rev-parse --show-toplevel)}
cd "$root"
stamp=$root/coverage/ccme.commit
[ -f "$stamp" ] || { echo 'badge cache test requires full-suite coverage profiles' >&2; exit 1; }
commit=$(sed -n '1p' "$stamp")
[ -n "$commit" ] || { echo 'coverage/ccme.commit is empty' >&2; exit 1; }
for file in coverage/*.commit; do
  [ "$(sed -n '1p' "$file")" = "$commit" ] || {
    echo "$file does not belong to the full-suite profile set at $commit" >&2
    exit 1
  }
done

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT HUP INT TERM
# Include tracked working changes as well as HEAD: this gate runs before the
# release commit, and those exact sources are what produced coverage/.
git ls-files -z | tar --null -T - -cf - | tar -xf - -C "$work"
mkdir -p "$work/coverage"
cp coverage/*.out coverage/*.commit "$work/coverage/"
fixture_stamp=$work/coverage/ccme.commit
build() {
  docker buildx build --file "$work/Dockerfile.gotest" --target badge-export \
    --build-arg "TEST_COMMIT=$commit" --output type=cacheonly "$work"
}

build
printf '%s\n' stale-commit > "$fixture_stamp"
stale_log=$work/stale.log
if build >"$stale_log" 2>&1; then
  echo 'badge accepted a stale coverage stamp' >&2
  exit 1
fi
grep -F "ccme.out was not measured at $commit" "$stale_log" >/dev/null || {
  echo 'stale profile failed for an unrelated reason' >&2
  cat "$stale_log" >&2
  exit 1
}
rm "$fixture_stamp"
missing_log=$work/missing.log
if build >"$missing_log" 2>&1; then
  echo 'badge accepted a missing coverage stamp' >&2
  exit 1
fi
grep -F 'ccme.out has no measurement stamp' "$missing_log" >/dev/null || {
  echo 'missing profile stamp failed for an unrelated reason' >&2
  cat "$missing_log" >&2
  exit 1
}

cp "$stamp" "$fixture_stamp"
for profile in "$work"/coverage/*.out; do
  awk 'NR == 1 { print; next } { $NF = 0; print }' "$profile" > "$profile.zero"
  mv "$profile.zero" "$profile"
done
threshold_log=$work/threshold.log
if build >"$threshold_log" 2>&1; then
  echo 'badge accepted coverage below 95%' >&2
  exit 1
fi
grep -E 'combined coverage [0-9]+/[0-9]+ is below 95%' "$threshold_log" >/dev/null || {
  echo 'below-threshold profiles failed for an unrelated reason' >&2
  cat "$threshold_log" >&2
  exit 1
}
echo "badge freshness validation passed at $commit"
