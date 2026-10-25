// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package gitx

// Publication receipts (CCME SPEC.md §19.4).
//
// A release cannot safely expose a tag before it has durable publication
// evidence. A receipt closes the gap after the publish command succeeds:
// the run pushes it before it creates a tag in tag-only mode or before the
// later release-commit phase. It is an annotated tag object naming the
// published target, kept under a namespace of its own so that no tag
// listing, baseline query or `git fetch --tags` reads it as a release record.
// The next run reads it as "published, record pending". Commit-mode finalize
// removes it atomically with the branch and release tag update.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ReceiptRefPrefix is the namespace receipts live under, locally and on the
// remote. A receipt's ref is this prefix followed by the release tag's name.
const ReceiptRefPrefix = "refs/dispat/receipts/"

// receiptHeader opens every receipt message and versions its format.
const receiptHeader = "dispat receipt v1"

// Receipt is one package's publication, recorded before its release tag can
// be written.
type Receipt struct {
	// Tag is the release tag the receipt stands in for.
	Tag     string
	Package string
	Version string
	// Commit is the commit the release was planned on, which is what the
	// receipt object names.
	Commit string
	// PlannedHead is the commit the release plan started from. Commit may be
	// an exported tag target created by a publish script.
	PlannedHead string
	// ExportedCommit is the publish script's explicit tag target, if any.
	ExportedCommit string
	// Integrity is the artefact identity the publication exported, empty when
	// it exported none.
	Integrity string
	// Object is the receipt object's own id, the value a delete is leased on.
	// Empty until the receipt is written or read.
	Object string
}

// Ref is the receipt's ref name.
func (r Receipt) Ref() string { return ReceiptRefPrefix + r.Tag }

