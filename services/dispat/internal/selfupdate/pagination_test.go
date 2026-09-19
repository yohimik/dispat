package selfupdate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListingDoesNotFollowPaginationOffTheApiHost: every listing request
// carries the operator's token, and the next page's address is the server's
// own text. A listing that pointed somewhere else would hand that token to
// whoever wrote the header, so the walk stops at the endpoint's own host
// instead of following it.
func TestListingDoesNotFollowPaginationOffTheApiHost(t *testing.T) {
	var elsewhere atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		elsewhere.Add(1)
		assert.Empty(t, req.Header.Get("Authorization"), "the token reached another host")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	}))
	t.Cleanup(other.Close)

	var pages atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		pages.Add(1)
		w.Header().Set("Link", `<`+other.URL+`/redirected>; rel="next"`)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			releaseJSON("services/dispat/v2.0.0", false, false),
		})
	}))
	t.Cleanup(api.Close)

	s := &Source{APIURL: api.URL, Owner: "o", Repo: "r", Token: "secret-token"}
	rel, err := s.Latest(context.Background())

	require.NoError(t, err, "the page that did arrive is still a complete answer")
	assert.Equal(t, "2.0.0", rel.Version.String())
	assert.EqualValues(t, 1, pages.Load(), "one page was read")
	assert.Zero(t, elsewhere.Load(), "the listing followed its Link header to another host")
}

// TestListingFollowsPaginationOnTheSameHost holds the guard to its own
// boundary: an ordinary next page, served by the configured endpoint, is
// still followed.
func TestListingFollowsPaginationOnTheSameHost(t *testing.T) {
	var base string
	var pages atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		page := pages.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if page == 1 {
			w.Header().Set("Link", `<`+base+`/releases?page=2>; rel="next"`)
			_ = json.NewEncoder(w).Encode([]map[string]any{})
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			releaseJSON("services/dispat/v4.1.0", false, false),
		})
	}))
	base = "http://" + srv.Listener.Addr().String()
	srv.Start()
	t.Cleanup(srv.Close)

	s := &Source{APIURL: base, Owner: "o", Repo: "r", Token: "secret-token"}
	rel, err := s.Latest(context.Background())

	require.NoError(t, err)
	assert.Equal(t, "4.1.0", rel.Version.String(), "the second page of the same host is read")
	assert.GreaterOrEqual(t, pages.Load(), int32(2))
}
