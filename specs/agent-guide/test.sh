#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
# Copyright (c) 2026 yohimik
set -eu

here=$(CDPATH='' cd -- "$(dirname "$0")" && pwd)
sh "$here/verify.sh"
dispat=${DISPAT_BIN:-$(command -v dispat || true)}
if [ -z "$dispat" ] || [ ! -x "$dispat" ]; then
  echo "native dispat binary is required on PATH or in DISPAT_BIN" >&2
  exit 1
fi
case "$dispat" in
  /*) ;;
  *) dispat=$(CDPATH='' cd -- "$(dirname "$dispat")" && pwd)/$(basename "$dispat") ;;
esac

tmp=$(mktemp -d "${TMPDIR:-/tmp}/agent-guide-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
trap 'exit 1' HUP INT TERM

fixture() {
  dir=$1
  version=$2
  mkdir -p "$dir"
  cp "$here/verify.sh" "$here/LICENSE" "$here/dispat.yaml" "$dir/"
  printf '%s\n' "$version" > "$dir/VERSION"
  cat > "$dir/README.md" <<EOF
**Version:** $version
**License:** GPL-3.0-or-later. See [LICENSE](./LICENSE).
Example version 1.0.0 is not a release declaration.
EOF
}

# The production package config must drive the real release scheduler and
# commit the replacements, without scanning or rewriting unrelated manifests.
repository() {
  repo=$1
  fixture "$repo/specs/agent-guide" 0.0.0
  printf '%s\n' '{"name":"agent-guide","version":"1.0.0"}' > "$repo/specs/agent-guide/package.json"
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
initials:
  agent-guide: 0.0.0
packages:
  agent-guide:
    path: specs/agent-guide
  unrelated:
    path: unrelated
EOF
  (
    cd "$repo"
    mkdir unrelated
    printf '%s\n' keep > unrelated/README.md
    git init -q -b main
    git config user.name 'Agent guide test'
    git config user.email 'agent-guide-test@example.invalid'
    git add .
    git commit -qm 'chore(agent-guide): establish baseline'
    printf '%s\n' 'initial agent guide' > specs/agent-guide/change.txt
    git add .
    git commit -qm 'release(agent-guide): publish initial guide' -m 'Release-As: 1.0.0'
  )
}

release=$tmp/release
repository "$release"
(
  cd "$release"
  DISPAT_BIN="$dispat" "$dispat" release --package agent-guide --require-release
  test "$(git show specs/agent-guide/v1.0.0:specs/agent-guide/VERSION)" = 1.0.0
  git show specs/agent-guide/v1.0.0:specs/agent-guide/README.md |
    grep -Fq '**Version:** 1.0.0'
  grep -Fq 'Example version 1.0.0' specs/agent-guide/README.md
  test "$(cat specs/agent-guide/package.json)" = '{"name":"agent-guide","version":"1.0.0"}'
  before=$(git rev-parse HEAD)
  DISPAT_BIN="$dispat" "$dispat" release --package agent-guide
  test "$(git rev-parse HEAD)" = "$before"
  printf '%s\n' 'editorial correction' >> specs/agent-guide/change.txt
  git add .
  git commit -qm 'fix(agent-guide): clarify wording'
  DISPAT_BIN="$dispat" "$dispat" release --package agent-guide --require-release
  test "$(git show specs/agent-guide/v1.0.1:specs/agent-guide/VERSION)" = 1.0.1
  sh specs/agent-guide/verify.sh
  test -z "$(git tag -l 'unrelated*')"
  test "$(cat unrelated/README.md)" = keep
)

# Bad source declarations and symlinks must fail before replacement. A fully
# consistent but stale version must also fail the build rather than be tagged.
for problem in malformed symlink readme-symlink license-symlink stale prefix duplicate; do
  repo=$tmp/$problem
  repository "$repo"
  case "$problem" in
    malformed)
      sed 's/Version:/Missing version:/' \
        "$repo/specs/agent-guide/README.md" > "$repo/specs/agent-guide/README.new"
      mv "$repo/specs/agent-guide/README.new" "$repo/specs/agent-guide/README.md"
      ;;
    symlink)
      mv "$repo/specs/agent-guide/VERSION" "$repo/specs/agent-guide/real-version"
      ln -s real-version "$repo/specs/agent-guide/VERSION"
      ;;
    readme-symlink|license-symlink)
      file=README.md
      [ "$problem" != license-symlink ] || file=LICENSE
      mv "$repo/specs/agent-guide/$file" "$repo/specs/agent-guide/real-$file"
      ln -s "real-$file" "$repo/specs/agent-guide/$file"
      ;;
    stale) fixture "$repo/specs/agent-guide" 9.0.0 ;;
    prefix) sed 's/0.0.0/0.0.01/' "$repo/specs/agent-guide/README.md" > "$repo/changed.md"; mv "$repo/changed.md" "$repo/specs/agent-guide/README.md" ;;
    duplicate) printf '\n**Version:** 9.0.0\n' >> "$repo/specs/agent-guide/README.md" ;;
  esac
  (
    cd "$repo"
    git add .
    git commit -qm 'test(agent-guide): invalid input'
    before=$(cksum specs/agent-guide/VERSION specs/agent-guide/README.md)
    if DISPAT_BIN="$dispat" "$dispat" release --package agent-guide --require-release > "$tmp/$problem.log" 2>&1; then
      echo "$problem specification unexpectedly released" >&2
      exit 1
    fi
    test -z "$(git tag -l 'specs/agent-guide/*')"
    test "$before" = "$(cksum specs/agent-guide/VERSION specs/agent-guide/README.md)"
  )
done

for version in '1.0.0+build.01' '1.0.0+meta-01'; do
  fixture "$tmp/metadata" "$version"
  (cd "$tmp/metadata" && sh verify.sh)
done

echo "Agent guide release tests passed"
