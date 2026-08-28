package download

import (
	"fmt"
	"strings"
)

// Repository is the GitHub repository a download comes from: the half of the
// command line that says whose releases to read.
//
// It is a value rather than three loose strings because the three travel
// together everywhere and because the two that reach a URL path have to be
// validated once, in one place. A name carrying a slash or a query would
// otherwise be pasted straight into the API path the source builds.
type Repository struct {
	// Owner and Repo are the two path segments GitHub addresses a repository
	// by.
	Owner string
	Repo  string
	// Host is the web host the reference named, empty when it named none.
	// GitHub Enterprise is the reason it is kept: only the host says which
	// API a repository's releases live behind.
	Host string
}

// String is how the repository is written back to the reader: the shorthand,
// which is what a person types, qualified by the host when it is not the
// public one.
func (r Repository) String() string {
	if r.Owner == "" && r.Repo == "" {
		return ""
	}
	if r.Host != "" && r.Host != githubHost {
		return r.Host + "/" + r.Owner + "/" + r.Repo
	}
	return r.Owner + "/" + r.Repo
}

// APIURL is the REST endpoint this repository's releases are read through, or
// empty when the source's own default (api.github.com) is right.
//
// A host that is not github.com is a GitHub Enterprise install, whose API
// lives under /api/v3 of the same host. It is a guess rather than a fact, and
// --api-url is what overrides it when an install is mounted somewhere else.
func (r Repository) APIURL() string {
	if r.Host == "" || r.Host == githubHost {
		return ""
	}
	return "https://" + r.Host + "/api/v3"
}

const githubHost = "github.com"

// ParseRepository reads a repository out of what was typed on the command
// line.
//
// Every spelling of a repository a reader has at hand is accepted, because the
// point of naming one by URL is that the URL is what the browser is showing:
// the page itself, a page inside it, the clone URL, the SSH remote, and the
// bare owner/repo shorthand. Anything past the two path segments is dropped,
// so pasting the releases page works as well as pasting the repository's.
//
// Whether a leading segment is a host or an owner is decided by the scheme
// rather than guessed at, because a GitHub owner may carry a dot of its own:
// a reference that named a scheme has a host in that position and a shorthand
// never does, so "some.org/repo" stays the owner it reads as.
func ParseRepository(raw string) (Repository, error) {
	ref := strings.TrimSpace(raw)
	if ref == "" {
		return Repository{}, fmt.Errorf("download: no repository given: %s", usageHint)
	}
	ref, scheme := cutScheme(ref)
	// The credentials of a clone URL, which name no part of the repository.
	if _, rest, ok := strings.Cut(ref, "@"); ok {
		ref = rest
		// git@host:owner/repo, whose host is separated by a colon rather than
		// a slash. Only the first colon, so a host carrying a port survives.
		if host, path, ok := strings.Cut(ref, ":"); ok && !strings.Contains(host, "/") {
			ref, scheme = host+"/"+path, true
		}
	}
	ref = strings.TrimSuffix(strings.Trim(ref, "/"), ".git")

	segments := strings.Split(ref, "/")
	var host string
	// With a scheme the first segment is the host by definition; without one
	// it is a host only when something follows the two the shorthand needs.
	if scheme || (len(segments) > 2 && strings.Contains(segments[0], ".")) {
		if len(segments) < 3 {
			return Repository{}, fmt.Errorf("download: %q names a host but no repository: %s", raw, usageHint)
		}
		host, segments = segments[0], segments[1:]
	}
	if len(segments) < 2 {
		return Repository{}, fmt.Errorf("download: %q names no repository: %s", raw, usageHint)
	}
	repo := Repository{Owner: segments[0], Repo: strings.TrimSuffix(segments[1], ".git"), Host: host}
	if err := validSegment("owner", repo.Owner); err != nil {
		return Repository{}, err
	}
	if err := validSegment("repository", repo.Repo); err != nil {
		return Repository{}, err
	}
	if host != "" {
		if err := validHost(host); err != nil {
			return Repository{}, err
		}
	}
	return repo, nil
}

// cutScheme removes the protocol a reference may carry and reports whether it
// carried one, which is what says that the segment after it is a host.
func cutScheme(ref string) (string, bool) {
	for _, scheme := range []string{"https://", "http://", "git://", "ssh://"} {
		if rest, ok := strings.CutPrefix(ref, scheme); ok {
			return rest, true
		}
	}
	return ref, false
}

// usageHint is the one line every refusal to read a repository ends with,
// because a reader who spelled it wrong wants the spelling rather than a
// grammar.
const usageHint = "name one as https://github.com/owner/repo or as owner/repo"

// validSegment refuses anything GitHub cannot name, which is also everything
// that would change the shape of the API path it is pasted into. GitHub allows
// letters, digits, dashes, underscores and dots in these two names and nothing
// else, so accepting exactly that set is both the truth and the whole of the
// injection check.
func validSegment(what, s string) error {
	if s == "" {
		return fmt.Errorf("download: the %s is empty: %s", what, usageHint)
	}
	if s == "." || s == ".." {
		return fmt.Errorf("download: %q is not a name for the %s", s, what)
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return fmt.Errorf("download: the %s %q carries %q, which no GitHub name may", what, s, string(r))
		}
	}
	return nil
}

// validHost holds the host to the same rule, since it becomes the authority of
// the derived API URL.
func validHost(host string) error {
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '.', r == ':':
		default:
			return fmt.Errorf("download: the host %q carries %q, which no host name may", host, string(r))
		}
	}
	return nil
}
