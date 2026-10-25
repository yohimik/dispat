package github

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/changelog"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// longRelease is a release with entries fixes, each carrying a commit body
// of text repeated to about bodySize characters, a benchmark table and a code
// fence: the shape of the rc.8 release whose 125,407-character body GitHub
// refused. Its changelog records the release, so a cut body links the file.
func longRelease(root string, entries, bodySize int, text string) *plan.Release {
	rel := testRelease()
	rel.Pkg = &model.Package{
		Name: "core", Dir: filepath.Join(root, "libs", "core"), Space: &model.Space{Name: "libs"},
		Changelog: model.ChangelogSpec{Enabled: true},
	}
	rel.Units = nil
	for i := range entries {
		paragraph := strings.Repeat(text, bodySize/utf8.RuneCountInString(text))
		rel.Units = append(rel.Units, &ccme.Unit{
			Header: ccme.Header{Type: "perf", Description: fmt.Sprintf("speed up step %d", i)},
			Bump:   ccme.BumpPatch,
			Valid:  true,
			Body: paragraph + "\n\n| n | ns/op |\n|---|-------|\n| 1 | 42 |\n\n" +
				"```sh\n- not an entry\n## not a heading\n```\n",
		})
	}
	return rel
}

// longFormat closes every body with a footer, so a test can see that the cut
// keeps it.
func longFormat() changelog.Format {
	return changelog.Format{RecordFormat: model.RecordFormat{
		Footer: []model.EntryLine{{Line: []string{"---", "", "Install with the script."}}},
	}}
}

// publicAPIClient sends what the releaser addresses to api.github.com to the
// test server instead, so a test sees the body a github.com release gets: the
// link in a cut body is derived for github.com alone.
func publicAPIClient(srv *httptest.Server) *http.Client {
	target, _ := neturl.Parse(srv.URL)
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req = req.Clone(req.Context())
		req.URL.Scheme, req.URL.Host = target.Scheme, target.Host
		return srv.Client().Transport.RoundTrip(req)
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestRecordCutsAnOversizedBodyAtAnEntry(t *testing.T) {
	root := t.TempDir()
	rel := longRelease(root, 80, 2_000, "the walk is cheaper now. ")
	srv, got := captureServer(t)
	var logs bytes.Buffer
	r := &Releaser{Owner: "acme", Repo: "mono", Token: "tkn", Client: publicAPIClient(srv),
		Root: root, CommitSHA: "abc123", Format: longFormat(), Log: zerolog.New(&logs)}

	require.NoError(t, r.Record(context.Background(), rel))

	release := "### Release\n\n- commit: abc123\n- tag: " + rel.TagName() + "\n"
	rendered := changelog.RenderBody(rel, longFormat(), nil, release)
	require.Greater(t, utf8.RuneCountInString(rendered), 125_000, "the fixture has to be over GitHub's limit")
	assert.LessOrEqual(t, utf8.RuneCountInString(got.Body), releaseBodyBudget)

	link := "The release notes continue in [libs/core/CHANGELOG.md]" +
		"(https://github.com/acme/mono/blob/" + rel.TagName() + "/libs/core/CHANGELOG.md)."
	kept, after, found := strings.Cut(got.Body, "\n\n"+link+"\n")
	require.True(t, found, "the cut body links the rest: %s", got.Body[len(got.Body)-500:])
	require.True(t, strings.HasPrefix(rendered, kept), "the body keeps a prefix of the rendered sections")
	assert.True(t, strings.HasPrefix(rendered[len(kept):], "\n\n- speed up step "),
		"the cut falls between two entries, not inside one")
	assert.Equal(t, "\n"+release+"\n---\n\nInstall with the script.\n", after,
		"the release details and the footer survive the cut")
	assert.Equal(t, 1, strings.Count(logs.String(), plan.CodeGitHubBodyCut), "the cut is logged once: %s", logs.String())
	assert.Contains(t, logs.String(), `"level":"warn"`)
}

func TestRecordSendsABodyWithinTheLimitUnchanged(t *testing.T) {
	root := t.TempDir()
	rel := longRelease(root, 40, 2_000, "the walk is cheaper now. ")
	srv, got := captureServer(t)
	var logs bytes.Buffer
	r := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client(),
		Root: root, Format: longFormat(), Log: zerolog.New(&logs)}

	require.NoError(t, r.Record(context.Background(), rel))

	rendered := changelog.RenderBody(rel, longFormat(), nil, "")
	require.Greater(t, utf8.RuneCountInString(rendered), 60_000)
	assert.Equal(t, rendered, got.Body, "a body within the limit is sent exactly as rendered")
	assert.NotContains(t, logs.String(), plan.CodeGitHubBodyCut)
}

