#!/bin/sh
# Bootstrap the latest Aqua with Dispat, then install the repository's tool pins.
# Usage: install-tools.sh [all|crier|tinygo] [destination]
# Crier is copied as a standalone executable. TinyGo links to Aqua's complete
# toolchain tree, retaining the lib/ and src/ siblings its compiler requires.
set -eu

tool=${1:-all}
case "$tool" in all|crier|tinygo) ;; *) echo "unknown tool: $tool" >&2; exit 2 ;; esac
[ "$#" -le 2 ] || { echo "usage: $0 [all|crier|tinygo] [destination]" >&2; exit 2; }
root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
destination=${2:-${DISPAT_BIN_DIR:-"$HOME/.local/bin"}}
mkdir -p "$destination"
destination=$(CDPATH='' cd -- "$destination" && pwd)

"${DISPAT_BIN:-dispat}" install aquaproj/aqua \
  --asset 'aqua_{os}_{arch}.tar.gz' --bin-dir "$destination" --pipe 'tar -xz aqua'
aqua="$destination/aqua"
# The checked-in policy trusts only this repository's two local definitions.
export AQUA_POLICY_CONFIG="$root/.aqua/aqua-policy.yaml"
export AQUA_GITHUB_TOKEN="${AQUA_GITHUB_TOKEN:-${GITHUB_TOKEN:-}}"
config="$root/.aqua/aqua.yaml"
if [ "$tool" = all ]; then
  "$aqua" -c "$config" install
else
  "$aqua" -c "$config" install --tags "$tool"
fi

if [ "$tool" = all ] || [ "$tool" = crier ]; then
  "$aqua" -c "$config" cp --tags crier -o "$destination"
fi
if [ "$tool" = all ] || [ "$tool" = tinygo ]; then
  tinygo_executable=$("$aqua" -c "$config" which tinygo)
  tinygo_root=$(dirname "$(dirname "$tinygo_executable")")
  test -d "$tinygo_root/lib"
  test -d "$tinygo_root/src"
  if [ -e "$destination/tinygo" ] && [ ! -L "$destination/tinygo" ]; then
    echo "refusing to replace an existing TinyGo directory: $destination/tinygo" >&2
    exit 1
  fi
  ln -sfn "$tinygo_root" "$destination/tinygo"
fi
