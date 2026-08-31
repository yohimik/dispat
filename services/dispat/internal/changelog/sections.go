package changelog

// How a release's work is grouped into sections, in what order the sections
// render, and what one line of a section says. The entry's frame — its title,
// its header and footer, the blocks around the sections — lives in
// changelog.go and lines.go; everything between the first "###" and the last
// bullet is here.

import (
	"net/url"
	"strings"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/pkg/ccme"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// The commit-reference placements, spelled once so the renderer, the validator
// and the docs cannot drift apart.
const (
	RefsOff    = "off"
	RefsSuffix = "suffix"
)

// The variables a section's own templates interpolate, layered over the
// release's. They are scoped to the line being rendered — a dependency line
// knows its provider, an entry line knows its commit — which is why they are
// not among the release's own DISPAT_* variables: there is no one value for
// them at the level of a release.
const (
	VarDepName = "DISPAT_DEP_NAME"
	VarDepFrom = "DISPAT_DEP_FROM"
	VarDepTo   = "DISPAT_DEP_TO"
	VarDepTag  = "DISPAT_DEP_TAG"

	VarCommit      = "DISPAT_COMMIT"
	VarCommitShort = "DISPAT_COMMIT_SHORT"
)

// shortSHALength is how much of a sha $DISPAT_COMMIT_SHORT carries: the width
// git itself abbreviates to by default, and the width a forge renders a commit
// reference at.
const shortSHALength = 7

// scoped layers per-line variables over a release's lookup. The layer wins,
// because it is the nearer scope, and a name it does not hold falls through to
// the release and then to the environment exactly as before.
func scoped(look Lookup, extra map[string]string) Lookup {
	return func(name string) (string, bool) {
		if v, ok := extra[name]; ok {
			return v, true
		}
		if look == nil {
			return "", false
		}
		return look(name)
	}
}

// sectionOrder is the order an entry renders its sections in: the configured
// one, or the built-ins when nothing is configured. The two answers differ in
// arrangement alone, because every built-in the configured list omitted is
// appended to it in the default relative order.
//
// The appending is done here as well as in the configuration's own resolution,
// which is where a list anyone can write is completed. A Format assembled in
// code goes through no resolution at all, and a built-in missing from the
// order is not a section left out of the record: it is released work that the
// record silently drops, with nothing in the file to say it happened.
func sectionOrder(f Format) []model.RecordSection {
	out := make([]model.RecordSection, 0, len(f.Sections)+len(model.DefaultSectionOrder()))
	out = append(out, f.Sections...)
	for _, key := range model.DefaultSectionOrder() {
		if builtinIndex(out, key) < 0 {
			out = append(out, model.RecordSection{Builtin: key})
		}
	}
	return out
}

// sectionTitle is the heading one section renders under: the format's own word
// for a built-in, so a section keeps its configured title wherever it is
// ordered, and the section's own for a custom one.
func sectionTitle(s model.RecordSection, f Format) string {
	switch s.Builtin {
	case model.SectionBreaking:
		return f.BreakingTitle
	case model.SectionFeatures:
		return f.FeaturesTitle
	case model.SectionFixes:
		return f.FixesTitle
	case model.SectionDependencies:
		return f.DependenciesTitle
	}
	return s.Title
}

// builtinIndex finds a built-in section in the order, -1 when the order does
// not hold it. Every order sectionOrder answers with holds all four, so a
// caller reading one never sees -1; the search itself is what sectionOrder
// completes an order with.
func builtinIndex(order []model.RecordSection, key string) int {
	for i, s := range order {
		if s.Builtin == key {
			return i
		}
	}
	return -1
}

// sectionClaims indexes an order by the commit types its custom sections
// claim, which is what sectionFor asks before falling back to the bump-keyed
// built-ins.
func sectionClaims(order []model.RecordSection) map[string]int {
	claims := make(map[string]int)
	for i, s := range order {
		for _, t := range s.Types {
			claims[t] = i
		}
	}
	return claims
}

// sectionFor decides which section a unit's line belongs to.
//
// Breaking wins outright: a change that breaks its consumers is the thing a
// reader scans an entry for, and letting `add(x)!: ...` render under a custom
// "Added" would hide it behind the word its author chose for ordinary work. A
// custom section's claim comes next, and what nothing claims falls to the
// bump-keyed built-in it always had.
func sectionFor(order []model.RecordSection, claims map[string]int, u *ccme.Unit) int {
	if u.Bump == ccme.BumpMajor {
		return builtinIndex(order, model.SectionBreaking)
	}
	if i, ok := claims[strings.ToLower(u.Header.Type)]; ok {
		return i
	}
	switch u.Bump {
	case ccme.BumpMinor:
		return builtinIndex(order, model.SectionFeatures)
	case ccme.BumpPatch:
		return builtinIndex(order, model.SectionFixes)
	}
	// A unit that bumps nothing renders nowhere, which is what it did before
	// sections were configurable. It cannot normally arrive here at all: a
	// BumpNone unit never enters a release's Units.
	return -1
}

// renderCtx is what every line of one entry shares: the format, the entry's
// lookup, and the base URL "auto" links hang off.
//
// The base is derived once per entry rather than once per line. It parses the
// configured API URL to decide whether the derivation applies at all, and the
// answer is the same for every line of the entry; a first release rendering a
// whole history's worth of lines would otherwise parse it thousands of times
// to reach the same string.
type renderCtx struct {
	f    Format
	look Lookup
	base string // the "auto" repository URL, empty when it cannot be derived
}

// renderSections groups the release's work and renders it, in the configured
// order. It is what RenderSections and RenderBody both go through; the lookup
// is the caller's, so one entry interpolates every template against the same
// variables.
func renderSections(rel *plan.Release, f Format, look Lookup) string {
	// A shared-versioning ride has no content to group: one line states that
	// the version moved and nothing else did, in the changelog and in the
	// GitHub release alike.
	if rel.NoChanges() {
		return noChangesLine(rel, f, look)
	}
	rc := renderCtx{f: f, look: look, base: autoBase(f)}
	order := sectionOrder(f)
	claims := sectionClaims(order)
	items := make([][]string, len(order))
	// NotesUnits, not Units: a prerelease's entry contains only its own
	// changeset, while a stable release (a graduation included) collects the
	// whole pending window since the last stable tag.
	for _, u := range rel.NotesUnits() {
		if i := sectionFor(order, claims, u); i >= 0 {
			items[i] = append(items[i], unitLine(rel, u, rc))
		}
	}
	if i := builtinIndex(order, model.SectionDependencies); i >= 0 {
		// One line per provider whose version this release picks up, carrying
		// the movement — a bare name would leave the reader to hunt the
		// provider's own changelog for what actually changed underneath. On a
		// catch-up the provider's version was already out, so the plan spans
		// the movement from what this package's previous release shipped
		// against (§13.10, providerUpdates), not from the provider's own
		// collapsed before-and-after.
		//
		// Updates rather than DueTo, so the section appears whenever a
		// provider moved rather than only when it propagated a bump. A
		// consumer released beside its provider with no caret between them
		// genuinely ships against the new version, and a changelog that says
		// nothing about it is the reader's problem later.
		for _, u := range rel.Updates {
			items[i] = append(items[i], dependencyLine(u, rc))
		}
	}
	var parts []string
	for i, s := range order {
		if len(items[i]) == 0 {
			continue
		}
		// Items are separated by a blank line and the section closes on a
		// single newline, whether or not its lines carry bodies. What an item
		// ends with is not the reader's business, and it used to be: a section
		// of bodiless bullets left a second blank line behind it and one whose
		// last bullet carried a body did not.
		//
		// Dependencies stay tight. Its lines are a table of movements, never
		// carry bodies, and have always read as one block; airing them out
		// would change every existing changelog's next entry for no reader's
		// benefit.
		sep := "\n\n"
		if s.Builtin == model.SectionDependencies {
			sep = "\n"
		}
		parts = append(parts, "### "+sectionTitle(s, f)+"\n\n"+strings.Join(items[i], sep)+"\n")
	}
	// Sections are never empty: a release can be admitted to the plan with
	// nothing to group (a pin, a channel transition, work its reverts cancel
	// out), and a record with an empty body reads as a broken write rather
	// than a deliberate one. The line names the release's actual cause.
	if len(parts) == 0 {
		return noChangesLine(rel, f, look)
	}
	return strings.Join(parts, "\n")
}

// unitLine renders one entry line: what changed, what it corrects, where the
// commit is, who wrote it, and the commit body indented underneath.
//
// The attribution follows the correction note and the reference: the note and
// the reference are part of what the line says about the work, and who did it
// comes after what was done.
func unitLine(rel *plan.Release, u *ccme.Unit, rc renderCtx) string {
	line := "- " + u.Header.Description + correctionNote(rel, u) +
		commitRefSuffix(rel, u, rc) + authorSuffix(rel, u, rc.f)
	if body := strings.TrimRight(u.Body, "\n"); body != "" {
		line += "\n" + indentBody(body)
	}
	return line
}

// indentBody indents a commit body two spaces, which is what makes it part of
// the bullet above it rather than a paragraph that happens to follow one.
//
// Flush-left, a body ends the list item in every markdown renderer there is:
// the paragraphs after the first leave the bullet entirely, and a body opening
// on something list-shaped starts a list of its own at the top level. Blank
// lines stay blank, because trailing spaces on an empty line are what a
// linter complains about next.
func indentBody(body string) string {
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			lines[i] = "  " + l
		}
	}
	return strings.Join(lines, "\n")
}

