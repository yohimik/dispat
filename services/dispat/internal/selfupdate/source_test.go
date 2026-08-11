package selfupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// releaseJSON is one release as the API returns it, with an asset for every
// platform a test might ask for.
func releaseJSON(tag string, prerelease, draft bool, assetNames ...string) map[string]any {
	assets := make([]map[string]any, 0, len(assetNames))
	for _, name := range assetNames {
		assets = append(assets, map[string]any{
			"name": name, "browser_download_url": "http://example.invalid/" + name,
			"size": 10, "digest": "sha256:abc",
		})
	}
	return map[string]any{"tag_name": tag, "prerelease": prerelease, "draft": draft, "assets": assets}
}

// listing serves one page of releases at the listing endpoint and answers the
// by-tag lookup out of the same set, which is what GitHub does.
func listing(t *testing.T, releases ...map[string]any) *Source {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if tag, ok := cutTagPath(req.URL.Path); ok {
			for _, r := range releases {
				if r["tag_name"] == tag {
					_ = json.NewEncoder(w).Encode(r)
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
			return
		}
		_ = json.NewEncoder(w).Encode(releases)
	}))
	t.Cleanup(srv.Close)
	return &Source{APIURL: srv.URL, Owner: "o", Repo: "r"}
}

func cutTagPath(path string) (string, bool) {
	_, tag, ok := strings.Cut(path, "/releases/tags/")
	return tag, ok
}

// TestLatestPicksTheHighestStableCarryingThePrefix: the listing is every
// module's releases mixed together and in publication order, so the choice is
// made on the tag prefix and on semver ordering — never on position. A
// backport published after a newer minor is the case that makes "most recent"
// wrong, and it is in here on purpose.
func TestLatestPicksTheHighestStableCarryingThePrefix(t *testing.T) {
	s := listing(t,
		releaseJSON("pkg/ccme/v9.9.9", false, false),        // another module, far higher
		releaseJSON("services/dispat/v1.0.1", false, false), // a backport, published last
		releaseJSON("services/dispat/v1.2.0", false, false), // the answer
		releaseJSON("services/dispat/v1.3.0-rc.1", true, false),
		releaseJSON("services/dispat/v2.0.0", false, true), // a draft
		releaseJSON("services/dispat/vnonsense", false, false),
		releaseJSON("v0.0.0", false, false),
	)

	rel, err := s.Latest(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "services/dispat/v1.2.0", rel.Tag)
	assert.Equal(t, "1.2.0", rel.Version.String())

	// --prerelease widens the field, and ordering still decides: the release
	// candidate is above 1.2.0, so now it wins.
	s.Prerelease = true
	rel, err = s.Latest(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "1.3.0-rc.1", rel.Version.String())
}

// TestLatestPrefersTheStableOverItsOwnCandidate: --prerelease means "consider
// them too", not "prefer them". 1.2.0 is above 1.2.0-rc.4 by semver, so a
// released version wins over the candidates that led to it even when both are
// in the running.
func TestLatestPrefersTheStableOverItsOwnCandidate(t *testing.T) {
	s := listing(t,
		releaseJSON("services/dispat/v1.2.0-rc.4", true, false),
		releaseJSON("services/dispat/v1.2.0", false, false),
	)
	s.Prerelease = true
	rel, err := s.Latest(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "1.2.0", rel.Version.String())
}

// TestLatestWithoutAStableRelease: a repository whose only dispat tags are
// release candidates has nothing to offer a default self-update, and says so
// rather than pretending the caller is up to date. It is exactly the state
// dispat's own repository is in before 1.0.0.
func TestLatestWithoutAStableRelease(t *testing.T) {
	s := listing(t, releaseJSON("services/dispat/v1.0.0-rc.3", true, false))
	_, err := s.Latest(context.Background())
	assert.ErrorIs(t, err, ErrNoRelease)

	s.Prerelease = true
	rel, err := s.Latest(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "1.0.0-rc.3", rel.Version.String())
}

