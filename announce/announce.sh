#!/bin/sh
# Announce a dispat release on Instagram and LinkedIn, with crier.
#
# dispat runs this from the announce stage, which only ever warns, so every
# reason not to post is a message and an exit 0, never a failed release. A
# missing secret must not turn a good release into a red build.
#
# One card and two clips per release, posted four ways: the feed carousel at
# 1080x1080, the anthem as a story, the changelog pages as stories fitted
# into 1080x1920, and the release on LinkedIn when that platform's secrets
# are set. One video post there that plays the anthem over the cover and then
# leafs through the changelog pages, because a LinkedIn post takes one video
# or many images and never both, and one post was the requirement.
#
# The anthem clip is rendered once and used twice: it opens the feed carousel
# as its lead video and it opens the story reel as the first story. Both
# surfaces want the same sixteen seconds, and encoding them separately would
# spend a minute of a release producing a file crier already has. The reel
# shares its frames: the same pages, paced across the same soundtrack.
#
# The card paginates. A release with a long changelog lays out into several
# pages, and each pass turns those into what its surface takes: the feed pass
# posts them as one carousel, the story pass as one story per page in order.
# Neither is a flag here; crier works it out from the platform.
#
# Every release looks and sounds a little different. The card has two layouts
# and a set of accent colours, and there are four anthems; crier draws all of
# it from one seeded source. The seed is the version, so the choice is made
# once for the whole announcement and re-running a release reproduces it
# exactly.
#
# crier is a tool the release installs rather than a binary this repository
# builds, so it is resolved from PATH or named by ANNOUNCE_CRIER_BIN. The
# release workflow puts the pinned version there through
# scripts/install-tools.sh; the replay workflow points the variable at its
# own copy.
set -eu

root=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
here=$root/announce
log() { printf 'announce: %s\n' "$*" >&2; }

# --- only a release announces -------------------------------------------------
#
# `dispat run announce -p dispat` is how a person tries this script out on a
# laptop, and DISPAT_STAGE tells that apart from the real thing: a stage
# script gets `announce`, a run command gets `run:announce`. Without the
# guard a rehearsal with the secrets exported would post a release that is
# already out. ANNOUNCE_FORCE is the way back in, and it is what the replay
# scripts set.
if [ "${DISPAT_STAGE:-}" != announce ] && [ -z "${ANNOUNCE_FORCE:-}" ]; then
	log "not the announce stage (DISPAT_STAGE=${DISPAT_STAGE:-unset}); set ANNOUNCE_FORCE=1 to post anyway"
	exit 0
fi

version=${DISPAT_NEW_VERSION:-}
if [ -z "$version" ]; then
	log "no DISPAT_NEW_VERSION, so there is no release to announce; skipping"
	exit 0
fi

# --- one seed for the whole announcement --------------------------------------
#
# The card's layout, its accent colours and the anthem are all drawn from
# crier's seeded random source, and this script runs crier half a dozen times
# over one release. Left alone, each run would draw a seed of its own and the
# stories would come out looking like a different release than the feed post.
#
# So the version decides. cksum of the version string is a stable number on
# every POSIX system, which makes one release one look and one soundtrack,
# re-runnable to the pixel, and the next release something else.
seed=$(printf '%s' "$version" | cksum | cut -d' ' -f1)
log "seed for v$version is $seed"

# ANNOUNCE_ONLY=linkedin replays the linkedin pass alone: no Instagram
# secrets wanted, no tunnel, no feed or stories. It exists because a platform
# can refuse a post for reasons of its own, and re-running a whole release to
# say one thing again is not an option once the tags exist.
only=${ANNOUNCE_ONLY:-}

# --- what has to be there -----------------------------------------------------
#
# Collected rather than checked one at a time, so somebody setting this up is
# told everything that is missing at once instead of one secret per release.
missing=""
if [ "$only" != "linkedin" ]; then
	[ -n "${CRIER_PUBLISH_INSTAGRAM_TOKEN:-}" ] || missing="$missing CRIER_PUBLISH_INSTAGRAM_TOKEN"
	[ -n "${CRIER_PUBLISH_INSTAGRAM_USER_ID:-}" ] || missing="$missing CRIER_PUBLISH_INSTAGRAM_USER_ID"
fi

# The tunnel is only needed when crier is staging the file itself. Pointing
# CRIER_STAGE_MODE at s3 or at a URL somebody else publishes skips it, which is
# the escape hatch for anyone who would rather not run a tunnel in CI.
stage_mode=${CRIER_STAGE_MODE:-server}
# LinkedIn takes the bytes directly, so the replay stages nothing at all, and
# the default server mode would demand a tunnel URL it will never use.
[ "$only" != "linkedin" ] || stage_mode=none
if [ "$only" != "linkedin" ] && [ "$stage_mode" = "server" ] && [ -z "${NGROK_AUTHTOKEN:-}" ]; then
	missing="$missing NGROK_AUTHTOKEN"
