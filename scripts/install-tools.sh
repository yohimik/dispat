#!/bin/sh
# Compatibility entry point for CI, Docker, and local installs.
# Tool pins and installation policy live in tools/bootstrap.yaml.
set -eu
[ "$#" -le 2 ] || { echo "usage: $0 [all|crier|tinygo] [destination]" >&2; exit 2; }
export DISPAT_TOOL_SELECTION="${1:-all}"
case "$DISPAT_TOOL_SELECTION" in all|crier|tinygo) ;; *) echo "unknown tool: $DISPAT_TOOL_SELECTION" >&2; exit 2 ;; esac
# An argument that is there says where to install, so an empty one is a
# caller whose variable did not expand rather than a caller asking for the
# default. Refusing it here is the difference between a clear error and a
# silent install into somewhere nobody named.
if [ "$#" -ge 2 ] && [ -z "$2" ]; then
  echo "empty destination: pass a folder, or no second argument for the default" >&2
  exit 2
fi
if [ -n "${DISPAT_BIN_DIR+set}" ] && [ -z "${DISPAT_BIN_DIR}" ]; then
  echo "DISPAT_BIN_DIR is set but empty: unset it to take the default" >&2
  exit 2
fi
# Resolve a relative destination before exec changes into the tooling folder.
destination=${2:-${DISPAT_BIN_DIR:-"$HOME/.local/bin"}}
case "$destination" in /*) ;; *) destination="$PWD/$destination" ;; esac
export DISPAT_TOOL_DESTINATION="$destination"
root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
exec "${DISPAT_BIN:-dispat}" --root "$root" --config tools/bootstrap.yaml exec install-tools --in root
