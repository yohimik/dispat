package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/changelog"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// testRelease returns a release whose scripts opted into a GitHub release:
// the recorder acts only on packages that exported plan.GitHubExport.
func testRelease() *plan.Release {
	return &plan.Release{
		Pkg:  &model.Package{Name: "core", Dir: "core", Space: &model.Space{Name: "libs"}},
		Next: ccme.Version{Major: 1, Minor: 3},
		Units: []*ccme.Unit{{
			Header: ccme.Header{Type: "feat", Description: "add streaming"},
			Bump:   ccme.BumpMinor,
			Valid:  true,
		}},
		DueTo: []string{"utils"},
		Updates: []plan.ProviderUpdate{{
			Name: "utils",
			From: ccme.Version{Major: 2},
			To:   ccme.Version{Major: 2, Patch: 1},
		}},
		Outputs: []plan.Output{{Name: plan.GitHubExport, Value: "", Source: "core:build"}},
	}
}

// releaseProbe answers the lookup Record makes before it creates anything —
// "does this tag already have a release?" — with the 404 that means no. Every
// fake server serving a creation needs it, so it lives here instead of in
// each handler; a server testing the skip answers 200 itself.
func releaseProbe(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet || !strings.Contains(r.URL.Path, "/releases/tags/") {
		return false
	}
	w.WriteHeader(http.StatusNotFound)
	return true
}

// captureServer serves the two calls these tests care about — the tag probe
// and the create-release POST — by decoding the payload and replying 201. It
// is for the tests whose only interest is what the recorder sent; servers
// that assert on paths, headers or uploads, or inject errors, stay bespoke
// next to their tests.
func captureServer(t *testing.T) (*httptest.Server, *releaseRequest) {
	t.Helper()
	got := &releaseRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if releaseProbe(w, r) {
			return
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(got))
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func TestRecordUsesExportedCommit(t *testing.T) {
	// A PACKAGE_<KEY> export overrides the release's commit for this one
	// package: it becomes the target_commitish and the documented commit,
	// even over the finalize phase's own values.
	srv, gotBody := captureServer(t)

	rel := testRelease()
	rel.Outputs = append(rel.Outputs, plan.Output{
		Name: plan.PackageCommitExportPrefix + plan.EnvKey("core"), Value: "deadbeef", Source: "core:publish"})

	r := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client(),
		CommitSHA: "aaaa", TargetCommitish: "aaaa"}
	require.NoError(t, r.Record(context.Background(), rel))
	assert.Equal(t, "deadbeef", gotBody.TargetCommitish)
	assert.Contains(t, gotBody.Body, "- commit: deadbeef")
	assert.NotContains(t, gotBody.Body, "aaaa")
}

func TestRecordSkipsWithoutExport(t *testing.T) {
	// A package that exported no DISPAT_EXPORT_GITHUB gets no GitHub release
	// — the recorder must not even reach the API.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected API call %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	rel := testRelease()
	rel.Outputs = nil

	r := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client()}
	assert.NoError(t, r.Record(context.Background(), rel))
}

func TestRecordCreatesRelease(t *testing.T) {
	var gotPath, gotAuth, gotAccept string
	var gotBody releaseRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if releaseProbe(w, r) {
			return
		}
		require.Equal(t, http.MethodPost, r.Method)
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	rel := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client()}
	require.NoError(t, rel.Record(context.Background(), testRelease()))

	assert.Equal(t, "/repos/acme/mono/releases", gotPath)
	assert.Equal(t, "Bearer tkn", gotAuth)
	assert.Equal(t, "application/vnd.github+json", gotAccept)
	assert.Equal(t, "core@1.3.0", gotBody.TagName)
	assert.Equal(t, "core@1.3.0", gotBody.Name)
	assert.Contains(t, gotBody.Body, "### Features")
	assert.Contains(t, gotBody.Body, "- add streaming")
	assert.Contains(t, gotBody.Body, "- utils: 2.0.0 -> 2.0.1",
		"the dependencies section carries the version movement, not the bare name")
	assert.NotContains(t, gotBody.Body, "## core@", "release body has no entry header")
	assert.False(t, gotBody.Prerelease, "1.3.0 is a stable release")
}

