package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"

	"github.com/yohimik/dispat/pkg/scanner"
	"github.com/yohimik/dispat/pkg/writer"

	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// `dispat autoreplace` is `dispat writer` pointed at a selection instead of at
// a list of files: the same edits, applied to every package the plan picks,
// with the manifest paths resolved by scanning each package folder rather than
// typed out by hand. It plans first — so it knows which packages are on the
// table and what version each of them is heading for — and then sweeps them in
// dependency order like any other covering command.

// VersionPlaceholder is what an edit writes where the covered package's own
// planned version, or the named provider's, belongs. It is the same spelling
// the autoVersion.range policy uses, so one idea has one syntax.
const VersionPlaceholder = "{version}"

// AutoReplaceOptions is one `dispat autoreplace` invocation.
type AutoReplaceOptions struct {
	// Window is which packages the command covers: the release window or
	// --since, narrowed by the filter, expanded by --consumers.
	Window WindowOptions
	// OnError is the failure policy for a failed package's dependents.
	OnError string
	// Version, when set, rewrites each covered package's own version field. It
	// may be VersionPlaceholder, which writes that package's planned version.
	Version string
	// Edits set declared dependency ranges. A range may contain
	// VersionPlaceholder, which resolves to the planned version of the package
	// the edit names.
	Edits []writer.Edit
	// Replacements point dependencies at local folders, or remove the redirect
	// when their Path is empty.
	Replacements []writer.Replacement
	// Manifests is which of a package's manifests are edited: model.ScopeRoot
	// (the default when empty) or model.ScopeAll.
	Manifests string
	// OnlyUpdated drops every edit and replacement whose dependency does not
	// name a package this run releases.
	OnlyUpdated bool
	// SyncLock runs each covered space's syncLock scripts where a manifest
	// actually changed, so the lock files do not fall behind the ranges.
	SyncLock bool
	// Strict turns an edit that matched no manifest anywhere into a failed
	// command.
	Strict bool
	// JSON renders one event per manifest through the log instead of a listing.
	JSON bool
	// Out receives the listing.
	Out io.Writer
}

// scope answers with the manifest scope this invocation asked for. The two it
// accepts are the parsing strategy's own; model.ScopeNone is deliberately not
// one of them, since a command whose whole job is to write manifests has
// nothing to do with the scope that reads none.
func (o AutoReplaceOptions) scope() model.ManifestScope {
	if o.Manifests == "" {
		return model.ScopeRoot
	}
	return model.ManifestScope(o.Manifests)
}

// needsWorkspace reports whether this invocation has to know which manifest
// name belongs to which package. Building that index scans every package's root
// manifests, so it is worth asking before paying for it.
func (o AutoReplaceOptions) needsWorkspace() bool {
	if o.OnlyUpdated || strings.Contains(o.Version, VersionPlaceholder) {
		return true
	}
	for _, e := range o.Edits {
		if strings.Contains(e.Range, VersionPlaceholder) {
			return true
		}
	}
	return false
}

// AutoReplace applies the invocation's edits to every covered package's
// manifests. Every package is swept in dependency order, exactly as `dispat
// run` sweeps scripts, and each one's manifests are rewritten in place,
// format-preserving, through pkg/writer.
//
// Nothing here depends on a package releasing: rewriting the manifests of a
// package that has no pending change is a perfectly good thing to ask for,
// which is why --since all reaches the whole monorepo. What a package does need
// is a manifest something can write; a package with none is a no-op, and a
// selection where none of them has one is an error, because writing nothing
// silently is how a typo hides.
func (a *App) AutoReplace(ctx context.Context, opts AutoReplaceOptions) error {
	pl, err := a.stepPlan(ctx)
	if err != nil {
		return err
	}
	covered, err := a.coveredPackages(ctx, pl, opts.Window)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot rewrite the manifests")
		return err
	}

	work, err := a.newReplaceWork(ctx, pl, opts)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot rewrite the manifests")
		return err
	}
	if work.nothingToWrite() {
		// --only-updated dropped every edit: this run updates none of the
		// packages they name. A clean no-op, and the ordinary state of a CI job
		// wired to run after every commit.
		a.log.Info().Msg("no edit names a package this run updates, nothing to write")
		return nil
	}
	rep, drainErr := a.runSweep(ctx, pl, covered, work, sweepOptions{OnError: opts.OnError})

	tally := work.tally()
	if opts.JSON {
		a.log.Info().Int("packages", rep.Resolved).
			Int("applied", tally.applied).Int("skipped", tally.skipped).Int("missing", tally.missing).
			Msg("autoreplace complete")
	} else {
		fmt.Fprintf(listing(opts.Out), "%d package(s): %d applied, %d skipped, %d missing\n",
			rep.Resolved, tally.applied, tally.skipped, tally.missing)
	}
	if drainErr != nil {
		a.log.Warn().Err(drainErr).Msg("autoreplace interrupted")
		return drainErr
	}
	if len(covered) > 0 && rep.Resolved == 0 {
		err := fmt.Errorf("no covered package has a manifest this command can write (covered: %s)",
			strings.Join(covered, ", "))
		a.log.Error().Err(err).Msg("nothing to write")
		return err
	}
	if rep.Failed > 0 {
		return fmt.Errorf("%d package(s) failed to rewrite", rep.Failed)
	}
	// The strict gate asks whether an edit found its target, so it only has an
	// answer once something was opened. A selection the window emptied tried
	// nothing, and calling every edit stale for that would turn "nothing
	// changed today" into a failure.
	if opts.Strict && rep.Resolved > 0 {
		if err := work.stale(); err != nil {
			return err
		}
	}
	if !opts.SyncLock {
		return nil
	}
	return a.syncLock(ctx, pl, work.changedPackages())
}

