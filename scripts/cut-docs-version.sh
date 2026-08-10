#!/bin/sh
# Version stage for the `docs` package: freeze the current docs as a
# Docusaurus version whenever the CLI reaches a new stable minor.
#
# `docs` currently versions independently, on its own train. The intent is to
# join it to `dispat` in a fixed version group so the snapshot cannot be named
# anything other than the release it documents; until then the name comes from
# whatever version this package is releasing.
set -eu

# Prereleases never get a snapshot. The rc train runs long (1.0.0-rc.0 ..
# rc.N) and every one of them would freeze another full copy of the docs.
if [ "${DISPAT_IS_PRERELEASE:-false}" = "true" ]; then
  echo "prerelease ${DISPAT_NEW_VERSION}: no docs snapshot"
  exit 0
fi

# Snapshot per minor, not per patch: 1.2.3 and 1.2.4 document the same
# surface, and a copy each would grow the repository for nothing.
MINOR="${DISPAT_VERSION%.*}"

if [ -d "versioned_docs/version-${MINOR}" ]; then
  echo "docs version ${MINOR} already cut"
  exit 0
fi

npm ci
npm run docusaurus docs:version "${MINOR}"
echo "cut docs version ${MINOR} (from ${DISPAT_NEW_VERSION})"
