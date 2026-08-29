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
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/services/dispat/internal/changelog"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// LogSkip explains why a resolved policy created no release, so the run's
// dispatcher and the standalone github command word it the same way. A policy
// switched off is ordinary configuration and stays at debug level; a release
// held back by the channels it records on is a release-shaped decision the
// operator should see, named by its channel.
func LogSkip(log zerolog.Logger, spec model.GitHubSpec, rel *plan.Release) {
	if !spec.Enabled {
		log.Debug().Str("package", rel.Pkg.Name).Msg("github release disabled by config")
		return
	}
	log.Info().Str("package", rel.Pkg.Name).Str("tag", rel.TagName()).Str("channel", rel.Channel).
		Msg("github release skipped: the release's channel is not in github.channels")
}

// DefaultAPIURL is the public GitHub REST API endpoint.
const DefaultAPIURL = "https://api.github.com"

const (
	// maxErrorBody bounds how much of a response body is read back — enough
	// for any error message and the created release's asset endpoint.
	maxErrorBody = 64 << 10
	// defaultTimeout is the request timeout of the default HTTP client.
	defaultTimeout = 30 * time.Second
	// uploadTimeoutFloor and uploadTimeoutPer scale an asset upload's own
	// deadline with its size — a floor of a minute plus a second per 100 KiB —
	// because one whole-request timeout sized for API calls would cut off any
	// sizeable artifact on a slow link.
	uploadTimeoutFloor = time.Minute
	uploadTimeoutPer   = 100 << 10
	// defaultRetries and defaultRetryDelay drive the transient-failure retry
	// of the read-only calls: three attempts, backing off from half a second.
	defaultRetries    = 3
	defaultRetryDelay = 500 * time.Millisecond
	// maxRetryAfter caps how long a Retry-After header can hold a run hostage.
	maxRetryAfter = 30 * time.Second
	// draftLookupPages and draftLookupPerPage bound the draft search: three
	// pages of a hundred releases, newest first. Deep enough that a draft a
	// previous attempt left behind is found, shallow enough that the search
	// costs a run less than the duplicate draft it prevents.
	draftLookupPages   = 3
	draftLookupPerPage = 100
	// listMaxBody bounds a release listing, which is legitimately far larger
	// than the error messages and single releases maxErrorBody was sized for:
	// a page carries a hundred rendered release bodies. A listing beyond even
	// this fails the release (the truncated JSON does not parse) rather than
	// reading as "no draft yet", which is the safe direction.
	listMaxBody = 8 << 20
)

// uploadClient carries no client-level timeout: an upload's deadline is the
// per-call context timeout, sized to the asset.
var uploadClient = &http.Client{}

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
	// AllPackages creates a release for every recorded package, even without
	// the DISPAT_EXPORT_GITHUB export (which then only adds assets).
	AllPackages bool
	// Draft creates every release as a draft, for a human to publish after
	// reading the rendered notes. A draft carries no tag ref until then, so
	// it is invisible to everything that resolves a release by its tag —
	// `dispat install`, self-update, the alias-tag chain — and invisible to
	// the by-tag lookup this recorder itself skips on, which is why a draft
	// releaser recognises its own earlier draft through the release listing
	// instead (lookupDraft).
	Draft  bool
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

	// retries and retryDelay override the transient-failure retry of the
	// read-only calls; the zero values mean defaultRetries/defaultRetryDelay.
	// Unexported: tests shrink the delay, nothing else has a reason to.
	retries    int
	retryDelay time.Duration
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
	// Draft holds the release back for a human to publish. Always sent, for
	// the same reason as Prerelease: a repository that stops drafting has to
	// be able to clear the flag as well as set it.
	Draft bool `json:"draft"`
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

