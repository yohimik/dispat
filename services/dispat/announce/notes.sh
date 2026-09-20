#!/bin/sh
# Build the announcement card's data document from a dispat release.
#
# It reads the release-notes variables dispat gives every stage and writes one
# JSON object on standard output. It is a script of its own rather than a
# function inside announce.sh so that it can be run and checked without a
# release, a network, or a binary:
#
#   DISPAT_NEW_VERSION=1.2.3 DISPAT_FEATURES="a
#   b" sh services/dispat/announce/notes.sh
#
# The variables are documented in reference/environment.md: entries are one per
# line, in history order, and a group with no entries is set to empty text
# rather than unset.
#
# `sh notes.sh --channel` prints only the channel the version selects, rc or
# stable. announce.sh asks it that way to choose the channel's crier.yaml, so
# the rule that reads a version lives in this file alone.
set -eu

version=${DISPAT_NEW_VERSION:-dev}
here=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
# Match the prerelease part, ignoring optional semver build metadata.
case "${version%%+*}" in
*-rc|*-rc.*) channel=rc ;;
*-*) printf 'announce: no announcement copy for prerelease version %s\n' "$version" >&2; exit 1 ;;
*) channel=stable ;;
esac
if [ "${1:-}" = --channel ]; then
	printf '%s\n' "$channel"
	exit 0
fi
# A channel's fixed text is two files committed beside its crier.yaml, and
# nothing else: announcement.md is the notes, links.md is one `Label: URL` per
# line. The card and every caption are built from the same two files, so what a
# picture says and what its description says cannot drift apart.
announcement=$here/$channel/announcement.md
if [ ! -s "$announcement" ]; then
	printf 'announce: missing %s announcement copy\n' "$channel" >&2
	exit 1
fi
links=$here/$channel/links.md
if [ ! -s "$links" ]; then
	printf 'announce: missing %s announcement links\n' "$channel" >&2
	exit 1
fi

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

# expand writes the announced version into committed copy. The copy spells it
# ${DISPAT_NEW_VERSION}, the way the github.footer lines in dispat.yaml do, so a
# link can name this exact release without anybody editing a number into it.
sed_version=$(printf '%s' "$version" | sed -e 's/[\\&|]/\\&/g')
expand() {
	sed -e "s|\${DISPAT_NEW_VERSION}|$sed_version|g"
}

# link_objects prints the {"label":…,"url":…} objects of a links file, comma
# separated. A line that is not `Label: https://…` fails the run here, before
# any output, rather than reaching a card as a link nobody can follow.
link_objects() {
	separator=""
	while IFS= read -r line || [ -n "$line" ]; do
		case "$line" in *[![:space:]]*) ;; *) continue ;; esac
		line=$(printf '%s' "$line" | expand)
		label=${line%%: *}
		url=${line#*: }
		case "$url" in
		*[[:space:]]*) url="" ;;
		https://*) ;;
		*) url="" ;;
		esac
		if [ -z "$url" ] || [ "$label" = "$line" ]; then
			printf 'announce: not a "Label: https://..." link: %s\n' "$line" >&2
			return 1
		fi
		printf '%s{"label":"%s","url":"%s"}' "$separator" \
			"$(printf '%s' "$label" | escape)" "$(printf '%s' "$url" | escape)"
		separator=,
	done <"$1"
}
link_list=$(link_objects "$links")

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
printf '"channel":"%s",' "$channel"
printf '"announcement":['
separator=""
while IFS= read -r line || [ -n "$line" ]; do
	printf '%s"%s"' "$separator" "$(printf '%s' "$line" | expand | escape)"
	separator=,
done <"$announcement"
printf '],'
printf '"links":[%s],' "$link_list"
printf '"sections":[%s],' "$sections"
printf '"install":['
printf '{"label":"curl","command":"curl -fsSL https://raw.githubusercontent.com/yohimik/dispat/v%s/install.sh | DISPAT_VERSION=%s sh"},' \
	"$esc_version" "$esc_version"
printf '{"label":"self-update","command":"dispat self-update --release %s"},' "$esc_version"
printf '{"label":"action","command":"{uses: yohimik/dispat@v1, with: {version: %s}}"}' "$esc_version"
printf ']}'
printf '\n'