// commitRefSuffix is the reference to the commit behind an entry line: " (a1b2c3d)",
// or " ([a1b2c3d](url))" when the policy links it, and nothing at all when the
// placement does not ask for one.
//
// A unit the planner has no sha for is left unreferenced rather than given the
// window key that stands in for one: the key names an entry in this run and
// nothing a reader could open. Whoever configured references is told once per
// release, by the recorder, rather than per line.
func commitRefSuffix(rel *plan.Release, u *ccme.Unit, rc renderCtx) string {
	if rc.f.CommitRefsPlacement != RefsSuffix {
		return ""
	}
	sha := rel.UnitCommit(u)
	if sha == "" {
		return ""
	}
	look := scoped(rc.look, map[string]string{VarCommit: sha, VarCommitShort: shortSHA(sha)})
	text := Expand(rc.f.CommitRefsFormat, look)
	if text == "" {
		return ""
	}
	if u := linkURL(rc.f.CommitRefsLink, rc.base, look, "/commit/"+sha); u != "" {
		return " ([" + text + "](" + u + "))"
	}
	return " (" + text + ")"
}

// shortSHA abbreviates a commit id. A key shorter than the abbreviation is
// returned whole: it is not a sha, and truncating it further would only make
// it less recognisable.
func shortSHA(sha string) string {
	if len(sha) <= shortSHALength {
		return sha
	}
	return sha[:shortSHALength]
}