fi

if [ -n "$missing" ]; then
	log "not announcing v$version: no$missing"
	log "set them as repository secrets, or set CRIER_STAGE_MODE to stage without a tunnel"
	exit 0
fi

# --- the tool -----------------------------------------------------------------
#
# From PATH by default, which is where the release workflow's install manifest
# put it. ANNOUNCE_CRIER_BIN names one by path instead, for the replay
# workflow and for a laptop.
crier=${ANNOUNCE_CRIER_BIN:-}
if [ -z "$crier" ]; then
	crier=$(command -v crier 2>/dev/null || true)
fi
if [ -z "$crier" ] || [ ! -x "$crier" ]; then
	log "no crier on PATH and no ANNOUNCE_CRIER_BIN; skipping (see scripts/install-tools.sh)"
	exit 0
fi
log "announcing v$version with $crier"

# --- the card's data ----------------------------------------------------------
#
# Written once and reused for both passes, so the story is the same card as the
# feed post rather than a second render of a moving target.
data=$(mktemp)
frames_dir=""
cover_data=""
updates_data=""
anthem_dir=""
# anthem_mp4 is the rendered clip, once it exists, and reel_mp4 the linkedin
# variant that leafs through the pages. Empty means there is none, which is
# what every pass checks rather than assuming a file is there.
anthem_mp4=""
reel_mp4=""
li_notes_data=""
trap 'rm -rf "$data" "$frames_dir" "$cover_data" "$updates_data" "$anthem_dir" "$li_notes_data"' EXIT
sh "$here/notes.sh" >"$data"

# --- staging ------------------------------------------------------------------
#
# Instagram fetches the media from a public URL of its own accord, and a runner
# has none. ngrok gives the stage server one for the length of the run.
if [ "$only" != "linkedin" ] && [ "$stage_mode" = "server" ]; then
	# Idempotent: writing the same token again is not an error, and the agent
	# reads its config file rather than the environment.
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

# --- post ---------------------------------------------------------------------
#
# The feed post is the card as the config draws it. The story is the same card
# fitted into 1080x1920, which is a set of flags rather than a second config:
# one file describing one card is easier to keep true than two.
#
# Every page goes out either way. A story sequence has no cover-page opt-out:
# posting page one and dropping the changelog would be announcing a release
# without saying what is in it.
post() {
	what=$1
	doc=$2
	shift 2
	log "posting the $what"
	if "$crier" --config "$here/crier.yaml" --render-data - --render-seed "$seed" "$@" <"$doc"; then
		log "posted the $what"
		return 0
	fi
	# One post failing must not take the other with it, and neither may fail
	# the release.
	log "the $what did not post; see the log above"
	return 1
}

