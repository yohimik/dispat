#!/bin/sh
# Install the dispat CLI from its GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/yohimik/dispat/main/install.sh | sh
#   wget -qO- https://raw.githubusercontent.com/yohimik/dispat/main/install.sh | sh
#
# One script for every Unix: the release binaries are static (CGO_ENABLED=0, see
# services/dispat/Dockerfile), so there is nothing to compile and nothing to link
# against, and the only tools this needs are the ones every base image already
# has: a POSIX shell and either curl or wget.
#
# The Docker images under docker/ run this same file rather than a copy of its
# logic, which is why it takes --os/--arch/--bin-dir: a cross-built image knows
# its target platform from $TARGETARCH and should not have to trust `uname`
# under emulation.
#
# Output contract: the resolved version goes to stdout, alone, and everything
# written for a human goes to stderr. `version=$(sh install.sh)` is therefore
# the whole of what the GitHub Action needs to report what it installed.
set -eu

OWNER="yohimik"
REPO="dispat"
# The CLI's tag prefix. This repository publishes a GitHub release per module,
# so /releases/latest is as likely to be a library as the CLI and the listing
# has to be filtered by tag instead. Mirrors selfupdate.DefaultTagPrefix.
TAG_PREFIX="services/dispat/v"
API_URL="${DISPAT_API_URL:-https://api.github.com}"
DOWNLOAD_URL="${DISPAT_DOWNLOAD_URL:-https://github.com}"

VERSION="${DISPAT_VERSION:-}"
BIN_DIR="${DISPAT_BIN_DIR:-}"
TOKEN="${GITHUB_TOKEN:-}"
OS=""
ARCH=""

log() { printf '%s\n' "$*" >&2; }
die() { printf 'install.sh: %s\n' "$*" >&2; exit 1; }

usage() {
	cat >&2 <<'EOF'
Install the dispat CLI.

Usage: install.sh [options]

Options:
  --version <v>    Version or tag to install: 1.2.3, v1.2.3 or
                   services/dispat/v1.2.3. Default: the latest stable release.
  --bin-dir <dir>  Where to install. Default: /usr/local/bin when writable,
                   otherwise $HOME/.local/bin.
  --os <os>        linux, darwin or windows. Default: this machine's.
  --arch <arch>    amd64 or arm64. Default: this machine's.
  --token <token>  Token for the releases API. A public repository needs none,
                   where it only raises the rate limit; a private one needs it
                   both to read the releases and to download the binary.
                   Default: $GITHUB_TOKEN.
  --help           This text.

Environment: DISPAT_VERSION, DISPAT_BIN_DIR and GITHUB_TOKEN are read as the
defaults of the matching options.

The installed version is printed to stdout; everything else goes to stderr.
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
	--version) [ $# -ge 2 ] || die "--version needs a value"; VERSION="$2"; shift 2 ;;
	--bin-dir) [ $# -ge 2 ] || die "--bin-dir needs a value"; BIN_DIR="$2"; shift 2 ;;
	--os) [ $# -ge 2 ] || die "--os needs a value"; OS="$2"; shift 2 ;;
	--arch) [ $# -ge 2 ] || die "--arch needs a value"; ARCH="$2"; shift 2 ;;
	--token) [ $# -ge 2 ] || die "--token needs a value"; TOKEN="$2"; shift 2 ;;
	--version=*) VERSION="${1#*=}"; shift ;;
	--bin-dir=*) BIN_DIR="${1#*=}"; shift ;;
	--os=*) OS="${1#*=}"; shift ;;
	--arch=*) ARCH="${1#*=}"; shift ;;
	--token=*) TOKEN="${1#*=}"; shift ;;
	-h | --help) usage; exit 0 ;;
	*) die "unknown option: $1 (try --help)" ;;
	esac
done

# --- the downloader -----------------------------------------------------------

# Either curl or wget is enough, and which one is present says a lot about the
# image: Debian and Ubuntu ship neither by default, Alpine has busybox wget.
if command -v curl >/dev/null 2>&1; then
	DOWNLOADER="curl"
elif command -v wget >/dev/null 2>&1; then
	DOWNLOADER="wget"
else
	die "neither curl nor wget is installed"
fi

# get fetches a URL to stdout. The token is passed through a branch rather than
# an expanded variable because a header carries spaces, and an unquoted
# expansion would split it into three arguments.
get() {
	case "$DOWNLOADER" in
	curl)
		if [ -n "$TOKEN" ]; then
			curl -fsSL -H "Accept: application/vnd.github+json" -H "Authorization: Bearer $TOKEN" "$1"
		else
			curl -fsSL -H "Accept: application/vnd.github+json" "$1"
		fi
		;;
	wget)
		if [ -n "$TOKEN" ]; then
			wget -qO- --header="Accept: application/vnd.github+json" --header="Authorization: Bearer $TOKEN" "$1"
		else
			wget -qO- --header="Accept: application/vnd.github+json" "$1"
		fi
		;;
	esac
}

