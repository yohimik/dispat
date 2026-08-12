package release

// The replacing strategy as a thing you can hold: one package folder, a set of
// rules, and the facts their placeholders render against.
//
// It was a method on the release executor's per-task context, which was fine
// while the release stage was the only caller. `dispat autosubstitute` is the
// second, and it has no task, no plan and no stage frame, so what the engine
// actually needs is passed in rather than reached for. The release path builds
// one of these from its task context; the command builds one from its flags.

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/pkg/scanner"
	"github.com/yohimik/dispat/pkg/writer"

	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// PackageFacts is the package being reconciled, as a rule's placeholders see
// it: {name}, {version} and {previous}.
type PackageFacts struct {
	Name       string
	Version    string // the end-of-run version
	Previous   string // the baseline it is moving from
	Prerelease bool   // the package itself is a prerelease, which mutes W203
}

// ProviderFacts is one provider as a rule's placeholders see it: {provider},
// {providerVersion} and {providerPrevious}.
type ProviderFacts struct {
	Name       string
	Version    string // the end-of-run version, planned or baseline
	Previous   string // the baseline it is moving from
	Releasing  bool
	Prerelease bool
}

// Substituter runs the replacing strategy over one package folder: literal
// text substitution across whatever files the rules point at, parsing nothing.
//
// It reaches the versions the manifest writers cannot, a coordinate a Gradle
// script builds by hand, a base image in a Helm chart, an example in a README,
// at the cost of doing exactly what it is told, which is why a rule states the
// text it looks for rather than deriving it.
type Substituter struct {
	// Dir is the package folder, and the root of the one walk.
	Dir string
	// Rules are the find/write pairs with their file globs.
	Rules []model.ReplaceRule
	// Facts render the package-scoped placeholders.
	Facts PackageFacts
	// Providers render the provider-scoped ones, fanning a rule that mentions
	// any of them out into one substitution per provider.
	Providers []ProviderFacts
	// Owned reports that a slash-relative path belongs to this package rather
	// than to one nested inside it. A nil Owned owns everything, which is what
	// a caller sweeping one package at a time wants.
	Owned func(rel string) bool
	// Log carries the per-file events and the rule diagnostics.
	Log zerolog.Logger
}

// SubstituteReport is what one Run did.
type SubstituteReport struct {
	// Changed reports that at least one file was rewritten, which is what a
	// caller keys a lock-file refresh or a commit off.
	Changed bool
	// Files is how many files were rewritten.
	Files int
	// Occurrences is how many pieces of text were rewritten across them.
	Occurrences int
	// Matched indexes into Rules: the rules whose text was found somewhere,
	// whether this run changed it or found it already written. A rule that
	// fanned out counts as matched when any one of its expansions did, since
	// the caller asked for a template rather than for every provider to carry
	// it.
	Matched map[int]bool
}

// Run renders the rules, walks the folder once and rewrites what matches.
//
// A rule expanding into nothing selects no files, so a package with no
// providers costs one map allocation rather than a walk. Reading errors that
// belong to the file rather than to the rule, a folder that cannot be entered
// or a binary blob a glob happened to reach, are reported and stepped over:
// failing a release over a PNG would be the worse trade.
func (s Substituter) Run(ctx context.Context) (SubstituteReport, error) {
	rep := SubstituteReport{Matched: map[int]bool{}}
	if len(s.Rules) == 0 {
		return rep, nil
	}
	expansions := s.expand()
	if len(expansions) == 0 {
		return rep, nil
	}

	// One walk of the package folder, matching every rule's globs as it goes,
	// so a package with six rules is still read once and a file matched by
	// four of them is still written once.
	files, err := selectFiles(ctx, s.Dir, globsOf(s.Rules, expansions), s.Owned, s.Log)
	if err != nil {
		return rep, err
	}

	// Keyed by substitution because that is what the writer reports back;
	// folded onto rule indexes for the caller, which thinks in rules.
	matched := map[writer.Substitution]bool{}
	for _, f := range files {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return rep, ctxErr // interrupted mid-stage: no more rewrites
		}
		subs := fileSubstitutions(expansions, f.rules)
		path := filepath.Join(s.Dir, filepath.FromSlash(f.path))
		res, err := writer.Substitute(path, subs)
		if err != nil {
			// A file too large to read or holding binary data is not a
			// failure of the rule: a glob reaching one is ordinary.
			if errors.Is(err, writer.ErrBinaryFile) || errors.Is(err, writer.ErrManifestTooLarge) {
				s.Log.Debug().Err(err).Str("file", f.path).Msg("file skipped")
				continue
			}
			return rep, err
		}
		for _, sub := range res.Applied {
			matched[sub] = true
		}
		for _, sub := range res.Skipped {
			matched[sub] = true
		}
		if len(res.Applied) > 0 {
			rep.Changed = true
			rep.Files++
			rep.Occurrences += res.Count
			s.Log.Info().Str("file", f.path).Int("occurrences", res.Count).
				Msg("file reconciled")
		}
	}
	for _, e := range expansions {
		if matched[e.sub] || matched[e.probe] {
			rep.Matched[e.rule] = true
		}
	}
	s.report(expansions, matched, selectingRules(files))
	return rep, nil
}