# --- the anthem ---------------------------------------------------------------
#
# The cover page, held for sixteen seconds over one of the four public-domain
# clips in announce/: the one way audio reaches Instagram, which takes no
# audio file and no track id (see announce/anthem.md). Which one is not a flag
# here: render.video.audio-pool in crier.yaml lists them and the seed above
# chooses, so a release has a soundtrack of its own and re-running it plays
# the same one. The changelog is not in this clip: the image stories carry it,
# and this is the fanfare.
#
# It renders once and is posted twice, because both surfaces want the same
# sixteen seconds: the feed carousel opens with it, and the story reel opens
# with it. Encoding it a second time would burn a minute of a release to
# produce a file crier already has.
#
# The cover renders once and is copied into frames, because 384 identical
# layouts would cost minutes and one copied 384 times costs a second.
render_anthem() {
	command -v ffmpeg >/dev/null 2>&1 || {
		log "ffmpeg is not installed; the release goes out without the anthem"
		return 0
	}
	frames_dir=$(mktemp -d)
	cover_data=$(mktemp)
	anthem_dir=$(mktemp -d)
	cover=$cover_data
	DISPAT_BREAKING_CHANGES='' DISPAT_FEATURES='' DISPAT_FIXES='' DISPAT_DEPENDENCIES='' \
		sh "$here/notes.sh" >"$cover"
	# One render of the whole card. Page one is the cover the anthem holds,
	# identical to a cover-only render since a page's layout does not depend
	# on the pages after it, and the pages behind it are what the linkedin
	# reel leafs through.
	pages=$frames_dir/pages
	mkdir "$pages"
	if ! "$crier" render --config "$here/crier.yaml" --render-data - \
		--render-seed "$seed" \
		--render-format png --render-background '#101713' \
		--render-output "$pages/page.png" <"$data"; then
		log "the card did not render; the release goes out without the anthem"
		return 1
	fi
	front=$pages/page-1.png
	[ -f "$front" ] || front=$pages/page.png

	# encode turns a frames directory into a clip. The soundtrack is not a
	# flag: render.video.audio-pool in crier.yaml and the run's seed choose
	# it, the same pick on every invocation of this release.
	encode() {
		in=$1
		out=$2
		shift 2
		"$crier" render --config "$here/crier.yaml" --render-data - \
			--render-seed "$seed" \
			--render-video-enabled=true \
			--render-video-fps 24 \
			--render-video-frames-input "$in" \
			--render-background '#101713' \
			"$@" \
			--render-output "$out" <"$cover"
	}

	frames=$frames_dir/anthem-frames
	mkdir "$frames"
	i=0
	while [ "$i" -lt 384 ]; do
		i=$((i + 1))
		cp "$front" "$frames/$(printf 'f%03d' "$i").png"
	done
	log "rendering the anthem"
	if ! encode "$frames" "$anthem_dir/anthem.mp4"; then
		log "the anthem did not render; the release goes out without it"
		return 1
	fi
	anthem_mp4=$anthem_dir/anthem.mp4
	log "rendered the anthem to $anthem_mp4"

	# The linkedin reel: the cover holds while the fanfare settles in, then
	# the changelog pages follow at reading pace, six seconds of cover and
	# four a page, however many pages there are. The reel outgrows the
	# sixteen-second anthem as soon as the changelog does, so its encode
	# loops the soundtrack for as long as the slides run.
	npages=$(find "$pages" -name '*.png' | wc -l | tr -d ' ')
	if [ "$npages" -le 1 ]; then
		reel_mp4=$anthem_mp4
		return 0
	fi
	page_frames=96
	cover_frames=144
	reel=$frames_dir/reel-frames
	mkdir "$reel"
	i=0
	n=1
	while [ "$n" -le "$npages" ]; do
		src=$pages/page-$n.png
		count=$page_frames
		[ "$n" -ne 1 ] || count=$cover_frames
		j=0
		while [ "$j" -lt "$count" ]; do
			j=$((j + 1))
			i=$((i + 1))
			cp "$src" "$reel/$(printf 'f%03d' "$i").png"
		done
		n=$((n + 1))
	done
	log "rendering the linkedin reel over $npages pages"
	if ! encode "$reel" "$anthem_dir/reel.mp4" --render-video-audio-loop=true; then
		log "the reel did not render; linkedin gets the anthem alone"
		reel_mp4=$anthem_mp4
		return 1
	fi
	reel_mp4=$anthem_dir/reel.mp4
	log "rendered the reel to $reel_mp4"
	return 0
}

# --- the anthem story ---------------------------------------------------------
#
# The clip render_anthem already made, published as a story. No second render:
# publish.input takes the file as it stands.
anthem_story() {
	[ -n "$anthem_mp4" ] || return 0
	log "posting the anthem story"
	# The cover's data still goes in on standard input. The card is not
	# rendered in this mode, but the caption template is still resolved, and
	# the config points render.data at stdin.
	if "$crier" --config "$here/crier.yaml" --render-data - \
		--render-seed "$seed" \
		--publish-input "$anthem_mp4" \
		--publish-instagram-story \
		--publish-instagram-width 1080 \
		--publish-instagram-height 1920 \
		--publish-instagram-fit contain \
		--publish-instagram-fit-background "#101713" <"$cover_data"; then
		log "posted the anthem story"
		return 0
	fi
	log "the anthem story did not post; see the log above"
	return 1
}

# The clip is made before anything is posted, because the feed post opens with
# it: every post row leads with the video that carries the music.
#
# The reel reads in posting order: the anthem video is the cover story, the
# changelog pages follow it as pictures. The picture cover would only repeat
# what the video already shows, so the story pass strips it.
failures=0
render_anthem || failures=$((failures + 1))

# The nocover pages, prepared up front: with the anthem leading the carousel,
# the feed post uses them too, since an image cover behind the video cover
# would just repeat it, which is the reel's lesson learned twice.
has_changelog=1
[ -n "$(printf '%s' "${DISPAT_BREAKING_CHANGES:-}${DISPAT_FEATURES:-}${DISPAT_FIXES:-}${DISPAT_DEPENDENCIES:-}" | tr -d '[:space:]')" ] || has_changelog=0
updates=$data
if [ "$has_changelog" = 1 ]; then
	updates=$(mktemp)
	updates_data=$updates
	ANNOUNCE_NO_COVER=1 sh "$here/notes.sh" >"$updates"
