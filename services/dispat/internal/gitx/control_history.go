// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

// ControlHistoryCommit is one reachable control repository commit. Gitlinks
// contains only submodule transitions introduced against that commit's first
// parent, and is nil for a commit without one; Files retains every changed
// path for ordinary control planning.
type ControlHistoryCommit struct {
	SHA         string
	Parents     []string
	AuthorName  string
	AuthorEmail string
	Message     string
	Files       []string
	Gitlinks    map[string]GitlinkTransition
}

// The marker is emitted as its own NUL-delimited field. A pathname equal to
// the marker is still unambiguous because paths are consumed only after a raw
// diff header, whereas the marker is accepted only where another header may
// begin.
const controlHistoryMarker = "dispat-control-gitlink-history-v1"

// ControlGitlinkHistory reads the control HEAD's whole reachable DAG and every
// gitlink delta in one cancellable Git process. Results are newest first in
// topological order, matching git log. Merge deltas use the first parent and
// the root commit is diffed against the empty tree.
func (c *LocalGitx) ControlGitlinkHistory(ctx context.Context) ([]ControlHistoryCommit, error) {
	out, err := c.run(ctx,
		"log",
		"--topo-order",
		"--format=%x00"+controlHistoryMarker+"%x00%H%x00%P%x00%an%x00%ae%x00%B%x00",
		"--raw",
		"--root",
		"--no-abbrev",
		"--no-renames",
		"--diff-merges=first-parent",
		"-z",
		"HEAD",
	)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	if strings.TrimSpace(out) == "" {
		return nil, fmt.Errorf("gitx: malformed empty control history")
	}
	history, err := parseControlGitlinkHistory(out)
	// --raw diffs every commit of the walk.
	commitsDiffed.Add(uint64(len(history)))
	return history, err
}

// parseControlGitlinkHistory reads the whole `git log` answer into the commits
// the planner keeps.
//
// Every string it keeps is copied out of the answer. A retained substring
// would pin the entire log output, tens of megabytes on a repository with a
// long history, for as long as the plan lives, and the plan lives for the
// whole run.
func parseControlGitlinkHistory(out string) ([]ControlHistoryCommit, error) {
	fields := nulFields{rest: out}
	// The planner keeps the slice for the whole run, so it is allocated once at
	// its size rather than grown. A pathname spelled like the marker only
	// makes the count a larger capacity hint.
	commits := make([]ControlHistoryCommit, 0, strings.Count(out, "\x00"+controlHistoryMarker+"\x00"))
	field, isField := fields.nextNonBlank()
	for isField {
		if field != controlHistoryMarker {
			return nil, fmt.Errorf("gitx: malformed control history marker %q", field)
		}
		commit, err := readControlHistoryHeader(&fields)
		if err != nil {
			return nil, err
		}
		field, isField, err = readControlHistoryRawRecords(&fields, &commit)
		if err != nil {
			return nil, err
		}
		commits = append(commits, commit)
	}
	return commits, nil
}

// nulFields walks the NUL-separated fields of a -z answer in place, the way
// strings.Split would list them, without materialising the list.
type nulFields struct {
	rest   string
	isDone bool
}

func (f *nulFields) next() (string, bool) {
	if f.isDone {
		return "", false
	}
	field, rest, isCut := strings.Cut(f.rest, "\x00")
	f.rest = rest
	f.isDone = !isCut
	return field, true
}

// nextNonBlank skips the empty and newline-only fields git's framing leaves
// between a commit's pretty format, its raw records and the next commit.
func (f *nulFields) nextNonBlank() (string, bool) {
	for {
		field, isField := f.next()
		if !isField || strings.Trim(field, "\n") != "" {
			return field, isField
		}
	}
}

