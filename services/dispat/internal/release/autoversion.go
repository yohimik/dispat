package release

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"
	"github.com/yohimik/dispat/pkg/scanner"
	"github.com/yohimik/dispat/pkg/writer"

	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// Native auto-versioning: the version stage's manifest reconciliation done by
// dispat itself instead of a user script (§9.4, §12.4). The policy lives on
// the space (model.AutoVersion); the data is the same end-of-run workspace
// view the DISPAT_WORKSPACE_* variables render. Rewriting is format-
// preserving via pkg/writer, so a failure mid-stage plus revertOnFail leaves
// no half-edited manifest behind.

// WorkspaceNames maps every package's manifest identity onto its package
// name, from the root manifests alone — a package's identity is declared at
// its root, not in a vendored example three levels down. Also returns the
// cleaned package-folder index for declared-local-path matching. The mapping
// is scanner.NameIndex's, the same rule compute uses, so a name compute
// refuses as ambiguous (W220) is never quietly rewritten here either.
//
// It is exported because it is the one place the workspace's manifest identity
// is decided: `dispat autowriter` resolves a dependency name onto a package
// through this index too, and a second answer to "whose manifest name is this"
// is exactly the kind of drift the index exists to prevent.
func WorkspaceNames(ctx context.Context, sc scanner.Scanner, p *plan.Plan, log zerolog.Logger) (names, dirs map[string]string) {
	owners := make([]scanner.Owner, 0, len(p.Order))
	dirs = make(map[string]string, len(p.Order))
	for _, name := range p.Order {
		rel := p.Releases[name]
		if rel == nil {
			continue
		}
		dirs[filepath.Clean(rel.Pkg.Dir)] = name
		mans, err := sc.ScanRoot(ctx, rel.Pkg.Dir)
		if err != nil {
			log.Debug().Err(err).Str("package", name).Msg("auto-versioning: root manifest failed to parse")
		}
		owners = append(owners, scanner.Owner{Package: name, Names: rel.Pkg.ManifestNames, Manifests: mans})
	}
	names, ambiguous := scanner.NameIndex(owners)
	for _, name := range ambiguous {
		log.Warn().Str("code", plan.CodeAmbiguousManifestName).Str("name", name).
			Msg("two packages declare the same manifest name; auto-versioning derives nothing from it")
	}
	return names, dirs
}

// autoVersion is the version stage's native reconciliation: the parsing
// strategy first, then the replacing one, so a package using both sees its
// manifests reconciled before the literal replacements run over the rest of
// its files. Runs inside the version stage frame, after beforeVersion and
// before any flow.version script, and its failure fails the stage. With
// neither strategy configured it does nothing at all, which is how a space
// asks for syncLock alone.
func (tc *taskCtx) autoVersion(ctx context.Context, av *model.AutoVersion) error {
	if err := tc.reconcileManifests(ctx, av); err != nil {
		return err
	}
	return tc.reconcileReplace(ctx, av)
}

// reconcileManifests natively rewrites the package's manifests: every declared
// workspace dependency passing the space's policy filters gets its range
// reconciled to the provider's end-of-run version, and the package's own
// version field is updated (§12.4).
func (tc *taskCtx) reconcileManifests(ctx context.Context, av *model.AutoVersion) error {
	if av.Manifests == model.ScopeNone {
		return nil // the parsing strategy is off; replace rules do the work
	}
	var (
		mans []scanner.Manifest
		err  error
	)
	if av.Manifests == model.ScopeAll {
		mans, err = tc.scan.Scan(ctx, tc.rel.Pkg.Dir)
	} else {
		mans, err = tc.scan.ScanRoot(ctx, tc.rel.Pkg.Dir)
	}
	if err != nil {
		// An interrupted scan is an interruption, not a partial parse: the
		// shutdown contract says stop issuing work.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		// A partial scan is reported but not fatal: the parsed manifests are
		// still reconciled — matching how compute treats the same situation.
		tc.log.Warn().Err(err).Msg("auto-versioning: some manifests failed to parse")
	}

	for _, m := range mans {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr // interrupted mid-stage: no more rewrites
		}
		if !writer.Supported(m.Path) {
			continue // read-only ecosystem (Cargo, Python, ...): flow.version's job
		}
		edits := tc.manifestEdits(av, m)
		version := ""
		// The own-version write applies to the package's *root* manifests
		// alone: a nested manifest (an example, a fixture) has its own
		// version story, and stamping the release version into it would be
		// wrong however the space scans.
		if av.WriteVersion && m.Root {
			version = tc.rel.Next.String()
			if m.Version != "" && m.Version != tc.rel.Previous().String() && m.Version != version {
				// §12.4: tags are authoritative; a manifest version disagreeing
				// with the baseline is drift worth telling the operator about —
				// the computed version is written over it either way.
				tc.log.Warn().Str("code", plan.CodeManifestVersionDrift).
					Str("manifest", m.Path).
					Str("manifestVersion", m.Version).
					Str("baseline", tc.rel.Previous().String()).
					Msg("manifest version disagrees with the baseline; writing the computed version")
			}
		}
		if len(edits) == 0 && version == "" {
			continue
		}
		path := filepath.Join(tc.rel.Pkg.Dir, filepath.FromSlash(m.Path))
		res, err := writer.Rewrite(path, version, edits)
		if err != nil {
			return err
		}
		for _, miss := range res.Missing {
			// Unreachable while edits derive from the same scan that found the
			// declaration; a race with a concurrent editor is worth a warning.
			tc.log.Warn().Str("manifest", m.Path).Str("dependency", miss.Name).
				Msg("auto-versioning: declaration disappeared between scan and rewrite")
		}
		if len(res.Applied) > 0 || res.VersionWritten {
			tc.markManifestsChanged()
			tc.log.Info().Str("manifest", m.Path).
				Int("ranges", len(res.Applied)).
				Bool("versionWritten", res.VersionWritten).
				Msg("manifest reconciled")
		}
	}
	return nil
}