// apiCall is one authenticated API request: the HTTP essentials plus What,
// the operation label every error is prefixed with, so a failure reads as an
// operation ("creating release core@1.3.0: ...") rather than a URL.
type apiCall struct {
	Method string
	URL    string
	Body   io.Reader
	// ContentType is sent when non-empty.
	ContentType string
	// ContentLength must be given for bodies the http package cannot measure
	// itself (a file stream) — GitHub's upload endpoint rejects chunked
	// requests; 0 leaves it to the package.
	ContentLength int64
	// WantStatus is the only status code accepted as success.
	WantStatus int
	// TolerateStatus is a second accepted status that is an answer rather
	// than a failure — a 404 from the tag probe means "no such release",
	// which is exactly what the probe asked. do reports which one arrived.
	TolerateStatus int
	// MaxBody overrides maxErrorBody for a response that is legitimately
	// bigger than an error message or one created release; 0 means the
	// default bound.
	MaxBody int64
	What    string
	// Timeout, when non-zero, bounds this one call through its context
	// instead of the client's own timeout — how an upload gets a deadline
	// sized to its asset.
	Timeout time.Duration
	// Retryable marks a call safe to re-issue: transient failures (transport
	// errors, 5xx, rate limits) are retried with backoff. Only the read-only
	// calls set it — a create re-issued after an ambiguous failure could
	// duplicate, and a half-done upload is the reconcile path's job.
	Retryable bool
}