// TestLatestFollowsPaginationToACap: the answer may not be on the first page,
// and the walk back through the listing is bounded so a repository with years
// of releases cannot turn one question into an unbounded number of calls.
func TestLatestFollowsPaginationToACap(t *testing.T) {
	var pages int
	var base string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		pages++
		// Every page says there is another one, forever.
		w.Header().Set("Link", `<`+base+`/next?page=`+fmt.Sprint(pages)+`>; rel="next"`)
		w.Header().Set("Content-Type", "application/json")
		if pages == 2 {
			_ = json.NewEncoder(w).Encode([]map[string]any{
				releaseJSON("services/dispat/v3.0.0", false, false),
			})
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	}))
	base = "http://" + srv.Listener.Addr().String()
	srv.Start()
	t.Cleanup(srv.Close)

	s := &Source{APIURL: base, Owner: "o", Repo: "r"}
	rel, err := s.Latest(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "3.0.0", rel.Version.String(), "a release on the second page is still found")
	assert.Equal(t, maxPages, pages, "the walk stops at the cap however many pages are offered")
}

// TestNextLinkReadsOnlyTheNextRelation: the Link header carries several
// relations and only one of them continues the walk. Anything malformed is
// read as "no more pages", because one page is a complete answer often enough
// that a header nobody can parse is not worth failing over.
func TestNextLinkReadsOnlyTheNextRelation(t *testing.T) {
	for name, tc := range map[string]struct{ header, want string }{
		"next among many": {`<https://a/1>; rel="prev", <https://a/3>; rel="next", <https://a/9>; rel="last"`, "https://a/3"},
		"last only":       {`<https://a/9>; rel="last"`, ""},
		"unquoted":        {`<https://a/3>; rel=next`, "https://a/3"},
		"no brackets":     {`https://a/3; rel="next"`, ""},
		"no relation":     {`<https://a/3>`, ""},
		"empty":           {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, nextLink(tc.header))
		})
	}
}

// TestLatestReportsWhatWentWrong: every way the API can refuse is an error
// with the status in it, never a silent "no update available". A rate limit
// read as "you are current" would hide an update for an hour at a time.
func TestLatestReportsWhatWentWrong(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"rate limited": {http.StatusForbidden, `{"message":"API rate limit exceeded"}`},
		"not found":    {http.StatusNotFound, `{"message":"Not Found"}`},
		"server error": {http.StatusInternalServerError, "boom"},
		"not json":     {http.StatusOK, "<html>this is not the api</html>"},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			s := &Source{APIURL: srv.URL, Owner: "o", Repo: "r"}
			_, err := s.Latest(context.Background())
			require.Error(t, err)
			assert.NotErrorIs(t, err, ErrNoRelease, "a refusal is not an answer")
		})
	}
}

// TestAtLooksUpOneVersionByTag: --release names a version, so the lookup is a
// single call against its tag rather than a walk through the listing, a
// leading v is tolerated, and a version nobody published is ErrNoRelease
// naming the tag it looked for.
func TestAtLooksUpOneVersionByTag(t *testing.T) {
	s := listing(t,
		releaseJSON("services/dispat/v1.0.0", false, false, "dispat-linux-amd64"),
		releaseJSON("services/dispat/v2.0.0", false, true, "dispat-linux-amd64"),
	)

	for _, spelling := range []string{"1.0.0", "v1.0.0", " 1.0.0 "} {
		rel, err := s.At(context.Background(), spelling)
		require.NoError(t, err, "spelling %q", spelling)
		assert.Equal(t, "services/dispat/v1.0.0", rel.Tag)
	}

	_, err := s.At(context.Background(), "9.9.9")
	assert.ErrorIs(t, err, ErrNoRelease)
	assert.Contains(t, err.Error(), "services/dispat/v9.9.9", "the refusal names the tag it looked for")

	_, err = s.At(context.Background(), "2.0.0")
	assert.ErrorIs(t, err, ErrNoRelease, "a draft is not something to install")

	_, err = s.At(context.Background(), "not-a-version")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNoRelease, "a version that cannot be parsed was never looked up")
}

