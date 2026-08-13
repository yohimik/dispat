package release

// The release stage's use of the replacing strategy (§9.4): the rules come
// from the space's autoVersion.replace, the facts from the run in flight, and
// the work itself from Substituter, which `dispat autoreplacer` drives too.
//
// Where the parsing strategy learns a package's providers from its manifests,
// this one takes them from the configured `dependencies` graph. With no
// manifest to read there is nothing else to learn them from, and the graph
// edge is in any case what orders this package after the provider, so a rule
// can never be optimistic about a publish nothing waits for.

import (
	"context"

	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// reconcileReplace runs the replacing strategy over the package's files.
func (tc *taskCtx) reconcileReplace(ctx context.Context, av *model.AutoVersion) error {
	if len(av.Replace) == 0 {
		return nil
	}
	// The provider facts are gathered once: every rule mentioning a provider
	// is rendered against the same view of the run, and providerVersion takes
	// the results lock each time it is asked.
	rep, err := Substituter{
		Dir:   tc.rel.Pkg.Dir,
		Rules: av.Replace,
		Facts: PackageFacts{
			Name:       tc.t.pkg,
			Version:    tc.rel.Next.String(),
			Previous:   tc.rel.Previous().String(),
			Prerelease: tc.rel.IsPrerelease(),
		},
		Providers: tc.providerFacts(av),
		Log:       tc.log,
	}.Run(ctx)
	if err != nil {
		return err
	}
	if rep.Changed {
		tc.markManifestsChanged()
	}
	return nil
}

// providerFacts gathers this package's configured providers, in graph order
// and narrowed by the policy's `only` list. The versions are the same ones the
// parsing strategy writes, so a provider that failed falls back to its
// baseline here exactly as it does there.
func (tc *taskCtx) providerFacts(av *model.AutoVersion) []ProviderFacts {
	names := tc.plan.Providers[tc.t.pkg]
	out := make([]ProviderFacts, 0, len(names))
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
		out = append(out, ProviderFacts{
			Name:       name,
			Version:    version,
			Previous:   pr.Previous().String(),
			Releasing:  releasing,
			Prerelease: prerelease,
		})
	}
	return out
}
