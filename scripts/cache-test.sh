#!/bin/sh
set -eu
root=${1:-$(git rev-parse --show-toplevel)}
dockerfile=$root/Dockerfile.gotest

# TEST_COMMIT must not enter the shared base or any test layer before the
# command it certifies. This static contract protects the cross-commit cache
# boundary; the Docker build probe exercises the same ordering by hand.
base=$(sed -n '/^FROM golang:.* AS base$/,/^FROM base AS deps$/p' "$dockerfile")
printf '%s\n' "$base" | grep -q TEST_COMMIT && { echo 'TEST_COMMIT invalidates the shared base' >&2; exit 1; }
for name in ccme config manifest models scanner writer tools dispat integration; do
  section=$(sed -n "/ AS test-$name\$/,/^FROM .* AS /p" "$dockerfile")
  run=$(printf '%s\n' "$section" | grep -n 'go run github.com/yohimik/dispat/tools/testreport test' | head -1 | cut -d: -f1)
  arg=$(printf '%s\n' "$section" | grep -n '^ARG TEST_COMMIT$' | head -1 | cut -d: -f1)
  stamp=$(printf '%s\n' "$section" | grep -n "$name.commit" | tail -1 | cut -d: -f1)
  if ! { [ -n "$run" ] && [ -n "$arg" ] && [ -n "$stamp" ] &&
    [ "$run" -lt "$arg" ] && [ "$arg" -lt "$stamp" ]; }; then
    echo "test-$name does not measure before its commit attestation" >&2
    exit 1
  fi
done

flags=$(GITHUB_ACTIONS=true GITHUB_SHA=commit sh "$root/scripts/buildx-cache.sh" measured gotest-a gotest-b)
case "$flags" in
  *'--build-arg TEST_COMMIT=commit'*'--cache-from type=gha,scope=measured'*'--cache-from type=gha,scope=gotest-a'*'--cache-from type=gha,scope=gotest-b'*'--cache-to type=gha,scope=measured,mode=max'*) ;;
  *) echo "aggregate cache flags are incomplete: $flags" >&2; exit 1 ;;
esac
echo 'Docker cache layout passed'