// replaceWork is `dispat autoreplace`'s share of a sweep: one package's
// manifests scanned, then rewritten.
//
// Everything it needs to decide *what* to write is computed once, in
// newReplaceWork, because the answer is the same for every package: the edits
// are named on the command line and the plan does not change under it. Only the
// own-version write is per package, since that is the one value that differs.
// What it collects back — the counts, which edits landed somewhere, whose
// manifests changed — is guarded by mu, and is the whole of the shared mutable
// state a sweep of this work carries.
type replaceWork struct {
	app     *App
	pl      *plan.Plan
	scope   model.ManifestScope
	dirs    map[string]string // cleaned package folder -> package name
	edits   manifestEdit      // resolved, minus the per-package own version
	version string            // the --set-version text, before placeholders

	mu        sync.Mutex
	counts    writeTally
	landed    map[string]bool // edit key -> matched at least one manifest
	changed   map[string]bool // package -> a manifest of its actually changed
	requested []string        // edit keys, in the order they were given

	json bool
	out  io.Writer
}

// nothingToWrite reports an invocation left with no edit at all, which is what
// --only-updated does on a run that updates none of the packages the edits
// name.
func (w *replaceWork) nothingToWrite() bool { return w.version == "" && w.edits.empty() }

// writeTally is the three outcomes summed over a whole sweep.
type writeTally struct{ applied, skipped, missing int }

// newReplaceWork resolves the invocation into the work a sweep can run: the
// workspace index when one is needed, the placeholders expanded, and the edits
// the --only-updated filter kept. Everything that can fail the whole command
// rather than one package fails here, before a single file is opened.
func (a *App) newReplaceWork(ctx context.Context, pl *plan.Plan, opts AutoReplaceOptions) (*replaceWork, error) {
	if s := opts.scope(); s != model.ScopeRoot && s != model.ScopeAll {
		return nil, fmt.Errorf("unknown manifest scope %q (want %q or %q)", s, model.ScopeRoot, model.ScopeAll)
	}
	// The workspace index and the folder index come from one scan of every
	// package's root manifests. Under the "all" scope the folder index is what
	// keeps two packages off one file, so it is needed whether or not anything
	// asked for a placeholder.
	var names, dirs map[string]string
	if opts.needsWorkspace() || opts.scope() == model.ScopeAll {
		names, dirs = release.WorkspaceNames(ctx, a.scan, pl, a.log)
	}

	w := &replaceWork{
		app: a, pl: pl, scope: opts.scope(), dirs: dirs, version: opts.Version,
		landed: map[string]bool{}, changed: map[string]bool{},
		json: opts.JSON, out: opts.Out,
	}
	for _, e := range opts.Edits {
		if opts.OnlyUpdated && !updating(pl, names[e.Name]) {
			a.log.Debug().Str("dependency", e.Name).
				Msg("edit dropped: it does not name a package this run updates")
			continue
		}
		rng, err := w.expand(names, e.Name, e.Range)
		if err != nil {
			return nil, err
		}
		e.Range = rng
		w.edits.Edits = append(w.edits.Edits, e)
		w.requested = append(w.requested, editKey(e))
	}
	for _, r := range opts.Replacements {
		if opts.OnlyUpdated && !updating(pl, names[r.Name]) {
			a.log.Debug().Str("dependency", r.Name).
				Msg("replacement dropped: it does not name a package this run updates")
			continue
		}
		w.edits.Replacements = append(w.edits.Replacements, r)
		w.requested = append(w.requested, replaceKey(r))
	}
	return w, nil
}