// markManifestsChanged records — under mu — that this package's version stage
// modified a manifest, which is what its syncLock task keys off.
func (tc *taskCtx) markManifestsChanged() {
	tc.mu.Lock()
	tc.avChanged[tc.t.pkg] = true
	tc.mu.Unlock()
}

// manifestEdits derives one manifest's range rewrites under the policy,
// emitting W197/W203/W221 as it goes.
func (tc *taskCtx) manifestEdits(av *model.AutoVersion, m scanner.Manifest) []writer.Edit {
	scheduled := make(map[string]bool, len(tc.plan.Providers[tc.t.pkg]))
	for _, prov := range tc.plan.Providers[tc.t.pkg] {
		scheduled[prov] = true
	}
	var edits []writer.Edit
	for _, d := range m.Deps {
		provider := tc.avNames[d.Name]
		if provider == "" && d.LocalPath != "" {
			provider = scanner.ResolveLocalDir(tc.avDirs, tc.rel.Pkg.Dir, m.Path, d.LocalPath)
		}
		if provider == "" && av.NameSubstring {
			// The substring fallback: a declared name whose last segment is a
			// package's folder name ("@core/app" -> package "app") matches
			// even when that package's manifests declare no name at all.
			if leaf := lastNameSegment(d.Name); tc.plan.Releases[leaf] != nil {
				provider = leaf
			}
		}
		if provider == "" || provider == tc.t.pkg {
			continue // not a workspace dependency
		}
		if !av.Kinds[model.DepKind(d.Kind)] {
			continue
		}
		if av.Only != nil && !av.Only[provider] {
			continue
		}
		if !matchAny(av.Match, d.Range) {
			continue // a hand-pinned range the policy protects
		}
		version, prerelease, releasing := tc.providerVersion(provider)
		if av.OnlyUpdated && !releasing {
			// The caller asked for the run's own updates only: a range that had
			// fallen behind a provider released earlier stays as it is, and the
			// catch-up below never has to explain itself.
			continue
		}
		next := RangeText(av.Range, version, m.Ecosystem)
		if next == d.Range {
			continue
		}
		if releasing && !scheduled[provider] {
			// The manifest says "depends on", the config's `dependencies` list
			// does not: nothing orders this package after the provider or
			// skips it when the provider fails, so the version written here is
			// optimistic about a publish still in flight (§9.4).
			tc.log.Warn().Str("code", plan.CodeUnscheduledRewriteEdge).
				Str("manifest", m.Path).
				Str("provider", provider).
				Msg("manifest dependency has no configured edge; run `dispat compute` to declare it")
		}
		if !releasing {
			// §9.4: the provider is not in this run — its release happened
			// earlier and this manifest had fallen behind. Reconciled anyway;
			// non-obvious enough to say out loud.
			tc.log.Warn().Str("code", plan.CodeRangeCatchUp).
				Str("manifest", m.Path).
				Str("provider", provider).
				Str("range", d.Range+" -> "+next).
				Msg("range caught up to a provider released outside this run")
		}
		if prerelease && !tc.rel.IsPrerelease() {
			tc.log.Warn().Str("code", plan.CodeStableOverPrerelease).
				Str("manifest", m.Path).
				Str("provider", provider).
				Str("providerVersion", version).
				Msg("stable release now ranges over a prerelease provider")
		}
		edits = append(edits, writer.Edit{Name: d.Name, Kind: d.Kind, Range: next})
	}
	return edits
}

// providerVersion is the version the provider carries at the end of the run,
// as the manifest should declare it: the planned version when the provider is
// releasing and has not failed, its baseline otherwise. prerelease reports
// whether that version is a prerelease — the W203 ingredient.
//
// "Has not failed" is deliberately optimistic: a provider whose publish has
// not run yet is treated as releasing, because with a configured edge the
// scheduler guarantees this stage runs after the provider's build (and, when
// its space needs it, after its publish), and a later provider failure skips
// this package's own publish anyway. Without a configured edge neither
// guarantee exists — which is exactly what W221 flags.
func (tc *taskCtx) providerVersion(name string) (version string, prerelease, releasing bool) {
	pr := tc.plan.Releases[name]
	tc.mu.Lock()
	res, ok := tc.results[name]
	dead := ok && (res.Status == StatusFailed || res.Status == StatusSkipped)
	tc.mu.Unlock()
	if pr.Releasing() && !dead {
		return pr.Next.String(), pr.IsPrerelease(), true
	}
	if pr.HasBaseline {
		return pr.Baseline.String(), len(pr.Baseline.Prerelease) > 0, false
	}
	return pr.Current.String(), len(pr.Current.Prerelease) > 0, false
}

