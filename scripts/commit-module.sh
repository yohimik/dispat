#!/bin/sh
# beforePublish of every module: commit the folder's release changes (the
# go.mod/go.sum the version stage rewrote), push the branch so the commit
# exists on the remote, and pin the module's tag to that commit through the
# PACKAGE_<KEY> export. Runs inside the package folder, serialised by the
# publish concurrency budget of 1.
set -eu

git add .
git diff --cached --quiet || git commit -q -m "chore(release): $DISPAT_PACKAGE $DISPAT_NEW_VERSION"
git push -q origin HEAD:main

KEY=$(printf '%s' "$DISPAT_PACKAGE" | tr '[:lower:]' '[:upper:]' | tr -c 'A-Z0-9' '_')
KEY=${KEY%_}
echo "PACKAGE_${KEY}=$(git rev-parse HEAD)" >> "$DISPAT_OUTPUT"