func TestRecordMarksPrereleases(t *testing.T) {
	// GitHub presents the newest non-prerelease as "Latest"; a beta shipped
	// without the flag would take that slot.
	srv, gotBody := captureServer(t)

	rel := testRelease()
	rel.Next = ccme.Version{Major: 1, Minor: 3, Prerelease: []string{"beta", "0"}}

	r := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client()}
	require.NoError(t, r.Record(context.Background(), rel))

	assert.Equal(t, "core@1.3.0-beta.0", gotBody.TagName)
	assert.True(t, gotBody.Prerelease)
}

func TestRecordUsesTheSpaceTagFormat(t *testing.T) {
	srv, gotBody := captureServer(t)

	rel := testRelease()
	rel.Pkg.Space.TagFormat = "services/{name}@v{version}"

	r := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client()}
	require.NoError(t, r.Record(context.Background(), rel))

	assert.Equal(t, "services/core@v1.3.0", gotBody.TagName,
		"the release must name the tag the run actually creates")
}

func TestRecordCustomFormat(t *testing.T) {
	srv, gotBody := captureServer(t)

	rel := &Releaser{
		APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client(),
		Format: changelog.Format{FeaturesTitle: "New Stuff"},
	}
	require.NoError(t, rel.Record(context.Background(), testRelease()))
	assert.Contains(t, gotBody.Body, "### New Stuff")
}

func TestRecordAuthorsSectionSitsBeforeTheReleaseBlockAndFooter(t *testing.T) {
	// The recorder appends its "### Release" details as an extra block and the
	// footer closes the body. The authors section goes in ahead of both: the
	// release details belong to the release the entry describes, and a footer
	// is the last word — self-update cuts release notes at the "---" a footer
	// conventionally opens with, so anything after it would be cut away.
	srv, gotBody := captureServer(t)

	unit := &ccme.Unit{
		Header: ccme.Header{Type: "feat", Description: "add streaming"},
		Bump:   ccme.BumpMinor, Valid: true,
	}
	r := testRelease()
	r.Units = []*ccme.Unit{unit}
	r.FreshUnits = r.Units
	r.UnitAuthors = map[*ccme.Unit][]plan.Author{unit: {{Name: "Ada Lovelace", Email: "ada@example.com"}}}
	r.WindowAuthors = []plan.Author{{Name: "Ada Lovelace", Email: "ada@example.com"}}

	rel := &Releaser{
		APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client(),
		CommitSHA: "abc123def456",
		Format: changelog.Format{
			AuthorsPlacement: changelog.AuthorsBoth,
			Footer:           []model.EntryLine{{Line: []string{"---", "Released by dispat."}}},
		},
	}
	require.NoError(t, rel.Record(context.Background(), r))

	body := gotBody.Body
	assert.Contains(t, body, "- add streaming (by Ada Lovelace)", "the inline suffix rides the line")
	assert.Contains(t, body, "### Authors\n\n- Ada Lovelace\n")

	authors := strings.Index(body, "### Authors")
	release := strings.Index(body, "### Release")
	footer := strings.Index(body, "---")
	require.Positive(t, authors)
	require.Positive(t, release)
	require.Positive(t, footer)
	assert.Less(t, authors, release, "the authors section precedes the release details")
	assert.Less(t, release, footer, "and both precede the footer")
}

func TestRecordWithCommitSHA(t *testing.T) {
	srv, gotBody := captureServer(t)

	// Push disabled: the commit SHA is documented in the body, but
	// target_commitish must NOT be sent (the SHA is not on the remote).
	rel := &Releaser{
		APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client(),
		CommitSHA: "abc123def456",
	}
	require.NoError(t, rel.Record(context.Background(), testRelease()))
	assert.Contains(t, gotBody.Body, "### Release")
	assert.Contains(t, gotBody.Body, "- commit: abc123def456")
	assert.Contains(t, gotBody.Body, "- tag: core@1.3.0")
	assert.Empty(t, gotBody.TargetCommitish)
}

func TestRecordWithTargetCommitish(t *testing.T) {
	srv, gotBody := captureServer(t)

	// Push enabled: the tag is additionally pinned to the pushed commit.
	rel := &Releaser{
		APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client(),
		CommitSHA: "abc123def456", TargetCommitish: "abc123def456",
	}
	require.NoError(t, rel.Record(context.Background(), testRelease()))
	assert.Equal(t, "abc123def456", gotBody.TargetCommitish)
	assert.Contains(t, gotBody.Body, "- commit: abc123def456")
}

