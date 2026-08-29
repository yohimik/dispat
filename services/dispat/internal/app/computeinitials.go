package app

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/filter"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// The baseline half of compute: the version a package's manifests declare,
// turned into the `initials` entry the first dispat release bumps from.
//
// A repository adopting dispat already carries its versions somewhere, and
// that somewhere is the manifests. Without an entry the planner starts such a
// package at 0.0.0 and releases 0.0.1, so the whole history the manifest
// knows about is lost. The entry is only ever proposed where it would
// actually be read, which the planner decides by the tags (§12.3): a package
// with a parseable stable release tag takes its baseline from that tag and
// ignores initials entirely.

// tagConcurrency bounds the tag queries, the way the planner bounds its own:
// they are independent per-package git reads, and a monorepo with hundreds of
// packages must not fork hundreds of gits at once.
const tagConcurrency = 16

// initialSuggestion is one proposed initials entry.
type initialSuggestion struct {
	pkg     string
	version ccme.Version
	// detail is the evidence: the manifest that declared the version, and why
	// the package needs a baseline written down.
	detail string
}

// render is the suggestion's listing line, in the column the edge lines use.
func (s initialSuggestion) render() string {
	return fmt.Sprintf("+ initial %s %s  %s", s.pkg, s.version, s.detail)
}

// manifestBaseline is a package's own version as its manifests declare it.
type manifestBaseline struct {
	pkg      *model.Package
	version  ccme.Version
	manifest string // the declaring manifest, relative to the repository root
}

// suggestInitials proposes an entry for every selected package whose version
// only its manifests know. It also reports whether the baselines were
// computed at all: telling a package that never released from one released
// long ago takes the release tags, so without a git repository the whole half
// steps aside rather than proposing a baseline for everything in sight.
func (a *App) suggestInitials(ctx context.Context, scanned []scannedPackage, sel filter.Result) ([]initialSuggestion, bool) {
	candidates := a.manifestBaselines(scanned, sel)
	if len(candidates) == 0 {
		// Nothing to propose, so nothing to check: a workspace whose
		// manifests carry no version costs no git at all.
		return nil, true
	}
	if err := a.checkGit(); err != nil {
		a.log.Warn().Err(err).
			Msg("skipping version baselines: compute reads release tags to tell a first release from a released package")
		return nil, false
	}
	return a.unreleased(ctx, candidates), true
}

// manifestBaselines is every selected package's declared version, minus the
// packages the configuration already has an answer for.
func (a *App) manifestBaselines(scanned []scannedPackage, sel filter.Result) []manifestBaseline {
	// An entry already in the config is the operator's own statement, and the
	// way to silence this suggestion for good: 0.0.0 included, it is never
	// rewritten. Matching is case-insensitive, the way App.initialVersions
	// matches them: a key keeps the case its file wrote.
	decided := make(map[string]bool, len(a.cfg.Initials))
	for key := range a.cfg.Initials {
		decided[strings.ToLower(key)] = true
	}
	var out []manifestBaseline
	for _, s := range scanned {
		if sel.Active() && !sel.Has(s.pkg.Name) {
			continue
		}
		if decided[strings.ToLower(s.pkg.Name)] {
			continue
		}
		if found, ok := a.pickManifestVersion(s); ok {
			out = append(out, found)
		}
	}
	return out
}

// pickManifestVersion is the version a package's manifests agree it is at.
// Root manifests are asked first and nested ones only when no root manifest
// declares a version, which is the rank scanner.NameIndex applies to names:
// a package's own identity beats a vendored or example manifest deeper
// inside it.
//
// Three declared versions cannot seed a baseline and are passed over. One
// that is not semver has no meaning here. A prerelease states a version being
// worked toward rather than one released, so the baseline stays where it is.
// And 0.0.0 is already what a package with no entry starts from.
func (a *App) pickManifestVersion(s scannedPackage) (manifestBaseline, bool) {
	for _, root := range []bool{true, false} {
		var (
			found manifestBaseline
			seen  []string
		)
		for _, m := range s.mans {
			if m.Root != root || m.Version == "" {
				continue
			}
			v, err := ccme.ParseVersion(m.Version)
			if err != nil {
				a.log.Debug().Str("package", s.pkg.Name).Str("manifest", m.Path).
					Str("version", m.Version).
					Msg("declared version is not a semantic version; no baseline derived from it")
				continue
			}
			if !slices.Contains(seen, v.String()) {
				seen = append(seen, v.String())
			}
			if found.manifest == "" {
				found = manifestBaseline{
					pkg:      s.pkg,
					version:  v,
					manifest: relPath(a.root, filepath.Join(s.pkg.Dir, filepath.FromSlash(m.Path))),
				}
			}
		}
		switch {
		case len(seen) == 0:
			continue // this rank says nothing; ask the next one
		case len(seen) > 1:
			a.log.Warn().Str("code", plan.CodeAmbiguousManifestVersion).
				Str("package", s.pkg.Name).Strs("versions", seen).
				Msg("manifests declare different versions for one package; no baseline derived from them")
			return manifestBaseline{}, false
		case found.version.IsPrerelease():
			a.log.Debug().Str("package", s.pkg.Name).Str("version", found.version.String()).
				Msg("declared version is a prerelease; no baseline derived from it")
			return manifestBaseline{}, false
		case found.version.Major == 0 && found.version.Minor == 0 && found.version.Patch == 0:
			return manifestBaseline{}, false // 0.0.0 is the default baseline
		default:
			return found, true
		}
	}
	return manifestBaseline{}, false
}

