// Package github creates a GitHub release for every published package. It is
// the same changelog data as the file writer, delivered through a different
// release.ReleaseRecorder implementation.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/yohimik/monorel/internal/changelog"
	"github.com/yohimik/monorel/internal/gitx"
	"github.com/yohimik/monorel/internal/plan"
)

// DefaultAPIURL is the public GitHub REST API endpoint.
const DefaultAPIURL = "https://api.github.com"

// Releaser creates a release named after the package tag via the GitHub REST
// API (POST /repos/{owner}/{repo}/releases). The release body is the rendered
// changelog sections, plus a "### Release" section documenting the release
// commit and tag when CommitSHA is set. If the tag has not been pushed yet,
// GitHub creates it at TargetCommitish when set, otherwise at the default
// branch head.
type Releaser struct {
	APIURL string // default DefaultAPIURL; set for GitHub Enterprise
	Owner  string
	Repo   string
	Token  string
	Format changelog.Format
	Client *http.Client // default: 30s-timeout client

	// CommitSHA, when set (release-commit mode), is recorded in the release
	// body together with the tag, so the release documents the exact commit
	// and tag even when they have not been pushed yet.
	CommitSHA string
	// TargetCommitish, when set, is sent as target_commitish so GitHub
	// creates the tag at exactly that commit. Only safe when the commit
	// already exists on the remote (i.e. after a push) — GitHub rejects
	// unknown SHAs.
	TargetCommitish string
}

type releaseRequest struct {
	TagName         string `json:"tag_name"`
	Name            string `json:"name"`
	Body            string `json:"body"`
	TargetCommitish string `json:"target_commitish,omitempty"`
}

// Verify checks that the repository is reachable with the configured token
// (GET /repos/{owner}/{repo} must return 200). Meant to run before any
// release work so misconfigured credentials fail fast.
func (r *Releaser) Verify(ctx context.Context) error {
	api := r.APIURL
	if api == "" {
		api = DefaultAPIURL
	}
	url := fmt.Sprintf("%s/repos/%s/%s", strings.TrimSuffix(api, "/"), r.Owner, r.Repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("github: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+r.Token)
	req.Header.Set("Accept", "application/vnd.github+json")

	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("github: verifying %s/%s: %w", r.Owner, r.Repo, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("github: verifying %s/%s: unexpected status %s: %s",
			r.Owner, r.Repo, resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

// Record implements release.ReleaseRecorder.
func (r *Releaser) Record(ctx context.Context, rel *plan.Release) error {
	tag := gitx.TagName(rel.Pkg.Name, rel.Next)
	body := changelog.RenderSections(rel, r.Format)
	if r.CommitSHA != "" {
		if body != "" {
			body += "\n"
		}
		body += "### Release\n\n- commit: " + r.CommitSHA + "\n- tag: " + tag + "\n"
	}
	payload, err := json.Marshal(releaseRequest{
		TagName:         tag,
		Name:            tag,
		Body:            body,
		TargetCommitish: r.TargetCommitish,
	})
	if err != nil {
		return fmt.Errorf("github: %w", err)
	}

	api := r.APIURL
	if api == "" {
		api = DefaultAPIURL
	}
	url := fmt.Sprintf("%s/repos/%s/%s/releases", strings.TrimSuffix(api, "/"), r.Owner, r.Repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("github: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+r.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("github: creating release %s: %w", tag, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("github: creating release %s: unexpected status %s: %s",
			tag, resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}
