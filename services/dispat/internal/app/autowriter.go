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

// `dispat autowriter` is `dispat writer` pointed at a selection instead of at
// a list of files: the same edits, applied to every package the plan picks,
// with the manifest paths resolved by scanning each package folder rather than
// typed out by hand. It plans first — so it knows which packages are on the
// table and what version each of them is heading for — and then sweeps them in
// dependency order like any other covering command.

// VersionPlaceholder is what an edit writes where the covered package's own
// planned version, or the named provider's, belongs. It is the same spelling
// the autoVersion.range policy uses, so one idea has one syntax.
const VersionPlaceholder = "{version}"

// AutoWriterOptions is one `dispat autowriter` invocation.
type AutoWriterOptions struct {
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
	// Links point dependencies at local folders, or remove the redirect
	// when their Path is empty.
	Links []writer.Link
	// SetLocal reconciles every declared workspace dependency to its
	// provider's end-of-run version, so the ranges need not be typed out.
	SetLocal bool
	// Range spells what SetLocal writes: the autoVersion.range vocabulary
	// (caret, tilde, exact, or a {version} template), rendered by
	// release.RangeText. Empty means a caret, which is what that renderer
	// means by an empty policy, so --range reads the same here as it does on
	// `dispat autoversion` and there is no second default to keep in step.
	Range string
	// LinkLocal points every declared workspace dependency at the provider's
	// folder; UnlinkLocal removes those same directives. They are mutually
	// exclusive.
	LinkLocal   bool
	UnlinkLocal bool
	// Manifests is which of a package's manifests are edited: model.ScopeRoot
	// (the default when empty) or model.ScopeAll.
	Manifests string
	// OnlyUpdated drops every edit and link, named or derived, whose
	// dependency does not name a package this run releases.
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
func (o AutoWriterOptions) scope() model.ManifestScope {
	if o.Manifests == "" {
		return model.ScopeRoot
	}
	return model.ManifestScope(o.Manifests)
}

// needsWorkspace reports whether this invocation has to know which manifest
// name belongs to which package. Building that index scans every package's root
// manifests, so it is worth asking before paying for it.
func (o AutoWriterOptions) needsWorkspace() bool {
	if o.OnlyUpdated || strings.Contains(o.Version, VersionPlaceholder) {
		return true
	}
	// The local flags are nothing *but* workspace resolution: every edit they
	// produce comes from asking which declarations name a package here.
	if o.SetLocal || o.LinkLocal || o.UnlinkLocal {
		return true
	}
	for _, e := range o.Edits {
		if strings.Contains(e.Range, VersionPlaceholder) {
			return true
		}
	}
	return false
}

// AutoWriter applies the invocation's edits to every covered package's
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
func (a *App) AutoWriter(ctx context.Context, opts AutoWriterOptions) error {
	pl, err := a.stepPlan(ctx)
	if err != nil {
		return err
	}
	covered, err := a.coveredPackages(ctx, pl, opts.Window)
	if err != nil {
		a.log.Error().Err(err).Msg("cannot rewrite the manifests")
		return err
	}

	work, err := a.newWriterWork(ctx, pl, opts)
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
			Msg("autowriter complete")
	} else {
		fmt.Fprintf(listing(opts.Out), "%d package(s): %d applied, %d skipped, %d missing\n",
			rep.Resolved, tally.applied, tally.skipped, tally.missing)
	}
	if drainErr != nil {
		a.log.Warn().Err(drainErr).Msg("autowriter interrupted")
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

// writerWork is `dispat autowriter`'s share of a sweep: one package's
// manifests scanned, then rewritten.
//
// Everything it needs to decide *what* to write is computed once, in
// newWriterWork, because the answer is the same for every package: the edits
// are named on the command line and the plan does not change under it. Only the
// own-version write is per package, since that is the one value that differs.
// What it collects back — the counts, which edits landed somewhere, whose
// manifests changed — is guarded by mu, and is the whole of the shared mutable
// state a sweep of this work carries.
type writerWork struct {
	app     *App
	pl      *plan.Plan
	scope   model.ManifestScope
	names   map[string]string // manifest name -> package name
	dirs    map[string]string // cleaned package folder -> package name
	edits   manifestEdit      // resolved, minus the per-package own version
	version string            // the --set-version text, before placeholders

	// The derived half: what --set-local, --link-local and --unlink-local ask
	// for. These cannot be resolved once because the answer is per manifest,
	// so they are carried here and read by derive during the sweep.
	setLocal     bool
	rangePolicy  string
	linkLocal    bool
	unlinkLocal  bool
	onlyUpdated  bool
	explicitSet  map[string]bool // dependency named by --set: it wins
	explicitLink map[string]bool // dependency named by --link: it wins

	mu        sync.Mutex
	counts    writeTally
	landed    map[string]bool // edit key -> matched at least one manifest
	changed   map[string]bool // package -> a manifest of its actually changed
	requested []string        // edit keys, in the order they were given
	npmWarned map[string]bool // package -> the npm link warning was said once

	json bool
	out  io.Writer
}

// nothingToWrite reports an invocation left with no edit at all, which is what
// --only-updated does on a run that updates none of the packages the edits
// name. An invocation deriving its edits always has something to try, because
// what it will write is not known until the manifests have been read.
func (w *writerWork) nothingToWrite() bool {
	return w.version == "" && w.edits.empty() && !w.derives()
}

// writeTally is the three outcomes summed over a whole sweep.
type writeTally struct{ applied, skipped, missing int }

// newWriterWork resolves the invocation into the work a sweep can run: the
// workspace index when one is needed, the placeholders expanded, and the edits
// the --only-updated filter kept. Everything that can fail the whole command
// rather than one package fails here, before a single file is opened.
func (a *App) newWriterWork(ctx context.Context, pl *plan.Plan, opts AutoWriterOptions) (*writerWork, error) {
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

	w := &writerWork{
		app: a, pl: pl, scope: opts.scope(), names: names, dirs: dirs, version: opts.Version,
		setLocal: opts.SetLocal, rangePolicy: opts.Range,
		linkLocal: opts.LinkLocal, unlinkLocal: opts.UnlinkLocal,
		onlyUpdated:  opts.OnlyUpdated,
		explicitSet:  map[string]bool{},
		explicitLink: map[string]bool{},
		landed:       map[string]bool{},
		changed:      map[string]bool{},
		npmWarned:    map[string]bool{},
		json:         opts.JSON, out: opts.Out,
	}
	for _, e := range opts.Edits {
		// Recorded before the --only-updated filter: a dependency the command
		// line named is one the operator has spoken for, so a derived edit must
		// not write it even when the filter drops the named one.
		w.explicitSet[e.Name] = true
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
	for _, l := range opts.Links {
		w.explicitLink[l.Name] = true
		if opts.OnlyUpdated && !updating(pl, names[l.Name]) {
			a.log.Debug().Str("dependency", l.Name).
				Msg("link dropped: it does not name a package this run updates")
			continue
		}
		w.edits.Links = append(w.edits.Links, l)
		w.requested = append(w.requested, linkKey(l))
	}
	if opts.LinkLocal {
		// Worth saying out loud rather than burying in the docs: nothing in the
		// release path removes these, so a link still in place at publish time
		// ships a manifest consumers cannot resolve.
		a.log.Warn().
			Msg("local links must be removed before publishing; run autowriter --unlink-local first")
	}
	return w, nil
}

// expand resolves the version placeholder inside one edit's range against the
// package the edit names. An edit whose name belongs to no package in the
// workspace cannot be templated, and saying so beats writing "{version}" into
// a manifest for someone to find later.
func (w *writerWork) expand(names map[string]string, dep, text string) (string, error) {
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

func (w *writerWork) stage() string { return "autowriter" }

// resolve finds the package's writable manifests and hands back the write. A
// package with nothing writable is a no-op; a scan that cannot be read at all
// fails that package alone.
func (w *writerWork) resolve(ctx context.Context, rel *plan.Release) (task, error) {
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
func (w *writerWork) manifests(ctx context.Context, rel *plan.Release) ([]scanner.Manifest, error) {
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
func (w *writerWork) ownedElsewhere(pkg, dir, manifest string) bool {
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
func (w *writerWork) write(ctx context.Context, rel *plan.Release, mans []scanner.Manifest, edits manifestEdit) error {
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
		if w.derives() {
			// Fresh slices, never an append onto the shared ones: every package
			// in the sweep holds the same edits value, and appending in place
			// would let two goroutines write one backing array.
			derivedEdits, derivedLinks := w.derive(rel, m)
			one.Edits = concat(one.Edits, derivedEdits)
			one.Links = concat(one.Links, derivedLinks)
		}
		if one.empty() {
			continue
		}
		path := filepath.Join(rel.Pkg.Dir, filepath.FromSlash(m.Path))
		res, linkRes, err := one.apply(path)
		if err != nil {
			w.app.log.Error().Err(err).Str("package", rel.Pkg.Name).Str("manifest", m.Path).
				Msg("manifest edit failed")
			errs = append(errs, err)
			continue
		}
		w.record(rel.Pkg.Name, one, res, linkRes)
		shown := w.relative(path)
		if w.json {
			logWrite(w.app.log.With().Str("package", rel.Pkg.Name).Logger(), shown, res, linkRes)
			continue
		}
		printWrite(listing(w.out), shown, res, linkRes)
	}
	// Each failure was already reported against the manifest it belongs to.
	return errors.Join(errs...)
}

// relative renders a manifest path the way the operator typed the repository:
// relative to the monorepo root, with slashes, so it can be pasted straight
// into `dispat writer`.
func (w *writerWork) relative(path string) string {
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
func (w *writerWork) record(pkg string, tried manifestEdit, res writer.Result, linkRes writer.LinkResult) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.counts.applied += len(res.Applied) + len(linkRes.Applied)
	w.counts.skipped += len(res.Skipped) + len(linkRes.Skipped)
	w.counts.missing += len(res.Missing) + len(linkRes.Missing)
	if res.VersionWritten || len(res.Applied) > 0 || len(linkRes.Applied) > 0 {
		w.changed[pkg] = true
	}

	missing := make(map[string]bool, len(res.Missing)+len(linkRes.Missing))
	for _, e := range res.Missing {
		missing[editKey(e)] = true
	}
	for _, r := range linkRes.Missing {
		missing[linkKey(r)] = true
	}
	for _, e := range tried.Edits {
		if key := editKey(e); !missing[key] {
			w.landed[key] = true
		}
	}
	for _, r := range tried.Links {
		if key := linkKey(r); !missing[key] {
			w.landed[key] = true
		}
	}
}

func (w *writerWork) tally() writeTally {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.counts
}

// changedPackages lists, in plan order, the packages whose manifests this run
// actually changed — what a syncLock pass keys off, exactly as a release does.
func (w *writerWork) changedPackages() []string {
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
// rule `dispat replacer` applies to its replacements.
func (w *writerWork) stale() error {
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

// editKey and linkKey identify one requested edit across the manifests it
// was tried against, so "did this ever land" has an answer that does not depend
// on which file answered it.
func editKey(e writer.Edit) string { return "set:" + e.Kind.String() + ":" + e.Name }
func linkKey(l writer.Link) string { return "link:" + l.Name }