func TestVerify(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rel := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client()}
	require.NoError(t, rel.Verify(context.Background()))
	assert.Equal(t, "/repos/acme/mono", gotPath)
	assert.Equal(t, "Bearer tkn", gotAuth)
}

func TestVerifyFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()

	rel := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "bad", Client: srv.Client()}
	err := rel.Verify(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestRecordAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if releaseProbe(w, r) {
			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"Validation Failed"}`))
	}))
	defer srv.Close()

	rel := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client()}
	err := rel.Record(context.Background(), testRelease())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "422")
	assert.Contains(t, err.Error(), "Validation Failed")
}

func TestDoBodyReadError(t *testing.T) {
	// A response that dies mid-body must fail the call even when the status
	// looked right: the caller may parse the body (a created release's
	// upload URL), and a truncated read is not a success.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("short"))
		// The handler returns with 95 promised bytes unsent; the client sees
		// an unexpected EOF while reading the body.
	}))
	defer srv.Close()

	rel := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client()}
	err := rel.Verify(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading response")
	assert.Contains(t, err.Error(), "verifying acme/mono")
}

func TestRecordConnectionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // immediately closed: connection refused

	rel := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn",
		retryDelay: time.Millisecond}
	assert.Error(t, rel.Record(context.Background(), testRelease()))
}

func TestRecordUploadsAttachments(t *testing.T) {
	// The created release advertises its own asset endpoint (upload_url, a
	// URI template); every attachment must be POSTed there as an
	// octet-stream named after its file.
	dir := t.TempDir()
	asset := filepath.Join(dir, "app.tgz")
	notes := filepath.Join(dir, "notes.txt")
	require.NoError(t, os.WriteFile(asset, []byte("archive-bytes"), 0o644))
	require.NoError(t, os.WriteFile(notes, []byte("notes-bytes"), 0o644))

	type upload struct {
		name, contentType, body string
	}
	var uploads []upload
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if releaseProbe(w, r) {
			return
		}
		switch r.URL.Path {
		case "/repos/acme/mono/releases":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": 7, "upload_url": "` + srv.URL + `/uploads/releases/7/assets{?name,label}"}`))
		case "/uploads/releases/7/assets":
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			uploads = append(uploads, upload{
				name:        r.URL.Query().Get("name"),
				contentType: r.Header.Get("Content-Type"),
				body:        string(body),
			})
			w.WriteHeader(http.StatusCreated)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	// The export's value is a whitespace-separated path list.
	rel := testRelease()
	rel.Outputs = []plan.Output{{Name: plan.GitHubExport, Value: asset + " " + notes}}

	r := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client()}
	require.NoError(t, r.Record(context.Background(), rel))

	require.Len(t, uploads, 2)
	assert.Equal(t, "app.tgz", uploads[0].name, "the asset is named after its file")
	assert.Equal(t, "application/octet-stream", uploads[0].contentType)
	assert.Equal(t, "archive-bytes", uploads[0].body)
	assert.Equal(t, "notes.txt", uploads[1].name)
	assert.Equal(t, "notes-bytes", uploads[1].body)
}

func TestRecordUploadFailureIsAnError(t *testing.T) {
	dir := t.TempDir()
	asset := filepath.Join(dir, "app.tgz")
	require.NoError(t, os.WriteFile(asset, []byte("x"), 0o644))

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if releaseProbe(w, r) {
			return
		}
		if r.URL.Path == "/repos/acme/mono/releases" {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"upload_url": "` + srv.URL + `/uploads{?name,label}"}`))
			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"asset rejected"}`))
	}))
	defer srv.Close()

	rel := testRelease()
	rel.Outputs = []plan.Output{{Name: plan.GitHubExport, Value: asset}}

	r := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client()}
	err := r.Record(context.Background(), rel)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "asset rejected")
}