func TestRecordCountsAMultibyteBodyInCharacters(t *testing.T) {
	// Three bytes a character: 50,000 characters are 150,000 bytes, which is
	// over the limit by bytes and well within it by characters.
	root := t.TempDir()
	short := longRelease(root, 20, 2_500, "変更")
	srv, got := captureServer(t)
	r := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client(), Root: root}
	require.NoError(t, r.Record(context.Background(), short))
	require.Greater(t, len(got.Body), 125_000)
	assert.Equal(t, changelog.RenderBody(short, changelog.Format{}, nil, ""), got.Body)

	long := longRelease(root, 60, 2_500, "変更")
	require.NoError(t, r.Record(context.Background(), long))
	assert.LessOrEqual(t, utf8.RuneCountInString(got.Body), releaseBodyBudget)
	assert.Contains(t, got.Body, "The release notes continue in")
}

func TestRenderBodyMatchesPublishedBodyAtTheLimit(t *testing.T) {
	for _, sample := range []struct {
		name string
		text string
	}{
		{name: "ASCII", text: "the walk is cheaper now. "},
		{name: "Unicode", text: "変更後の処理は速い。"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			root := t.TempDir()
			rel := longRelease(root, 80, 2_000, sample.text)
			srv, got := captureServer(t)
			r := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client(),
				Root: root, CommitSHA: "abc123", Format: longFormat()}
			preview := r.RenderBody(rel, changelog.ReleaseLookup(rel), "abc123", "")
			require.NoError(t, r.Record(context.Background(), rel))
			assert.Equal(t, got.Body, preview)
			assert.LessOrEqual(t, utf8.RuneCountInString(preview), releaseBodyBudget)
			assert.Contains(t, preview, "### Release\n\n- commit: abc123")
			assert.Contains(t, preview, "Install with the script.")
			assert.Contains(t, preview, "The release notes continue in")
		})
	}
}

func TestBoundBodyIsDeterministic(t *testing.T) {
	root := t.TempDir()
	r := &Releaser{Owner: "acme", Repo: "mono", Root: root}
	rel := longRelease(root, 80, 2_000, "the walk is cheaper now. ")
	parts := func() changelog.BodyParts { return changelog.RenderBodyParts(rel, longFormat(), nil) }

	first := r.boundBody(rel, parts())
	for range 3 {
		assert.Equal(t, first, r.boundBody(rel, parts()))
	}
}

func TestBoundBodyKeepsWholeBlocks(t *testing.T) {
	body := "### Fixes\n\n- one\n  body\n\n- two\n  body\n\n### Features\n\n- three\n"
	cases := []struct {
		name   string
		budget int
		want   string
	}{
		{"fits", len(body), body},
		{"drops the last section, heading and all", 50, "### Fixes\n\n- one\n  body\n\n- two\n  body\n\nmore\n"},
		{"drops an entry", 35, "### Fixes\n\n- one\n  body\n\nmore\n"},
		{"cuts at a line when no entry fits", 20, "### Fixes\n\nmore\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, boundBody(body, c.budget, "more"))
		})
	}
}

func TestBoundBodyNeverCutsInsideAFence(t *testing.T) {
	body := "Install:\n\n```sh\n- not an entry\n\n# not a heading\n```\n\n- after the fence\n"
	got := boundBody(body, 40, "more")
	assert.Equal(t, "Install:\n\nmore\n", got)
}

