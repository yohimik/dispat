package gitx

import (
	"context"
	"fmt"
	"strings"
)

// PushRelease writes a repository's branch and release refs without forcing
// immutable tags. Only explicitly moving aliases may replace a remote ref.
// An empty branch pushes tags only, including from a detached checkout.
func (c *CLI) PushRelease(ctx context.Context, remote, branch string, tags, moving []string) error {
	refs := make([]string, 0, len(tags)+len(moving))
	branchRef := ""
	if branch != "" {
		if err := ValidRefName("refs/heads/" + branch); err != nil {
			return fmt.Errorf("invalid release branch %q: %w", branch, err)
		}
		branchRef = "HEAD:refs/heads/" + branch
	}
	for _, tag := range tags {
		if err := ValidRefName("refs/tags/" + tag); err != nil {
			return err
		}
		refs = append(refs, "refs/tags/"+tag+":refs/tags/"+tag)
	}
	for _, tag := range moving {
		if err := ValidRefName("refs/tags/" + tag); err != nil {
			return err
		}
		refs = append(refs, "+refs/tags/"+tag+":refs/tags/"+tag)
	}
	if branchRef != "" {
		if _, err := c.run(ctx, "push", "--", remote, branchRef); err != nil {
			return classifyPush(err)
		}
	}
	if len(refs) == 0 {
		return nil
	}
	_, err := c.run(ctx, append([]string{"push", "--", remote}, refs...)...)
	return classifyPush(err)
}

// VerifyRemoteRelease proves the immutable source tag is remotely available
// at the exact recorded revision before a control checkpoint can reference it.
func (c *CLI) VerifyRemoteRelease(ctx context.Context, remote, tag, revision string) error {
	ref := "refs/tags/" + tag
	out, err := c.run(ctx, "ls-remote", "--", remote, ref, ref+"^{}")
	if err != nil {
		return err
	}
	var target, peeled string
	for _, line := range strings.Split(out, "\n") {
		sha, name, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if name == ref {
			target = sha
		}
		if name == ref+"^{}" {
			peeled = sha
		}
	}
	if peeled != "" {
		target = peeled
	}
	if target != revision {
		return fmt.Errorf("source tag %s at %s is not available from %s; record and push that source release before pushing its control checkpoint", tag, revision, RedactURL(remote))
	}
	return nil
}

// VerifyRemoteBranch proves a source revision is the configured remote branch
// tip before a control push makes a gitlink to it durable.
func (c *CLI) VerifyRemoteBranch(ctx context.Context, remote, branch, revision string) error {
	ref := "refs/heads/" + branch
	out, err := c.run(ctx, "ls-remote", "--heads", "--", remote, ref)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(out, "\n") {
		sha, name, ok := strings.Cut(line, "\t")
		if ok && name == ref && sha == revision {
			return nil
		}
	}
	return fmt.Errorf("source revision %s is not available as %s/%s; push that source branch before pushing its control checkpoint", revision, RedactURL(remote), branch)
}

// GitlinkCommit resolves one exact gitlink entry from a tree without treating
// the repository-relative path as revision syntax.
func (c *CLI) GitlinkCommit(ctx context.Context, revision, path string) (string, error) {
	out, err := c.run(ctx, "ls-tree", "-z", revision, "--", path)
	if err != nil {
		return "", err
	}
	for _, record := range strings.Split(out, "\x00") {
		metadata, gotPath, ok := strings.Cut(record, "\t")
		if !ok || gotPath != path {
			continue
		}
		fields := strings.Fields(metadata)
		if len(fields) == 3 && fields[0] == "160000" && fields[1] == "commit" {
			return fields[2], nil
		}
	}
	return "", fmt.Errorf("%s has no gitlink at %s", revision, path)
}

// RemoteURL returns the configured destination for a repository's records.
func (c *CLI) RemoteURL(ctx context.Context, remote string) (string, error) {
	out, err := c.run(ctx, "remote", "get-url", remote)
	return strings.TrimSpace(out), err
}
