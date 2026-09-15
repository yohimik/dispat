// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"context"
	"fmt"
	"strings"
)

// ControlHistoryCommit is one reachable control repository commit. Gitlinks
// contains only submodule transitions introduced against that commit's first
// parent; Files retains every changed path for ordinary control planning.
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
func (c *CLI) ControlGitlinkHistory(ctx context.Context) ([]ControlHistoryCommit, error) {
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
	return parseControlGitlinkHistory(out)
}

func parseControlGitlinkHistory(out string) ([]ControlHistoryCommit, error) {
	fields := strings.Split(out, "\x00")
	commits := make([]ControlHistoryCommit, 0)
	for i := 0; ; {
		for i < len(fields) && strings.Trim(fields[i], "\n") == "" {
			i++
		}
		if i >= len(fields) {
			break
		}
		if fields[i] != controlHistoryMarker {
			return nil, fmt.Errorf("gitx: malformed control history marker %q", fields[i])
		}
		i++
		if i+4 >= len(fields) {
			return nil, fmt.Errorf("gitx: truncated control history header")
		}
		sha, parents := fields[i], fields[i+1]
		authorName, authorEmail, message := fields[i+2], fields[i+3], fields[i+4]
		i += 5
		if !fullObjectID(sha) {
			return nil, fmt.Errorf("gitx: malformed control history object id %q", sha)
		}
		parentIDs := strings.Fields(parents)
		for _, parent := range parentIDs {
			if !fullObjectID(parent) {
				return nil, fmt.Errorf("gitx: malformed control history parent %q", parent)
			}
		}
		commit := ControlHistoryCommit{
			SHA:         sha,
			Parents:     parentIDs,
			AuthorName:  strings.TrimSpace(authorName),
			AuthorEmail: strings.TrimSpace(authorEmail),
			Message:     strings.Trim(message, "\n"),
			Gitlinks:    make(map[string]GitlinkTransition),
		}

		// Git's -z framing leaves an empty field after the pretty format. Raw
		// diff records then alternate metadata and pathname until the next
		// marker. Only the first metadata field carries a presentation newline.
		for i < len(fields) {
			field := fields[i]
			if strings.Trim(field, "\n") == "" {
				i++
				continue
			}
			if field == controlHistoryMarker {
				break
			}
			meta := strings.TrimPrefix(field, "\n")
			if !strings.HasPrefix(meta, ":") || i+1 >= len(fields) {
				return nil, fmt.Errorf("gitx: malformed control history raw record %q", field)
			}
			path := fields[i+1]
			i += 2
			commit.Files = append(commit.Files, path)
			parts := strings.Fields(strings.TrimPrefix(meta, ":"))
			if len(parts) != 5 {
				return nil, fmt.Errorf("gitx: malformed control history raw metadata %q", meta)
			}
			if parts[0] == "160000" || parts[1] == "160000" {
				commit.Gitlinks[path] = GitlinkTransition{From: parts[2], To: parts[3]}
			}
		}
		commits = append(commits, commit)
	}
	return commits, nil
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
