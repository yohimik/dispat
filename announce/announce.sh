#!/bin/sh
# One Crier invocation renders the release and publishes every configured
# destination, including Instagram's additional cover story with music.
set -eu

root=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
here=$root/announce
log() { printf 'announce: %s\n' "$*" >&2; }

if [ "${DISPAT_STAGE:-}" != announce ] && [ -z "${ANNOUNCE_FORCE:-}" ]; then
	log "not the announce stage; set ANNOUNCE_FORCE=1 to post anyway"
	exit 0
fi
version=${DISPAT_NEW_VERSION:-}
if [ -z "$version" ]; then
	log "no DISPAT_NEW_VERSION; skipping"
	exit 0
fi

only=${ANNOUNCE_ONLY:-}
case "$only" in
""|instagram|linkedin|discord) ;;
*) log "unknown ANNOUNCE_ONLY: $only"; exit 1 ;;
esac
instagram=false
linkedin=false
discord=false
if [ -z "$only" ] || [ "$only" = instagram ]; then
	if [ -n "${CRIER_PUBLISH_INSTAGRAM_TOKEN:-}${CRIER_PUBLISH_INSTAGRAM_USER_ID:-}" ]; then
		if [ -z "${CRIER_PUBLISH_INSTAGRAM_TOKEN:-}" ] || [ -z "${CRIER_PUBLISH_INSTAGRAM_USER_ID:-}" ]; then
			log "instagram needs both its token and user ID"; exit 1
		fi
		instagram=true
	fi
fi
if [ -z "$only" ] || [ "$only" = linkedin ]; then
	if [ -n "${CRIER_PUBLISH_LINKEDIN_TOKEN:-}${CRIER_PUBLISH_LINKEDIN_AUTHOR_URN:-}" ]; then
		if [ -z "${CRIER_PUBLISH_LINKEDIN_TOKEN:-}" ] || [ -z "${CRIER_PUBLISH_LINKEDIN_AUTHOR_URN:-}" ]; then
			log "linkedin needs both its token and author URN"; exit 1
		fi
		linkedin=true
	fi
fi
if [ -z "$only" ] || [ "$only" = discord ]; then
	[ -z "${CRIER_PUBLISH_DISCORD_WEBHOOK_URL:-}" ] || discord=true
fi
if [ "$instagram" = false ] && [ "$linkedin" = false ] && [ "$discord" = false ]; then
	log "no configured platform for this announcement"
	[ -z "$only" ] || exit 1
	exit 0
fi

crier=${ANNOUNCE_CRIER_BIN:-}
[ -n "$crier" ] || crier=$(command -v crier 2>/dev/null || true)
if [ -z "$crier" ] || [ ! -x "$crier" ]; then
	log "crier is unavailable; run scripts/install-tools.sh"; exit 1
fi

CRIER_STAGE_MODE=${CRIER_STAGE_MODE:-server}
if [ "$instagram" = false ]; then
	CRIER_STAGE_MODE=none
elif [ "$CRIER_STAGE_MODE" = server ]; then
	if [ -z "${NGROK_AUTHTOKEN:-}" ]; then
		log "instagram staging needs NGROK_AUTHTOKEN"; exit 1
	fi
	if ! ngrok config add-authtoken "$NGROK_AUTHTOKEN" >/dev/null 2>&1; then
		log "could not configure the instagram staging tunnel"; exit 1
	fi
	CRIER_STAGE_SERVER_TUNNEL_MODE=${CRIER_STAGE_SERVER_TUNNEL_MODE:-ngrok}
	export CRIER_STAGE_SERVER_TUNNEL_MODE
fi
export CRIER_STAGE_MODE

seed=$(printf '%s' "$version" | cksum | cut -d' ' -f1)
data=$(mktemp)
trap 'rm -f "$data"' EXIT
sh "$here/notes.sh" >"$data"
log "announcing v$version with one crier call (instagram=$instagram linkedin=$linkedin discord=$discord)"
if "$crier" publish --config "$here/crier.yaml" --render-data - \
	--render-seed "$seed" --render-video-enabled=false --render-pages-max 10 \
	--publish-instagram-enabled="$instagram" \
	--publish-linkedin-enabled="$linkedin" \
	--publish-discord-enabled="$discord" <"$data"; then
	log "all configured announcements completed"
else
	log "crier reported an incomplete announcement; inspect its platform results before replaying to avoid duplicate posts"
	exit 1
fi