func TestContinuationLineNamesWhereTheNotesAre(t *testing.T) {
	root := t.TempDir()
	rel := longRelease(root, 1, 10, "x")
	tag := rel.TagName()
	public := &Releaser{Owner: "acme", Repo: "mono", Root: root}
	unknown := &Releaser{Root: root}
	enterprise := &Releaser{APIURL: "https://ghe.example.com/api/v3", Owner: "acme", Repo: "mono", Root: root}

	assert.Equal(t, "The release notes continue in [libs/core/CHANGELOG.md](https://github.com/acme/mono/blob/"+
		tag+"/libs/core/CHANGELOG.md).", public.continuationLine(rel, tag))
	assert.Equal(t, "The release notes continue in libs/core/CHANGELOG.md at "+tag+".",
		unknown.continuationLine(rel, tag), "a token-free preview may lack repository coordinates")
	assert.Equal(t, "The release notes continue in libs/core/CHANGELOG.md at "+tag+".",
		enterprise.continuationLine(rel, tag))

	rel.Pkg.Changelog.File = "docs/HISTORY.md"
	assert.Contains(t, public.continuationLine(rel, tag), "/libs/core/docs/HISTORY.md)")

	rel.Pkg.Changelog.Enabled = false
	assert.Equal(t, "The release notes continue in [the commit history of libs/core](https://github.com/acme/mono/commits/"+
		tag+"/libs/core).", public.continuationLine(rel, tag))

	rel.Pkg.RepoRoot = filepath.Join(root, "libs", "core")
	assert.Equal(t, "The release notes continue in [the commit history of the repository](https://github.com/acme/mono/commits/"+
		tag+").", public.continuationLine(rel, tag), "a polyrepo package's folder is relative to its own repository")

	outside := &Releaser{Owner: "acme", Repo: "mono", Root: filepath.Join(root, "elsewhere")}
	rel.Pkg.RepoRoot = ""
	assert.Equal(t, "The release notes continue in the repository at ["+tag+"](https://github.com/acme/mono/tree/"+tag+").",
		outside.continuationLine(rel, tag))
}

// TestRecordStillFailsOnAnotherRefusal fences what the cut must not hide: a
// body GitHub refuses for any other reason still fails the release, with
// GitHub's own words.
func TestRecordStillFailsOnAnotherRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if releaseProbe(w, r) {
			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"message":"Validation Failed","errors":[{"field":"tag_name","code":"invalid"}]}`))
	}))
	defer srv.Close()

	root := t.TempDir()
	r := &Releaser{APIURL: srv.URL, Owner: "acme", Repo: "mono", Token: "tkn", Client: srv.Client(), Root: root}
	err := r.Record(context.Background(), longRelease(root, 80, 2_000, "the walk is cheaper now. "))
	var status *StatusError
	require.ErrorAs(t, err, &status)
	assert.Equal(t, http.StatusUnprocessableEntity, status.Status)
	assert.Contains(t, status.Body, "tag_name")
}

// TestCutBodyCutsTheWholeBodyWhenTheFooterLeavesNoRoom: parts around the
// sections that are themselves near the limit leave nothing to cut the
// sections into, so the whole body is cut and still fits.
func TestCutBodyCutsTheWholeBodyWhenTheFooterLeavesNoRoom(t *testing.T) {
	footer := strings.Repeat("A footer paragraph that goes on.\n\n", 3_700)
	parts := changelog.BodyParts{
		Sections: "### Fixes\n\n- one\n\n- two\n",
		Trailing: []string{footer},
	}
	got := cutBody(parts, "more")
	assert.LessOrEqual(t, utf8.RuneCountInString(got), releaseBodyBudget)
	assert.True(t, strings.HasPrefix(got, "### Fixes\n\n- one\n\n- two\n\nA footer paragraph"))
	assert.True(t, strings.HasSuffix(got, "A footer paragraph that goes on.\n\nmore\n"))
}
