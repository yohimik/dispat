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
  cp "$here/version.sh" "$here/verify.sh" "$here/LICENSE" "$dir/"
  printf '%s\n' "$version" > "$dir/VERSION"
  cat > "$dir/SPEC.md" <<EOF
**Version:** $version **Status:** Normative
**License:** GPL-3.0-or-later. See [LICENSE](./LICENSE).
An implementation conforms to CCME $version if and only if it:
This document is CCME **$version** and is itself versioned under SemVer:
EOF
}

success=$tmp/success
fixture "$success" 2.0.0
(cd "$success" && DISPAT_BIN="$dispat" DISPAT_NEW_VERSION=2.0.1 sh version.sh)
test "$(cat "$success/VERSION")" = 2.0.1
(cd "$success" && sh verify.sh)
(cd "$success" && DISPAT_BIN="$dispat" DISPAT_NEW_VERSION=2.0.1 sh version.sh)

metadata=$tmp/metadata
fixture "$metadata" '1.0.0+build.01'
(cd "$metadata" && sh verify.sh)
fixture "$tmp/metadata-hyphen" '1.0.0+meta-01'
(cd "$tmp/metadata-hyphen" && sh verify.sh)

malformed=$tmp/malformed
fixture "$malformed" 2.0.0
sed 's/conforms to CCME 2.0.0 if and only if/missing conformance declaration/' \
  "$malformed/SPEC.md" > "$malformed/SPEC.new"
mv "$malformed/SPEC.new" "$malformed/SPEC.md"
before=$(cksum "$malformed/VERSION" "$malformed/SPEC.md")
if (cd "$malformed" && DISPAT_BIN="$dispat" DISPAT_NEW_VERSION=2.0.1 sh version.sh >/dev/null 2>&1); then
  echo "malformed specification unexpectedly versioned" >&2
  exit 1
fi
test "$before" = "$(cksum "$malformed/VERSION" "$malformed/SPEC.md")"

linked=$tmp/linked
fixture "$linked" 2.0.0
mv "$linked/VERSION" "$linked/real-version"
ln -s real-version "$linked/VERSION"
if (cd "$linked" && DISPAT_BIN="$dispat" DISPAT_NEW_VERSION=2.0.1 sh version.sh >/dev/null 2>&1); then
  echo "symlinked VERSION unexpectedly versioned" >&2
  exit 1
fi
test "$(cat "$linked/real-version")" = 2.0.0

rollback=$tmp/rollback
fixture "$rollback" 2.0.0
before=$(cksum "$rollback/VERSION" "$rollback/SPEC.md")
mkdir "$tmp/bin"
cat > "$tmp/bin/cp" <<'EOF'
#!/bin/sh
case "$1:$2" in
  .ccme-version.*/SPEC.md:SPEC.md|*/.ccme-version.*/SPEC.md:SPEC.md) exit 73 ;;
  *) exec /bin/cp "$@" ;;
esac
EOF
chmod +x "$tmp/bin/cp"
if (cd "$rollback" && PATH="$tmp/bin:$PATH" DISPAT_BIN="$dispat" \
    DISPAT_NEW_VERSION=2.0.1 sh version.sh >/dev/null 2>&1); then
  echo "injected installation failure unexpectedly succeeded" >&2
  exit 1
fi
test "$before" = "$(cksum "$rollback/VERSION" "$rollback/SPEC.md")"
test -z "$(find "$rollback" -maxdepth 1 -type d -name '.ccme-version.*' -print -quit)"

# Exercise the release scheduler, not only version.sh in isolation. A
# standalone package has no provider updates, so it has no native version
# task; its beforeBuild hook must still stamp the exact planned version before
# the build verifier runs.
release=$tmp/release
mkdir -p "$release/specs/ccme-spec"
fixture "$release/specs/ccme-spec" 1.0.0
cp "$here/dispat.yaml" "$release/specs/ccme-spec/dispat.yaml"
cat > "$release/dispat.yaml" <<'EOF'
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
  cd "$release"
  git init -q -b main
  git config user.name 'CCME specification test'
  git config user.email 'ccme-spec-test@example.invalid'
  git add .
  git commit -qm 'chore(ccme-spec): establish baseline'
  printf '%s\n' 'breaking specification revision' > specs/ccme-spec/change.txt
  git add specs/ccme-spec/change.txt
  git commit -qm 'feat(ccme-spec)!: revise normative algorithm'
  DISPAT_BIN="$dispat" "$dispat" release --package ccme-spec --require-release
  test "$(cat specs/ccme-spec/VERSION)" = 2.0.0
  grep -Fq '**Version:** 2.0.0 **Status:**' specs/ccme-spec/SPEC.md
  test "$(git show specs/ccme-spec/v2.0.0:specs/ccme-spec/VERSION)" = 2.0.0
  git show specs/ccme-spec/v2.0.0:specs/ccme-spec/SPEC.md |
    grep -Fq '**Version:** 2.0.0 **Status:**'
)

echo "CCME specification release tests passed"
