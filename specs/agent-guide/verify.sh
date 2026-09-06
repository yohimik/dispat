#!/bin/sh
# SPDX-License-Identifier: GPL-3.0-or-later
# Copyright (c) 2026 yohimik
set -eu

cd "$(dirname "$0")"

for file in VERSION README.md LICENSE; do
  if [ -L "$file" ] || [ ! -f "$file" ]; then
    echo "refusing to verify symlinked or non-regular $file" >&2
    exit 1
  fi
done

version=$(cat VERSION)
if [ "${DISPAT_STAGE:-}" = build ] && [ -n "${DISPAT_NEW_VERSION:-}" ] && [ "$version" != "$DISPAT_NEW_VERSION" ]; then
  echo "agent guide version $version does not match planned release $DISPAT_NEW_VERSION" >&2
  exit 1
fi
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
  count=$(grep -F -x -c "$text" README.md || true)
  if [ "$count" -ne 1 ]; then
    echo "README.md must contain exactly one $label for $version (found $count)" >&2
    exit 1
  fi
}

require_once "version header" "**Version:** $version"
require_once "local license link" "**License:** GPL-3.0-or-later. See [LICENSE](./LICENSE)."
# Reject an extra version header even if it names a different version.
[ "$(grep -c '^\*\*Version:\*\* ' README.md)" -eq 1 ]
grep -Fq 'GNU GENERAL PUBLIC LICENSE' LICENSE
echo "Agent guide $version is internally consistent"
