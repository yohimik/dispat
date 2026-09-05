#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
# Copyright (c) 2026 yohimik
set -eu

cd "$(dirname "$0")"

if [ -L VERSION ] || [ -L SPEC.md ]; then
  echo "refusing to verify a symlinked VERSION or SPEC.md" >&2
  exit 1
fi

version=$(cat VERSION)
case "$version" in
  ''|*[!0-9A-Za-z.+-]*)
    echo "VERSION is not a semantic version: $version" >&2
    exit 1
    ;;
esac
if ! printf '%s\n' "$version" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'; then
  echo "VERSION is not a semantic version: $version" >&2
  exit 1
fi
without_build=${version%%+*}
prerelease=${without_build#*-}
if [ "$prerelease" != "$without_build" ]; then
  if ! printf '%s\n' "$prerelease" | awk -F. '{
    for (i = 1; i <= NF; i++)
      if ($i ~ /^[0-9]+$/ && length($i) > 1 && substr($i, 1, 1) == "0") exit 1
  }'; then
    echo "VERSION has a numeric prerelease identifier with a leading zero: $version" >&2
    exit 1
  fi
fi

require_once() {
  label=$1
  text=$2
  count=$(grep -F -c "$text" SPEC.md || true)
  if [ "$count" -ne 1 ]; then
    echo "SPEC.md must contain exactly one $label for $version (found $count)" >&2
    exit 1
  fi
}

require_once "document header" "**Version:** $version **Status:**"
require_once "conformance declaration" "conforms to CCME $version if and only if"
require_once "versioning declaration" "This document is CCME **$version**"
require_once "local license link" "**License:** GPL-3.0-or-later. See [LICENSE](./LICENSE)."

grep -Fq 'GNU GENERAL PUBLIC LICENSE' LICENSE
grep -Fq 'Version 3, 29 June 2007' LICENSE
grep -Fq 'changing it is not allowed.' LICENSE

echo "CCME specification $version is internally consistent"
