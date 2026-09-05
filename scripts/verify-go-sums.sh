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

root=$(git -C "$(dirname "$mod")" rev-parse --show-toplevel)
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
  local_path=
  case "$module $version" in
    "github.com/yohimik/dispat/pkg/ccme/v2 v2.0.0-0") local_path=./pkg/ccme ;;
    "github.com/yohimik/dispat/pkg/models/v2 v2.0.0-0") local_path=./pkg/models ;;
  esac
  if [ -n "$local_path" ]; then
    if [ "$phase" = post ]; then
      echo "$mod still requires the unpublished placeholder $module $version" >&2
      exit 1
    fi
    if ! awk -v m="$module" -v v="$version" -v p="$local_path" '
      $1 == "replace" && $2 == m && $3 == v && $4 == "=>" && $5 == p { found = 1 }
      END { exit !found }
    ' "$root/go.work"; then
      echo "$mod uses $module $version without the exact local go.work replacement" >&2
      exit 1
    fi
    continue
  fi
  if ! grep -Fq "$module $version " "$sum" && ! grep -Fq "$module $version/go.mod " "$sum"; then
    echo "$sum has no checksum for required module $module $version" >&2
    exit 1
  fi
done < "$list"