# download writes a URL to a file, with no headers of any kind. Separate from
# get because a binary must never go through a command substitution, which
# would eat its trailing newlines.
download() {
	case "$DOWNLOADER" in
	curl) curl -fsSL -o "$2" "$1" ;;
	wget) wget -qO "$2" "$1" ;;
	esac
}

# public_asset_url is the address anybody can fetch the asset from.
public_asset_url() {
	printf '%s' "${DOWNLOAD_URL}/${OWNER}/${REPO}/releases/download/${TAG}/${ASSET}"
}

# download_asset writes the release asset to the file named by $1.
#
# Without a token, or against a listing that named no asset endpoint, this is
# the public download URL fetched with no headers, exactly as before. With one
# the bytes come from the asset's own API endpoint, which is the only address
# that serves a private repository's asset: the public URL answers with a
# sign-in page instead.
#
# An endpoint that refuses is tried once more on the public URL with no
# credential, which is what dispat's own downloader does. A token that reads
# the listing and not the assets is a real shape, and before the endpoint
# existed that install simply worked; the digest still decides what is
# installed either way.
#
# The credential must never reach the storage host that endpoint redirects to.
# curl arranges that itself: since 7.58 it drops Authorization on a redirect
# that changes host and keeps it on one that does not, which is what a GitHub
# Enterprise install redirecting to itself needs. wget forwards every --header
# across every redirect, so it is given none to follow: the endpoint is asked
# with --max-redirect=0, and the Location it answers with is then fetched on
# its own, with no headers at all. An endpoint that answers with the bytes
# rather than a redirect, which is what an Enterprise install serving them
# itself does, has already written the file.
download_asset() {
	if [ -z "$TOKEN" ] || [ -z "$ASSET_API_URL" ]; then
		download "$(public_asset_url)" "$1"
		return
	fi
	if authed_asset "$1"; then
		return 0
	fi
	log "warning: the release API would not serve ${ASSET}; trying the public download URL"
	download "$(public_asset_url)" "$1"
}

# authed_asset fetches the asset from its API endpoint with the credential, and
# answers non-zero when that address would not serve it.
authed_asset() {
	case "$DOWNLOADER" in
	curl)
		curl -fsSL -H "Accept: application/octet-stream" \
			-H "Authorization: Bearer $TOKEN" -o "$1" "$ASSET_API_URL"
		;;
	wget)
		# busybox's wget has neither option and follows redirects carrying
		# whatever headers it was given, so there is no way to send the
		# credential through it safely. Refusing says so instead of leaking
		# it. The probe is the exit code of the option itself rather than a
		# search of the help text, which is wording nobody promised.
		if ! wget --max-redirect=0 --version >/dev/null 2>&1; then
			die "this wget cannot be stopped at a redirect, so a token sent through it would reach the storage host. Install curl, or download ${ASSET} yourself."
		fi
		RESPONSE=$(wget -O "$1" --max-redirect=0 --server-response \
			--header="Accept: application/octet-stream" \
			--header="Authorization: Bearer $TOKEN" "$ASSET_API_URL" 2>&1)
		RC=$?
		# The Location header is what says a redirect happened, which is worth
		# more than the exit code: refusing to follow one is itself an error to
		# wget, and the status it exits with does not distinguish that from a
		# 404. With no Location, the exit code is all there is and it decides.
		LOCATION=$(printf '%s\n' "$RESPONSE" |
			sed -n 's|^[ 	]*Location:[ 	]*\([^ 	]*\).*$|\1|p' | tail -n 1)
		if [ -n "$LOCATION" ]; then
			download "$LOCATION" "$1"
		else
			[ "$RC" -eq 0 ] && [ -s "$1" ]
		fi
		;;
	esac
}

# json_fields puts one JSON field per line, so the few things this script reads
# out of the API can be matched with an anchored pattern instead of a parser.
# The API pretty-prints, which would make the newlines enough on their own;
# splitting on the structural characters as well means a compact body (a proxy,
# an Enterprise instance) reads the same rather than silently matching nothing.
json_fields() { tr -d ' \t' | tr ',{}[]' '\n'; }

# --- platform -----------------------------------------------------------------