// dependencyLine renders one provider's movement, linked to the provider's own
// release when the policy asks for it and the link resolves.
func dependencyLine(u plan.ProviderUpdate, rc renderCtx) string {
	from, to := u.From.String(), u.To.String()
	name := u.Name
	// The per-line variables are layered only for a policy that interpolates
	// them. The default writes the plain line, and a consumer that picks up
	// fifty providers should not build a lookup layer per line to throw it
	// away unread.
	if linkable(rc.f.DependencyLink) && !taglessAuto(rc.f.DependencyLink, u.Tag) {
		look := scoped(rc.look, map[string]string{
			VarDepName: u.Name, VarDepFrom: from, VarDepTo: to, VarDepTag: u.Tag,
		})
		if target := linkURL(rc.f.DependencyLink, rc.base, look, "/releases/tag/"+u.Tag); target != "" {
			name = "[" + u.Name + "](" + target + ")"
		}
	}
	return "- " + name + ": " + from + " -> " + to
}

// taglessAuto reports an "auto" dependency link with no tag to hang itself off.
//
// An update the plan built from a step's environment states the movement and
// not the tag it was published under: `dispat changelog` aligned to a run
// knows core moved 1.3.2 -> 1.4.0 without knowing what core's own tagFormat
// called it. "auto" would append an empty tag and publish
// ".../releases/tag/", which is a listing page rather than the release, so the
// line renders plain — the same answer "auto" gives to coordinates it cannot
// derive a base from.
//
// A template is the operator's own and is expanded regardless: it may name the
// versions rather than the tag, and refusing to render it would decline a link
// that would have resolved.
func taglessAuto(value, tag string) bool {
	return value == model.LinkAuto && tag == ""
}

// linkURL resolves one configured link value against the entry's "auto" base:
// empty and "off" render plain, a template is interpolated, and "auto" is the
// base with autoPath appended. A template always wins over "auto", because a
// template is what an operator writes when the derivation is not what they
// want.
//
// "off" is the written form of empty, and it is checked before the template
// branch because that is where it would otherwise land: a package that says
// "off" over a space's "auto" means to switch the link off, and rendering the
// word as a URL template would publish "[core](off)". Empty cannot serve on
// its own, since an omitted key inherits the broader layer rather than
// clearing it.
//
// "auto" that cannot be derived answers empty, which every caller renders as
// the plain unlinked form. A record is published and permanent: a link that
// leads nowhere is worse than no link, and there is no later run in which it
// would come out right.
func linkURL(value, base string, look Lookup, autoPath string) string {
	switch value {
	case "", model.LinkOff:
		return ""
	case model.LinkAuto:
		if base == "" {
			return ""
		}
		return base + autoPath
	default:
		return Expand(value, look)
	}
}

