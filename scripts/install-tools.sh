#!/bin/sh
# The tools a build or a release needs beside dispat itself, as one
# `dispat install` line each.
#
# This is the install manifest reference/ci.md and cli/install.md describe,
# written for this repository: the versions are pinned here and read from here
# by everything that installs them. The TinyGo toolchain stage of
# services/dispat/Dockerfile, the fork stage of Dockerfile.tinygo, the darwin
# half of the spike, the release workflow, the ping job and the replay
# workflow all call this file, so moving to a newer crier or a newer fork is
# bumping one number below and nothing else.
#
#   sh scripts/install-tools.sh                  every tool
#   sh scripts/install-tools.sh crier            one of them, by name
#   sh scripts/install-tools.sh --version crier  print a pin and install nothing
#
# The environment it reads:
#
#   INSTALL_TOOLS_DISPAT   the dispat to install with; without it, dispat on
#                          PATH. A build that has just produced a dispat of
#                          its own points this at that binary.
#   INSTALL_TOOLS_BIN_DIR  where a bare binary goes; without it, dispat's own
#                          resolution ($DISPAT_BIN_DIR, then /usr/local/bin
#                          when it is writable, then ~/.local/bin).
#   INSTALL_TOOLS_PREFIX   where a toolchain tree is unpacked; without it,
#                          /usr/local. A toolchain is a bin/ plus the lib/ and
#                          src/ beside it, so it lands under a prefix rather
#                          than as one file in a bin folder.
#   GITHUB_TOKEN           read by `dispat install` for the releases listing.
#                          Optional, and what keeps a shared runner IP off the
#                          unauthenticated rate limit.
set -eu

# The pins. Both are release versions of repositories this repository does not
# build, so there is nothing to derive them from and one place to state them.
CRIER_VERSION=1.0.0
TINYGO_FORK_VERSION=0.43.0-net.1

# Every tool this manifest knows, in the order a bare run installs them.
TOOLS="crier tinygo"

log() { printf 'install-tools: %s\n' "$*" >&2; }

die() {
	log "$*"
	exit 1
}

usage() {
	cat <<EOF
usage: install-tools.sh [--version] [tool...]

Installs the pinned tools; with no arguments, all of them.
Tools: $TOOLS
EOF
}

# The pin of one tool, on standard output. It is the same value the install
# below passes to --release, read from the same variable, so a caller that
# needs the number (a docs check, a cache key, a workflow) can never be told
# a different one than the install uses.
pin() {
	case $1 in
	crier) echo "$CRIER_VERSION" ;;
	tinygo) echo "$TINYGO_FORK_VERSION" ;;
	*) die "unknown tool $1; the manifest carries: $TOOLS" ;;
	esac
}

want_version=false
case ${1:-} in
-h | --help)
	usage
	exit 0
	;;
--version)
	want_version=true
	shift
	;;
esac

if [ "$want_version" = true ]; then
	[ $# -eq 1 ] || die "--version takes exactly one tool name"
	pin "$1"
	exit 0
fi

# No arguments is every tool, which is what a fresh machine wants and what
# keeps the list in one place rather than in each caller. The split is the
# point of the unquoted expansion here: TOOLS is a list of names, and the
# names are the shell words this loop wants.
# shellcheck disable=SC2086
[ $# -gt 0 ] || set -- $TOOLS

dispat_bin=${INSTALL_TOOLS_DISPAT:-dispat}
command -v "$dispat_bin" >/dev/null 2>&1 ||
	die "no dispat at $dispat_bin; https://dispat.dev/reference/ci/#the-install-script"

bin_dir=${INSTALL_TOOLS_BIN_DIR:-}
prefix=${INSTALL_TOOLS_PREFIX:-/usr/local}

# crier: one binary under the repository's own name, which is the spelling a
# bare `dispat install` resolves (crier-{os}-{arch}), so this line needs no
# --asset. Re-running it costs no transfer: dispat compares what is already
# at the destination against the checksum the release published and installs
# nothing when they agree.
install_crier() {
	set -- install yohimik/crier --release "$CRIER_VERSION"
	[ -z "$bin_dir" ] || set -- "$@" --bin-dir "$bin_dir"
	log "installing crier $CRIER_VERSION${bin_dir:+ into $bin_dir}"
	"$dispat_bin" "$@"
	log "installed crier $CRIER_VERSION"
}

# The TinyGo fork the release's two tiny linux binaries are built with. It is
# a toolchain tree rather than a binary, so --pipe 'tar -xz' unpacks the
# release's tarball into the prefix and the compiler lands at
# <prefix>/tinygo/bin/tinygo with its lib/ and src/ beside it.
#
# A --pipe install has no destination file to compare, so dispat cannot tell
# an installed toolchain from a missing one and would fetch 185 MB every run.
# What the tree reports is the check instead: the right version already there
# is nothing to do, and anything else is thrown away and fetched, because a
# half-unpacked tree is worse than no tree.
install_tinygo() {
	tinygo_bin=$prefix/tinygo/bin/tinygo
	if "$tinygo_bin" version 2>/dev/null | grep -qF "$TINYGO_FORK_VERSION"; then
		log "tinygo $TINYGO_FORK_VERSION is already at $prefix/tinygo"
		return 0
	fi
	rm -rf "$prefix/tinygo"
	# --pipe runs its command in --bin-dir, so the prefix has to be a folder
	# before the tarball is unpacked into it. A caller pointing at a cache
	# folder that does not exist yet is the ordinary case, not a mistake.
	mkdir -p "$prefix"
	log "installing tinygo $TINYGO_FORK_VERSION into $prefix"
	"$dispat_bin" install yohimik/tinygo \
		--prerelease --release "$TINYGO_FORK_VERSION" \
		--asset 'tinygo{version}.{os}-{arch}.tar.gz' \
		--bin-dir "$prefix" --pipe 'tar -xz'
	"$tinygo_bin" version | grep -qF "$TINYGO_FORK_VERSION" ||
		die "the installed tinygo does not report $TINYGO_FORK_VERSION"
	log "installed tinygo $TINYGO_FORK_VERSION"
}

for tool in "$@"; do
	case $tool in
	crier) install_crier ;;
	tinygo) install_tinygo ;;
	*) die "unknown tool $tool; the manifest carries: $TOOLS" ;;
	esac
done
