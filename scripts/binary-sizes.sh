#!/bin/sh
# Measure release artifacts after compilation; never estimate sizes from source.
# Usage: binary-sizes.sh directory version source-commit go-version tinygo-version
set -eu
[ "$#" -eq 5 ] || { echo "usage: $0 directory version source-commit go-version tinygo-version" >&2; exit 2; }
directory=$1
version=$2
revision=$3
go_version=$4
tinygo_version=$5
case "$revision" in *[!0-9a-f]*|'') echo "source commit must be 40 lowercase hex characters" >&2; exit 2 ;; esac
[ "${#revision}" -eq 40 ] || { echo "source commit must be 40 lowercase hex characters" >&2; exit 2; }

rows=$(mktemp)
trap 'rm -f "$rows"' EXIT HUP INT TERM
for compiler in go tinygo; do
  case "$compiler" in
    go) targets='linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64'; prefix=dispat ;;
    tinygo) targets='linux/amd64 linux/arm64'; prefix=dispat-tiny ;;
  esac
  for target in $targets; do
    system=${target%/*}
    arch=${target#*/}
    case "$system" in windows) suffix=.exe ;; *) suffix= ;; esac
    name="$prefix-$system-$arch$suffix"
    file="$directory/$name"
    if [ ! -f "$file" ] || [ -L "$file" ] || [ ! -s "$file" ]; then
      echo "missing release binary: $file" >&2
      exit 1
    fi
    bytes=$(wc -c < "$file" | tr -d ' ')
    digest=$(sha256sum "$file"); digest=${digest%% *}
    jq -cn --arg name "$name" --arg os "$system" --arg arch "$arch" \
      --arg compiler "$compiler" --argjson bytes "$bytes" --arg sha256 "$digest" \
      '{name:$name,os:$os,arch:$arch,compiler:$compiler,bytes:$bytes,sha256:$sha256}' >> "$rows"
  done
done
jq -s --arg version "$version" --arg revision "$revision" \
  --arg go "$go_version" --arg tinygo "$tinygo_version" \
  '{schemaVersion:1,version:$version,sourceCommit:$revision,toolchains:{go:$go,tinygo:$tinygo},binaries:.}' "$rows"
