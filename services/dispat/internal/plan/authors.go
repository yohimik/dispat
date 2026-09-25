// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package plan

import (
	"slices"
	"strings"

	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/globx"
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
// far more often than under several addresses. Both sides are folded with
// globx.Fold, the equivalence every other name comparison in the tool uses,
// because neither git nor a forge treats the case as significant here. The
// fold, unlike lowercasing, keeps a final sigma, a micro sign or a long s in
// the class of its ordinary letter and leaves a dotted capital I apart from i.
func (a Author) key() string {
	if a.Email != "" {
		return globx.Fold(a.Email)
	}
	return globx.Fold(a.Name)
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
	return DedupeAuthors(out)
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

// collectReleaseAuthors fills in the window attribution of every release that
// has an entry to attribute: a changed package that versions at all. Those are
// the only releases anything renders, a record, a preview or a step's aligned
// plan, and every one of them is changed. A package with nothing to release,
// and a versioning none package, which never has a record, is not scanned: in
// a large workspace that is most packages, and the scan is its whole window.
func (cp *computation) collectReleaseAuthors() {
	for _, name := range cp.order {
		rel := cp.rel[name]
		if rel == nil || !rel.IsReleasable() || !rel.IsChanged() {
			continue
		}
		rel.WindowAuthors, rel.FreshWindowAuthors = cp.collectWindowAuthors(name)
		if len(rel.WindowAuthors) > 0 {
			cp.log.Debug().Str("package", name).Int("window", len(rel.WindowAuthors)).
				Int("fresh", len(rel.FreshWindowAuthors)).Msg("release authors collected")
		}
	}
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
//
// The collection reads every commit of the union once per package, which is
// the workspace's whole history for a monorepo and every repository's history
// for a fleet. Packages released at the same boundaries see the same commits
// as pending and the same subset as fresh, so the answer is computed once per
// window identity and shared: without that, the pass costs packages × commits,
// which is what makes a large workspace's planning superlinear in its own size.
func (cp *computation) collectWindowAuthors(name string) (window, fresh []Author) {
	// Sharing is worth its key only when the scan it replaces is long enough
	// to pay for one. A workspace whose union holds a handful of commits scans
	// them directly rather than identifying a window it would never look up
	// twice.
	shared := len(cp.commits) >= windowAuthorSharingMinimum
	var identity windowIdentity
	if shared {
		identity = cp.windowIdentity(name)
		if cached, ok := cp.windowAuthors[identity]; ok {
			return cached.window, cached.fresh
		}
	}
	windowSeen := make(map[string]bool)
	freshSeen := make(map[string]bool)
	attribute := func(rec *commitRec) {
		a := Author{Name: rec.commit.AuthorName, Email: rec.commit.AuthorEmail}
		if a.empty() {
			return
		}
		window = appendUniqueAuthor(window, windowSeen, a)
		if !cp.containedInBaseline(name, rec.key) {
			fresh = appendUniqueAuthor(fresh, freshSeen, a)
		}
	}
	if len(cp.histories) == 0 {
		// A single history's window is a set of ranks, and a rank is a
		// position in the union: reading the set in rank order visits
		// exactly the window's commits, newest first, and nothing else.
		set := cp.window[name]
		if cp.stats != nil {
			cp.stats.AuthorScans.Add(int64(set.len()))
		}
		set.each(func(rank int) { attribute(cp.commits[rank]) })
	} else {
		reachable := cp.windowRepositories(name)
		if cp.stats != nil {
			cp.stats.AuthorScans.Add(int64(len(cp.commits)))
		}
		for _, rec := range cp.commits {
			// A commit from a repository the package's window cannot reach
			// is not pending for it, whatever its key says. Deciding that
			// from the repository alone saves consulting every shared window
			// view for every commit of every other repository in the fleet.
			if !reachable[globx.Fold(rec.repository)] || !cp.inWindow(name, rec.key) {
				continue
			}
			attribute(rec)
		}
	}
	// Clipped, so a consumer that appends to a shared list reallocates rather
	// than writing into another release's attribution.
	window, fresh = slices.Clip(window), slices.Clip(fresh)
	if shared {
		cp.windowAuthors[identity] = windowAuthorSet{window: window, fresh: fresh}
	}
	return window, fresh
}

// windowAuthorSharingMinimum is the union length from which one window's
// attribution is worth identifying and sharing: below it, hashing the identity
// costs more than scanning the commits it stands for.
const windowAuthorSharingMinimum = 16

// windowAuthorSet is one window identity's attribution: the authors of its
// pending window and of the fresh part of it.
type windowAuthorSet struct{ window, fresh []Author }

// windowIdentity names every input inWindow and containedInBaseline read for a
// package. Two packages with the same identity give the same answer to both
// questions for every commit in the union, so they share one attribution.
//
// It is a comparable value rather than an assembled key because a single
// history — the ordinary monorepo — identifies a window with three strings it
// already holds, and building a key for each of a thousand packages would
// cost more than the scan the sharing saves. Only a composed workspace, whose
// window spans a variable set of repositories, assembles one.
type windowIdentity struct {
	// The single-history case: the window's shared boundary, and the two
	// commits containedInBaseline compares against.
	window, baseline, stable string
	// The composed case: the package's repository role, the history views its
	// window was attached from, and its boundary in each of them.
	composed string
}

func (cp *computation) windowIdentity(name string) windowIdentity {
	if len(cp.histories) == 0 {
		id := windowIdentity{window: cp.windowKey[name]}
		if rel := cp.rel[name]; rel != nil {
			id.baseline, id.stable = rel.BaselineCommit, rel.StableCommit
		}
		return id
	}
	var b strings.Builder
	switch p := cp.byName[name]; {
	case p == nil:
		b.WriteString("unknown")
	case strings.EqualFold(p.Repository, cp.controlRepo):
		b.WriteString("control")
	default:
		b.WriteString("source")
	}
	b.WriteByte(0x02)
	for _, key := range cp.windowKeys[name] {
		b.WriteString(key)
		b.WriteByte(0x01)
	}
	b.WriteByte(0x02)
	repositories := make([]string, 0, len(cp.stableBoundaries[name]))
	for repository := range cp.stableBoundaries[name] {
		repositories = append(repositories, repository)
	}
	slices.Sort(repositories)
	for _, repository := range repositories {
		b.WriteString(repository)
		b.WriteByte(0x01)
		b.WriteString(cp.stableBoundaries[name][repository])
		b.WriteByte(0x01)
		b.WriteString(cp.publishedBoundaries[name][repository])
		b.WriteByte(0x02)
	}
	return windowIdentity{composed: b.String()}
}

// windowRepositories are the histories whose commits a package's window can
// contain: the repositories its boundaries were resolved against, plus the
// control repository, whose intent reaches a source package through its own
// admission rule rather than through a shared window. A nil result means a
// legacy single history, where every commit is a candidate.
func (cp *computation) windowRepositories(name string) map[string]bool {
	if len(cp.histories) == 0 {
		return nil
	}
	out := make(map[string]bool, len(cp.stableBoundaries[name])+1)
	for repository := range cp.stableBoundaries[name] {
		out[globx.Fold(repository)] = true
	}
	out[globx.Fold(cp.controlRepo)] = true
	return out
}

func appendUniqueAuthor(out []Author, seen map[string]bool, author Author) []Author {
	key := author.key()
	if seen[key] {
		return out
	}
	seen[key] = true
	return append(out, author)
}

// DedupeAuthors keeps the first occurrence of each identity, preserving order.
// Order is what carries the meaning here — the git author before the people a
// trailer adds, and the commit sequence across a window — so sorting would
// throw away the one thing the list says besides who.
//
// The changelog's section reads its authors through this function too, so the
// planner and the renderer cannot disagree about who one person is.
func DedupeAuthors(in []Author) []Author {
	if len(in) < 2 {
		return in
	}
	seen := make(map[string]bool)
	out := make([]Author, 0, min(len(in), 8))
	for _, a := range in {
		out = appendUniqueAuthor(out, seen, a)
	}
	return out
}
