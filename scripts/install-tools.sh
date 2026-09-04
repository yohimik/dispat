#!/bin/sh
# The tools the release flow needs beside dispat itself, each at its newest
# release. Nothing is pinned here on purpose: a run takes the crier and the
# TinyGo fork their repositories published last, verified by dispat against
# the digest GitHub carries for the asset. To install one tool, or into a
# particular folder, run its line by hand with the flags it needs; without
# --bin-dir, dispat's own ladder decides where a tool lands (DISPAT_BIN_DIR,
# then /usr/local/bin when it is writable, then ~/.local/bin).
#
# The fork ships prereleases only and a tarball rather than a binary, so its
# line says so: --prerelease admits them, and --pipe unpacks the whole
# toolchain tree (bin/ plus the lib/ and src/ beside it) into the folder a
# binary would have landed in, as <folder>/tinygo/bin/tinygo.
set -eu
dispat install yohimik/tinygo --prerelease --asset 'tinygo{version}.{os}-{arch}.tar.gz' --pipe 'tar -xz'
dispat install yohimik/crier
