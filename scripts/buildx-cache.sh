#!/bin/sh
# The one place the CI cache backend is decided. Every dispat script that runs
# a buildx build asks this helper for its cache flags, so switching backends
# (say, to type=registry when the repository's GitHub cache budget thrashes)
# is an edit here and nowhere else.
#
# Takes one argument, the scope, so each build target keeps its own cache
# entry and a fat target cannot evict a cheap one. Outside Actions it prints
# nothing: a local build uses the builder's own cache, which is faster than
# any remote backend and needs no credentials.
#
# The gha backend authenticates through ACTIONS_RUNTIME_TOKEN and
# ACTIONS_RESULTS_URL, which the runner hands only to action steps; the
# workflows export them into run steps with crazy-max/ghaction-github-runtime
# before any dispat command runs.
set -eu
scope=$1
commit=${GITHUB_SHA:-$(git rev-parse HEAD)}
printf '%s' "--build-arg TEST_COMMIT=$commit"
[ "${GITHUB_ACTIONS:-}" = "true" ] || exit 0
printf '%s' " --cache-from type=gha,scope=$scope --cache-to type=gha,scope=$scope,mode=max"
