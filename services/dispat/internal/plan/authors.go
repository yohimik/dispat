package plan

import (
	"strings"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
)

// coAuthorTrailer is the trailer key that names someone besides the git author
// as an author of the commit. It is one of ccme's default message-level
// trailers (§4.5), which is why a footer carrying it is ignored for versioning
// and available here.
const coAuthorTrailer = "Co-authored-by"

// Author is one person a release record attributes work to.
//
// The identity is git's own and nothing else: the name and email the commit
// was authored under. No forge is asked who that is, so the attribution works
// the same on a repository that has never seen GitHub, costs no API call and
// cannot change under a record that has already been published.
type Author struct {
	Name  string
	Email string
}

// Username is the short form of the identity: the local part of the email,
// which is what a forge account is almost always called.
//
// An email with no "@" is returned whole rather than dropped — it is not a
// well-formed address, but it is what the commit says, and inventing nothing
// beats attributing to nobody. An identity with no email at all falls back to
// the name, so the username form never renders an empty author.
func (a Author) Username() string {
	if a.Email == "" {
		return a.Name
	}
	if at := strings.Index(a.Email, "@"); at >= 0 {
		return a.Email[:at]
	}
	return a.Email
}

// key identifies the person for deduplication. The email is the identifier
// when there is one: one person commits under several spellings of their name
// far more often than under several addresses. Both sides are lowercased
// because neither git nor a forge treats the case as significant here.
func (a Author) key() string {
	if a.Email != "" {
		return strings.ToLower(a.Email)
	}
	return strings.ToLower(a.Name)
}

// empty reports an identity with nothing to render.
func (a Author) empty() bool { return a.Name == "" && a.Email == "" }

// ParseCoAuthor reads a Co-authored-by trailer value.
//
// The conventional form is "Name <email>", but the trailer is free text — no
// specification governs what a person types after the colon — so the two
// degenerate forms are accepted rather than discarded: a bare address (it has
// an "@" and no angle brackets) and a bare name. A value that carries neither
// a name nor an address is not an author and reports false, which is the
// caller's signal to skip it silently: a malformed trailer is a typo in a
// commit message that has already been written, and no diagnostic could now be
// acted on.
func ParseCoAuthor(value string) (Author, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Author{}, false
	}
	if open := strings.LastIndex(value, "<"); open >= 0 {
		if shut := strings.Index(value[open:], ">"); shut >= 0 {
			a := Author{
				Name:  strings.TrimSpace(value[:open]),
				Email: strings.TrimSpace(value[open+1 : open+shut]),
			}
			if a.empty() {
				return Author{}, false
			}
			return a, true
		}
	}
	if strings.Contains(value, "@") && !strings.ContainsAny(value, " \t") {
		return Author{Email: value}, true
	}
	return Author{Name: value}, true
}

// unitAuthors is who a unit is by: the commit's git author, then everyone its
// Co-authored-by trailers name, in the order they were written.
//
// The git author comes first because they are the one identity git guarantees
// exists; the trailers are what the convention adds on top. A trailer naming
// the git author again — which a squash-merge of one person's own branch
// produces routinely — is deduplicated away rather than rendered twice.
func unitAuthors(c gitx.Commit, u *ccme.Unit) []Author {
	var out []Author
	if git := (Author{Name: c.AuthorName, Email: c.AuthorEmail}); !git.empty() {
		out = append(out, git)
	}
	if u != nil {
		for _, f := range u.Footers {
			// EqualFold, not equality: CanonicalKey keeps the spelling the
			// author actually wrote for any key outside the §8.1 registry, and
			// Co-authored-by is not in it. MessageLevel is itself computed
			// case-insensitively, so "CO-AUTHORED-BY:" reaches here with its
			// own casing intact and an exact comparison would drop it.
			if !f.MessageLevel || !strings.EqualFold(f.CanonicalKey, coAuthorTrailer) {
				continue
			}
			if a, ok := ParseCoAuthor(f.Value); ok {
				out = append(out, a)
			}
		}
	}
	return dedupeAuthors(out)
}

// resolveAuthors records who each of a commit's parsed units is by (§13.4).
//
// It runs per commit rather than per unit so the trailers are read once for a
// message that carries several units: every unit of one commit shares the
// commit's git author and its Co-authored-by trailers are message-level, which
// §4.5 defines as describing the message rather than any one unit in it.
func (cp *computation) resolveAuthors(rec *commitRec) {
	if len(rec.units) == 0 {
		return
	}
	distinct := make(map[string]bool)
	for _, u := range rec.units {
		authors := unitAuthors(rec.commit, u)
		if len(authors) == 0 {
			continue
		}
		cp.unitAuthors[u] = authors
		for _, a := range authors {
			distinct[a.key()] = true
		}
	}
	if len(distinct) == 0 {
		return
	}
	// Trace, not debug: this is one line per commit of every window, which is
	// the volume a reader asks for only when chasing an attribution that came
	// out wrong.
	primary := 0
	if !(Author{Name: rec.commit.AuthorName, Email: rec.commit.AuthorEmail}).empty() {
		primary = 1
	}
	cp.log.Trace().Str("commit", rec.key).Int("authors", len(distinct)).
		Int("coauthors", len(distinct)-primary).Msg("authors resolved")
}

// collectWindowAuthors aggregates the authors of every commit in a package's
// pending window, and of the fresh part of it.
//
// Every commit counts, not only the ones carrying valid units: a commit whose
// message is not a record at all still changed the package, and an "Authors"
// section that silently omitted whoever wrote it would be worse than no
// section. Only the primary author is taken — a message that did not parse has
// no footers worth reading — and the newest-first order of cp.commits is kept,
// which is the order rel.Units is built in.
func (cp *computation) collectWindowAuthors(name string) (window, fresh []Author) {
	for _, rec := range cp.commits {
		if !cp.window[name][rec.key] {
			continue
		}
		a := Author{Name: rec.commit.AuthorName, Email: rec.commit.AuthorEmail}
		if a.empty() {
			continue
		}
		window = append(window, a)
		if !cp.containedInBaseline(name, rec.key) {
			fresh = append(fresh, a)
		}
	}
	return dedupeAuthors(window), dedupeAuthors(fresh)
}

// dedupeAuthors keeps the first occurrence of each identity, preserving order.
// Order is what carries the meaning here — the git author before the people a
// trailer adds, and the commit sequence across a window — so sorting would
// throw away the one thing the list says besides who.
func dedupeAuthors(in []Author) []Author {
	if len(in) < 2 {
		return in
	}
	seen := make(map[string]bool, len(in))
	out := make([]Author, 0, len(in))
	for _, a := range in {
		k := a.key()
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, a)
	}
	return out
}
