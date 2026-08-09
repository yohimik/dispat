#!/bin/sh
# beforePublish of every module, serialised by the publish budget of 1:
#
#  1. commit the folder's release changes (the go.mod/go.sum the version
#     stage rewrote) and push the branch, so the commit exists on origin;
#  2. push a remote-only tag at that commit, so the consumers scheduled
#     behind this module can `go mod tidy` against it mid-run;
#  3. pin the module's finalize-phase annotated tag and its GitHub release
#     to the same commit through the PACKAGE_<KEY> export. The finalize
#     push then finds the tag already on origin and skips it, by design.
#  4. opt the module into a GitHub release; a package whose earlier stage
#     already exported assets (the CLI's build) keeps them.
set -eu

git add .
git diff --cached --quiet || git commit -q -m "chore(release): $DISPAT_PACKAGE $DISPAT_NEW_VERSION"
git push -q origin HEAD:main
git push -q origin "HEAD:refs/tags/$DISPAT_TAG"

KEY=$(printf '%s' "$DISPAT_PACKAGE" | tr '[:lower:]' '[:upper:]' | tr -c 'A-Z0-9' '_')
KEY=${KEY%_}
echo "PACKAGE_${KEY}=$(git rev-parse HEAD)" >> "$DISPAT_OUTPUT"

if [ -z "${DISPAT_EXPORT_GITHUB+x}" ]; then
  echo "DISPAT_EXPORT_GITHUB=" >> "$DISPAT_OUTPUT"
fi
