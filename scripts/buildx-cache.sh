#!/bin/sh
# The one place the CI cache backend is decided. Every dispat script that runs
# a buildx build asks this helper for its cache flags, so switching backends
# (say, to type=registry when the repository's GitHub cache budget thrashes)
# is an edit here and nowhere else.
#
# The first argument is the scope to read and update. Further arguments are
# read-only scopes: aggregate targets use them to reuse the package gates'
# layers without folding every package into one cache entry. Outside Actions
# only TEST_COMMIT is printed; a local build uses the builder's own cache.
#
# The gha backend authenticates through ACTIONS_RUNTIME_TOKEN and
# ACTIONS_RESULTS_URL, which the runner hands only to action steps; the
# workflows export them into run steps with crazy-max/ghaction-github-runtime
# before any dispat command runs.
set -eu
scope=$1
shift
commit=${GITHUB_SHA:-$(git rev-parse HEAD)}
printf '%s' "--build-arg TEST_COMMIT=$commit"
[ "${GITHUB_ACTIONS:-}" = "true" ] || exit 0
printf '%s' " --cache-from type=gha,scope=$scope"
for import_scope in "$@"; do
  printf '%s' " --cache-from type=gha,scope=$import_scope"
done
printf '%s' " --cache-to type=gha,scope=$scope,mode=max"
