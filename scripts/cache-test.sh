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

# The integration suite's key is the production code, not the checkout: its
# source stage takes pkg and services/dispat only from the pruned stage, never
# from the context and never through src-dispat, and the prune removes the
# unit tests. A unit-test or changelog commit then replays the suite. The one
# file it names under services/dispat is the release Dockerfile, which the
# harness reads to hold the TinyGo fixture stage to its version list.
stage() { sed -n "/^FROM .* AS $1\$/,/^FROM .* AS /p" "$dockerfile" | sed '$d'; }
integration=$(stage src-integration)
printf '%s\n' "$integration" | head -1 | grep -q '^FROM runner AS src-integration$' ||
  { echo 'src-integration must build on runner, not on a stage that copies the checkout' >&2; exit 1; }
if printf '%s\n' "$integration" | grep '^COPY' | grep -v -- '--from=src-production' |
  grep -v ' services/dispat/Dockerfile ./services/dispat/Dockerfile$' |
  grep -Eq '[[:space:]](\./)?(pkg|services/dispat)(/|[[:space:]])'; then
  echo 'src-integration copies pkg or services/dispat from the build context' >&2
  exit 1
fi
for tree in pkg services/dispat; do
  printf '%s\n' "$integration" | grep -q "^COPY --from=src-production .* /src/$tree ./$tree\$" ||
    { echo "src-integration does not take $tree from src-production" >&2; exit 1; }
done
for pruned in src-production tools-production; do
  printf '%s\n' "$(stage "$pruned")" | grep -q "find .*-name '\*_test.go'.*-delete" ||
    { echo "$pruned does not delete the *_test.go files" >&2; exit 1; }
done
stage runner | grep -q '^COPY --from=tools-production ' ||
  { echo 'runner does not take the tooling from tools-production' >&2; exit 1; }

flags=$(GITHUB_ACTIONS=true GITHUB_SHA=commit sh "$root/scripts/buildx-cache.sh" measured gotest-a gotest-b)
case "$flags" in
  *'--build-arg TEST_COMMIT=commit'*'--cache-from type=gha,scope=measured'*'--cache-from type=gha,scope=gotest-a'*'--cache-from type=gha,scope=gotest-b'*'--cache-to type=gha,scope=measured,mode=max'*) ;;
  *) echo "aggregate cache flags are incomplete: $flags" >&2; exit 1 ;;
esac
echo 'Docker cache layout passed'
