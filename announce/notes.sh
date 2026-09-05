#!/bin/sh
# Build the announcement card's data document from a dispat release.
#
# It reads the release-notes variables dispat gives every stage and writes one
# JSON object on standard output. It is a script of its own rather than a
# function inside announce.sh so that it can be run and checked without a
# release, a network, or a binary:
#
#   DISPAT_NEW_VERSION=1.2.3 DISPAT_FEATURES="a
#   b" sh announce/notes.sh
#
# The variables are documented in reference/environment.md: entries are one per
# line, in history order, and a group with no entries is set to empty text
# rather than unset.
set -eu

version=${DISPAT_NEW_VERSION:-dev}

# How many entries a section shows before it says how many are left.
#
# The card paginates, so the changelog no longer has to fit in one thumbnail:
# a long release becomes a carousel and the entries carry on across its pages.
# This is therefore a ceiling on absurdity rather than a design constraint. A
# release with sixty entries in one section is a release nobody is going to
# read to the end of, and it also has to stay under render.pages-max, which
# refuses the render outright rather than truncating it.
max=${ANNOUNCE_MAX_ITEMS:-20}

# escape makes one line safe to put inside a JSON string.
#
# Backslash first, or it would escape the backslashes the later rules add. Tab
# and carriage return are spelled out because a raw control character inside a
# JSON string is invalid, and release subjects have been known to carry both.
escape() {
	sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e 's/\t/\\t/g' -e 's/\r//g'
}

# section prints one {"label":…,"items":[…],"more":N} object, or nothing at all
# when the group is empty. An empty section is omitted rather than rendered
# blank: a card with a FIXES heading and no fixes under it looks broken.
section() {
	label=$1
	body=$2

	# Blank lines are dropped rather than counted: dispat separates entries
	# with newlines, and a here-doc or a trailing newline leaves an empty one.
	items=$(printf '%s\n' "$body" | grep -v '^[[:space:]]*$' || true)
	[ -n "$items" ] || return 0

	total=$(printf '%s\n' "$items" | wc -l | tr -d ' ')
	shown=$total
	[ "$shown" -le "$max" ] || shown=$max
	more=$((total - shown))

	printf '{"label":"%s","items":[' "$label"
	n=0
	printf '%s\n' "$items" | head -n "$shown" | while IFS= read -r line; do
		n=$((n + 1))
		[ "$n" -eq 1 ] || printf ','
		printf '"%s"' "$(printf '%s' "$line" | escape)"
	done
	printf '],"more":%d}' "$more"
}

# The four groups, in the order the changelog and the GitHub release use.
#
# PICKS UP is the dependencies section: dispat rewrites a consumer's manifest
# when a provider it depends on releases in the same run, and the changelog
# records that as one "name: old -> new" line per provider. On a monorepo that
# is half of what a release did, so the card says it rather than leaving a
# reader to infer it from a version bump with no features in it.
sections=""
for pair in "BREAKING:${DISPAT_BREAKING_CHANGES:-}" \
	"FEATURES:${DISPAT_FEATURES:-}" \
	"FIXES:${DISPAT_FIXES:-}" \
	"PICKS UP:${DISPAT_DEPENDENCIES:-}"; do
	label=${pair%%:*}
	body=${pair#*:}
	one=$(section "$label" "$body")
	[ -n "$one" ] || continue
	[ -z "$sections" ] || sections="$sections,"
	sections="$sections$one"
done

esc_version=$(printf '%s' "$version" | escape)

# The install commands are built here rather than in the template so that the
# template stays a layout and the commands stay testable. The three routes the
# README documents, the first pinned to the version being announced: the alias
# tag dispat writes for its own CLI is v<version>, so the raw URL resolves to
# the installer that shipped with it.
printf '{'
# Optional render controls leave sections in the data for text captions.
[ -z "${ANNOUNCE_NO_COVER:-}" ] || printf '"nocover":true,'
[ -z "${ANNOUNCE_COVER_ONLY:-}" ] || printf '"coveronly":true,'
printf '"version":"%s",' "$esc_version"
printf '"sections":[%s],' "$sections"
printf '"install":['
printf '{"label":"curl","command":"curl -fsSL https://raw.githubusercontent.com/yohimik/dispat/v%s/install.sh | DISPAT_VERSION=%s sh"},' \
	"$esc_version" "$esc_version"
printf '{"label":"self-update","command":"dispat self-update"},'
printf '{"label":"action","command":"uses: yohimik/dispat@v1"}'
printf ']}'
printf '\n'
