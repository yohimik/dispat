#!/bin/sh
# The dispat package's announce stage: publish the tags the GitHub Action is
# consumed through.
#
# The action at the repository root is a wrapper over install.sh and has no
# version of its own — what it installs is the CLI, so the CLI's version is the
# only honest number to give it. Consumers write `uses: yohimik/dispat@v1`,
# which is the convention every action follows and the only ref shape the
# Marketplace accepts, so these tags are bare `v1.4.2` and `v1` rather than
# carrying one of this repository's path prefixes.
#
# `v1` is force-moved on every stable release. That is the deal a major tag
# makes: it means "the newest 1.x", and nothing else in this repository points
# at it.
#
# This script is a stand-in for a dispat feature that does not exist yet: a
# package emitting more than one tag per release. With it, the whole of this
# file is configuration —
#
#	"dispat": {
#	  "tagFormat": "services/{name}/v{version}",
#	  "aliasTags": [
#	    { "format": "v{version}" },
#	    { "format": "v{major}", "moving": true, "channels": ["stable"] }
#	  ]
#	}
#
# — and the announce slot goes away with it. Two things that shape needs beyond
# the templating: `moving` (force-create and force-push, where every tag dispat
# writes today is an immutable record), and alias tags being *write-only*, never
# matched when baselines are read back — a bare `v1.4.2` must not become some
# package's history.
#
# Unlike the images' channel files, this cannot be written speculatively:
# dispat rejects unknown configuration keys, so an `aliasTags` block would stop
# every command until the feature lands.
set -eu

# Pushed tags are the only record this leg ran, and a developer's checkout is
# not where the action's public refs should come from. Same guard as
# deploy-docs.sh.
if [ "${CI:-}" != "true" ]; then
	echo "refusing to tag outside CI (set CI=true to override)" >&2
	exit 1
fi
: "${DISPAT_NEW_VERSION:?DISPAT_NEW_VERSION is required}"

# A prerelease gets its exact tag so `uses: yohimik/dispat@v1.5.0-rc.1` works for
# anyone testing one, but never the major: `v1` must not start resolving to a
# release candidate because one happened to go out last.
VERSION_TAG="v${DISPAT_NEW_VERSION}"
git tag -f "$VERSION_TAG"
git push -f origin "refs/tags/${VERSION_TAG}"
echo "tagged ${VERSION_TAG}"

if [ "${DISPAT_IS_PRERELEASE:-}" = "true" ]; then
	echo "prerelease ${DISPAT_NEW_VERSION}: leaving the major tag where it is"
	exit 0
fi

MAJOR_TAG="v${DISPAT_NEW_VERSION%%.*}"
git tag -f "$MAJOR_TAG"
git push -f origin "refs/tags/${MAJOR_TAG}"
echo "moved ${MAJOR_TAG} to ${DISPAT_NEW_VERSION}"