// unreleased keeps the candidates the planner would actually read an entry
// for: the packages whose tags give no parseable stable baseline, either
// because there is no stable tag at all or because the newest one cannot be
// read as a version.
func (a *App) unreleased(ctx context.Context, candidates []manifestBaseline) []initialSuggestion {
	reasons, errs := a.baselineReasons(ctx, candidates)

	var out []initialSuggestion
	for i, c := range candidates {
		if errs[i] != nil {
			a.log.Warn().Err(errs[i]).Str("package", c.pkg.Name).
				Msg("cannot read the release tags; no baseline suggested")
			continue
		}
		if reasons[i] == "" {
			continue // released and readable: the tag is the baseline
		}
		out = append(out, initialSuggestion{
			pkg:     c.pkg.Name,
			version: c.version,
			detail:  fmt.Sprintf("%s declares %s; %s", c.manifest, c.version, reasons[i]),
		})
	}
	slices.SortFunc(out, func(a, b initialSuggestion) int { return strings.Compare(a.pkg, b.pkg) })
	return out
}

// baselineReasons says, per candidate, why the package still needs an entry
// written down, or "" when its tags already answer.
//
// The tag queries are independent per-package git reads, so they run
// concurrently and are read back strictly in candidate order. A package whose
// tags cannot be read loses its suggestion and nothing else: the dependency
// half of the command has no business failing over a git error.
func (a *App) baselineReasons(ctx context.Context, candidates []manifestBaseline) ([]string, []error) {
	reasons := make([]string, len(candidates))
	errs := make([]error, len(candidates))

	if _, err := a.git.HeadSHA(ctx); err != nil {
		// A repository with no commits yet, which is where adopting dispat
		// often starts. Nothing can be reachable from a HEAD that does not
		// exist, so every candidate is a first release and no tag query is
		// worth making.
		a.log.Debug().Err(err).Msg("no commit to read release tags from; every package is a first release")
		for i := range reasons {
			reasons[i] = "the repository has no commits yet"
		}
		return reasons, errs
	}

	sem := make(chan struct{}, tagConcurrency)
	var wg sync.WaitGroup
	for i, c := range candidates {
		wg.Add(1)
		go func(i int, c manifestBaseline) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			tags, err := a.git.Tags(ctx, c.pkg.Name, plan.TagFormatFor(c.pkg))
			if err != nil {
				errs[i] = err
				return
			}
			stable, ok := tags.StableBaseline()
			switch {
			case !ok:
				reasons[i] = "no release tag yet"
			case !stable.Parsed:
				reasons[i] = fmt.Sprintf("newest tag %s is not a version", stable.Name)
			}
		}(i, c)
	}
	wg.Wait()
	return reasons, errs
}

// collectInitialEdits adds the accepted baselines to the config's initials
// map, leaving every entry already there exactly as it is.
//
// The current map is re-read from the file rather than taken from the loaded
// config, so the entries already there are written back exactly as the file
// holds them, comments and spelling included.
func (a *App) collectInitialEdits(edits *fileEdits, cfgPath string, apply []initialSuggestion) error {
	if len(apply) == 0 {
		return nil
	}
	next, err := config.StringMapAt(cfgPath, []string{"initials"})
	if err != nil {
		return err
	}
	if next == nil {
		next = make(map[string]string, len(apply))
	}
	if a.cfg.Initials == nil {
		a.cfg.Initials = make(map[string]string, len(apply))
	}
	if a.cfg.InitialVersions == nil {
		a.cfg.InitialVersions = make(map[string]ccme.Version, len(apply))
	}
	for _, s := range apply {
		next[s.pkg] = s.version.String()
		// The in-memory view keys these the way a load would have, so a future
		// long-lived caller reads back what a reload would give it: under the
		// package's own name, which is the key the edit just wrote.
		a.cfg.Initials[s.pkg] = s.version.String()
		a.cfg.InitialVersions[s.pkg] = s.version
	}
	return edits.add(cfgPath, config.Edit{KeyPath: []string{"initials"}, Value: next})
}
