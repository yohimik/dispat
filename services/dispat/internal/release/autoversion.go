package release

import (
	"context"
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

// workspaceNames maps every package's manifest identity onto its package
// name, from the root manifests alone — a package's identity is declared at
// its root, not in a vendored example three levels down. Also returns the
// cleaned package-folder index for declared-local-path matching. First in
// plan order wins a name collision (compute reports the ambiguity as W220;
// the executor stays deterministic about it).
func workspaceNames(p *plan.Plan, log zerolog.Logger) (names, dirs map[string]string) {
	names = make(map[string]string)
	dirs = make(map[string]string, len(p.Order))
	for _, name := range p.Order {
		rel := p.Releases[name]
		dirs[filepath.Clean(rel.Pkg.Dir)] = name
		mans, err := scanner.ScanRoot(rel.Pkg.Dir)
		if err != nil {
			log.Debug().Err(err).Str("package", name).Msg("auto-versioning: root manifest failed to parse")
		}
		for _, m := range mans {
			if m.Name == "" {
				continue
			}
			if _, taken := names[m.Name]; !taken {
				names[m.Name] = name
			}
		}
	}
	return names, dirs
}

// autoVersion natively rewrites the package's manifests: every declared
// workspace dependency passing the space's policy filters gets its range
// reconciled to the provider's end-of-run version, and the package's own
// version field is updated (§12.4). Runs inside the version stage frame,
// after beforeVersion and before any flow.version script, and its failure
// fails the stage.
func (tc *taskCtx) autoVersion(ctx context.Context) error {
	av := tc.rel.Pkg.Space.AutoVersion
	var (
		mans []scanner.Manifest
		err  error
	)
	if av.AllManifests {
		mans, err = tc.scan.Scan(ctx, tc.rel.Pkg.Dir)
	} else {
		mans, err = scanner.ScanRoot(tc.rel.Pkg.Dir)
	}
	if err != nil {
		// A partial scan is reported but not fatal: the parsed manifests are
		// still reconciled — matching how compute treats the same situation.
		tc.log.Warn().Err(err).Msg("auto-versioning: some manifests failed to parse")
	}

	for _, m := range mans {
		if !writer.Supported(m.Path) {
			continue // read-only ecosystem (Cargo, Python, ...): flow.version's job for now
		}
		edits := tc.manifestEdits(av, m)
		version := ""
		if av.WriteVersion {
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
			tc.log.Info().Str("manifest", m.Path).
				Int("ranges", len(res.Applied)).
				Bool("versionWritten", res.VersionWritten).
				Msg("manifest reconciled")
		}
	}
	return nil
}

// manifestEdits derives one manifest's range rewrites under the policy,
// emitting W197/W203 as it goes.
func (tc *taskCtx) manifestEdits(av *model.AutoVersion, m scanner.Manifest) []writer.Edit {
	var edits []writer.Edit
	for _, d := range m.Deps {
		provider := tc.avNames[d.Name]
		if provider == "" && d.LocalPath != "" {
			provider = resolveLocalDir(tc.avDirs, tc.rel.Pkg.Dir, m.Path, d.LocalPath)
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
		next := rangeText(av.Range, version, m.Ecosystem)
		if next == d.Range {
			continue
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
		edits = append(edits, writer.Edit{Name: d.Name, Kind: string(d.Kind), Range: next})
	}
	return edits
}

// providerVersion is the version the provider carries at the end of the run,
// as the manifest should declare it: the planned version when the provider is
// releasing and has not failed, its baseline otherwise. prerelease reports
// whether that version is a prerelease — the W203 ingredient.
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

// lastNameSegment is a declared name's final /- or :-separated segment: the
// leaf of an npm scope, a Go module path or a Maven coordinate — the part
// conventionally matching the folder name.
func lastNameSegment(name string) string {
	if i := strings.LastIndexAny(name, "/:"); i >= 0 {
		return name[i+1:]
	}
	return name
}

// rangeText renders the policy against the provider's version. Ecosystems
// with their own version spelling override the keyword policies: go.mod
// declares exact canonical versions (vX.Y.Z), Python specifiers have no
// caret/tilde so the keywords all pin (==X.Y.Z); a {version} template or a
// verbatim literal always passes through.
func rangeText(policy, version, ecosystem string) string {
	switch ecosystem {
	case scanner.EcosystemGoMod:
		return "v" + version
	case scanner.EcosystemPython:
		switch policy {
		case "", "caret", "tilde", "exact":
			return "==" + version
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
// empty glob list means every range is eligible.
func matchAny(globs []string, rng string) bool {
	if len(globs) == 0 {
		return true
	}
	for _, g := range globs {
		if ok, _ := filepath.Match(g, rng); ok {
			return true
		}
	}
	return false
}

// resolveLocalDir maps a declared local path (file:, replace, path =) onto
// the package whose folder it points into, ascending from the exact target so
// a path into a sub-folder still finds it.
func resolveLocalDir(dirs map[string]string, pkgDir, manifestRel, local string) string {
	dir := filepath.Clean(filepath.Join(pkgDir, filepath.Dir(filepath.FromSlash(manifestRel)), filepath.FromSlash(local)))
	for {
		if name, ok := dirs[dir]; ok {
			return name
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
