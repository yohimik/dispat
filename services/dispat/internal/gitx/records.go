// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

// What the store a run records to holds, and how a record is written to it.
//
// A release record is a tag, and a tag on a remote is the only copy of it two
// machines share. Everything here follows from that. The inventory is read in
// one ls-remote, because a run asks about every package at once and a fork per
// package would make the question cost grow with the workspace. A record is
// written create-only, because it is a record: the remote decides whether the
// name was free, in the same operation that writes it, so no run can conclude
// from a read a moment earlier that it may replace what somebody else
// published (CCME SPEC.md §19.1, §19.4).

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// RemoteReleaseTags is every tag the remote advertises that one of the given
// packages' release formats could have written, keyed by package and peeled to
// the commit the record names.
//
// One ls-remote for the whole workspace, dispatched through the same literal
// prefix trie the local listing uses, so a remote with a large tag inventory
// costs one walk of each name rather than one match per package.
//
// The coordination refs are left out for the reason the local reader leaves
// them out: the release lock is a tag on HEAD for the whole of a run, and a
// format broad enough to match it would otherwise read it as a record.
func (c *LocalGitx) RemoteReleaseTags(ctx context.Context, remote string, formats map[string]TagFormat) (map[string]Tags, error) {
	out, err := c.run(ctx, "ls-remote", "--tags", "--", remote)
	if err != nil {
		return nil, fmt.Errorf("reading the release records of %s: %w", RedactURL(remote), err)
	}
	index := newPackageTagIndex(formats)
	for _, entry := range parseRemoteTagInventory(out) {
		index.add(entry)
	}
	return index.tags, nil
}

// parseRemoteTagInventory reads `git ls-remote --tags` into one entry per tag
// name, at the commit the name records.
//
// An annotated tag is advertised twice, as the tag object and as the peeled
// `^{}` line, and the peeled line is the one a record is compared by: two runs
// annotating the same commit write different tag objects, and a comparison on
// those would call one release two.
func parseRemoteTagInventory(out string) []tagInventoryEntry {
	commits := make(map[string]string)
	for line := range strings.Lines(out) {
		object, ref, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || !fullObjectID(object) {
			continue
		}
		name, isTag := strings.CutPrefix(ref, "refs/tags/")
		if !isTag || name == "" {
			continue
		}
		peeled, isPeeled := strings.CutSuffix(name, "^{}")
		if isPeeled {
			commits[peeled] = object
			continue
		}
		if _, known := commits[name]; !known {
			commits[name] = object
		}
	}
	names := make([]string, 0, len(commits))
	for name := range commits {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := make([]tagInventoryEntry, 0, len(names))
	for _, name := range names {
		entries = append(entries, tagInventoryEntry{name: name, commit: commits[name]})
	}
	return entries
}

// IsReachableFromHead reports whether the checkout's own head history holds
// the commit.
//
// It is a membership test rather than an ancestry question: the commit graph
// this asks is exactly the commits reachable from HEAD, so a commit absent
// from it is either one the checkout does not hold at all or one that sits off
// its head, and neither can affect what this run plans. A name that is no
// commit at all, the empty string included, is absent for the same reason and
// needs no arm of its own.
//
// That matters for the shape this is asked in, which is once per record the
// checkout lacks: a clone made without tags lacks every one of them, and a
// fork per record would turn one comparison into a process per release the
// repository ever made.
func (c *LocalGitx) IsReachableFromHead(ctx context.Context, commit string) (bool, error) {
	graph, err := c.commitDAG(ctx)
	if err != nil {
		return false, err
	}
	_, isReachable := graph.index[commit]
	return isReachable, nil
}

// ReleaseRef is one tag ref a release push writes: a record under its own
// name, or an alias configured to move.
//
// The distinction is the whole of what this type carries, because it is the
// distinction the push turns on. A record is written create-only and a moving
// alias is the one ref a release may replace, so a caller that cannot say
// which of the two a name is cannot push either of them correctly.
type ReleaseRef struct {
	Name string
	// IsMoving marks an alias declared `moving: true`. A release record is
	// never moving, whatever commit.force says: force means "do not fail
	// because the ref is already there", never "move a published record".
	IsMoving bool
}

// ReleaseRefNames lists the names a push carried, for the line that reports
// what it delivered. The moving flag is a push decision and says nothing to a
// person reading the outcome, so it stays out of the log.
func ReleaseRefNames(refs []ReleaseRef) []string {
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		names = append(names, ref.Name)
	}
	return names
}

// RefResult is what became of one pushed ref.
type RefResult int

const (
	// RefCreated is a name the store did not hold.
	RefCreated RefResult = iota
	// RefExisting is a name the store already held at this release's commit:
	// the retry of a write whose answer was lost (§19.4), and a success.
	RefExisting
	// RefMoved is a moving alias that was re-pointed.
	RefMoved
	// RefAtOtherCommit is a name the store holds at another commit. Nothing
	// was written, and the store keeps what it had.
	RefAtOtherCommit
)

