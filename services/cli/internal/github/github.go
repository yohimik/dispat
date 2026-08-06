// Package github creates a GitHub release for every published package. It is
// the same changelog data as the file writer, delivered through a different
// release.ReleaseRecorder implementation.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yohimik/dispat/services/cli/internal/changelog"
	"github.com/yohimik/dispat/services/cli/internal/plan"
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
	// Prerelease marks a release on a prerelease channel (§11.1), so that
	// GitHub does not present a `1.3.0-beta.0` as the repository's latest
	// release. It is always sent: a graduation has to be able to clear the
	// flag as well as set it.
	Prerelease bool `json:"prerelease"`
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
	tag := rel.TagName()
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
		Prerelease:      rel.IsPrerelease(),
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
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("github: creating release %s: unexpected status %s: %s",
			tag, resp.Status, strings.TrimSpace(string(respBody)))
	}
	paths, err := attachmentPaths(rel)
	if err != nil {
		return fmt.Errorf("github: release %s: %w", tag, err)
	}
	if len(paths) == 0 {
		return nil
	}
	uploadURL, err := assetUploadURL(respBody)
	if err != nil {
		return fmt.Errorf("github: release %s: %w", tag, err)
	}
	for _, p := range paths {
		if err := r.uploadAsset(ctx, client, uploadURL, tag, p); err != nil {
			return err
		}
	}
	return nil
}

// AttachmentsOutput is the script output the recorder reads release assets
// from: a whitespace-separated list of absolute file paths a script exported
// under this name (so scripts see it as DISPAT_OUTPUT_GITHUB_ATTACHMENTS).
const AttachmentsOutput = "GITHUB_ATTACHMENTS"

// attachmentPaths resolves and validates the GITHUB_ATTACHMENTS output. An
// invalid entry — a relative path, a missing file, a directory — is an error
// rather than a skip: a typo would otherwise surface as a silently missing
// asset on the release.
func attachmentPaths(rel *plan.Release) ([]string, error) {
	raw, ok := rel.Output(AttachmentsOutput)
	if !ok {
		return nil, nil
	}
	paths := strings.Fields(raw)
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("%s: not an absolute path: %q", AttachmentsOutput, p)
		}
		fi, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", AttachmentsOutput, err)
		}
		if fi.IsDir() {
			return nil, fmt.Errorf("%s: %q names a directory, want a file", AttachmentsOutput, p)
		}
	}
	return paths, nil
}

// assetUploadURL extracts the asset endpoint from a created release: the
// upload_url field, stripped of its {?name,label} URI-template suffix.
func assetUploadURL(created []byte) (string, error) {
	var release struct {
		UploadURL string `json:"upload_url"`
	}
	if err := json.Unmarshal(created, &release); err != nil {
		return "", fmt.Errorf("parsing created release: %w", err)
	}
	if release.UploadURL == "" {
		return "", errors.New("created release carries no upload_url, cannot attach assets")
	}
	if i := strings.IndexByte(release.UploadURL, '{'); i >= 0 {
		return release.UploadURL[:i], nil
	}
	return release.UploadURL, nil
}

// uploadAsset attaches one file to a release through the endpoint the release
// itself advertised (uploads.github.com on the public API). The asset is
// named after the file.
func (r *Releaser) uploadAsset(ctx context.Context, client *http.Client, uploadURL, tag, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("github: release %s asset: %w", tag, err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("github: release %s asset: %w", tag, err)
	}

	name := filepath.Base(path)
	url := uploadURL + "?name=" + neturl.QueryEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, f)
	if err != nil {
		return fmt.Errorf("github: %w", err)
	}
	req.ContentLength = fi.Size()
	req.Header.Set("Authorization", "Bearer "+r.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("github: uploading asset %s to release %s: %w", name, tag, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("github: uploading asset %s to release %s: unexpected status %s: %s",
			name, tag, resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}