// AutoVersioner runs the version stage's native manifest reconciliation
// outside a release run, one package at a time and safely from several
// goroutines at once. The workspace indexes it needs are built once, when it is
// created, because deriving them per package would rescan every package's
// manifests for every package reconciled.
//
// With no run in progress there are no per-package results, so every releasing
// provider counts as live — the same semantics CommandEnv gives `dispat run`
// scripts. Reconciliation is naturally idempotent: rewriting already-reconciled
// manifests yields zero edits.
type AutoVersioner struct {
	run *run
	log zerolog.Logger
}

// NewAutoVersioner prepares the reconciliation for one plan. A nil scanner
// defaults to the filesystem scanner.
func NewAutoVersioner(ctx context.Context, p *plan.Plan, sc scanner.Scanner, log zerolog.Logger) *AutoVersioner {
	if sc == nil {
		sc = scanner.New()
	}
	r := &run{Executor: &Executor{Log: log}, plan: p, scan: sc, avChanged: make(map[string]bool)}
	r.avNames, r.avDirs = WorkspaceNames(ctx, sc, p, log)
	return &AutoVersioner{run: r, log: log}
}

// Package reconciles one package's manifests under the given policy. A nil
// policy means the package's own space block; a policy that answers nil skips
// the package, which is what a space with no autoVersion block does.
func (v *AutoVersioner) Package(ctx context.Context, rel *plan.Release, policy func(*plan.Release) *model.AutoVersion) error {
	pkg := rel.Pkg.Name
	av := rel.Pkg.Space.AutoVersion
	if policy != nil {
		av = policy(rel)
	}
	if av == nil {
		v.log.Debug().Str("package", pkg).Msg("space has no autoVersion block, nothing to reconcile")
		return nil
	}
	tc := &taskCtx{run: v.run, t: task{pkg, taskVersion}, rel: rel,
		log: v.log.With().Str("package", pkg).Str("stage", "version").Logger()}
	if err := tc.autoVersion(ctx, av); err != nil {
		return fmt.Errorf("%s: %w", pkg, err)
	}
	return nil
}

// Changed reports whether the package's manifests were actually modified,
// which is what a caller keys syncLock off, exactly as the executor does.
func (v *AutoVersioner) Changed(pkg string) bool {
	v.run.mu.Lock()
	defer v.run.mu.Unlock()
	return v.run.avChanged[pkg]
}

// lastNameSegment is a declared name's final /- or :-separated segment: the
// leaf of an npm scope, a Go module path or a Maven coordinate — the part
// conventionally matching the folder name.
func lastNameSegment(name string) string {
	if i := strings.LastIndexAny(name, "/:"); i >= 0 {
		return name[i+1:]
	}
	return name
}

// RangeText renders the policy against the provider's version. Ecosystems
// with their own version spelling override the keyword policies: go.mod
// declares exact canonical versions (vX.Y.Z), Python specifiers have no
// caret/tilde so the keywords all pin (==X.Y.Z), and a Docker tag is a plain
// label with no range syntax at all, so the keywords all write the bare
// version; a {version} template or a verbatim literal always passes through.
func RangeText(policy, version, ecosystem string) string {
	switch ecosystem {
	case scanner.EcosystemGoMod:
		return "v" + version
	case scanner.EcosystemPython:
		switch policy {
		case "", "caret", "tilde", "exact":
			return "==" + version
		}
	case scanner.EcosystemDocker:
		switch policy {
		case "", "caret", "tilde", "exact":
			// "^1.2.3" is not a tag any registry can resolve. A caret asked
			// for here means "track this provider", and the closest a tag can
			// come to that is naming the version outright.
			return version
		}
	}
	switch policy {
	case "", "caret":
		return "^" + version
	case "tilde":
		return "~" + version
	case "exact":
		return version
	default:
		if strings.Contains(policy, "{version}") {
			return strings.ReplaceAll(policy, "{version}", version)
		}
		return policy // a verbatim literal, e.g. "workspace:*"
	}
}

// matchAny reports whether the declared range matches one of the globs; an
// empty glob list means every range is eligible. Globs use the planner's
// scope semantics — "*" matches any run of bytes — because a version range is
// not a filesystem path: under filepath.Match, "*" would quietly refuse to
// cross the "/" in "file:../core" and behave differently per OS.
func matchAny(globs []string, rng string) bool {
	if len(globs) == 0 {
		return true
	}
	for _, g := range globs {
		if plan.GlobMatch(g, rng) {
			return true
		}
	}
	return false
}
