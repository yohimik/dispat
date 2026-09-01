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
// platform a test might ask for. Every asset carries "url" as well as
// "browser_download_url", because a real listing always sends both.
func releaseJSON(tag string, prerelease, draft bool, assetNames ...string) map[string]any {
	assets := make([]map[string]any, 0, len(assetNames))
	for _, name := range assetNames {
		assets = append(assets, map[string]any{
			"name": name, "browser_download_url": "http://example.invalid/" + name,
			"url":  "http://example.invalid/api/assets/" + name,
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

// TestAssetsCarryTheAPIEndpoint: an asset's "url" is the endpoint that serves
// its bytes to an authenticated request, and it has to survive the decode for
// a private repository to be installable at all. A listing that sends no
// "url", which is what an older GitHub Enterprise does, leaves it empty rather
// than inventing an address.
func TestAssetsCarryTheAPIEndpoint(t *testing.T) {
	s := listing(t, releaseJSON("services/dispat/v1.0.0", false, false, "dispat-linux-amd64"))

	rel, err := s.Latest(context.Background())
	require.NoError(t, err)
	require.Len(t, rel.Assets, 1)
	assert.Equal(t, "http://example.invalid/api/assets/dispat-linux-amd64", rel.Assets[0].APIURL)
	assert.Equal(t, "http://example.invalid/dispat-linux-amd64", rel.Assets[0].URL,
		"the public URL is still read, and the two are different addresses")

	byTag, err := s.At(context.Background(), "1.0.0")
	require.NoError(t, err)
	require.Len(t, byTag.Assets, 1)
	assert.Equal(t, rel.Assets[0].APIURL, byTag.Assets[0].APIURL,
		"both lookups convert through the same place, so neither can drift")

	bare := releaseJSON("services/dispat/v1.0.0", false, false)
	bare["assets"] = []map[string]any{{
		"name": "dispat-linux-amd64", "browser_download_url": "http://example.invalid/dispat-linux-amd64",
		"size": 10,
	}}
	older := listing(t, bare)
	rel, err = older.Latest(context.Background())
	require.NoError(t, err)
	require.Len(t, rel.Assets, 1)
	assert.Empty(t, rel.Assets[0].APIURL, "a listing without a url field reports no endpoint")
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
// repository need no credentials, so the header appears only once there is a
// token to put in it.
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

// TestLatestAndAtCarryTheNotes: the body and the release's own page ride along
// with whichever lookup found the release, so nothing has to go back to the API
// to find out what changed. Both paths, because a version named with --release
// deserves the same answer as the one found by looking.
func TestLatestAndAtCarryTheNotes(t *testing.T) {
	const body = "### Features\n\n- streaming\n"
	const page = "https://github.com/o/r/releases/tag/services%2Fdispat%2Fv1.1.0"
	rel := releaseJSON("services/dispat/v1.1.0", false, false, "dispat-linux-amd64")
	rel["body"], rel["html_url"] = body, page
	src := listing(t, rel)

	latest, err := src.Latest(context.Background())
	require.NoError(t, err)
	assert.Equal(t, body, latest.Body)
	assert.Equal(t, page, latest.HTMLURL)

	at, err := src.At(context.Background(), "1.1.0")
	require.NoError(t, err)
	assert.Equal(t, body, at.Body, "the by-tag lookup answers the same")
	assert.Equal(t, page, at.HTMLURL)
}

// TestGetRefusesAResponseItCannotHaveReadWhole: a listing past the read cap is
// a truncated document, and parsing one reports a syntax error about JSON that
// was never malformed. Naming the cap is the difference between a reader who
// knows what happened and one who thinks GitHub is broken.
func TestGetRefusesAResponseItCannotHaveReadWhole(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// A well formed listing that simply does not fit.
		fmt.Fprint(w, `[{"tag_name":"services/dispat/v1.1.0","body":"`)
		filler := strings.Repeat("a", 1<<16)
		for written := 0; written <= maxListBody; written += len(filler) {
			fmt.Fprint(w, filler)
		}
		fmt.Fprint(w, `"}]`)
	}))
	t.Cleanup(srv.Close)
	src := &Source{APIURL: srv.URL, Owner: "o", Repo: "r"}

	_, err := src.Latest(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "larger than", "the refusal names the cap")
	assert.NotContains(t, err.Error(), "invalid character", "and is not a JSON complaint")
}

// TestChangelogURLPointsAtTheTag: the link is printed under "you now have
// 1.1.0", so it has to keep showing 1.1.0's changelog forever. The tag carries
// slashes of its own, which is why the ref is fully qualified.
func TestChangelogURLPointsAtTheTag(t *testing.T) {
	src := &Source{Owner: "yohimik", Repo: "dispat"}
	rel := Release{
		Tag:     "services/dispat/v1.1.0",
		HTMLURL: "https://github.com/yohimik/dispat/releases/tag/services%2Fdispat%2Fv1.1.0",
	}
	assert.Equal(t,
		"https://github.com/yohimik/dispat/blob/refs/tags/services/dispat/v1.1.0/services/dispat/CHANGELOG.md",
		src.ChangelogURL(rel))
}

// TestChangelogURLWithoutAReleasePage: an older GitHub Enterprise, or a fake
// that never sent one, still gets a link — from the owner and repo the source
// was pointed at, which is the only other thing that knows the repository.
func TestChangelogURLFallsBack(t *testing.T) {
	for name, tc := range map[string]struct {
		src  Source
		rel  Release
		want string
	}{
		"no release page": {
			src:  Source{Owner: "o", Repo: "r"},
			rel:  Release{Tag: "services/dispat/v1.1.0"},
			want: "https://github.com/o/r/blob/refs/tags/services/dispat/v1.1.0/services/dispat/CHANGELOG.md",
		},
		"a release page of an unexpected shape": {
			src:  Source{Owner: "o", Repo: "r"},
			rel:  Release{Tag: "services/dispat/v1.1.0", HTMLURL: "https://example.invalid/whatever"},
			want: "https://github.com/o/r/blob/refs/tags/services/dispat/v1.1.0/services/dispat/CHANGELOG.md",
		},
		"github enterprise, taken from the release's own page": {
			src:  Source{APIURL: "https://ghe.example/api/v3", Owner: "o", Repo: "r", TagPrefix: "cli/v"},
			rel:  Release{Tag: "cli/v2.0.0", HTMLURL: "https://ghe.example/o/r/releases/tag/cli%2Fv2.0.0"},
			want: "https://ghe.example/o/r/blob/refs/tags/cli/v2.0.0/cli/CHANGELOG.md",
		},
		"a tag prefix naming no folder falls back to the root changelog": {
			src:  Source{Owner: "o", Repo: "r", TagPrefix: "v"},
			rel:  Release{Tag: "v1.1.0"},
			want: "https://github.com/o/r/blob/refs/tags/v1.1.0/CHANGELOG.md",
		},
		"a tag prefix that is not a version prefix at all": {
			src:  Source{Owner: "o", Repo: "r", TagPrefix: "release-"},
			rel:  Release{Tag: "release-1.1.0"},
			want: "https://github.com/o/r/blob/refs/tags/release-1.1.0/CHANGELOG.md",
		},
		"no tag, no link": {
			src:  Source{Owner: "o", Repo: "r"},
			rel:  Release{},
			want: "",
		},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.src.ChangelogURL(tc.rel))
		})
	}
}