func readControlHistoryHeader(fields *nulFields) (ControlHistoryCommit, error) {
	var header [5]string
	for i := range header {
		field, isField := fields.next()
		if !isField {
			return ControlHistoryCommit{}, fmt.Errorf("gitx: truncated control history header")
		}
		header[i] = field
	}
	sha, parents := header[0], header[1]
	authorName := strings.TrimSpace(header[2])
	authorEmail := strings.TrimSpace(header[3])
	message := strings.Trim(header[4], "\n")
	if !fullObjectID(sha) {
		return ControlHistoryCommit{}, fmt.Errorf("gitx: malformed control history object id %q", sha)
	}
	// One copy holds every header string of the commit: they are kept and
	// dropped together, so separate copies would only cost an allocation each.
	kept := copyJoined(sha, parents, authorName, authorEmail, message)
	commit := ControlHistoryCommit{SHA: kept[:len(sha)]}
	kept = kept[len(sha):]
	commit.Parents = strings.Fields(kept[:len(parents)])
	for _, parent := range commit.Parents {
		if !fullObjectID(parent) {
			return ControlHistoryCommit{}, fmt.Errorf("gitx: malformed control history parent %q", parent)
		}
	}
	kept = kept[len(parents):]
	commit.AuthorName, kept = kept[:len(authorName)], kept[len(authorName):]
	commit.AuthorEmail, commit.Message = kept[:len(authorEmail)], kept[len(authorEmail):]
	return commit, nil
}

// readControlHistoryRawRecords reads one commit's raw diff records and returns
// the field that ended them: the next commit's marker, or none at the end.
//
// Git's -z framing leaves an empty field after the pretty format. Raw diff
// records then alternate metadata and pathname until the next marker. Only the
// first metadata field carries a presentation newline.
func readControlHistoryRawRecords(fields *nulFields, commit *ControlHistoryCommit) (string, bool, error) {
	if records := countControlHistoryRawRecords(*fields); records > 0 {
		commit.Files = make([]string, 0, records)
	}
	for {
		field, isField := fields.nextNonBlank()
		if !isField || field == controlHistoryMarker {
			return field, isField, nil
		}
		meta := strings.TrimPrefix(field, "\n")
		path, isPath := fields.next()
		if !strings.HasPrefix(meta, ":") || !isPath {
			return "", false, fmt.Errorf("gitx: malformed control history raw record %q", field)
		}
		path = strings.Clone(path)
		commit.Files = append(commit.Files, path)
		parts, isRecord := splitRawMetadata(strings.TrimPrefix(meta, ":"))
		if !isRecord {
			return "", false, fmt.Errorf("gitx: malformed control history raw metadata %q", meta)
		}
		if parts[0] != "160000" && parts[1] != "160000" {
			continue
		}
		if commit.Gitlinks == nil {
			commit.Gitlinks = make(map[string]GitlinkTransition)
		}
		ids := copyJoined(parts[2], parts[3])
		commit.Gitlinks[path] = GitlinkTransition{From: ids[:len(parts[2])], To: ids[len(parts[2]):]}
	}
}

// countControlHistoryRawRecords counts the records ahead of the next marker so
// that Files is allocated once at its final size. It reads a copy of the
// cursor; a malformed answer only makes the count a worse capacity hint.
func countControlHistoryRawRecords(fields nulFields) int {
	records := 0
	for {
		field, isField := fields.nextNonBlank()
		if !isField || field == controlHistoryMarker {
			return records
		}
		if _, isPath := fields.next(); !isPath {
			return records
		}
		records++
	}
}

// copyJoined is the values written into one new string. Unlike a
// concatenation, it never hands back one of its operands, which would be a
// substring of the log answer.
func copyJoined(values ...string) string {
	size := 0
	for _, value := range values {
		size += len(value)
	}
	var joined strings.Builder
	joined.Grow(size)
	for _, value := range values {
		joined.WriteString(value)
	}
	return joined.String()
}

// splitRawMetadata is strings.Fields over a raw diff header, which has
// exactly five fields, without allocating the slice for the ASCII headers git
// writes.
func splitRawMetadata(meta string) ([5]string, bool) {
	var parts [5]string
	if !isASCII(meta) {
		fields := strings.Fields(meta)
		copy(parts[:], fields)
		return parts, len(fields) == len(parts)
	}
	n := 0
	for start := 0; start < len(meta); {
		if isASCIISpace(meta[start]) {
			start++
			continue
		}
		end := start
		for end < len(meta) && !isASCIISpace(meta[end]) {
			end++
		}
		if n == len(parts) {
			return parts, false
		}
		parts[n] = meta[start:end]
		n++
		start = end
	}
	return parts, n == len(parts)
}

func isASCII(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// isASCIISpace is unicode.IsSpace restricted to ASCII, the set strings.Fields
// splits an ASCII string on.
func isASCIISpace(b byte) bool {
	switch b {
	case '\t', '\n', '\v', '\f', '\r', ' ':
		return true
	}
	return false
}

func fullObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
