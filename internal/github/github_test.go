package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/internal/changelog"
	"github.com/yohimik/dispat/internal/conventional"
	"github.com/yohimik/dispat/internal/model"
	"github.com/yohimik/dispat/internal/plan"
	"github.com/yohimik/dispat/internal/semver"
)

func testRelease() *plan.Release {
	return &plan.Release{
		Pkg:  &model.Package{Name: "core", Dir: "core", Space: &model.Space{Name: "libs"}},
		Next: semver.Version{Major: 1, Minor: 3},
		Commits: []conventional.Commit{
			{Kind: conventional.KindFeat, Scope: "core", Description: "add streaming"},
		},
		DueTo: []string{"utils"},
	}
}

func TestRecordCreatesRelease(t *testing.T) {
	var gotPath, gotAuth, gotAccept string
	var gotBody releaseRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	assert.Contains(t, gotBody.Body, "- updated providers: utils")
	assert.NotContains(t, gotBody.Body, "## core@", "release body has no entry header")
}

func TestRecordCustomFormat(t *testing.T) {
	var gotBody releaseRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	rel := &Releaser{
		APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client(),
		Format: changelog.Format{FeaturesTitle: "New Stuff"},
	}
	require.NoError(t, rel.Record(context.Background(), testRelease()))
	assert.Contains(t, gotBody.Body, "### New Stuff")
}

func TestRecordAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestRecordWithCommitSHA(t *testing.T) {
	var gotBody releaseRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

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
	var gotBody releaseRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

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

func TestRecordConnectionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.Close() // immediately closed: connection refused

	rel := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn"}
	assert.Error(t, rel.Record(context.Background(), testRelease()))
}