// expand resolves the version placeholder inside one edit's range against the
// package the edit names. An edit whose name belongs to no package in the
// workspace cannot be templated, and saying so beats writing "{version}" into
// a manifest for someone to find later.
func (w *replaceWork) expand(names map[string]string, dep, text string) (string, error) {
	if !strings.Contains(text, VersionPlaceholder) {
		return text, nil
	}
	pkg := names[dep]
	rel := w.pl.Releases[pkg]
	if rel == nil {
		return "", fmt.Errorf("%s: %s names no package in this workspace, so %s cannot be resolved",
			dep, dep, VersionPlaceholder)
	}
	return strings.ReplaceAll(text, VersionPlaceholder, plannedVersion(rel)), nil
}

// plannedVersion is the version a package carries at the end of the run: the
// planned one when it is releasing, its current one otherwise. It is the same
// answer auto-versioning writes into a consumer's range.
func plannedVersion(rel *plan.Release) string {
	if rel.Releasing() {
		return rel.Next.String()
	}
	if rel.HasBaseline {
		return rel.Baseline.String()
	}
	return rel.Current.String()
}

// updating reports whether the named package is one this run releases. An empty
// name — a dependency that is no package of this workspace — never is.
func updating(pl *plan.Plan, pkg string) bool {
	rel := pl.Releases[pkg]
	return rel != nil && rel.Releasing()
}

func (w *replaceWork) stage() string { return "autoreplace" }

// resolve finds the package's writable manifests and hands back the write. A
// package with nothing writable is a no-op; a scan that cannot be read at all
// fails that package alone.
func (w *replaceWork) resolve(ctx context.Context, rel *plan.Release) (task, error) {
	mans, err := w.manifests(ctx, rel)
	if err != nil {
		return nil, err
	}
	if len(mans) == 0 {
		w.app.log.Debug().Str("package", rel.Pkg.Name).
			Msg("no manifest here that this command can write, skipping")
		return nil, nil
	}
	edits := w.edits
	if w.version != "" {
		edits.Version = strings.ReplaceAll(w.version, VersionPlaceholder, plannedVersion(rel))
	}
	return func(ctx context.Context) error { return w.write(ctx, rel, mans, edits) }, nil
}

