package release

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

// The replacing strategy: literal text substitution over whatever files a rule
// points at, parsing nothing (§9.4). It reaches the versions the manifest
// writers cannot — a coordinate a Gradle script builds by hand, a base image
// in a Helm chart, an example in a README — at the cost of doing exactly what
// it is told, which is why a rule states the text it looks for rather than
// deriving it.
//
// Where the parsing strategy learns a package's providers from its manifests,
// this one takes them from the configured `dependencies` graph. With no
// manifest to read there is nothing else to learn them from, and the graph
// edge is in any case what orders this package after the provider, so a rule
// can never be optimistic about a publish nothing waits for.

// providerFacts is one provider as a rule's placeholders see it.
type providerFacts struct {
	name       string
	version    string // the end-of-run version, planned or baseline
	previous   string // the baseline it is moving from
	releasing  bool
	prerelease bool
}

// expansion is one rule rendered against one set of facts: the substitution to
// make, and where it came from so a rule that reconciles nothing can be named.
type expansion struct {
	rule     int
	provider string // empty when the rule mentions no provider
	sub      writer.Substitution
	// probe changes nothing and asks whether the file already reads the way
	// the rule wants. It is what tells "already reconciled" apart from "never
	// matched": after a first run the text sub looks for is gone, and without
	// the probe every re-run would report the rule stale.
	probe writer.Substitution
}

// reconcileReplace runs the replacing strategy over the package's files.
func (tc *taskCtx) reconcileReplace(ctx context.Context, av *model.AutoVersion) error {
	if len(av.Replace) == 0 {
		return nil
	}
	// The provider facts are gathered once: every rule mentioning a provider
	// is rendered against the same view of the run, and providerVersion takes
	// the results lock each time it is asked.
	provs := tc.providerFacts(av)
	expansions := tc.expandRules(av, provs)
	if len(expansions) == 0 {
		return nil
	}

	// One walk of the package folder, matching every rule's globs as it goes,
	// so a package with six rules is still read once and a file matched by
	// four of them is still written once. Only the rules that expanded into
	// something are matched: a provider-scoped rule in a package with no
	// providers selects nothing, and reading its files to substitute nothing
	// would be pure waste.
	files, err := selectFiles(ctx, tc.rel.Pkg.Dir, expandedRules(av, expansions), tc.log)
	if err != nil {
		return err
	}

	matched := make(map[writer.Substitution]bool, len(expansions))
	for _, f := range files {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr // interrupted mid-stage: no more rewrites
		}
		subs := fileSubstitutions(expansions, f.rules)
		path := filepath.Join(tc.rel.Pkg.Dir, filepath.FromSlash(f.path))
		res, err := writer.Substitute(path, subs)
		if err != nil {
			// A file too large to read or holding binary data is not a
			// failure of the rule: a glob reaching one is ordinary, and the
			// alternative is failing the release over a PNG.
			if errors.Is(err, writer.ErrBinaryFile) || errors.Is(err, writer.ErrManifestTooLarge) {
				tc.log.Debug().Err(err).Str("file", f.path).Msg("auto-versioning: file skipped")
				continue
			}
			return err
		}
		for _, s := range res.Applied {
			matched[s] = true
		}
		for _, s := range res.Skipped {
			matched[s] = true
		}
		if len(res.Applied) > 0 {
			tc.markManifestsChanged()
			tc.log.Info().Str("file", f.path).Int("occurrences", res.Count).
				Msg("file reconciled")
		}
	}
	tc.reportRuleOutcomes(av, expansions, provs, matched, selectingRules(files))
	return nil
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

// expandRules renders every rule against the package and its providers. A rule
// mentioning a provider placeholder is rendered once per configured provider,
// which is what lets one rule cover a package with four dependencies; a rule
// mentioning none is rendered once, for the package's own version.
func (tc *taskCtx) expandRules(av *model.AutoVersion, provs []providerFacts) []expansion {
	own := tc.rel.Next.String()
	previous := tc.rel.Previous().String()
	var out []expansion
	for i, rule := range av.Replace {
		if !mentionsProvider(rule) {
			sub := writer.Substitution{
				Find:  renderRule(rule.Find, tc.t.pkg, own, previous, providerFacts{}),
				Write: renderRule(rule.Write, tc.t.pkg, own, previous, providerFacts{}),
			}
			out = append(out, expansion{rule: i, sub: sub, probe: probeFor(sub)})
			continue
		}
		for _, prov := range provs {
			sub := writer.Substitution{
				Find:  renderRule(rule.Find, tc.t.pkg, own, previous, prov),
				Write: renderRule(rule.Write, tc.t.pkg, own, previous, prov),
			}
			out = append(out, expansion{rule: i, provider: prov.name, sub: sub, probe: probeFor(sub)})
		}
	}
	return out
}

