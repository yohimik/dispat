// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package github

import (
	neturl "net/url"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/yohimik/dispat/services/dispat/internal/changelog"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// releaseBodyBudget is how many characters a release body may carry. GitHub
// refuses a body over 125,000 characters with a 422, and the refusal arrives
// after the tag is out. The budget stays 5,000 below that limit because GitHub
// does not document how it counts, and a body cut to the exact limit by one
// way of counting could still be refused by another.
const releaseBodyBudget = 120_000

// boundBody fits a body into budget characters. A body that fits is returned
// untouched. A longer one keeps its whole blocks up to the last one that fits
// with the continuation line after it, so the cut never splits an entry, its
// commit body or a code fence.
func boundBody(body string, budget int, continuation string) string {
	if utf8.RuneCountInString(body) <= budget {
		return body
	}
	tail := "\n\n" + continuation + "\n"
	room := budget - utf8.RuneCountInString(tail)
	kept := body[:lastFittingCut(body, room)]
	return strings.TrimRight(kept, "\n") + tail
}

// lastFittingCut answers the byte offset the kept text ends at: the last block
// boundary whose prefix fits in room characters. A body whose first block
// alone is larger than room has no such boundary, and is cut at the last line
// that fits instead, or at room characters when not even its first line does.
func lastFittingCut(body string, room int) int {
	cut, lineCut := 0, 0
	characters := 0
	isFenced := false
	previousBlank := false
	lastHeading := false
	for offset := 0; offset < len(body); {
		end := strings.IndexByte(body[offset:], '\n')
		if end < 0 {
			end = len(body) - offset
		} else {
			end++
		}
		line := body[offset : offset+end]
		if characters > room {
			break
		}
		if offset > 0 {
			lineCut = offset
		}
		if offset > 0 && !isFenced && !lastHeading && isBlockStart(line, previousBlank) {
			cut = offset
		}
		trimmed := strings.TrimRight(line, "\n")
		if isFence(trimmed) {
			isFenced = !isFenced
		}
		if strings.TrimSpace(trimmed) != "" {
			lastHeading = !isFenced && isHeading(trimmed)
		}
		previousBlank = strings.TrimSpace(trimmed) == ""
		characters += utf8.RuneCountInString(line)
		offset += end
	}
	if cut > 0 {
		return cut
	}
	if lineCut > 0 {
		return lineCut
	}
	return runeOffset(body, room)
}

// isBlockStart reports whether a line opens a block of its own at the top
// level: a heading, a list item, or anything after a blank line. Indented
// lines never do, because they are the commit bodies under an entry.
func isBlockStart(line string, previousBlank bool) bool {
	if line == "" || line[0] == ' ' || line[0] == '\t' || strings.TrimSpace(line) == "" {
		return false
	}
	return previousBlank || isHeading(line) || strings.HasPrefix(line, "- ")
}

// isHeading reports whether a line is a markdown heading: one to six "#" and
// a space.
func isHeading(line string) bool {
	level := len(line) - len(strings.TrimLeft(line, "#"))
	return level >= 1 && level <= 6 && len(line) > level && line[level] == ' '
}

// isFence reports whether a top-level line opens or closes a code fence. Only
// a fence at column zero can hold a line that looks like a heading or a list
// item at column zero; a fence inside a commit body is indented with it.
func isFence(line string) bool {
	return strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~")
}

// runeOffset is the byte offset of the character at index characters.
func runeOffset(text string, characters int) int {
	for offset := range text {
		if characters == 0 {
			return offset
		}
		characters--
	}
	return len(text)
}

// continuationLine is the last line of a cut body: where the rest of the notes
// are. The package's changelog holds them when it records this release;
// otherwise the package's commit history at the tag is the closest record.
// GitHub Enterprise serves its web pages on a host its API URL does not
// name, so the line names the file and the tag without a link there.
func (r *Releaser) continuationLine(rel *plan.Release, tag string) string {
	folder, isInRepository := r.packageFolder(rel)
	if !isInRepository {
		if !changelog.IsPublicGitHub(r.APIURL) {
			return "The release notes continue in the repository at " + tag + "."
		}
		return "The release notes continue in the repository at [" + tag + "](" + r.webURL("tree", tag, "") + ")."
	}
	if !rel.Pkg.Changelog.IsRecorded(rel.Channel) {
		label := "the commit history of " + displayFolder(folder)
		if !changelog.IsPublicGitHub(r.APIURL) {
			return "The release notes continue in " + label + " at " + tag + "."
		}
		return "The release notes continue in [" + label + "](" + r.webURL("commits", tag, folder) + ")."
	}
	file := rel.Pkg.Changelog.File
	if file == "" {
		file = "CHANGELOG.md"
	}
	path := filepath.ToSlash(filepath.Join(folder, file))
	if !changelog.IsPublicGitHub(r.APIURL) {
		return "The release notes continue in " + path + " at " + tag + "."
	}
	return "The release notes continue in [" + path + "](" + r.webURL("blob", tag, path) + ")."
}

// packageFolder is the package's folder relative to the root of the repository
// its release is created in, slash-separated, "." for the root itself. A
// polyrepo package's repository is its own; any other package's is Root.
func (r *Releaser) packageFolder(rel *plan.Release) (string, bool) {
	root := rel.Pkg.RepoRoot
	if root == "" {
		root = r.Root
	}
	if root == "" {
		return "", false
	}
	folder, err := filepath.Rel(root, rel.Pkg.Dir)
	if err != nil || folder == ".." || strings.HasPrefix(folder, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(folder), true
}

func displayFolder(folder string) string {
	if folder == "." {
		return "the repository"
	}
	return folder
}

// webURL is a github.com page of the repository at tag: kind is "blob",
// "tree" or "commits", and path is repository-relative. A tag carries
// slashes of its own, so it is escaped segment by segment.
func (r *Releaser) webURL(kind, tag, path string) string {
	url := "https://github.com/" + r.Owner + "/" + r.Repo + "/" + kind + "/" + escapeSegments(tag)
	if path != "" && path != "." {
		url += "/" + escapeSegments(path)
	}
	return url
}

func escapeSegments(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		segments[i] = neturl.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

// boundBody renders one release's body within the API's limit. A body over it
// is cut inside its sections, so the header, the authors, the "### Release"
// details, a note and the footer all survive, and the line linking the rest
// closes the sections. Only when the parts around the sections leave them no
// room is the whole body cut instead.
func (r *Releaser) boundBody(rel *plan.Release, parts changelog.BodyParts) string {
	rendered := parts.String()
	if utf8.RuneCountInString(rendered) <= releaseBodyBudget {
		return rendered
	}
	tag := rel.TagName()
	text := cutBody(parts, r.continuationLine(rel, tag))
	r.Log.Warn().Str("code", plan.CodeGitHubBodyCut).Str("package", rel.Pkg.Name).Str("tag", tag).
		Int("characters", utf8.RuneCountInString(rendered)).Int("kept", utf8.RuneCountInString(text)).
		Int("limit", releaseBodyBudget).
		Msg("github release body is over the API limit; it was cut at an entry and links the rest")
	return text
}

// cutBody cuts a body over the budget inside its sections when the parts
// around them leave the sections room, and cuts the whole body otherwise.
func cutBody(parts changelog.BodyParts, continuation string) string {
	room := releaseBodyBudget - surroundingLength(parts)
	if room <= minimumSectionsRoom {
		return boundBody(parts.String(), releaseBodyBudget, continuation)
	}
	parts.Sections = boundBody(parts.Sections, room, continuation)
	return parts.String()
}

// minimumSectionsRoom is the least room worth cutting the sections into. Parts
// around them that leave less are themselves the problem, and cutting the
// whole body is the only way under the limit.
const minimumSectionsRoom = 1_000

// surroundingLength is how many characters a body carries besides its
// sections' own text: the parts around them and the separators between. A
// one-character sections block stands in for the real one, so the separators
// are counted exactly as String writes them.
func surroundingLength(parts changelog.BodyParts) int {
	parts.Sections = "x"
	return utf8.RuneCountInString(parts.String()) - 1
}