// expansion is one rule rendered against one set of facts: the substitution to
// make, and where it came from so a rule that reconciles nothing can be named.
type expansion struct {
	rule     int
	provider string // empty when the rule mentions no provider
	// probe changes nothing and asks whether the file already reads the way
	// the rule wants. It is what tells "already reconciled" apart from "never
	// matched": after a first run the text sub looks for is gone, and without
	// the probe every re-run would report the rule stale.
	sub   writer.Substitution
	probe writer.Substitution
}

// expand renders every rule against the package and its providers. A rule
// mentioning a provider placeholder is rendered once per provider, which is
// what lets one rule cover a package with four dependencies; a rule mentioning
// none is rendered once, for the package's own version.
func (s Substituter) expand() []expansion {
	var out []expansion
	for i, rule := range s.Rules {
		if !mentionsProvider(rule) {
			sub := writer.Substitution{
				Find:  renderRule(rule.Find, s.Facts, ProviderFacts{}),
				Write: renderRule(rule.Write, s.Facts, ProviderFacts{}),
			}
			out = append(out, expansion{rule: i, sub: sub, probe: probeFor(sub)})
			continue
		}
		for _, prov := range s.Providers {
			sub := writer.Substitution{
				Find:  renderRule(rule.Find, s.Facts, prov),
				Write: renderRule(rule.Write, s.Facts, prov),
			}
			out = append(out, expansion{rule: i, provider: prov.Name, sub: sub, probe: probeFor(sub)})
		}
	}
	return out
}

// report narrates what the rules did that the commit log cannot explain: a
// provider caught up from an earlier release (W197), a stable release now
// naming a prerelease provider (W203), and a rule that reached files but found
// its text in none of them (W222).
func (s Substituter) report(expansions []expansion, matched map[writer.Substitution]bool, selecting map[int]bool) {
	byName := make(map[string]ProviderFacts, len(s.Providers))
	for _, p := range s.Providers {
		byName[p.Name] = p
	}
	told := make(map[string]bool, len(s.Providers))
	for _, e := range expansions {
		if !matched[e.sub] && !matched[e.probe] {
			if !selecting[e.rule] {
				continue // the rule reached no file here; it is about other packages
			}
			s.Log.Warn().Str("code", plan.CodeReplaceRuleMatchedNothing).
				Str("find", e.sub.Find).
				Strs("files", s.Rules[e.rule].Files).
				Msg("replace rule matched nothing; check the pattern and the file globs")
			continue
		}
		// Said once per provider, however many rules named it.
		if e.provider == "" || told[e.provider] || !matched[e.sub] {
			continue
		}
		told[e.provider] = true
		prov := byName[e.provider]
		if !prov.Releasing {
			s.Log.Warn().Str("code", plan.CodeRangeCatchUp).
				Str("provider", prov.Name).
				Str("version", prov.Version).
				Msg("replace rule caught up to a provider released outside this run")
		}
		if prov.Prerelease && !s.Facts.Prerelease {
			s.Log.Warn().Str("code", plan.CodeStableOverPrerelease).
				Str("provider", prov.Name).
				Str("providerVersion", prov.Version).
				Msg("stable release now names a prerelease provider")
		}
	}
}

// providerTokens are the placeholders that make a rule per-provider.
var providerTokens = []string{"{provider}", "{providerVersion}", "{providerPrevious}"}

// mentionsProvider reports a rule that speaks about a provider rather than
// about the package being released.
func mentionsProvider(rule model.ReplaceRule) bool {
	for _, token := range providerTokens {
		if strings.Contains(rule.Find, token) || strings.Contains(rule.Write, token) {
			return true
		}
	}
	return false
}