func TestRecordInvalidAttachmentsAreSkippedWithAWarning(t *testing.T) {
	// A bad DISPAT_EXPORT_GITHUB entry — a relative path, a missing file, a
	// directory — is skipped with a warning while the release itself and the
	// sound entries go through: the release is already out, and one typo must
	// not lose the rest of the assets.
	dir := t.TempDir()
	sound := filepath.Join(dir, "app.tgz")
	require.NoError(t, os.WriteFile(sound, []byte("archive-bytes"), 0o644))

	var uploads []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if releaseProbe(w, r) {
			return
		}
		if r.URL.Path == "/repos/acme/mono/releases" {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"upload_url": "` + srv.URL + `/uploads{?name,label}"}`))
			return
		}
		uploads = append(uploads, r.URL.Query().Get("name"))
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	for name, value := range map[string]string{
		"relative_path": "dist/app.tgz",
		"missing_file":  filepath.Join(t.TempDir(), "never-created.tgz"),
		"directory":     t.TempDir(),
	} {
		t.Run(name, func(t *testing.T) {
			uploads = nil
			var logs strings.Builder
			r := &Releaser{
				APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client(),
				Log: zerolog.New(&logs),
			}
			rel := testRelease()
			rel.Outputs = []plan.Output{{Name: plan.GitHubExport, Value: value + " " + sound}}

			require.NoError(t, r.Record(context.Background(), rel), "an invalid entry must not fail the recording")
			assert.Equal(t, []string{"app.tgz"}, uploads, "the sound entry is still uploaded")
			assert.Contains(t, logs.String(), "asset skipped")
			assert.Contains(t, logs.String(), plan.GitHubExport)
		})
	}
}

func TestRecordWithoutAttachmentsIgnoresMissingUploadURL(t *testing.T) {
	// Servers in tests (and proxies) may return an empty creation body; with
	// nothing to upload that must stay a success.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if releaseProbe(w, r) {
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	r := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client()}
	assert.NoError(t, r.Record(context.Background(), testRelease()))
}

// TestRecordSkipsAnExistingRelease: the tag already has a release, so the
// recorder logs W224 and creates nothing. This is what makes a re-run after
// a failed later stage — and a flow that published from `dispat github`
// earlier — land on a no-op instead of a 422.
func TestRecordSkipsAnExistingRelease(t *testing.T) {
	var created int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/repos/acme/mono/releases/tags/core@1.3.0" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id": 7}`))
			return
		}
		created++
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	var logs strings.Builder
	r := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client(),
		Log: zerolog.New(&logs)}
	require.NoError(t, r.Record(context.Background(), testRelease()))
	assert.Zero(t, created, "an existing release is never created twice")
	assert.Contains(t, logs.String(), plan.CodeGitHubReleaseExists)
	assert.Contains(t, logs.String(), "already exists")
}

// TestRecordProbeFailureIsAnError: an unreadable repository must not be read
// as "no release yet" — that would turn a permissions problem into a
// duplicate-release attempt.
func TestRecordProbeFailureIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible"}`))
	}))
	defer srv.Close()

	r := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client()}
	err := r.Record(context.Background(), testRelease())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "looking up release core@1.3.0")
	assert.Contains(t, err.Error(), "403")
}

func TestAssetUploadURL(t *testing.T) {
	url, err := assetUploadURL([]byte(`{"upload_url": "https://uploads.test/assets{?name,label}"}`))
	require.NoError(t, err)
	assert.Equal(t, "https://uploads.test/assets", url, "the URI-template suffix is stripped")

	url, err = assetUploadURL([]byte(`{"upload_url": "https://uploads.test/assets"}`))
	require.NoError(t, err)
	assert.Equal(t, "https://uploads.test/assets", url, "a plain URL passes through")

	_, err = assetUploadURL([]byte(`{}`))
	assert.ErrorContains(t, err, "no upload_url")

	_, err = assetUploadURL([]byte(`not json`))
	assert.ErrorContains(t, err, "parsing created release")
}

func TestVerifyConnectionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // immediately closed: connection refused

	rel := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn"}
	assert.Error(t, rel.Verify(context.Background()))
}

// TestRecordNamesTheReleaseAfterTheTag: with no releaseName configured the
// release is named after its tag, as it always was.
func TestRecordNamesTheReleaseAfterTheTag(t *testing.T) {
	srv, gotBody := captureServer(t)

	r := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client()}
	require.NoError(t, r.Record(context.Background(), testRelease()))

	assert.Equal(t, "core@1.3.0", gotBody.Name)
	assert.Equal(t, "core@1.3.0", gotBody.TagName)
}