// manifests is the package's share of the file system: the manifests in scope
// that something can actually write.
//
// Under the "all" scope a package folder containing another package's folder
// would otherwise reach that package's manifests as well, and both packages
// would then edit the same file from two goroutines. The nested ones are left
// to their owner, which is also the only reading under which "every manifest of
// this package" means what it says.
func (w *replaceWork) manifests(ctx context.Context, rel *plan.Release) ([]scanner.Manifest, error) {
	var (
		mans []scanner.Manifest
		err  error
	)
	if w.scope == model.ScopeAll {
		mans, err = w.app.scan.Scan(ctx, rel.Pkg.Dir)
	} else {
		mans, err = w.app.scan.ScanRoot(ctx, rel.Pkg.Dir)
	}
	if err != nil {
		// An interrupted scan is an interruption, not a partial parse.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		// A partial scan is reported but not fatal: the manifests that parsed
		// are still written, which is how every other reader here treats it.
		w.app.log.Warn().Err(err).Str("package", rel.Pkg.Name).
			Msg("some manifests failed to parse")
	}
	out := make([]scanner.Manifest, 0, len(mans))
	for _, m := range mans {
		if !writer.Supported(m.Path) {
			continue // a read-only ecosystem: nothing here can write it
		}
		if w.scope == model.ScopeAll && w.ownedElsewhere(rel.Pkg.Name, rel.Pkg.Dir, m.Path) {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// ownedElsewhere reports that the manifest belongs to a different package,
// whose own turn in the sweep will reach it.
func (w *replaceWork) ownedElsewhere(pkg, dir, manifest string) bool {
	folder := filepath.Dir(filepath.Join(dir, filepath.FromSlash(manifest)))
	for {
		if owner, ok := w.dirs[filepath.Clean(folder)]; ok {
			return owner != pkg
		}
		parent := filepath.Dir(folder)
		if parent == folder || len(parent) < len(filepath.Clean(dir)) {
			return false
		}
		folder = parent
	}
}

// write applies the edits to one package's manifests and records what they did.
func (w *replaceWork) write(ctx context.Context, rel *plan.Release, mans []scanner.Manifest, edits manifestEdit) error {
	var errs []error
	for _, m := range mans {
		if err := ctx.Err(); err != nil {
			return err
		}
		one := edits
		if !m.Root {
			// The own-version write applies to the package's root manifests
			// alone: a nested example has its own version story, and stamping
			// the release version into it would be wrong however the sweep
			// scans. Same rule the version stage follows.
			one.Version = ""
		}
		if one.empty() {
			continue
		}
		path := filepath.Join(rel.Pkg.Dir, filepath.FromSlash(m.Path))
		res, replRes, err := one.apply(path)
		if err != nil {
			w.app.log.Error().Err(err).Str("package", rel.Pkg.Name).Str("manifest", m.Path).
				Msg("manifest edit failed")
			errs = append(errs, err)
			continue
		}
		w.record(rel.Pkg.Name, one, res, replRes)
		shown := w.relative(path)
		if w.json {
			logWrite(w.app.log.With().Str("package", rel.Pkg.Name).Logger(), shown, res, replRes)
			continue
		}
		printWrite(listing(w.out), shown, res, replRes)
	}
	// Each failure was already reported against the manifest it belongs to.
	return errors.Join(errs...)
}

// relative renders a manifest path the way the operator typed the repository:
// relative to the monorepo root, with slashes, so it can be pasted straight
// into `dispat writer`.
func (w *replaceWork) relative(path string) string {
	rel, err := filepath.Rel(w.app.root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// record folds one manifest's outcomes into the run-wide tally.
//
// "Landed" is answered by elimination rather than by the applied list, because
// the writer reports nothing at all for an edit the manifest already spells
// exactly as asked — and a second, converged run must not then call that edit
// stale. So every edit this manifest was asked for landed here unless the
// manifest came back saying it declares no such thing.
func (w *replaceWork) record(pkg string, tried manifestEdit, res writer.Result, replRes writer.ReplaceResult) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.counts.applied += len(res.Applied) + len(replRes.Applied)
	w.counts.skipped += len(res.Skipped) + len(replRes.Skipped)
	w.counts.missing += len(res.Missing) + len(replRes.Missing)
	if res.VersionWritten || len(res.Applied) > 0 || len(replRes.Applied) > 0 {
		w.changed[pkg] = true
	}

	missing := make(map[string]bool, len(res.Missing)+len(replRes.Missing))
	for _, e := range res.Missing {
		missing[editKey(e)] = true
	}
	for _, r := range replRes.Missing {
		missing[replaceKey(r)] = true
	}
	for _, e := range tried.Edits {
		if key := editKey(e); !missing[key] {
			w.landed[key] = true
		}
	}
	for _, r := range tried.Replacements {
		if key := replaceKey(r); !missing[key] {
			w.landed[key] = true
		}
	}
}

func (w *replaceWork) tally() writeTally {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.counts
}

// changedPackages lists, in plan order, the packages whose manifests this run
// actually changed — what a syncLock pass keys off, exactly as a release does.
func (w *replaceWork) changedPackages() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []string
	for _, name := range w.pl.Order {
		if w.changed[name] {
			out = append(out, name)
		}
	}
	return out
}

// stale is the --strict gate, and it asks the question across the whole sweep
// rather than per manifest: an edit missing from *this* package's manifest is
// the ordinary case when one invocation covers twenty of them, while an edit no
// manifest anywhere declares is a pattern that has gone stale. It is the same
// rule `dispat replacer` applies to its substitutions.
func (w *replaceWork) stale() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	var stale int
	for _, key := range w.requested {
		if !w.landed[key] {
			w.app.log.Error().Str("edit", key).Msg("edit matched no manifest")
			stale++
		}
	}
	if stale == 0 {
		return nil
	}
	err := fmt.Errorf("%d edit(s) matched no manifest", stale)
	w.app.log.Error().Err(err).Msg("edits are not clean")
	return err
}

// editKey and replaceKey identify one requested edit across the manifests it
// was tried against, so "did this ever land" has an answer that does not depend
// on which file answered it.
func editKey(e writer.Edit) string           { return "set:" + e.Kind.String() + ":" + e.Name }
func replaceKey(r writer.Replacement) string { return "replace:" + r.Name }