// retryableStatus reports a failure worth re-issuing a read-only call for: a
// transport error (status 0), a server-side 5xx, or a rate limit.
func retryableStatus(status int) bool {
	switch status {
	case 0, http.StatusTooManyRequests, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// do performs one authenticated API call and enforces the expected status.
// The response body is returned (bounded) because a created release carries
// the asset endpoint the caller needs, and so is the status, because a call
// naming a TolerateStatus needs to know which of the two arrived. A Retryable
// call is re-issued on transient failures with exponential backoff, honoring
// a Retry-After the server names (capped at maxRetryAfter).
func (r *Releaser) do(ctx context.Context, call apiCall) ([]byte, int, error) {
	attempts := 1
	if call.Retryable {
		if attempts = r.retries; attempts <= 0 {
			attempts = defaultRetries
		}
	}
	delay := r.retryDelay
	if delay <= 0 {
		delay = defaultRetryDelay
	}
	var data []byte
	var status int
	var retryAfter time.Duration
	var err error
	for attempt := 1; ; attempt++ {
		data, status, retryAfter, err = r.once(ctx, call, attempt)
		if err == nil || attempt == attempts || !retryableStatus(status) {
			return data, status, err
		}
		wait := delay << (attempt - 1)
		if retryAfter > wait {
			wait = min(retryAfter, maxRetryAfter)
		}
		select {
		case <-ctx.Done():
			return data, status, err
		case <-time.After(wait):
		}
	}
}

// once is one attempt of do: build, send, read, judge — and one debug line
// per request, so a GitHub failure is diagnosable from the log alone.
func (r *Releaser) once(ctx context.Context, call apiCall, attempt int) ([]byte, int, time.Duration, error) {
	if call.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, call.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, call.Method, call.URL, call.Body)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("github: %w", err)
	}
	if call.ContentLength > 0 {
		req.ContentLength = call.ContentLength
	}
	req.Header.Set("Authorization", "Bearer "+r.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	if call.ContentType != "" {
		req.Header.Set("Content-Type", call.ContentType)
	}

	client := r.Client
	if client == nil {
		// The default client's own timeout suits the API calls; a call that
		// brought a deadline of its own (an upload) must not inherit it.
		if client = uploadClient; call.Timeout == 0 {
			client = &http.Client{Timeout: defaultTimeout}
		}
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		r.logCall(call, 0, attempt, start)
		return nil, 0, 0, fmt.Errorf("github: %s: %w", call.What, err)
	}
	defer resp.Body.Close()
	r.logCall(call, resp.StatusCode, attempt, start)
	limit := call.MaxBody
	if limit <= 0 {
		limit = maxErrorBody
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		// A truncated success body would corrupt what the caller parses out
		// of it (a created release's upload URL), so it fails the call even
		// when the status looked right.
		return nil, resp.StatusCode, 0, fmt.Errorf("github: %s: reading response: %w", call.What, err)
	}
	if resp.StatusCode != call.WantStatus && resp.StatusCode != call.TolerateStatus {
		return nil, resp.StatusCode, retryAfterOf(resp), fmt.Errorf("github: %s: unexpected status %s: %s",
			call.What, resp.Status, strings.TrimSpace(string(data)))
	}
	return data, resp.StatusCode, 0, nil
}

// logCall is the one debug line every request attempt leaves behind; status 0
// stands for a transport error that produced no response.
func (r *Releaser) logCall(call apiCall, status, attempt int, start time.Time) {
	r.Log.Debug().Str("method", call.Method).Str("what", call.What).
		Int("status", status).Int("attempt", attempt).
		Dur("elapsed", time.Since(start)).Msg("github api call")
}

// retryAfterOf reads a Retry-After the server named in whole seconds; zero
// when absent or unparseable.
func retryAfterOf(resp *http.Response) time.Duration {
	header := resp.Header.Get("Retry-After")
	if header == "" {
		return 0
	}
	seconds, err := strconv.Atoi(header)
	if err != nil || seconds < 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// Verify checks that the repository is reachable with the configured token
// (GET /repos/{owner}/{repo} must return 200). Meant to run before any
// release work so misconfigured credentials fail fast.
func (r *Releaser) Verify(ctx context.Context) error {
	_, _, err := r.do(ctx, apiCall{
		Method:     http.MethodGet,
		URL:        r.endpoint("/repos/" + r.Owner + "/" + r.Repo),
		WantStatus: http.StatusOK,
		What:       fmt.Sprintf("verifying %s/%s", r.Owner, r.Repo),
		Retryable:  true,
	})
	return err
}

// Record implements release.ReleaseRecorder. A package whose scripts did not
// export plan.GitHubExport gets no GitHub release unless AllPackages is set:
// the export is the per-package opt-in, and its value names the files to
// attach.
func (r *Releaser) Record(ctx context.Context, rel *plan.Release) error {
	tag := rel.TagName()
	export, ok := rel.Output(plan.GitHubExport)
	if !ok && !r.AllPackages {
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
	// A release the repository already carries is a skip, not a 422: the
	// flow may have published it from an earlier stage with `dispat github`,
	// or this may be a re-run after a later stage failed. Its assets are
	// still reconciled — a re-run is exactly how a half-uploaded set heals.
	switch existing, err := r.lookup(ctx, tag); {
	case err != nil:
		return err
	case existing != nil:
		r.Log.Warn().Str("code", plan.CodeGitHubReleaseExists).
			Str("package", rel.Pkg.Name).Str("tag", tag).Bool("draft", existing.Draft).
			Msg("github release already exists, skipped")
		return r.reconcileAssets(ctx, existing, export, tag)
	}

	// One lookup for the whole release: the name and every configured line
	// interpolate against the same variables.
	look := changelog.ReleaseLookup(rel)
	var release string
	if sha != "" {
		release = "### Release\n\n- commit: " + sha + "\n- tag: " + tag + "\n"
	}
	// The release section sits inside the body, before the footer: it belongs
	// to the release the entry describes, and a footer is the last word.
	body := changelog.RenderBody(rel, r.Format, look, release)
	// The release is named after its tag unless the format says otherwise. The
	// tag itself is never renamed: it is what the release hangs off.
	name := tag
	if configured := changelog.Expand(r.Format.ReleaseName, look); configured != "" {
		name = configured
	}
	payload, err := json.Marshal(releaseRequest{
		TagName:         tag,
		Name:            name,
		Body:            body,
		TargetCommitish: commitish,
		Prerelease:      rel.IsPrerelease(),
		Draft:           r.Draft,
	})
	if err != nil {
		return fmt.Errorf("github: %w", err)
	}

	respBody, _, err := r.do(ctx, apiCall{
		Method:      http.MethodPost,
		URL:         r.endpoint("/repos/" + r.Owner + "/" + r.Repo + "/releases"),
		Body:        bytes.NewReader(payload),
		ContentType: "application/json",
		WantStatus:  http.StatusCreated,
		What:        "creating release " + tag,
	})
	if err != nil {
		return err
	}
	r.Log.Info().Str("package", rel.Pkg.Name).Str("tag", tag).Bool("draft", r.Draft).
		Msg("github release created")
	if r.Draft {
		// Said out loud, because the tag the release names does not exist as
		// a ref until someone publishes it: an installer or a self-update
		// pointed at that tag finds nothing in the meantime.
		r.Log.Info().Str("package", rel.Pkg.Name).Str("tag", tag).
			Msg("the release is a draft: it stays invisible at its tag until it is published")
	}
	paths := r.attachmentPaths(export, tag)
	if len(paths) == 0 {
		return nil
	}
	uploadURL, err := assetUploadURL(respBody)
	if err != nil {
		return fmt.Errorf("github: release %s: %w", tag, err)
	}
	return r.uploadAll(ctx, uploadURL, tag, paths, nil)
}

// existingRelease is what a re-run needs to know about a release the
// repository already carries: where assets go, and which ones arrived.
type existingRelease struct {
	UploadURL string `json:"upload_url"`
	// TagName and Draft are what a listing entry is recognised by: the by-tag
	// endpoint was asked about one tag and needs neither, but the draft search
	// reads every release the repository carries and has to pick its own.
	TagName string `json:"tag_name"`
	Draft   bool   `json:"draft"`
	Assets  []struct {
		Name  string `json:"name"`
		State string `json:"state"`
	} `json:"assets"`
}

// lookup fetches the release for tag (GET /repos/{owner}/{repo}/releases/
// tags/{tag}), nil when there is none. A 404 is the answer "no", not a
// failure; anything else is a real error, because silently treating an
// unreadable repository as "no release yet" would turn a permissions problem
// into a duplicate-release attempt.
func (r *Releaser) lookup(ctx context.Context, tag string) (*existingRelease, error) {
	data, status, err := r.do(ctx, apiCall{
		Method:         http.MethodGet,
		URL:            r.endpoint("/repos/" + r.Owner + "/" + r.Repo + "/releases/tags/" + neturl.PathEscape(tag)),
		WantStatus:     http.StatusOK,
		TolerateStatus: http.StatusNotFound,
		What:           "looking up release " + tag,
		Retryable:      true,
	})
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		// A draft has no tag ref, so the by-tag endpoint answers 404 for one
		// even when it is sitting in the repository. Only a releaser that
		// creates drafts goes looking further: for everyone else this stays
		// the single call it has always been.
		if r.Draft {
			return r.lookupDraft(ctx, tag)
		}
		return nil, nil
	}
	var release existingRelease
	if err := json.Unmarshal(data, &release); err != nil {
		return nil, fmt.Errorf("github: release %s: parsing lookup: %w", tag, err)
	}
	return &release, nil
}

// lookupDraft searches the repository's release listing for a draft carrying
// tag, nil when there is none. It is how a re-run recognises the draft it
// created last time, which the by-tag lookup cannot see.
//
// The listing is newest first, so the draft a previous attempt left behind is
// on the first page unless the repository created draftLookupPerPage releases
// since. draftLookupPages bounds the search rather than following the listing
// to its end: an unbounded walk over a repository with thousands of releases
// would cost a run more than the duplicate it prevents. A page short of full
// is the last page, and stops the walk early.
//
// Anything but a 200 is a hard error, for the same reason a failed by-tag
// lookup is: treating an unreadable listing as "no draft yet" would turn a
// permissions problem into a second draft on every run.
func (r *Releaser) lookupDraft(ctx context.Context, tag string) (*existingRelease, error) {
	base := r.endpoint("/repos/" + r.Owner + "/" + r.Repo + "/releases")
	for page := 1; page <= draftLookupPages; page++ {
		data, _, err := r.do(ctx, apiCall{
			Method:     http.MethodGet,
			URL:        fmt.Sprintf("%s?per_page=%d&page=%d", base, draftLookupPerPage, page),
			WantStatus: http.StatusOK,
			MaxBody:    listMaxBody,
			What:       "looking up draft release " + tag,
			Retryable:  true,
		})
		if err != nil {
			return nil, err
		}
		var releases []existingRelease
		if err := json.Unmarshal(data, &releases); err != nil {
			return nil, fmt.Errorf("github: release %s: parsing release listing: %w", tag, err)
		}
		for i := range releases {
			if releases[i].Draft && releases[i].TagName == tag {
				return &releases[i], nil
			}
		}
		if len(releases) < draftLookupPerPage {
			return nil, nil
		}
	}
	r.Log.Debug().Str("tag", tag).Int("pages", draftLookupPages).
		Msg("no draft found within the searched pages of the release listing")
	return nil, nil
}

// reconcileAssets uploads whatever an existing release is missing of the
// exported files. Without it, a run cut off mid-upload would leave the
// release under-recorded forever: the release itself is a skip on every
// re-run, so this is the only path the missing assets have.
func (r *Releaser) reconcileAssets(ctx context.Context, release *existingRelease, export, tag string) error {
	paths := r.attachmentPaths(export, tag)
	if len(paths) == 0 {
		return nil
	}
	if release.UploadURL == "" {
		return fmt.Errorf("github: release %s carries no upload_url, cannot attach assets", tag)
	}
	uploaded := make(map[string]bool, len(release.Assets))
	for _, a := range release.Assets {
		if a.State == "uploaded" {
			uploaded[a.Name] = true
		}
	}
	return r.uploadAll(ctx, stripURITemplate(release.UploadURL), tag, paths, uploaded)
}

// uploadAll attaches every path not already uploaded, and keeps going past a
// failed one: each sound asset deserves its upload, and the failed ones are
// exactly what the next re-run's reconcile picks up. An asset name repeated
// within one call — two paths sharing a base name, or one export restated —
// is uploaded once and warned about, because the API would refuse the second
// copy anyway and one warning reads better than one 422.
func (r *Releaser) uploadAll(ctx context.Context, uploadURL, tag string, paths []string, uploaded map[string]bool) error {
	var errs []error
	sent := make(map[string]bool, len(paths))
	for _, p := range paths {
		name := filepath.Base(p)
		if uploaded[name] {
			r.Log.Debug().Str("tag", tag).Str("asset", name).
				Msg("github release asset already uploaded, skipped")
			continue
		}
		if sent[name] {
			r.Log.Warn().Str("tag", tag).Str("asset", name).Str("path", p).
				Msg("github release asset name repeats in the export, skipped")
			continue
		}
		sent[name] = true
		if err := r.uploadAsset(ctx, uploadURL, tag, p); err != nil {
			r.Log.Error().Str("tag", tag).Str("path", p).Err(err).
				Msg("github release asset upload failed")
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
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
	return stripURITemplate(release.UploadURL), nil
}

// stripURITemplate cuts the {?name,label} suffix off an upload_url; GitHub
// advertises the endpoint as an RFC 6570 template.
func stripURITemplate(url string) string {
	if i := strings.IndexByte(url, '{'); i >= 0 {
		return url[:i]
	}
	return url
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
	_, _, err = r.do(ctx, apiCall{
		Method:        http.MethodPost,
		URL:           uploadURL + "?name=" + neturl.QueryEscape(name),
		Body:          f,
		ContentType:   "application/octet-stream",
		ContentLength: fi.Size(),
		WantStatus:    http.StatusCreated,
		What:          fmt.Sprintf("uploading asset %s to release %s", name, tag),
		Timeout:       uploadTimeout(fi.Size()),
	})
	return err
}

// uploadTimeout sizes an upload's deadline to its asset: the floor plus a
// second per uploadTimeoutPer bytes.
func uploadTimeout(size int64) time.Duration {
	return uploadTimeoutFloor + time.Duration(size/uploadTimeoutPer)*time.Second
}