// TestRecordReleaseNameOverridesTheName: releaseName renames the release and
// nothing else — the tag it hangs off is untouched.
func TestRecordReleaseNameOverridesTheName(t *testing.T) {
	srv, gotBody := captureServer(t)

	r := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client(),
		Format: changelog.Format{ReleaseName: "${DISPAT_PACKAGE} ${DISPAT_VERSION} is out"}}
	require.NoError(t, r.Record(context.Background(), testRelease()))

	assert.Equal(t, "core 1.3.0 is out", gotBody.Name, "the name is interpolated")
	assert.Equal(t, "core@1.3.0", gotBody.TagName, "the tag is never renamed")
	assert.NotContains(t, gotBody.Body, "core 1.3.0 is out",
		"on GitHub the name is the release's title, not a sub-header in its body")
}

// TestRecordBodyOrder: the header opens the body, the footer closes it, and
// the release section documenting the commit sits between the sections and
// the footer.
func TestRecordBodyOrder(t *testing.T) {
	srv, gotBody := captureServer(t)

	r := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client(),
		CommitSHA: "abc123",
		Format: changelog.Format{
			Header: []model.EntryLine{{Line: []string{"Built by CI."}}},
			Footer: []model.EntryLine{{Line: []string{"", "Changelog: /blob/${DISPAT_TAG}/CHANGELOG.md"}}},
		}}
	require.NoError(t, r.Record(context.Background(), testRelease()))

	body := gotBody.Body
	order := []string{"Built by CI.", "### Features", "### Dependencies", "### Release",
		"Changelog: /blob/core@1.3.0/CHANGELOG.md"}
	at := -1
	for _, marker := range order {
		i := strings.Index(body, marker)
		require.NotEqual(t, -1, i, "missing %q in:\n%s", marker, body)
		assert.Greater(t, i, at, "%q is out of order in:\n%s", marker, body)
		at = i
	}
}

// TestRecordSkipsLinesForOtherPackages: a line filtered to another package
// leaves the body exactly as an unconfigured format would have rendered it.
func TestRecordSkipsLinesForOtherPackages(t *testing.T) {
	plainSrv, plainBody := captureServer(t)
	plain := &Releaser{APIURL: plainSrv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: plainSrv.Client()}
	require.NoError(t, plain.Record(context.Background(), testRelease()))

	srv, gotBody := captureServer(t)
	r := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client(),
		Format: changelog.Format{
			Header: []model.EntryLine{{Line: []string{"apps only"}, Space: []string{"apps"}}},
			Footer: []model.EntryLine{{Line: []string{"other only"}, Package: []string{"other"}}},
		}}
	require.NoError(t, r.Record(context.Background(), testRelease()))

	assert.Equal(t, plainBody.Body, gotBody.Body)
}

// TestRecordExpandsAScriptOutput: a value a build script exported reaches the
// release body, which is how a footer links an artefact the run produced.
func TestRecordExpandsAScriptOutput(t *testing.T) {
	srv, gotBody := captureServer(t)

	rel := testRelease()
	rel.Outputs = append(rel.Outputs, plan.Output{Name: "IMAGE", Value: "acme/core:1.3.0", Source: "core:build"})

	r := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client(),
		Format: changelog.Format{Footer: []model.EntryLine{{Line: []string{"image: ${DISPAT_OUTPUT_IMAGE}"}}}}}
	require.NoError(t, r.Record(context.Background(), rel))

	assert.Contains(t, gotBody.Body, "image: acme/core:1.3.0")
}

