package install

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseRepositoryReadsEverySpellingAReaderHasAtHand: the point of naming a
// repository by URL is that the URL is whatever is already on the clipboard,
// so every shape one can be copied from resolves to the same two names.
func TestParseRepositoryReadsEverySpellingAReaderHasAtHand(t *testing.T) {
	for name, tc := range map[string]struct {
		in   string
		want Repository
	}{
		"the page itself":       {"https://github.com/acme/tool", Repository{Owner: "acme", Repo: "tool", Host: "github.com"}},
		"a page inside it":      {"https://github.com/acme/tool/releases/tag/v1.0.0", Repository{Owner: "acme", Repo: "tool", Host: "github.com"}},
		"a trailing slash":      {"https://github.com/acme/tool/", Repository{Owner: "acme", Repo: "tool", Host: "github.com"}},
		"the clone URL":         {"https://github.com/acme/tool.git", Repository{Owner: "acme", Repo: "tool", Host: "github.com"}},
		"the SSH remote":        {"git@github.com:acme/tool.git", Repository{Owner: "acme", Repo: "tool", Host: "github.com"}},
		"the ssh:// remote":     {"ssh://git@github.com/acme/tool.git", Repository{Owner: "acme", Repo: "tool", Host: "github.com"}},
		"the git:// remote":     {"git://github.com/acme/tool", Repository{Owner: "acme", Repo: "tool", Host: "github.com"}},
		"http rather than tls":  {"http://github.com/acme/tool", Repository{Owner: "acme", Repo: "tool", Host: "github.com"}},
		"the shorthand":         {"acme/tool", Repository{Owner: "acme", Repo: "tool"}},
		"the host shorthand":    {"github.com/acme/tool", Repository{Owner: "acme", Repo: "tool", Host: "github.com"}},
		"surrounding space":     {"  acme/tool  ", Repository{Owner: "acme", Repo: "tool"}},
		"a GitHub Enterprise":   {"https://ghe.corp.example/acme/tool", Repository{Owner: "acme", Repo: "tool", Host: "ghe.corp.example"}},
		"an enterprise on port": {"https://ghe.corp.example:8443/acme/tool", Repository{Owner: "acme", Repo: "tool", Host: "ghe.corp.example:8443"}},
		// A GitHub owner may carry a dot, so a shorthand's first segment is an
		// owner whatever it looks like: only a scheme, or a third segment,
		// makes it a host.
		"an owner with a dot": {"some.org/tool", Repository{Owner: "some.org", Repo: "tool"}},
		"names with dashes":   {"a-c/t_o.ol", Repository{Owner: "a-c", Repo: "t_o.ol"}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseRepository(tc.in)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestParseRepositoryRefusesWhatIsNotOne: the two names are pasted into an API
// path, so everything that would change that path's shape is refused here
// rather than sent. A URL naming only a host is the one that matters most: it
// used to read as the owner, and the request went somewhere nobody asked for.
func TestParseRepositoryRefusesWhatIsNotOne(t *testing.T) {
	for name, in := range map[string]string{
		"nothing at all":           "",
		"only spaces":              "   ",
		"a host and no repository": "https://github.com/onlyowner",
		"a host and nothing else":  "https://github.com/",
		"one bare word":            "tool",
		"a traversal":              "../../etc/passwd",
		"a query string":           "acme/tool?ref=main",
		"a space in the name":      "acme/my tool",
		"an owner that is a dot":   "./tool",
		"a percent escape":         "acme/to%2Fol",
		"an empty owner":           "https://github.com//tool",
		"an empty repository":      "https://github.com/acme/",
		"a host no host may be":    "https://gh_e.corp/acme/tool",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseRepository(in)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "install: ")
		})
	}
}