# The host is detected even when the target was named, so the smoke test at the
# end can tell "installed for this machine" from "installed for an image being
# cross-built", which is not a runnable binary here.
case "$(uname -s)" in
Linux) HOST_OS="linux" ;;
Darwin) HOST_OS="darwin" ;;
*) HOST_OS="" ;;
esac
case "$(uname -m)" in
x86_64 | amd64) HOST_ARCH="amd64" ;;
aarch64 | arm64) HOST_ARCH="arm64" ;;
*) HOST_ARCH="" ;;
esac

if [ -z "$OS" ]; then
	[ -n "$HOST_OS" ] || die "unsupported operating system: $(uname -s). Use install.ps1 on Windows."
	OS="$HOST_OS"
fi
if [ -z "$ARCH" ]; then
	[ -n "$HOST_ARCH" ] || die "unsupported architecture: $(uname -m). Pass --arch."
	ARCH="$HOST_ARCH"
fi

# Mirrors selfupdate.AssetName, the other half of this contract.
ASSET="dispat-${OS}-${ARCH}"
if [ "$OS" = "windows" ]; then
	ASSET="${ASSET}.exe"
fi

# --- the version --------------------------------------------------------------

# Both spellings are accepted because the releases page shows the tag and the
# changelog shows the number, and a reader should be able to paste either.
VERSION="${VERSION#"$TAG_PREFIX"}"
VERSION="${VERSION#v}"

if [ -z "$VERSION" ]; then
	log "resolving the latest stable release..."
	# Highest, not most recent: a patch cut on an older line comes back first
	# and would otherwise look like an upgrade to everyone on the newer one.
	# Prereleases drop out on the "-" test, which needs no JSON structure:
	# a stable version has no hyphen in it. Three pages mirrors the walk
	# internal/selfupdate makes: one release run cuts a release per package,
	# so the newest stable of this one can sit past the first page.
	TAGS=""
	PAGE=1
	while [ "$PAGE" -le 3 ]; do
		BODY=$(get "${API_URL}/repos/${OWNER}/${REPO}/releases?per_page=100&page=${PAGE}") || break
		PAGE_TAGS=$(printf '%s' "$BODY" | json_fields |
			sed -n 's|^"tag_name":"\(.*\)"$|\1|p')
		# A page with no tags at all is the end of the listing; a page whose
		# tags all belong to other packages still asks for the next one.
		[ -n "$PAGE_TAGS" ] || break
		TAGS="${TAGS}${PAGE_TAGS}
"
		PAGE=$((PAGE + 1))
	done
	VERSION=$(
		printf '%s' "$TAGS" |
			sed -n "s|^${TAG_PREFIX}\([0-9][0-9.]*\)\$|\1|p" |
			sort -t. -k1,1n -k2,2n -k3,3n |
			tail -n 1
	)
	[ -n "$VERSION" ] || die "no stable release found under ${TAG_PREFIX}*"
fi

case "$VERSION" in
[0-9]*.[0-9]*.[0-9]*) ;;
*) die "not a version: $VERSION (expected 1.2.3, v1.2.3 or ${TAG_PREFIX}1.2.3)" ;;
esac

TAG="${TAG_PREFIX}${VERSION}"

# --- the digest and the asset's endpoint ---------------------------------------

# GitHub reports a "digest" per asset, which is what internal/selfupdate checks
# too. Fetching the release by tag also turns "no such version" into a clean
# failure here rather than a 404 on the download.
RELEASE=$(get "${API_URL}/repos/${OWNER}/${REPO}/releases/tags/${TAG}" 2>/dev/null) ||
	die "no release for ${TAG}. Check the version, or the releases page."

# Both walks start at the assets array and not before it. The release object
# carries a "name" of its own, which is its title, and a title somebody set to
# the asset's name would otherwise match: the digest walk would find nothing
# and the url walk would print whichever "url" came last, the release's own or
# the author's. The key survives the field splitter as a bare `"assets":`,
# which is what makes the gate a single anchored pattern.
#
# One field per line, then a two-state walk: an asset's "name" precedes its
# "digest", and the "uploader" object between them carries neither key.
DIGEST=$(
	printf '%s' "$RELEASE" | json_fields | awk -v want="\"name\":\"${ASSET}\"" '
		/^"assets":$/ { in_assets = 1; next }
		!in_assets { next }
		$0 == want { found = 1; next }
		found && /^"digest":"sha256:/ {
			sub(/^"digest":"sha256:/, ""); sub(/".*$/, ""); print; exit
		}
		/^"name":"/ { found = 0 }
	'
)