// TestRecordReconcilesMissingAssets: a release the repository already carries
// is a skip, but its assets are still reconciled — the exported files its
// asset list is missing are uploaded, the ones it has are not re-sent. This
// is the one path a run cut off mid-upload has back to a complete record.
func TestRecordReconcilesMissingAssets(t *testing.T) {
	dir := t.TempDir()
	have := filepath.Join(dir, "a.bin")
	missing := filepath.Join(dir, "b.bin")
	require.NoError(t, os.WriteFile(have, []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(missing, []byte("b"), 0o644))

	var uploads []string
	var created int
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/releases/tags/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id": 7, "upload_url": "` + srv.URL + `/uploads{?name,label}",
				"assets": [{"name": "a.bin", "state": "uploaded"}]}`))
		case r.URL.Path == "/uploads":
			uploads = append(uploads, r.URL.Query().Get("name"))
			w.WriteHeader(http.StatusCreated)
		default:
			created++
			w.WriteHeader(http.StatusCreated)
		}
	}))
	defer srv.Close()

	rel := testRelease()
	rel.Outputs = []plan.Output{{Name: plan.GitHubExport, Value: have + " " + missing}}

	var logs strings.Builder
	r := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client(),
		Log: zerolog.New(&logs)}
	require.NoError(t, r.Record(context.Background(), rel))

	assert.Zero(t, created, "the release itself stays a skip")
	assert.Equal(t, []string{"b.bin"}, uploads, "only the missing asset is uploaded")
	assert.Contains(t, logs.String(), plan.CodeGitHubReleaseExists)
}

// TestVerifyRetriesTransientFailures: a read-only call re-issues itself past
// transient server failures instead of turning a hiccup into a critical.
func TestVerifyRetriesTransientFailures(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits++; hits < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client(),
		retryDelay: time.Millisecond}
	require.NoError(t, r.Verify(context.Background()))
	assert.Equal(t, 3, hits)
}

// TestVerifyGivesUpAfterItsAttempts: the retry is bounded; a persistent 503
// still comes back as the error it is.
func TestVerifyGivesUpAfterItsAttempts(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	r := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client(),
		retryDelay: time.Millisecond}
	require.Error(t, r.Verify(context.Background()))
	assert.Equal(t, defaultRetries, hits)
}

// TestVerifyDoesNotRetryAnAnswer: a 404 is an answer about the repository,
// not a transient failure — one attempt, one error.
func TestVerifyDoesNotRetryAnAnswer(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	r := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client(),
		retryDelay: time.Millisecond}
	require.Error(t, r.Verify(context.Background()))
	assert.Equal(t, 1, hits)
}

// TestVerifyHonorsRetryAfter: a rate-limited call waits at least what the
// server named before its next attempt.
func TestVerifyHonorsRetryAfter(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits++; hits == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client(),
		retryDelay: time.Millisecond}
	start := time.Now()
	require.NoError(t, r.Verify(context.Background()))
	assert.Equal(t, 2, hits)
	assert.GreaterOrEqual(t, time.Since(start), time.Second, "the named wait is honored over the backoff")
}

// TestCreateReleaseIsNotRetried: the create POST is not idempotent — an
// ambiguous failure re-issued could duplicate the release, so it gets exactly
// one attempt.
func TestCreateReleaseIsNotRetried(t *testing.T) {
	var creates int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if releaseProbe(w, r) {
			return
		}
		creates++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	r := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client(),
		retryDelay: time.Millisecond}
	require.Error(t, r.Record(context.Background(), testRelease()))
	assert.Equal(t, 1, creates)
}

// TestUploadContinuesPastAFailedAsset: one rejected asset must not cost the
// ones behind it their upload — every sound asset is attempted, and the
// failure still comes back as the error it is.
func TestUploadContinuesPastAFailedAsset(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.bin")
	good := filepath.Join(dir, "good.bin")
	require.NoError(t, os.WriteFile(bad, []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(good, []byte("y"), 0o644))

	var uploads []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if releaseProbe(w, r) {
			return
		}
		if r.URL.Path == "/repos/acme/mono/releases" {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"upload_url": "` + srv.URL + `/uploads{?name,label}"}`))
			return
		}
		name := r.URL.Query().Get("name")
		uploads = append(uploads, name)
		if name == "bad.bin" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"message":"asset rejected"}`))
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	rel := testRelease()
	rel.Outputs = []plan.Output{{Name: plan.GitHubExport, Value: bad + " " + good}}

	r := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client()}
	err := r.Record(context.Background(), rel)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "asset rejected")
	assert.Equal(t, []string{"bad.bin", "good.bin"}, uploads, "the sound asset is still uploaded")
}

// TestUploadTimeoutScalesWithTheAsset: the floor covers any small file, and a
// large one buys itself the seconds its bytes need.
func TestUploadTimeoutScalesWithTheAsset(t *testing.T) {
	assert.Equal(t, time.Minute, uploadTimeout(0))
	assert.Equal(t, time.Minute, uploadTimeout(100<<10-1))
	assert.Equal(t, time.Minute+10*time.Second, uploadTimeout(10*(100<<10)))
}
