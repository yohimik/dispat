#!/bin/sh
# Publish the coverage badge: force-push coverage/coverage.json to an orphan
# badges branch, a single commit with no history, the same mechanism
# deploy-docs.sh uses for the site. README references the file through the
# shields.io endpoint URL.
#
# Run from the repository root, after coverage-badge.sh has written the JSON.
# The guard that keeps it inside CI is the `push-badge` script in dispat.yaml,
# which reaches this file only when `dispat if CI!=true` says otherwise.
set -eu

: "${GITHUB_TOKEN:?GITHUB_TOKEN is required to push the badges branch}"
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required to push the badges branch}"

rm -rf badge && mkdir badge && cp coverage/coverage.json badge/
cd badge
git init -q -b badges
git config user.name "github-actions[bot]"
git config user.email "github-actions[bot]@users.noreply.github.com"
git add coverage.json
git commit -qm "chore: update coverage badge"
git push -f "https://x-access-token:${GITHUB_TOKEN}@github.com/${GITHUB_REPOSITORY}.git" badges
echo "pushed the coverage badge to the badges branch"
