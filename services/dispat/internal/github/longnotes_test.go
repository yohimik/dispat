package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// longReleaseJSON is what GitHub answers about one release whose notes are
// far longer than an error message: the notes echoed back, escaped, beside
// the release's metadata. GitHub accepts notes of 125,000 characters.
func longReleaseJSON(t *testing.T, tag string) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"id":         7,
		"tag_name":   tag,
		"upload_url": "https://uploads.example.test/repos/acme/mono/releases/7/assets{?name,label}",
		"body":       strings.Repeat("- a line of the notes\n", 5000),
		"assets":     []any{},
	})
	require.NoError(t, err)
	require.Greater(t, len(data), maxErrorBody, "the fixture has to exceed the error bound to fence anything")
	return data
}

// TestRecordReadsBackAReleaseWithLongNotes fences the publication of a
// candidate with eighty own commits: GitHub created the release and echoed
// 63,627 bytes of notes, and reading that answer under the bound sized for an
// error message failed a publication the server had already performed.
func TestRecordReadsBackAReleaseWithLongNotes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if releaseProbe(w, r) {
			return
		}
		require.Equal(t, http.MethodPost, r.Method)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(longReleaseJSON(t, "core@1.3.0"))
	}))
	defer srv.Close()

	rel := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client()}
	require.NoError(t, rel.Record(context.Background(), testRelease()))
}

// TestRecordReadsAnExistingReleaseWithLongNotes fences the other read of one
// release: the probe for a release the tag already has, which a later run
// makes before deciding to skip it, answers with the same long notes.
func TestRecordReadsAnExistingReleaseWithLongNotes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/releases/tags/") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(longReleaseJSON(t, "core@1.3.0"))
			return
		}
		t.Errorf("unexpected %s %s: an existing release is never created again", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	rel := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client()}
	require.NoError(t, rel.Record(context.Background(), testRelease()))
}