// renderRule fills a rule's placeholders in. An unknown {token} is left
// standing, the same way a tag format treats one: it is far more likely to be
// text the file really contains than a placeholder that was meant to exist.
func renderRule(text string, pkg PackageFacts, prov ProviderFacts) string {
	return strings.NewReplacer(
		"{name}", pkg.Name,
		"{version}", pkg.Version,
		"{previous}", pkg.Previous,
		"{provider}", prov.Name,
		"{providerVersion}", prov.Version,
		"{providerPrevious}", prov.Previous,
	).Replace(text)
}

// probeFor is the no-op substitution that asks whether a rule's result is
// already in the file.
func probeFor(sub writer.Substitution) writer.Substitution {
	return writer.Substitution{Find: sub.Write, Write: sub.Write}
}

// selectedFile is one file a rule reached, with the rules that reached it.
type selectedFile struct {
	path  string // slash-relative to the package folder
	rules []int
}

// selectingRules is the rules that reached at least one file. A rule that
// reached none has nothing to say about this package: one space-wide rule over
// "README.md" is the ordinary way to keep every README that exists in step,
// and warning about each package that has none would drown the warning that
// matters.
func selectingRules(files []selectedFile) map[int]bool {
	out := make(map[int]bool, len(files))
	for _, f := range files {
		for _, i := range f.rules {
			out[i] = true
		}
	}
	return out
}

// globsOf is each rule's file globs, positioned so an expansion's rule index
// still indexes the result, and emptied for the rules that expanded into
// nothing: a provider-scoped rule in a package with no providers selects
// nothing, and reading its files to substitute nothing would be pure waste.
func globsOf(rules []model.ReplaceRule, expansions []expansion) [][]string {
	out := make([][]string, len(rules))
	for _, e := range expansions {
		out[e.rule] = rules[e.rule].Files
	}
	return out
}

// selectFiles walks the package folder once and returns the files at least one
// rule's globs select, in the walk's (lexical, so deterministic) order. The
// folders a workspace walk never enters are skipped here too: a rule must not
// reach into node_modules or a build output tree, where the version text it
// looks for belongs to somebody else's code.
func selectFiles(ctx context.Context, dir string, globs [][]string,
	owned func(string) bool, log zerolog.Logger) ([]selectedFile, error) {
	var out []selectedFile
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			// The package folder itself has to be readable; anything below it
			// is reported and stepped over, matching how a scan treats a
			// sub-tree it cannot enter. Failing the release over an
			// unreadable folder no rule was ever going to reach would be the
			// worse trade, and a rule that then reconciles nothing is exactly
			// what W222 is for.
			if path == dir {
				return err
			}
			log.Warn().Err(err).Str("path", path).Msg("folder skipped")
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path != dir && scanner.SkipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // a symlink, socket or device is not a file to rewrite
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if owned != nil && !owned(rel) {
			// A package nested inside this one owns its own files, and its own
			// turn will reach them. Without this the two would rewrite one
			// file from two goroutines.
			return nil
		}
		var matched []int
		for i, fileGlobs := range globs {
			// A rule with no globs selects nothing. Saying so here matters:
			// matchAny reads an empty list as "any", which is right for a
			// range policy and exactly wrong for a file selector.
			if len(fileGlobs) == 0 {
				continue
			}
			if matchAny(fileGlobs, rel) {
				matched = append(matched, i)
			}
		}
		if len(matched) > 0 {
			out = append(out, selectedFile{path: rel, rules: matched})
		}
		return nil
	})
	return out, err
}

// fileSubstitutions is what one file receives: every expansion of every rule
// selecting it, in rule order, each followed by its probe, and with duplicates
// dropped so a text two rules ask for the same way is counted once.
func fileSubstitutions(expansions []expansion, rules []int) []writer.Substitution {
	wanted := make(map[int]bool, len(rules))
	for _, i := range rules {
		wanted[i] = true
	}
	seen := make(map[writer.Substitution]bool, len(expansions))
	subs := make([]writer.Substitution, 0, len(expansions)*2)
	for _, e := range expansions {
		if !wanted[e.rule] {
			continue
		}
		for _, s := range []writer.Substitution{e.sub, e.probe} {
			if seen[s] {
				continue
			}
			seen[s] = true
			subs = append(subs, s)
		}
	}
	return subs
}
