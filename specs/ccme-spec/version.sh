#!/bin/sh
set -eu

cd "$(dirname "$0")"
new=${DISPAT_NEW_VERSION:?DISPAT_NEW_VERSION is required by the version stage}

if [ -L VERSION ] || [ -L SPEC.md ]; then
  echo "refusing to version a symlinked VERSION or SPEC.md" >&2
  exit 1
fi

sh verify.sh
old=$(cat VERSION)
if [ "$old" = "$new" ]; then
  exit 0
fi

stage=$(mktemp -d .ccme-version.XXXXXX)
committed=false
write_started=false
cleaned=false
cleanup() {
  [ "$cleaned" = false ] || return
  cleaned=true
  keep=false
  if [ "$committed" != true ] && [ "$write_started" = true ]; then
    restore_failed=false
    if ! cp "$stage/original.VERSION" VERSION; then
      restore_failed=true
    fi
    if ! cp "$stage/original.SPEC.md" SPEC.md; then
      restore_failed=true
    fi
    if [ "$restore_failed" = true ]; then
      echo "rollback failed; original files are preserved in $stage" >&2
      keep=true
    fi
  fi
  if [ "$keep" = false ]; then
    rm -rf "$stage"
  fi
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

cp VERSION "$stage/original.VERSION"
cp SPEC.md "$stage/original.SPEC.md"
cp VERSION SPEC.md LICENSE verify.sh "$stage/"

(
  cd "$stage"
  "${DISPAT_BIN:-dispat}" replacer VERSION --strict --replace "$old=>$new"
  "${DISPAT_BIN:-dispat}" replacer SPEC.md --strict \
    --replace "**Version:** $old **Status:**=>**Version:** $new **Status:**" \
    --replace "conforms to CCME $old if and only if=>conforms to CCME $new if and only if" \
    --replace "This document is CCME **$old**=>This document is CCME **$new**"
  sh verify.sh
)

write_started=true
cp "$stage/VERSION" VERSION
cp "$stage/SPEC.md" SPEC.md
committed=true
