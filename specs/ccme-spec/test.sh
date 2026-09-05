#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
# Copyright (c) 2026 yohimik
set -eu

here=$(CDPATH='' cd -- "$(dirname "$0")" && pwd)
dispat=${DISPAT_BIN:-$(command -v dispat || true)}
if [ -z "$dispat" ] || [ ! -x "$dispat" ]; then
  echo "native dispat binary is required on PATH or in DISPAT_BIN" >&2
  exit 1
fi
case "$dispat" in
  /*) ;;
  *) dispat=$(CDPATH='' cd -- "$(dirname "$dispat")" && pwd)/$(basename "$dispat") ;;
esac

tmp=$(mktemp -d "${TMPDIR:-/tmp}/ccme-spec-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
trap 'exit 1' HUP INT TERM

fixture() {
  dir=$1
  version=$2
  mkdir -p "$dir"
  cp "$here/verify.sh" "$here/LICENSE" "$here/dispat.yaml" "$dir/"
  printf '%s\n' "$version" > "$dir/VERSION"
  cat > "$dir/SPEC.md" <<EOF
**Version:** $version **Status:** Normative
**License:** GPL-3.0-or-later. See [LICENSE](./LICENSE).
An implementation conforms to CCME $version if and only if it:
This document is CCME **$version** and is itself versioned under SemVer:
Example version 1.0.0 is not a normative declaration.
EOF
}

# The production package config must drive the real release scheduler and
# commit the replacements, without scanning or rewriting unrelated manifests.
repository() {
  repo=$1
  fixture "$repo/specs/ccme-spec" 1.0.0
  printf '%s\n' '{"name":"ccme-spec","version":"1.0.0"}' > "$repo/specs/ccme-spec/package.json"
  cat > "$repo/dispat.yaml" <<'EOF'
unsafeDisableLock: true
changelog:
  enabled: false
github:
  enabled: false
commit:
  enabled: true
  push: false
scripts:
  changelog: 'true'
  commit: '"$DISPAT_BIN" commit --tag'
versionGroups:
  ccme:
    versioning: fixedMajorMinor
initials:
  ccme-spec: 1.0.0
packages:
  ccme-spec:
    path: specs/ccme-spec
EOF
  (
    cd "$repo"
    git init -q -b main
    git config user.name 'CCME specification test'
    git config user.email 'ccme-spec-test@example.invalid'
    git add .
    git commit -qm 'chore(ccme-spec): establish baseline'
    printf '%s\n' 'breaking specification revision' > specs/ccme-spec/change.txt
    git add .
    git commit -qm 'feat(ccme-spec)!: revise normative algorithm'
  )
}

release=$tmp/release
repository "$release"
(
  cd "$release"
  DISPAT_BIN="$dispat" "$dispat" release --package ccme-spec --require-release
  test "$(git show specs/ccme-spec/v2.0.0:specs/ccme-spec/VERSION)" = 2.0.0
  git show specs/ccme-spec/v2.0.0:specs/ccme-spec/SPEC.md |
    grep -Fq '**Version:** 2.0.0 **Status:**'
  grep -Fq 'Example version 1.0.0' specs/ccme-spec/SPEC.md
  test "$(cat specs/ccme-spec/package.json)" = '{"name":"ccme-spec","version":"1.0.0"}'
  before=$(git rev-parse HEAD)
  DISPAT_BIN="$dispat" "$dispat" release --package ccme-spec
  test "$(git rev-parse HEAD)" = "$before"
  printf '%s\n' 'editorial correction' >> specs/ccme-spec/change.txt
  git add .
  git commit -qm 'fix(ccme-spec): clarify wording'
  DISPAT_BIN="$dispat" "$dispat" release --package ccme-spec --require-release
  test "$(git show specs/ccme-spec/v2.0.1:specs/ccme-spec/VERSION)" = 2.0.1
  sh specs/ccme-spec/verify.sh
)

# Bad source declarations and symlinks must fail before replacement. A fully
# consistent but stale version must also fail the build rather than be tagged.
for problem in malformed symlink stale; do
  repo=$tmp/$problem
  repository "$repo"
  case "$problem" in
    malformed)
      sed 's/conforms to CCME 1.0.0 if and only if/missing conformance declaration/' \
        "$repo/specs/ccme-spec/SPEC.md" > "$repo/specs/ccme-spec/SPEC.new"
      mv "$repo/specs/ccme-spec/SPEC.new" "$repo/specs/ccme-spec/SPEC.md"
      ;;
    symlink)
      mv "$repo/specs/ccme-spec/VERSION" "$repo/specs/ccme-spec/real-version"
      ln -s real-version "$repo/specs/ccme-spec/VERSION"
      ;;
    stale) fixture "$repo/specs/ccme-spec" 9.0.0 ;;
  esac
  (
    cd "$repo"
    git add .
    git commit -qm 'test(ccme-spec): invalid input'
    before=$(cksum specs/ccme-spec/VERSION specs/ccme-spec/SPEC.md)
    if DISPAT_BIN="$dispat" "$dispat" release --package ccme-spec --require-release > "$tmp/$problem.log" 2>&1; then
      echo "$problem specification unexpectedly released" >&2
      exit 1
    fi
    test -z "$(git tag -l 'specs/ccme-spec/*')"
    test "$before" = "$(cksum specs/ccme-spec/VERSION specs/ccme-spec/SPEC.md)"
  )
done

for version in '1.0.0+build.01' '1.0.0+meta-01'; do
  fixture "$tmp/metadata" "$version"
  (cd "$tmp/metadata" && sh verify.sh)
done

echo "CCME specification release tests passed"