# The asset's own REST endpoint, which is the address that serves the bytes to
# an authenticated request. Here the walk runs the other way round: "url"
# precedes "name" inside an asset, so the last one seen when the name matches
# is the one that belongs to it. Every other key ending in _url is a different
# field and the pattern is anchored against them; the "url" of whoever uploaded
# the asset is overwritten by the next asset's own before its name is reached.
ASSET_API_URL=$(
	printf '%s' "$RELEASE" | json_fields | awk -v want="\"name\":\"${ASSET}\"" '
		/^"assets":$/ { in_assets = 1; next }
		!in_assets { next }
		/^"url":"/ { last = $0; sub(/^"url":"/, "", last); sub(/".*$/, "", last) }
		$0 == want { print last; exit }
	'
)

# --- download and verify ------------------------------------------------------

if [ -z "$BIN_DIR" ]; then
	if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
		BIN_DIR="/usr/local/bin"
	elif [ -n "${HOME:-}" ]; then
		BIN_DIR="${HOME}/.local/bin"
	else
		die "nowhere to install: /usr/local/bin is not writable and HOME is unset. Pass --bin-dir."
	fi
fi
mkdir -p "$BIN_DIR" || die "cannot create $BIN_DIR"
[ -w "$BIN_DIR" ] || die "$BIN_DIR is not writable. Pass --bin-dir, or re-run with sudo."

TARGET="${BIN_DIR}/dispat"
if [ "$OS" = "windows" ]; then
	TARGET="${TARGET}.exe"
fi
# Staged inside the target directory so the final move is a rename on the same
# filesystem: a half-downloaded binary never appears on PATH.
TMP="${TARGET}.download.$$"
trap 'rm -f "$TMP"' EXIT INT TERM

if [ -n "$TOKEN" ] && [ -n "$ASSET_API_URL" ]; then
	log "downloading ${ASSET} ${VERSION} from the release API..."
else
	log "downloading ${ASSET} ${VERSION}..."
fi
download_asset "$TMP" ||
	die "download failed: ${ASSET} is not attached to ${TAG}"

if [ -z "$DIGEST" ]; then
	log "warning: the release reports no digest for ${ASSET}; skipping verification"
elif command -v sha256sum >/dev/null 2>&1; then
	GOT=$(sha256sum "$TMP" | cut -d' ' -f1)
elif command -v shasum >/dev/null 2>&1; then
	GOT=$(shasum -a 256 "$TMP" | cut -d' ' -f1)
else
	log "warning: neither sha256sum nor shasum is installed; skipping verification"
fi
if [ -n "$DIGEST" ] && [ -n "${GOT:-}" ]; then
	[ "$GOT" = "$DIGEST" ] || die "checksum mismatch: expected $DIGEST, got $GOT"
	log "checksum verified"
fi

chmod 0755 "$TMP"
mv -f "$TMP" "$TARGET"
trap - EXIT INT TERM
log "installed ${TARGET}"

# A binary built for another platform cannot be run here, and that is the normal
# case inside a cross-built image rather than something to warn about.
if [ "$OS" = "$HOST_OS" ] && [ "$ARCH" = "$HOST_ARCH" ]; then
	DISPAT_UPDATE_CHECK=0 "$TARGET" --version >&2 || die "the installed binary does not run"
fi

# The PATH story, both halves of it. A directory missing from PATH gets the
# one-off export and the line that makes it permanent, aimed at the profile
# the user's shell actually reads. A directory already on PATH can still lose
# to an older dispat installed somewhere earlier, which looks exactly like
# the new version failing to install, so that shadowing is said out loud.
case "${SHELL:-}" in
*/zsh) PROFILE="\$HOME/.zshrc" ;;
*/bash) PROFILE="\$HOME/.bashrc" ;;
*) PROFILE="\$HOME/.profile" ;;
esac
case ":${PATH}:" in
*":${BIN_DIR}:"*)
	if [ "$OS" = "$HOST_OS" ] && [ "$ARCH" = "$HOST_ARCH" ]; then
		FOUND=$(command -v dispat 2>/dev/null || true)
		if [ -n "$FOUND" ] && [ "$FOUND" != "$TARGET" ]; then
			log "warning: ${FOUND} comes earlier on PATH and shadows ${TARGET}."
			log "  \`dispat --version\` will keep answering with the old binary; remove it or reorder PATH:"
			log "  rm \"${FOUND}\"    # or move ${BIN_DIR} ahead in ${PROFILE}"
		fi
	fi
	;;
*)
	log "note: ${BIN_DIR} is not on PATH."
	log "  this shell only:  export PATH=\"${BIN_DIR}:\$PATH\""
	log "  permanently:      echo 'export PATH=\"${BIN_DIR}:\$PATH\"' >> ${PROFILE}"
	log "  then open a new shell (or \`source ${PROFILE}\`)."
	;;
esac

printf '%s\n' "$VERSION"
