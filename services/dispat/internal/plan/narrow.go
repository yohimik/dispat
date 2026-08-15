package plan

// Narrowing a plan: releasing part of the graph rather than all of it.
//
// A plan is computed for the whole repository — that is what makes the
// versions in it correct — and only then narrowed to what one invocation asked
// for. Doing it in that order is what keeps `dispat release --package core` and
// a whole-monorepo release from ever disagreeing about core's next version.
//
// What a selection may not do is break the publish order. Providers are
// published before their consumers because that is the one staleness case no
// rule over tags can detect (§19.2, §13.7b): a consumer released first would
// carry a version whose notes credit provider updates that do not exist yet,
// and would owe a catch-up release the moment the provider went out. So a
// selected package whose provider is releasing in the same plan and is *not*
// selected does not release either. It is withheld, reported, and released by
// the next run, which is the run where its provider is already out.
//
// A versioning group is the softer case, and the difference is deliberate.
// Releasing part of a group leaves its shared version briefly untrue, but
// nothing is published out of order and the next run rides the members left
// behind up to the group's version (W234) with no operator involved. So a split
// group releases and says so, where a broken order stays put.

// Withheld is one selected package the release order cannot reach in this run.
type Withheld struct {
	// Pkg is the package the selection asked for and did not get.
	Pkg string
	// Waiting names the releasing providers it must follow, in the plan's
	// dependency order — the packages to add to the selection (or to release
	// first) to make it go.
	Waiting []string
}

// SplitGroup is one versioning group a selection releases only part of.
type SplitGroup struct {
	// Name is the group key: a declared versionGroups name, or the space's own
	// name for a space that joined none.
	Name string
	// Releasing and LeftBehind partition the group's releasing members, both in
	// dependency order. Both are non-empty by construction — a group entirely
	// in or entirely out of the selection is not split.
	Releasing, LeftBehind []string
}

// Narrowing is what one call to Narrow did and what the caller has to report.
type Narrowing struct {
	// Release lists the packages that still release, in dependency order.
	Release []string
	// Withheld and Split are the findings: packages the order held back, and
	// versioning groups the selection splits.
	Withheld []Withheld
	Split    []SplitGroup
}

// Clean reports a narrowing that cost nothing: every selected package
// releases and no versioning group is split. It is what --strict gates on.
func (n Narrowing) Clean() bool { return len(n.Withheld) == 0 && len(n.Split) == 0 }

// Narrow restricts the plan to the named packages: every other releasing
// package is marked Deselected, which is all it takes for the executor, the
// environment listings, auto-versioning, the finalize phase and the summary to
// leave it alone. Names that are not releasing (unchanged, held, or not
// packages of this plan at all) are ignored — the selection is a filter, and a
// filter never adds anything to a plan.
//
// The findings are returned rather than logged so that one caller decides what
// they mean: a warning and a smaller release, or a refusal.
func (p *Plan) Narrow(selected []string) Narrowing {
	want := make(map[string]bool, len(selected))
	for _, name := range selected {
		want[name] = true
	}

	// The releasing set as the plan computed it, before anything is
	// deselected. Deciding against a live Releasing() would be a bug: this
	// walk turns providers off as it goes, and a provider already turned off
	// would then read as "not releasing at all" and stop blocking the very
	// consumer it must be published before.
	planned := make(map[string]bool, len(p.Order))
	order := make([]string, 0, len(p.Order))
	for _, name := range p.Order {
		if rel := p.Releases[name]; rel != nil && rel.Releasing() {
			planned[name] = true
			order = append(order, name)
		}
	}

	// One pass, in dependency order. Every provider is decided before the
	// consumer that reads its answer, so transitivity falls out of the order
	// and needs neither a second pass nor a fixpoint: a package held back
	// holds back everything downstream of it in the same walk.
	n := Narrowing{}
	keep := make(map[string]bool, len(order))
	for _, name := range order {
		if !want[name] {
			continue
		}
		waiting := p.waitingOn(name, planned, keep)
		if len(waiting) == 0 {
			keep[name] = true
			continue
		}
		n.Withheld = append(n.Withheld, Withheld{Pkg: name, Waiting: waiting})
		p.Releases[name].WaitingFor = waiting
	}

	for _, name := range order {
		if keep[name] {
			n.Release = append(n.Release, name)
			continue
		}
		p.Releases[name].Deselected = true
	}
	n.Split = splitGroups(p, order, keep)
	return n
}

// waitingOn lists the providers of one package that this run is not releasing
// although the plan planned to — the reason it cannot go now. Duplicates are
// dropped: one pair declared under two dependency kinds is two edges and one
// provider.
func (p *Plan) waitingOn(name string, planned, keep map[string]bool) []string {
	var waiting []string
	seen := make(map[string]bool)
	for _, prov := range p.Providers[name] {
		if !planned[prov] || keep[prov] || seen[prov] {
			continue
		}
		seen[prov] = true
		waiting = append(waiting, prov)
	}
	return waiting
}

// splitGroups finds the versioning groups the selection releases only part of.
// It runs over the *final* release set, so a member the order withheld splits
// its group exactly as an unselected one does.
func splitGroups(p *Plan, order []string, keep map[string]bool) []SplitGroup {
	byName := make(map[string]*SplitGroup)
	var names []string // first-appearance order, so the report is deterministic
	for _, pkg := range order {
		group := p.Releases[pkg].Pkg.VersionGroupName()
		if group == "" {
			continue
		}
		sg, ok := byName[group]
		if !ok {
			sg = &SplitGroup{Name: group}
			byName[group] = sg
			names = append(names, group)
		}
		if keep[pkg] {
			sg.Releasing = append(sg.Releasing, pkg)
		} else {
			sg.LeftBehind = append(sg.LeftBehind, pkg)
		}
	}
	var out []SplitGroup
	for _, group := range names {
		if sg := byName[group]; len(sg.Releasing) > 0 && len(sg.LeftBehind) > 0 {
			out = append(out, *sg)
		}
	}
	return out
}