// formatReceiptMessage renders the receipt's message: the header line, then
// one `key: value` line per field.
func formatReceiptMessage(r Receipt) (string, error) {
	lines := []string{receiptHeader, ""}
	for _, field := range [][2]string{
		{"package", r.Package}, {"version", r.Version}, {"tag", r.Tag},
		{"planned-head", r.PlannedHead}, {"exported-commit", r.ExportedCommit}, {"integrity", r.Integrity},
	} {
		if strings.ContainsAny(field[1], "\r\n") {
			return "", fmt.Errorf("gitx: receipt %s: the %s holds a line break", r.Tag, field[0])
		}
		if field[1] != "" {
			lines = append(lines, field[0]+": "+field[1])
		}
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// ParseReceiptMessage reads a receipt message back. A message that does not
// open with the receipt header is not a receipt.
func ParseReceiptMessage(message string) (Receipt, error) {
	header, body, _ := strings.Cut(message, "\n")
	if strings.TrimSpace(header) != receiptHeader {
		return Receipt{}, fmt.Errorf("gitx: not a dispat receipt: %q", header)
	}
	var r Receipt
	for line := range strings.Lines(body) {
		key, value, isField := strings.Cut(strings.TrimSpace(line), ": ")
		if !isField {
			continue
		}
		switch key {
		case "package":
			r.Package = value
		case "version":
			r.Version = value
		case "tag":
			r.Tag = value
		case "integrity":
			r.Integrity = value
		case "planned-head":
			r.PlannedHead = value
		case "exported-commit":
			r.ExportedCommit = value
		}
	}
	if r.Package == "" || r.Version == "" || r.Tag == "" || r.PlannedHead == "" {
		return Receipt{}, errors.New("gitx: a receipt names no package, version, tag or planned head")
	}
	return r, nil
}

// WriteReceipt creates the receipt object and its local ref, and answers the
// receipt with its object id. A receipt this checkout already holds at the
// same commit is answered as it is, which is what makes a retry of the same
// publication write nothing new; one at another commit is refused.
func (c *LocalGitx) WriteReceipt(ctx context.Context, r Receipt) (Receipt, error) {
	if r.PlannedHead == "" {
		r.PlannedHead = r.Commit
	}
	if !fullObjectID(r.Commit) || !fullObjectID(r.PlannedHead) ||
		(r.ExportedCommit != "" && !fullObjectID(r.ExportedCommit)) {
		return Receipt{}, fmt.Errorf("gitx: receipt %s has a malformed commit identity", r.Tag)
	}
	if (r.ExportedCommit == "" && r.Commit != r.PlannedHead) ||
		(r.ExportedCommit != "" && r.Commit != r.ExportedCommit) {
		return Receipt{}, fmt.Errorf("gitx: receipt %s has a tag target inconsistent with its planned head or export", r.Tag)
	}
	if err := validRefName(r.Ref()); err != nil {
		return Receipt{}, fmt.Errorf("gitx: receipt for %s: %w", r.Tag, err)
	}
	existing, isHeld, err := c.findReceipt(ctx, r.Ref())
	if err != nil {
		return Receipt{}, err
	}
	if isHeld {
		if existing.Commit != r.Commit || existing.PlannedHead != r.PlannedHead ||
			existing.ExportedCommit != r.ExportedCommit || existing.Package != r.Package ||
			existing.Version != r.Version || existing.Integrity != r.Integrity {
			return Receipt{}, fmt.Errorf("gitx: a receipt for %s already records a different publication identity", r.Tag)
		}
		return existing, nil
	}
	message, err := formatReceiptMessage(r)
	if err != nil {
		return Receipt{}, err
	}
	ident, err := c.run(ctx, "var", "GIT_COMMITTER_IDENT")
	if err != nil {
		return Receipt{}, fmt.Errorf("gitx: reading the identity a receipt is written under: %w", err)
	}
	object := fmt.Sprintf("object %s\ntype commit\ntag %s\ntagger %s\n\n%s", r.Commit, r.Tag, strings.TrimSpace(ident), message)
	oid, err := c.runStream(ctx, gitStream{stdin: strings.NewReader(object)}, "mktag")
	if err != nil {
		return Receipt{}, fmt.Errorf("gitx: writing the receipt for %s: %w", r.Tag, err)
	}
	r.Object = strings.TrimSpace(oid)
	// The empty old value makes the write create-only, so two writers of one
	// receipt cannot replace each other's.
	if _, err := c.run(ctx, "update-ref", r.Ref(), r.Object, ""); err != nil {
		return Receipt{}, fmt.Errorf("gitx: storing the receipt for %s: %w", r.Tag, err)
	}
	return r, nil
}

// findReceipt reads one local receipt by its ref name.
func (c *LocalGitx) findReceipt(ctx context.Context, ref string) (Receipt, bool, error) {
	receipts, err := c.listReceipts(ctx, ref)
	if err != nil || len(receipts) == 0 {
		return Receipt{}, false, err
	}
	return receipts[0], true, nil
}

// Receipts lists every receipt this checkout holds, in tag order. A ref under
// the namespace that is not a readable receipt is skipped with a warning
// rather than failing the run, because it records nothing dispat wrote.
func (c *LocalGitx) Receipts(ctx context.Context) ([]Receipt, error) {
	return c.listReceipts(ctx, ReceiptRefPrefix)
}

func (c *LocalGitx) listReceipts(ctx context.Context, pattern string) ([]Receipt, error) {
	out, err := c.run(ctx, "for-each-ref",
		"--format=%(refname)%00%(objectname)%00%(*objectname)%00%(contents)%1e", pattern)
	if err != nil {
		return nil, fmt.Errorf("gitx: listing receipts: %w", err)
	}
	var receipts []Receipt
	for _, record := range strings.Split(out, "\x1e") {
		fields := strings.SplitN(strings.TrimLeft(record, "\n"), "\x00", 4)
		if len(fields) != 4 {
			continue
		}
		name, object, commit, message := fields[0], fields[1], fields[2], fields[3]
		if pattern != ReceiptRefPrefix && name != pattern {
			continue
		}
		r, err := ParseReceiptMessage(message)
		if err != nil || commit == "" || ReceiptRefPrefix+r.Tag != name {
			c.Log.Warn().Str("ref", name).Msg("a ref in the receipt namespace is not a dispat receipt; ignored")
			continue
		}
		r.Commit, r.Object = commit, object
		if !fullObjectID(r.Commit) || !fullObjectID(r.PlannedHead) ||
			(r.ExportedCommit != "" && !fullObjectID(r.ExportedCommit)) {
			return nil, fmt.Errorf("gitx: receipt %s has a malformed commit identity", r.Tag)
		}
		if (r.ExportedCommit == "" && r.Commit != r.PlannedHead) ||
			(r.ExportedCommit != "" && r.Commit != r.ExportedCommit) {
			return nil, fmt.Errorf("gitx: receipt %s has a tag target inconsistent with its planned head or export", r.Tag)
		}
		receipts = append(receipts, r)
	}
	sort.Slice(receipts, func(i, j int) bool { return receipts[i].Tag < receipts[j].Tag })
	return receipts, nil
}

// FetchReceipts brings the remote's receipts into this checkout's receipt
// namespace in one fetch. No tag travels with it, and a remote holding none
// fetches nothing.
func (c *LocalGitx) FetchReceipts(ctx context.Context, remote string) error {
	_, err := c.run(ctx, "fetch", "--prune", "--no-tags", "--no-write-fetch-head", "--", remote,
		"+"+ReceiptRefPrefix+"*:"+ReceiptRefPrefix+"*")
	if err != nil {
		return fmt.Errorf("gitx: fetching the receipts of %s: %w", RedactURL(remote), err)
	}
	return nil
}

// PushReceipt writes a local receipt to the remote create-only and answers
// what became of it, in the words a release record push answers in: created,
// already there at this receipt's commit (the retry of a write whose answer
// was lost), or held at another commit.
func (c *LocalGitx) PushReceipt(ctx context.Context, remote string, r Receipt) (RefOutcome, error) {
	ref := r.Ref()
	out, runErr := c.runStream(ctx, gitStream{}, "push", "--porcelain",
		"--force-with-lease="+ref+":", "--", remote, ref+":"+ref)
	status, isReported := findPushStatus(out, ref)
	if !isReported {
		return RefOutcome{}, transportError(runErr, remote, "pushing %s", ref)
	}
	if status.flag != pushRejected {
		return RefOutcome{Name: r.Tag, Result: refResultOf(status.flag)}, nil
	}
	stored, err := c.remotePeeledRef(ctx, remote, ref)
	if err != nil {
		return RefOutcome{}, err
	}
	switch stored {
	case "":
		return RefOutcome{}, fmt.Errorf("gitx: pushing %s to %s ended as %s, and the remote holds no receipt by that name: %w",
			ref, RedactURL(remote), status.formatReason(), ErrRemoteRefused)
	case r.Commit:
		object, err := c.remoteRefObject(ctx, remote, ref)
		if err != nil {
			return RefOutcome{}, err
		}
		if object == r.Object {
			return RefOutcome{Name: r.Tag, Result: RefExisting, Commit: stored}, nil
		}
		return RefOutcome{}, fmt.Errorf("gitx: %s on %s is a different receipt object at the same commit: %w",
			ref, RedactURL(remote), ErrRemoteRefused)
	}
	return RefOutcome{Name: r.Tag, Result: RefAtOtherCommit, Commit: stored}, nil
}

func (c *LocalGitx) remoteRefObject(ctx context.Context, remote, ref string) (string, error) {
	out, err := c.run(ctx, "ls-remote", "--", remote, ref)
	if err != nil {
		return "", fmt.Errorf("reading %s from %s: %w", ref, RedactURL(remote), err)
	}
	for line := range strings.Lines(out) {
		oid, name, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if ok && name == ref {
			return oid, nil
		}
	}
	return "", nil
}

// remotePeeledRef is the commit a remote ref names, annotated objects peeled,
// and the empty string when the remote does not carry the ref.
func (c *LocalGitx) remotePeeledRef(ctx context.Context, remote, ref string) (string, error) {
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

// DeleteReceipt removes a receipt from the remote and from this checkout, the
// remote's copy only while it is still the object this checkout holds. A
// remote that no longer carries it is a delete that already happened.
func (c *LocalGitx) DeleteReceipt(ctx context.Context, remote string, r Receipt) error {
	ref := r.Ref()
	if remote != "" {
		out, runErr := c.runStream(ctx, gitStream{}, "push", "--porcelain",
			"--force-with-lease="+ref+":"+r.Object, "--", remote, ":"+ref)
		status, isReported := findPushStatus(out, ref)
		if !isReported {
			return transportError(runErr, remote, "deleting %s", ref)
		}
		if status.resolve() != pushApplied {
			stored, err := c.remotePeeledRef(ctx, remote, ref)
			if err != nil {
				return err
			}
			if stored != "" {
				return fmt.Errorf("gitx: %s on %s was not removed (%s): %w",
					ref, RedactURL(remote), status.formatReason(), ErrLeaseRejected)
			}
		}
	}
	var commands bytes.Buffer
	commands.WriteString("delete " + ref + " " + r.Object + "\n")
	if _, err := c.runStream(ctx, gitStream{stdin: &commands}, "update-ref", "--stdin"); err != nil {
		return fmt.Errorf("gitx: removing the local receipt for %s: %w", r.Tag, err)
	}
	return nil
}
