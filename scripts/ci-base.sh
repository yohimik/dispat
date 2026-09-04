#!/bin/sh
set -eu

root=${CI_REPOSITORY:-$(git rev-parse --show-toplevel)}
cd "$root"

case ${GITHUB_EVENT_NAME:-local} in
  pull_request)
    # Actions tests a merge commit. A branch checkout has no trustworthy
    # base parent and must use the full sweep.
    git rev-parse --verify HEAD^2 >/dev/null 2>&1 || { echo all; exit 0; }
    candidate=HEAD^1 ;;
  push) candidate=${GITHUB_EVENT_BEFORE:-} ;;
  *) candidate=${CI_BASE_REVISION:-HEAD^} ;;
esac
case $candidate in
  ''|0000000000000000000000000000000000000000) echo all; exit 0 ;;
esac
if ! git cat-file -e "$candidate^{commit}" 2>/dev/null; then
  echo all
  exit 0
fi
if ! git merge-base --is-ancestor "$candidate" HEAD; then
  echo all
  exit 0
fi

# Check the whole event range: a shared change may be the first of several
# pushed commits rather than HEAD itself.
if git diff --name-only "$candidate" HEAD | grep -Eq \
  '^(README\.md$|\.github/workflows/|Dockerfile\.gotest($|\.)|\.dockerignore$|go\.work(\.sum)?$|dispat\.yaml$|scripts/|tools/testreport/|packages/docs/demo/fixtures/)'; then
  echo all
else
  echo "$candidate"
fi