// providerFacts gathers this package's configured providers, in graph order
// and narrowed by the policy's `only` list. The versions are the same ones the
// parsing strategy writes, so a provider that failed falls back to its
// baseline here exactly as it does there.
func (tc *taskCtx) providerFacts(av *model.AutoVersion) []providerFacts {
	names := tc.plan.Providers[tc.t.pkg]
	out := make([]providerFacts, 0, len(names))
	for _, name := range names {
		if av.Only != nil && !av.Only[name] {
			continue
		}
		pr := tc.plan.Releases[name]
		if pr == nil {
			continue // an edge to something outside the plan
		}
		version, prerelease, releasing := tc.providerVersion(name)
		if av.OnlyUpdated && !releasing {
			// The caller asked for the run's own updates only, so a rule
			// scoped to a provider released outside this run expands into
			// nothing and reconciles nothing.
			continue
		}
		out = append(out, providerFacts{
			name:       name,
			version:    version,
			previous:   pr.Previous().String(),
			releasing:  releasing,
			prerelease: prerelease,
		})
	}
	return out
}

// reportRuleOutcomes narrates what the rules did that the commit log cannot
// explain: a provider caught up from an earlier release (W197), a stable
// release now naming a prerelease provider (W203), and a rule that reached
// files but found its text in none of them (W222).
func (tc *taskCtx) reportRuleOutcomes(av *model.AutoVersion, expansions []expansion,
	provs []providerFacts, matched map[writer.Substitution]bool, selecting map[int]bool) {
	byName := make(map[string]providerFacts, len(provs))
	for _, p := range provs {
		byName[p.name] = p
	}
	told := make(map[string]bool, len(provs))
	for _, e := range expansions {
		if !matched[e.sub] && !matched[e.probe] {
			if !selecting[e.rule] {
				continue // the rule reached no file here; it is about other packages
			}
			tc.log.Warn().Str("code", plan.CodeReplaceRuleMatchedNothing).
				Str("find", e.sub.Find).
				Strs("files", av.Replace[e.rule].Files).
				Msg("replace rule matched nothing; check the pattern and the file globs")
			continue
		}
		// Said once per provider, however many rules named it.
		if e.provider == "" || told[e.provider] || !matched[e.sub] {
			continue
		}
		told[e.provider] = true
		prov := byName[e.provider]
		if !prov.releasing {
			tc.log.Warn().Str("code", plan.CodeRangeCatchUp).
				Str("provider", prov.name).
				Str("version", prov.version).
				Msg("replace rule caught up to a provider released outside this run")
		}
		if prov.prerelease && !tc.rel.IsPrerelease() {
			tc.log.Warn().Str("code", plan.CodeStableOverPrerelease).
				Str("provider", prov.name).
				Str("providerVersion", prov.version).
				Msg("stable release now names a prerelease provider")
		}
	}
}

// expandedRules is the rules that produced at least one expansion, keeping
// their positions so an expansion's rule index still indexes the result.
func expandedRules(av *model.AutoVersion, expansions []expansion) []model.ReplaceRule {
	out := make([]model.ReplaceRule, len(av.Replace))
	for _, e := range expansions {
		out[e.rule] = av.Replace[e.rule]
	}
	return out
}

// probeFor is the no-op substitution that asks whether a rule's result is
// already in the file.
func probeFor(sub writer.Substitution) writer.Substitution {
	return writer.Substitution{Find: sub.Write, Write: sub.Write}
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
func renderRule(text, pkg, version, previous string, prov providerFacts) string {
	return strings.NewReplacer(
		"{name}", pkg,
		"{version}", version,
		"{previous}", previous,
		"{provider}", prov.name,
		"{providerVersion}", prov.version,
		"{providerPrevious}", prov.previous,
	).Replace(text)
}

// selectedFile is one file a rule reached, with the rules that reached it.
type selectedFile struct {
	path  string // slash-relative to the package folder
	rules []int
}

// selectFiles walks the package folder once and returns the files at least one
// rule's globs select, in the walk's (lexical, so deterministic) order. The
// folders a workspace walk never enters are skipped here too: a rule must not
// reach into node_modules or a build output tree, where the version text it
// looks for belongs to somebody else's code.
func selectFiles(ctx context.Context, dir string, rules []model.ReplaceRule, log zerolog.Logger) ([]selectedFile, error) {
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
			log.Warn().Err(err).Str("path", path).Msg("auto-versioning: folder skipped")
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
		var matched []int
		for i, rule := range rules {
			// A rule with no globs selects nothing. Saying so here matters:
			// matchAny reads an empty list as "any", which is right for a
			// range policy and exactly wrong for a file selector.
			if len(rule.Files) == 0 {
				continue
			}
			if matchAny(rule.Files, rel) {
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