// linkable reports whether a link value can produce a URL at all, so a caller
// can skip the work of preparing one it would never use.
func linkable(value string) bool {
	return value != "" && value != model.LinkOff
}

// autoBase is the repository URL "auto" hangs its paths off, empty when the
// package's coordinates cannot produce one.
//
// The derivation is github.com's, so a configured API URL pointing elsewhere
// declines rather than guesses: a GitHub Enterprise installation serves its
// web UI on a host the API URL does not state, and inventing one would publish
// a link into a repository that may not exist.
func autoBase(f Format) string {
	if f.LinkOwner == "" || f.LinkRepo == "" || !isPublicGitHub(f.LinkAPIURL) {
		return ""
	}
	return "https://github.com/" + f.LinkOwner + "/" + f.LinkRepo
}

// isPublicGitHub reports whether an API URL is github.com's own. Empty is the
// default endpoint, which is.
func isPublicGitHub(apiURL string) bool {
	if apiURL == "" {
		return true
	}
	u, err := url.Parse(apiURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "api.github.com" || host == "github.com"
}

// carriesNoChangesLine reports whether the entry's body will be the single
// line naming the release's cause rather than grouped sections. It answers by
// the same rules renderSections renders by, so the recorder speaks about the
// entry that is actually written.
//
// rel.NoChanges() alone would not do it. That names one of the causes, the
// shared-versioning ride; a pin, a channel transition and a window whose work
// its reverts cancel out reach the same line by grouping into nothing at all.
func carriesNoChangesLine(rel *plan.Release, f Format) bool {
	if rel.NoChanges() {
		return true
	}
	if len(rel.Updates) > 0 {
		return false // the dependencies section, which every order holds
	}
	order := sectionOrder(f)
	claims := sectionClaims(order)
	for _, u := range rel.NotesUnits() {
		if sectionFor(order, claims, u) >= 0 {
			return false
		}
	}
	return true
}

// LogRecordPolicy reports, once per release and per destination, what the
// entry's own configuration could not do — the questions a rendered record
// cannot answer, because what it shows is the fallback rather than the failure.
//
// It is the recorders' call rather than the renderer's so that the count is
// per release: a line-by-line notice about a policy that is wrong for every
// line of the entry would say the same thing as many times as the release has
// work in it.
func LogRecordPolicy(log zerolog.Logger, rel *plan.Release, f Format) {
	f = f.withDefaults()
	pkg := rel.Pkg.Name
	if autoBase(f) == "" && (f.DependencyLink == model.LinkAuto || f.CommitRefsLink == model.LinkAuto) {
		log.Debug().Str("package", pkg).Str("owner", f.LinkOwner).Str("repo", f.LinkRepo).
			Str("apiUrl", f.LinkAPIURL).
			Msg("record links fall back to plain text: no github.com owner and repo to derive them from")
	}
	if f.CommitRefsPlacement == RefsSuffix {
		missing := 0
		for _, u := range rel.NotesUnits() {
			if rel.UnitCommit(u) == "" {
				missing++
			}
		}
		if missing > 0 {
			log.Warn().Str("code", plan.CodeCommitRefUnavailable).Str("package", pkg).
				Int("units", missing).
				Msg("commit refs are configured but some entry lines have no commit id, rendered without one")
		}
	}
	if f.NoChangesText != "" && carriesNoChangesLine(rel, f) {
		// What the entry carries, not what was configured: a sentence whose
		// every variable is unset expands to nothing, or to the space between
		// two of them, and the entry falls back to the built-in line. The
		// fallback is visible in the file only to a reader who knows what the
		// configuration said, so it is warned rather than left to be found.
		if strings.TrimSpace(Expand(f.NoChangesText, ReleaseLookup(rel))) == "" {
			log.Warn().Str("code", plan.CodeNoChangesTextEmpty).Str("package", pkg).
				Msg("no-changes text expands to nothing, the built-in line was written instead")
		} else {
			log.Debug().Str("package", pkg).Msg("no-changes text applied from configuration")
		}
	}
	if len(f.Sections) > 0 {
		log.Debug().Str("package", pkg).Int("sections", len(f.Sections)).
			Msg("entry sections rendered in the configured order")
	}
}
