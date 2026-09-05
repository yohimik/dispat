#!/bin/sh
# Announce a dispat release on Instagram and LinkedIn, with crier.
#
# Each configured platform receives one image post: the cover followed by the
# paginated changelog. A platform-specific cover-only switch keeps the complete
# release data for its caption while asking the templates to draw only page one.
# There is deliberately no retry after a publish error: an ambiguous network
# failure may already have created the post.
set -eu

root=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
here=$root/announce
log() { printf 'announce: %s\n' "$*" >&2; }

if [ "${DISPAT_STAGE:-}" != announce ] && [ -z "${ANNOUNCE_FORCE:-}" ]; then
	log "not the announce stage (DISPAT_STAGE=${DISPAT_STAGE:-unset}); set ANNOUNCE_FORCE=1 to post anyway"
	exit 0
fi

version=${DISPAT_NEW_VERSION:-}
if [ -z "$version" ]; then
	log "no DISPAT_NEW_VERSION, so there is no release to announce; skipping"
	exit 0
fi

seed=$(printf '%s' "$version" | cksum | cut -d' ' -f1)
log "seed for v$version is $seed"

# ANNOUNCE_ONLY=linkedin replays LinkedIn without requiring Instagram's secrets
# or starting its public staging tunnel.
only=${ANNOUNCE_ONLY:-}
missing=""
if [ "$only" != "linkedin" ]; then
	[ -n "${CRIER_PUBLISH_INSTAGRAM_TOKEN:-}" ] || missing="$missing CRIER_PUBLISH_INSTAGRAM_TOKEN"
	[ -n "${CRIER_PUBLISH_INSTAGRAM_USER_ID:-}" ] || missing="$missing CRIER_PUBLISH_INSTAGRAM_USER_ID"
fi

stage_mode=${CRIER_STAGE_MODE:-server}
[ "$only" != "linkedin" ] || stage_mode=none
if [ "$only" != "linkedin" ] && [ "$stage_mode" = "server" ] && [ -z "${NGROK_AUTHTOKEN:-}" ]; then
	missing="$missing NGROK_AUTHTOKEN"
fi
if [ -n "$missing" ]; then
	log "not announcing v$version: no$missing"
	log "set them as repository secrets, or set CRIER_STAGE_MODE to stage without a tunnel"
	exit 0
fi

crier=${ANNOUNCE_CRIER_BIN:-}
if [ -z "$crier" ]; then
	crier=$(command -v crier 2>/dev/null || true)
fi
if [ -z "$crier" ] || [ ! -x "$crier" ]; then
	log "no crier on PATH and no ANNOUNCE_CRIER_BIN; skipping (see scripts/install-tools.sh)"
	exit 0
fi
log "announcing v$version with $crier"

data=$(mktemp)
instagram_data=""
linkedin_data=""
trap 'rm -f "$data" "$instagram_data" "$linkedin_data"' EXIT
sh "$here/notes.sh" >"$data"

# Instagram fetches staged images from a public URL. LinkedIn uploads bytes and
# the replay therefore needs no staging at all.
if [ "$only" != "linkedin" ] && [ "$stage_mode" = "server" ]; then
	if ! ngrok config add-authtoken "$NGROK_AUTHTOKEN" >/dev/null 2>&1; then
		log "could not write the ngrok authtoken; skipping"
		exit 0
	fi
	CRIER_STAGE_SERVER_TUNNEL_MODE=${CRIER_STAGE_SERVER_TUNNEL_MODE:-ngrok}
	export CRIER_STAGE_SERVER_TUNNEL_MODE
fi
CRIER_STAGE_MODE=$stage_mode
export CRIER_STAGE_MODE
log "staging mode is $stage_mode"

platform_data() {
	platform=$1
	case "$platform" in
	instagram) cover_only=${ANNOUNCE_INSTAGRAM_COVER_ONLY:-${ANNOUNCE_COVER_ONLY:-}} ;;
	linkedin) cover_only=${ANNOUNCE_LINKEDIN_COVER_ONLY:-${ANNOUNCE_COVER_ONLY:-}} ;;
	esac
	if [ -z "$cover_only" ]; then
		printf '%s\n' "$data"
		return
	fi
	doc=$(mktemp)
	ANNOUNCE_COVER_ONLY=1 sh "$here/notes.sh" >"$doc"
	printf '%s\n' "$doc"
}

post() {
	what=$1
	doc=$2
	shift 2
	log "posting the $what"
	if "$crier" --config "$here/crier.yaml" --render-data - \
		--render-seed "$seed" --render-video-enabled=false "$@" <"$doc"; then
		log "posted the $what"
		return 0
	fi
	log "the $what did not post; not retrying because the platform may already have accepted it"
	return 1
}

failures=0
if [ "$only" = "linkedin" ]; then
	log "ANNOUNCE_ONLY=linkedin: the instagram pass stays quiet"
else
	instagram_data=$(platform_data instagram)
	post "instagram post" "$instagram_data" \
		--publish-linkedin-enabled=false \
		--publish-instagram-enabled=true --publish-instagram-story=false \
		--render-pages-max 10 \
		--publish-instagram-max-attachments 10 || failures=$((failures + 1))
fi

if [ -z "${CRIER_PUBLISH_LINKEDIN_TOKEN:-}" ] || [ -z "${CRIER_PUBLISH_LINKEDIN_AUTHOR_URN:-}" ]; then
	log "no CRIER_PUBLISH_LINKEDIN_TOKEN or CRIER_PUBLISH_LINKEDIN_AUTHOR_URN; the release goes out without the linkedin post"
else
	linkedin_data=$(platform_data linkedin)
	post "linkedin post" "$linkedin_data" \
		--publish-instagram-enabled=false \
		--publish-linkedin-enabled=true \
		--render-pages-max 20 \
		--publish-linkedin-max-attachments 20 || failures=$((failures + 1))
fi

if [ "$failures" -gt 0 ]; then
	log "$failures of the announcement's posts did not go out; the release itself is unaffected"
	[ "$only" != "linkedin" ] || exit 1
fi
exit 0