// RefOutcome is one ref of a release push and what became of it. Commit is
// what the store holds the name at, and is filled only for the refused case,
// which is the one a person has to reconcile.
type RefOutcome struct {
	Name   string
	Result RefResult
	Commit string
}

// PushReleaseRefs writes release records to the store and answers what became
// of each, in the order they were given.
//
// Every record is leased against the name being absent, which is git's
// spelling of createRecord's `expectedAbsent`: the check and the write are one
// operation, so a record that appeared between this run's plan and its push
// cannot be replaced by it. A refused name is then read back once, because the
// two refusals call for opposite answers: the same commit is this run's own
// write arriving twice and is a success, while another commit is a published
// record this run must leave exactly where it is.
//
// Moving aliases travel in the same push, forced, because moving is what they
// are for. One push rather than one per ref: a release writes a record and its
// aliases together, and git updates each ref independently, so one refusal
// fails its own ref and no other. A ref the remote declined without holding
// the name is that push failing (resolveRefusedRef): the outcomes of the other
// refs are still answered, beside the error that names it.
func (c *LocalGitx) PushReleaseRefs(ctx context.Context, remote string, refs []ReleaseRef) ([]RefOutcome, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	args := []string{"push", "--porcelain"}
	specs := make([]string, 0, len(refs))
	for _, ref := range refs {
		name := "refs/tags/" + ref.Name
		if err := ValidRefName(name); err != nil {
			return nil, err
		}
		if ref.IsMoving {
			specs = append(specs, "+"+name+":"+name)
			continue
		}
		args = append(args, "--force-with-lease="+name+":")
		specs = append(specs, name+":"+name)
	}
	args = append(args, "--", remote)
	out, runErr := c.runStream(ctx, gitStream{}, append(args, specs...)...)
	outcomes := make([]RefOutcome, 0, len(refs))
	var declined []error
	for _, ref := range refs {
		name := "refs/tags/" + ref.Name
		status, isReported := findPushStatus(out, name)
		if !isReported {
			return nil, transportError(runErr, remote, "pushing %s", name)
		}
		if status.flag != pushRejected {
			outcomes = append(outcomes, RefOutcome{Name: ref.Name, Result: refResultOf(status.flag)})
			continue
		}
		outcome, err := c.resolveRefusedRef(ctx, remote, ref.Name, status)
		if errors.Is(err, ErrRemoteRefused) {
			declined = append(declined, err)
			continue
		}
		if err != nil {
			return nil, err
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes, errors.Join(declined...)
}

// refResultOf reads the porcelain flag of a ref git did write. A flag this
// does not name is some other shape of successful update, which for a
// create-only lease can only be the creation itself.
func refResultOf(status rune) RefResult {
	switch status {
	case pushUpToDate:
		return RefExisting
	case pushForced:
		return RefMoved
	default:
		return RefCreated
	}
}

// resolveRefusedRef reads the store's answer for a name whose create-only push
// was refused, and tells the retry of an uncertain write from a published
// record this run may not touch.
//
// A refusal that leaves the store without the name was about no record at
// all: a hook, a tag rule or a missing permission declined the write. That is
// the push failing, with the reason the remote gave, and never a record at
// another commit, which would send the operator looking for a release nobody
// made.
func (c *LocalGitx) resolveRefusedRef(ctx context.Context, remote, tag string, status pushStatus) (RefOutcome, error) {
	stored, err := c.RemoteTagCommit(ctx, remote, tag)
	if err != nil {
		return RefOutcome{}, err
	}
	if stored == "" {
		return RefOutcome{}, fmt.Errorf("gitx: pushing refs/tags/%s to %s ended as %s, and the remote holds no tag by that name: %w",
			tag, RedactURL(remote), status.formatReason(), ErrRemoteRefused)
	}
	local, err := c.ResolveCommit(ctx, "refs/tags/"+tag)
	if err != nil {
		return RefOutcome{}, fmt.Errorf("reading the commit of the local tag %s: %w", tag, err)
	}
	if stored == local {
		return RefOutcome{Name: tag, Result: RefExisting, Commit: stored}, nil
	}
	return RefOutcome{Name: tag, Result: RefAtOtherCommit, Commit: stored}, nil
}

// RemoteTagCommit is the commit a remote's tag records, annotated tags peeled,
// and the empty string when the remote does not carry the name.
//
// It answers about the commit rather than the tag object, which is what
// RemoteTagObject exists for: a record is identified by what it names, and two
// annotations of one commit are one record written twice.
func (c *LocalGitx) RemoteTagCommit(ctx context.Context, remote, tag string) (string, error) {
	ref := "refs/tags/" + tag
	out, err := c.run(ctx, "ls-remote", "--", remote, ref, ref+"^{}")
	if err != nil {
		return "", fmt.Errorf("reading %s from %s: %w", ref, RedactURL(remote), err)
	}
	object, peeled := "", ""
	for line := range strings.Lines(out) {
		oid, name, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok {
			continue
		}
		switch name {
		case ref:
			object = oid
		case ref + "^{}":
			peeled = oid
		}
	}
	if peeled != "" {
		return peeled, nil
	}
	return object, nil
}
