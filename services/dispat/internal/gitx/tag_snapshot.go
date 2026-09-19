// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

import (
	"context"
	"sort"
	"strings"
)

// TagNamespace describes every tag spelling a package may write. Release is
// the readable release-tag format; Aliases are write-only convenience refs.
type TagNamespace struct {
	Package string
	Release TagFormat
	Aliases []AliasFormat
}

// TagRefTarget is the exact value of one refs/tags entry. Object is the ref's
// direct object id. Peeled is the referenced object's id for an annotated tag
// and is empty for a lightweight tag. Retaining both makes an annotation
// replacement observable even when it still peels to the same commit.
type TagRefTarget struct {
	Object string
	Peeled string
}

// Commit returns the commit-like target releases compare with: the peeled
// object for an annotated tag and the direct object for a lightweight one.
func (t TagRefTarget) Commit() string {
	if t.Peeled != "" {
		return t.Peeled
	}
	return t.Object
}

// TagSnapshot is the exact relevant local tag-ref inventory at one instant,
// keyed by short tag name. It excludes release-lock coordination refs.
type TagSnapshot map[string]TagRefTarget

// TagSnapshotMatcher is a compiled repository-local set of package and alias
// tag namespaces. It is immutable and may be reused by every guard read in a
// release run.
type TagSnapshotMatcher struct {
	root *tagPrefixNode[refNamespaceMatcher]
}

// NewTagSnapshotMatcher compiles the namespaces once for repeated snapshot
// reads. The configuration validator has already checked each format.
func NewTagSnapshotMatcher(namespaces []TagNamespace) *TagSnapshotMatcher {
	root := &tagPrefixNode[refNamespaceMatcher]{}
	for _, matcher := range namespaceMatchers(namespaces) {
		root.add(matcher.prefix, matcher)
	}
	return &TagSnapshotMatcher{root: root}
}

// RelevantTagSnapshot reads the repository's tag refs once and retains only
// refs which a configured package can write. Matching is dispatched through
// a literal-prefix trie, so disjoint package namespaces do not multiply the
// cost of a large ref inventory.
func (c *LocalGitx) RelevantTagSnapshot(ctx context.Context, matcher *TagSnapshotMatcher) (TagSnapshot, error) {
	if matcher == nil || matcher.root == nil {
		return TagSnapshot{}, nil
	}
	out, err := c.run(ctx, "for-each-ref",
		"--format=%(refname:short)\t%(objectname)\t%(*objectname)", "refs/tags")
	if err != nil {
		return nil, err
	}
	return parseRelevantTagSnapshot(out, matcher), nil
}

type refNamespaceMatcher struct {
	prefix  string
	matches func(string) bool
}

func literalTagPrefix(tpl *tagTemplate, pkg string) string {
	var b strings.Builder
	for _, segment := range tpl.segs {
		switch segment.kind {
		case segLiteral:
			b.WriteString(segment.text)
		case segName:
			b.WriteString(pkg)
		default:
			return b.String()
		}
	}
	return b.String()
}

func namespaceMatchers(namespaces []TagNamespace) []refNamespaceMatcher {
	ordered := append([]TagNamespace(nil), namespaces...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Package < ordered[j].Package })
	matchers := make([]refNamespaceMatcher, 0, len(ordered)*2)
	for _, namespace := range ordered {
		if release, ok := newPackageTagMatcher(namespace.Package, namespace.Release); ok {
			matchers = append(matchers, refNamespaceMatcher{prefix: release.prefix, matches: release.matches})
		}
		for _, format := range namespace.Aliases {
			tpl := compileTagFormat(string(format))
			alias := format.Matcher(namespace.Package)
			matchers = append(matchers, refNamespaceMatcher{
				prefix: literalTagPrefix(tpl, namespace.Package), matches: alias.IsMatch,
			})
		}
	}
	return matchers
}

func parseRelevantTagSnapshot(out string, matcher *TagSnapshotMatcher) TagSnapshot {
	if matcher == nil || matcher.root == nil {
		return TagSnapshot{}
	}
	result := make(TagSnapshot)
	for line := range strings.Lines(out) {
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		name, rest, ok := strings.Cut(line, "\t")
		if !ok || name == "" || name == LockTagName || strings.HasPrefix(name, LockAttemptTagPrefix) {
			continue
		}
		object, peeled, ok := strings.Cut(rest, "\t")
		if !ok || object == "" {
			continue
		}
		if extra, _, found := strings.Cut(peeled, "\t"); found {
			peeled = extra
		}
		matched := false
		for node, depth := matcher.root, 0; node != nil; depth++ {
			for _, matcher := range node.matchers {
				if matcher.matches(name) {
					matched = true
					break
				}
			}
			if matched || depth == len(name) {
				break
			}
			node = node.child(name[depth])
		}
		if matched {
			result[strings.Clone(name)] = TagRefTarget{
				Object: strings.Clone(object), Peeled: strings.Clone(peeled),
			}
		}
	}
	return result
}
