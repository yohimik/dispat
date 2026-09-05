#!/bin/sh
set -eu

if [ "$#" -lt 2 ] || [ "$#" -gt 3 ]; then
  echo "usage: verify-go-sums.sh <go.mod> <go.sum> [pre|post]" >&2
  exit 2
fi

mod=$1
sum=$2
phase=${3:-pre}
case "$phase" in
  pre|post) ;;
  *) echo "verify-go-sums.sh: phase must be pre or post" >&2; exit 2 ;;
esac

list=$(mktemp "${TMPDIR:-/tmp}/dispat-go-requires.XXXXXX")
trap 'rm -f "$list"' EXIT
trap 'exit 1' HUP INT TERM

# Emit the internal module path and version from both legal require forms.
awk '
  $1 == "require" && $2 == "(" { block = 1; next }
  block && $1 == ")" { block = 0; next }
  $1 == "require" && $2 ~ /^github.com\/yohimik\/dispat\/pkg\// { print $2, $3; next }
  block && $1 ~ /^github.com\/yohimik\/dispat\/pkg\// { print $1, $2 }
' "$mod" > "$list"

while read -r module version; do
  [ -n "$module" ] || continue
  if ! grep -Fq "$module $version " "$sum" && ! grep -Fq "$module $version/go.mod " "$sum"; then
    echo "$sum has no checksum for required module $module $version" >&2
    exit 1
  fi
done < "$list"