// TestAtIgnoresThePrereleaseSetting: an explicit version is whatever it is.
// Naming a release candidate and then being told no such release exists,
// because a flag nobody set says prereleases do not count, would be absurd.
func TestAtIgnoresThePrereleaseSetting(t *testing.T) {
	s := listing(t, releaseJSON("services/dispat/v1.0.0-rc.1", true, false))
	rel, err := s.At(context.Background(), "1.0.0-rc.1")
	require.NoError(t, err)
	assert.Equal(t, "1.0.0-rc.1", rel.Version.String())
}

// TestReleaseAssetSelection: the asset is chosen by the name the build script
// writes, and a release that has no binary for the running platform can say
// which ones it does have.
func TestReleaseAssetSelection(t *testing.T) {
	rel := Release{Assets: []Asset{
		{Name: "dispat-linux-amd64"}, {Name: "dispat-darwin-arm64"}, {Name: "dispat-windows-amd64.exe"},
	}}

	a, ok := rel.Asset("darwin", "arm64")
	require.True(t, ok)
	assert.Equal(t, "dispat-darwin-arm64", a.Name)

	a, ok = rel.Asset("windows", "amd64")
	require.True(t, ok)
	assert.Equal(t, "dispat-windows-amd64.exe", a.Name, "windows binaries carry the extension")

	_, ok = rel.Asset("plan9", "386")
	assert.False(t, ok)
	assert.Equal(t, []string{"dispat-linux-amd64", "dispat-darwin-arm64", "dispat-windows-amd64.exe"},
		rel.AssetNames(), "the refusal can name what is there")
}

// TestSourceDefaultsToDispatsOwnRepository: an unconfigured source points at
// the repository dispat is released from, because that is the only repository
// that has dispat binaries in it.
func TestSourceDefaultsToDispatsOwnRepository(t *testing.T) {
	var s Source
	assert.Equal(t, "https://api.github.com", s.api())
	assert.Equal(t, DefaultOwner, s.owner())
	assert.Equal(t, DefaultRepo, s.repo())
	assert.Equal(t, DefaultTagPrefix, s.prefix())
	assert.NotNil(t, s.client())

	s = Source{APIURL: "https://ghe.acme.test/api/v3/", Owner: "acme", Repo: "tools", TagPrefix: "cli/v"}
	assert.Equal(t, "https://ghe.acme.test/api/v3", s.api(), "a trailing slash never doubles up")
	assert.Equal(t, "acme", s.owner())
	assert.Equal(t, "tools", s.repo())
	assert.Equal(t, "cli/v", s.prefix())
}

// TestSourceSendsTheTokenOnlyWhenItHasOne: the releases of a public
// repository need no credentials, and a token is only ever there to raise the
// rate limit.
func TestSourceSendsTheTokenOnlyWhenItHasOne(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		seen = append(seen, req.Header.Get("Authorization"))
		assert.Equal(t, "application/vnd.github+json", req.Header.Get("Accept"))
		fmt.Fprint(w, "[]")
	}))
	defer srv.Close()

	s := &Source{APIURL: srv.URL}
	_, _ = s.Latest(context.Background())
	s.Token = "t0ken"
	_, _ = s.Latest(context.Background())
	assert.Equal(t, []string{"", "Bearer t0ken"}, seen)
}

// TestLatestStopsWithTheContext: the check runs beside a command that may end
// at any moment, so a cancelled context ends the request rather than leaving
// it running.
func TestLatestStopsWithTheContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		<-req.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &Source{APIURL: srv.URL}
	_, err := s.Latest(ctx)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled), "got %v", err)
}