fi

if [ "$only" = "linkedin" ]; then
	log "ANNOUNCE_ONLY=linkedin: the instagram passes stay quiet"
elif [ -n "$anthem_mp4" ] && [ "$has_changelog" = 1 ]; then
	post "feed post" "$updates" --publish-instagram-lead-video "$anthem_mp4" || failures=$((failures + 1))
elif [ -n "$anthem_mp4" ]; then
	# Nothing to page: the video is the whole carousel, and the image cover
	# stays home rather than repeating it.
	post "feed post" "$cover_data" --publish-input "$anthem_mp4" || failures=$((failures + 1))
else
	post "feed post" "$data" || failures=$((failures + 1))
fi
[ "$only" = "linkedin" ] || anthem_story || failures=$((failures + 1))
if [ "$only" = "linkedin" ]; then
	: # the stories are instagram's
elif [ "$has_changelog" = 0 ]; then
	log "no changelog pages; the cover video is the whole story"
else
	post_updates() {
		log "posting the stories"
		if "$crier" --config "$here/crier.yaml" --render-data - \
			--render-seed "$seed" \
			--publish-instagram-story \
			--publish-instagram-width 1080 \
			--publish-instagram-height 1920 \
			--publish-instagram-fit contain \
			--publish-instagram-fit-background "#101713" <"$updates"; then
			log "posted the stories"
			return 0
		fi
		log "the stories did not post; see the log above"
		return 1
	}
	post_updates || failures=$((failures + 1))
fi

# --- linkedin -----------------------------------------------------------------
#
# The same release, posted where the changelog's readers work. LinkedIn takes
# the bytes directly, so this pass wants no tunnel and no staging.
#
# One post, everything in it. A LinkedIn post takes one video or many images
# and never both (content.multiImage is documented images-only), so the one
# post that can carry the whole release is the reel: the cover holding under
# the fanfare, then the changelog pages in reading order, sixteen seconds for
# all of it. With no ffmpeg to make a clip, the whole card goes out as one
# multi-image album instead, cover first.
#
# Its own pass rather than a platform enabled beside Instagram, because the
# two want different documents and different captions: announce/crier.yaml
# carries a commentary written for people who read release notes for a living.
linkedin_post() {
	if [ -z "${CRIER_PUBLISH_LINKEDIN_TOKEN:-}" ] || [ -z "${CRIER_PUBLISH_LINKEDIN_AUTHOR_URN:-}" ]; then
		log "no CRIER_PUBLISH_LINKEDIN_TOKEN or CRIER_PUBLISH_LINKEDIN_AUTHOR_URN; the release goes out without the linkedin post"
		return 0
	fi
	set -- --publish-instagram-enabled=false --publish-linkedin-enabled=true
	if [ -z "$reel_mp4" ]; then
		post "linkedin post" "$data" "$@"
		return $?
	fi
	# The reel's caption gets a shorter changelog than the card: the pages in
	# the clip carry the full list, and a monorepo release with every package
	# in it can run a caption past LinkedIn's 4000-character cap and be
	# refused outright. Eight a section and "and N more" keeps the text a
	# summary of the film above it; crier cuts at the cap as the last line of
	# defence either way.
	li_notes=$(mktemp)
	li_notes_data=$li_notes
	ANNOUNCE_MAX_ITEMS=${ANNOUNCE_LINKEDIN_MAX_ITEMS:-8} sh "$here/notes.sh" >"$li_notes"
	if ! post "linkedin post" "$li_notes" "$@" --publish-input "$reel_mp4"; then
		# A member token posts text and images, but the video API is a
		# partner product (LinkedIn's Community Management API) that some
		# tokens do not carry: a clip refused with 403 ACCESS_DENIED
		# partnerApiVideosExternal takes the album chained behind it with
		# it, and nothing lands at all. The release still reaches LinkedIn
		# without that product: the whole card as one album, cover first,
		# under the same commentary.
		log "the linkedin clip was refused; posting the card as an album instead"
		post "linkedin album" "$data" "$@"
		return $?
	fi
}
linkedin_post || failures=$((failures + 1))

if [ "$failures" -gt 0 ]; then
	# Five things can go wrong rather than three: the clips render is a step
	# of its own since the anthem posts twice and the reel once, and linkedin
	# is a pass of its own with a document of its own.
	log "$failures of the announcement's steps did not go out; the release itself is unaffected"
	# A replay is a person asking for one thing. Failing to do it is the
	# answer, and a green run would hide it; only a release must never go
	# red over a post.
	[ "$only" != "linkedin" ] || exit 1
fi
exit 0
