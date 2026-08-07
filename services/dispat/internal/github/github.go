// Package github creates a GitHub release for every published package that
// opted in by exporting DISPAT_EXPORT_GITHUB. It is the same changelog data
// as the file writer, delivered through a different release.ReleaseRecorder
// implementation.
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

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/changelog"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// DefaultAPIURL is the public GitHub REST API endpoint.
const DefaultAPIURL = "https://api.github.com"

// Releaser creates a release named after the package tag via the GitHub REST
// API (POST /repos/{owner}/{repo}/releases) — but only for packages whose
// scripts exported plan.GitHubExport (DISPAT_EXPORT_GITHUB); a package
// without the export is skipped. The release body is the rendered changelog
// sections, plus a "### Release" section documenting the release commit and
// tag when CommitSHA is set. If the tag has not been pushed yet, GitHub
// creates it at TargetCommitish when set, otherwise at the default branch
// head.
type Releaser struct {
	APIURL string // default DefaultAPIURL; set for GitHub Enterprise
	Owner  string
	Repo   string
	Token  string
	Format changelog.Format
	Client *http.Client // default: 30s-timeout client
	// Log carries the skip notices and the invalid-attachment warnings. The
	// zero value discards them.
	Log zerolog.Logger

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

// endpoint joins a path onto the configured API base (DefaultAPIURL when
// unset).
func (r *Releaser) endpoint(path string) string {
	api := r.APIURL
	if api == "" {
		api = DefaultAPIURL
	}
	return strings.TrimSuffix(api, "/") + path
}

// do performs one authenticated API call and enforces the expected status.
// Every error is prefixed with what the call was doing, so a failure reads as
// an operation ("creating release core@1.3.0: ...") rather than a URL. The
// response body is returned (bounded) because a created release carries the
// asset endpoint the caller needs. contentLength must be given for bodies the
// http package cannot measure itself (a file stream) — GitHub's upload
// endpoint rejects chunked requests; 0 leaves it to the package.
func (r *Releaser) do(ctx context.Context, method, url string, body io.Reader, contentType string, contentLength int64, wantStatus int, what string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("github: %w", err)
	}
	if contentLength > 0 {
		req.ContentLength = contentLength
	}
	req.Header.Set("Authorization", "Bearer "+r.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: %s: %w", what, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != wantStatus {
		return nil, fmt.Errorf("github: %s: unexpected status %s: %s",
			what, resp.Status, strings.TrimSpace(string(data)))
	}
	return data, nil
}

// Verify checks that the repository is reachable with the configured token
// (GET /repos/{owner}/{repo} must return 200). Meant to run before any
// release work so misconfigured credentials fail fast.
func (r *Releaser) Verify(ctx context.Context) error {
	_, err := r.do(ctx, http.MethodGet, r.endpoint("/repos/"+r.Owner+"/"+r.Repo), nil, "", 0,
		http.StatusOK, fmt.Sprintf("verifying %s/%s", r.Owner, r.Repo))
	return err
}

// Record implements release.ReleaseRecorder. A package whose scripts did not
// export plan.GitHubExport gets no GitHub release: the export is the opt-in,
// per package and per run, and its value names the files to attach.
func (r *Releaser) Record(ctx context.Context, rel *plan.Release) error {
	tag := rel.TagName()
	export, ok := rel.Output(plan.GitHubExport)
	if !ok {
		r.Log.Info().Str("package", rel.Pkg.Name).Str("tag", tag).
			Msgf("github release skipped: no script exported %s", plan.GitHubExport)
		return nil
	}
	// A PACKAGE_<KEY> export overrides both the documented commit and the
	// target_commitish for this one package: its scripts produced the commit
	// the release should hang off, wherever the run's own commit is.
	sha, commitish := r.CommitSHA, r.TargetCommitish
	if exported := rel.ExportedCommit(); exported != "" {
		sha, commitish = exported, exported
	}
	body := changelog.RenderSections(rel, r.Format)
	if sha != "" {
		if body != "" {
			body += "\n"
		}
		body += "### Release\n\n- commit: " + sha + "\n- tag: " + tag + "\n"
	}
	payload, err := json.Marshal(releaseRequest{
		TagName:         tag,
		Name:            tag,
		Body:            body,
		TargetCommitish: commitish,
		Prerelease:      rel.IsPrerelease(),
	})
	if err != nil {
		return fmt.Errorf("github: %w", err)
	}

	respBody, err := r.do(ctx, http.MethodPost, r.endpoint("/repos/"+r.Owner+"/"+r.Repo+"/releases"),
		bytes.NewReader(payload), "application/json", 0, http.StatusCreated, "creating release "+tag)
	if err != nil {
		return err
	}
	paths := r.attachmentPaths(export, tag)
	if len(paths) == 0 {
		return nil
	}
	uploadURL, err := assetUploadURL(respBody)
	if err != nil {
		return fmt.Errorf("github: release %s: %w", tag, err)
	}
	for _, p := range paths {
		if err := r.uploadAsset(ctx, uploadURL, tag, p); err != nil {
			return err
		}
	}
	return nil
}

// attachmentPaths resolves the plan.GitHubExport value: a whitespace-separated
// list of absolute paths to existing files. An invalid entry — a relative
// path, a missing file, a directory — is skipped with a warning rather than
// failing the release: the release itself is out, and the sound files still
// deserve to be attached.
func (r *Releaser) attachmentPaths(export, tag string) []string {
	var paths []string
	for _, p := range strings.Fields(export) {
		reason := ""
		switch fi, err := os.Stat(p); {
		case !filepath.IsAbs(p):
			reason = "not an absolute path"
		case err != nil:
			reason = err.Error()
		case fi.IsDir():
			reason = "names a directory, want a file"
		default:
			paths = append(paths, p)
			continue
		}
		r.Log.Warn().Str("tag", tag).Str("path", p).Str("reason", reason).
			Msgf("github release asset skipped: invalid %s entry", plan.GitHubExport)
	}
	return paths
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
func (r *Releaser) uploadAsset(ctx context.Context, uploadURL, tag, path string) error {
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
	_, err = r.do(ctx, http.MethodPost, uploadURL+"?name="+neturl.QueryEscape(name), f,
		"application/octet-stream", fi.Size(), http.StatusCreated,
		fmt.Sprintf("uploading asset %s to release %s", name, tag))
	return err
}