// TestParseRepositoryReadsAColonAsTheShapeItIsIn: a colon means two different
// things in the two spellings a git remote comes in, and which one is decided
// by the scheme rather than by the characters around it. In the scp form,
// git@host:owner/repo, the colon separates the host from the path; in a URL it
// introduces a port and belongs to the host. A reference that named a scheme
// is never in the scp form, so its colon is never a separator.
//
// This is where the enterprise clone URL a git remote actually hands somebody
// lives — a scheme, a credential and a port at once — and it is the shape that
// used to be misread: the scp split ran after the scheme had been consumed, so
// `ssh://git@host:22/acme/tool` named the owner "22" and the repository
// "acme", and the command queried a repository nobody typed. Dropping either
// the credential or the port hid it, which is why the rows below carry every
// combination of the two rather than only the broken one.
func TestParseRepositoryReadsAColonAsTheShapeItIsIn(t *testing.T) {
	for name, tc := range map[string]struct {
		in   string
		want Repository
	}{
		"a scheme, a credential and a port": {"ssh://git@ghe.example.com:22/acme/tool",
			Repository{Owner: "acme", Repo: "tool", Host: "ghe.example.com:22"}},
		"the same over https": {"https://ci-bot@ghe.example.com:8443/acme/tool.git",
			Repository{Owner: "acme", Repo: "tool", Host: "ghe.example.com:8443"}},
		"a scheme and a port": {"https://ghe.example.com:8443/acme/tool",
			Repository{Owner: "acme", Repo: "tool", Host: "ghe.example.com:8443"}},
		"a scheme and a credential": {"ssh://git@github.com/acme/tool.git",
			Repository{Owner: "acme", Repo: "tool", Host: "github.com"}},
		"a port and a page inside the repository": {"https://git@ghe.example.com:8443/acme/tool/releases/tag/v1.0.0",
			Repository{Owner: "acme", Repo: "tool", Host: "ghe.example.com:8443"}},
		"the scp form": {"git@ghe.example.com:acme/tool.git",
			Repository{Owner: "acme", Repo: "tool", Host: "ghe.example.com"}},
		"the scp form of a public remote": {"git@github.com:acme/tool",
			Repository{Owner: "acme", Repo: "tool", Host: "github.com"}},
		// A credential with no colon after it: the host is separated by a
		// slash like any other URL's, and there is nothing for the scp split
		// to read.
		"a credential and a colon-free host": {"git@github.com/acme/tool",
			Repository{Owner: "acme", Repo: "tool", Host: "github.com"}},
		// No scheme and no credential, so the colon can only be a port: the
		// third segment is what makes the first one a host.
		"a host shorthand carrying a port": {"ghe.example.com:8443/acme/tool",
			Repository{Owner: "acme", Repo: "tool", Host: "ghe.example.com:8443"}},
		"the bare shorthand, which carries no colon at all": {"acme/tool",
			Repository{Owner: "acme", Repo: "tool"}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseRepository(tc.in)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestParseRepositoryRefusesAColonItCannotPlace: the colon shapes above are
// read rather than guessed at, so a colon in a position neither shape allows
// is refused with the name that carries it. The first row is the reason the
// scp split is asked only of a reference carrying a credential: without one,
// "host:acme/tool" and "host:8443/acme/tool" are the same string with a
// different word in it, and only the second has an answer that cannot be
// wrong.
func TestParseRepositoryRefusesAColonItCannotPlace(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"the scp form without a credential": {"ghe.example.com:acme/tool",
			"which no GitHub name may"},
		"a scheme, a credential and a port naming only an owner": {"ssh://git@ghe.example.com:22/acme",
			"names a host but no repository"},
		"a scheme, a credential and a port naming nothing after them": {"ssh://git@ghe.example.com:22/",
			"names a host but no repository"},
		"a port on a host no host name is": {"https://git@ghe_corp.example:8443/acme/tool",
			"which no host name may"},
		"a colon inside the repository's own name": {"git@github.com/acme/to:ol",
			"which no GitHub name may"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseRepository(tc.in)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestRepositoryReportsItselfAndItsAPI: the host is the one thing a reference
// carries that the two names do not, and it decides both how the repository is
// written back and which API its releases are read through.
func TestRepositoryReportsItselfAndItsAPI(t *testing.T) {
	public := Repository{Owner: "acme", Repo: "tool", Host: "github.com"}
	assert.Equal(t, "acme/tool", public.String())
	assert.Empty(t, public.APIURL(), "the public API is the source's own default")

	short := Repository{Owner: "acme", Repo: "tool"}
	assert.Equal(t, "acme/tool", short.String())
	assert.Empty(t, short.APIURL())

	ghe := Repository{Owner: "acme", Repo: "tool", Host: "ghe.corp.example"}
	assert.Equal(t, "ghe.corp.example/acme/tool", ghe.String(),
		"a host that is not the public one is part of the answer")
	assert.Equal(t, "https://ghe.corp.example/api/v3", ghe.APIURL(),
		"which is the whole reason a GitHub Enterprise URL works with nothing else typed")

	assert.Empty(t, Repository{}.String(), "a rollback names a file rather than a repository")
}
