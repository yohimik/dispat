package app

// The three manifest commands: `dispat scanner` reads what a folder declares,
// `dispat writer` edits it, and `dispat replacer` replaces literal text in any
// file at all. They are the pkg/scanner and pkg/writer libraries exposed
// directly, with no config file, no git history and no plan behind them, which
// is what makes them usable on any checkout and inside a CI step that only
// wants to look at (or fix) a file.
//
// All three render two ways from one code path: a listing on Out for a
// terminal, and one structured event per file through Log when the run asked
// for JSON, so the output joins the same event stream CI already ingests.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/pkg/scanner"
	"github.com/yohimik/dispat/pkg/writer"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// ScanOptions is one `dispat scanner` invocation.
type ScanOptions struct {
	// Root is the folder Dir resolves against.
	Root string
	// Dir is the folder to scan, relative to Root. Empty scans Root itself.
	Dir string
	// RootOnly reads only the manifests sitting directly in the folder
	// (scanner.ScanRoot) instead of walking the whole tree.
	RootOnly bool
	// Strict turns a manifest that failed to parse into a failed command.
	Strict bool
	// JSON renders one event per manifest through Log instead of a listing.
	JSON bool
	// Out receives the listing.
	Out io.Writer
	// Log reports the manifests that failed to parse, and carries the
	// per-manifest events in JSON mode.
	Log zerolog.Logger
	// Scanner reads the manifests; nil means the filesystem scanner.
	Scanner scanner.Scanner
	// Writer enumerates link directives for the verify gates; nil means the
	// filesystem writer.
	Writer writer.Writer
	// VerifyUnlinked fails the command when any scanned manifest still
	// carries a local-link directive: exactly what --link-local can inject,
	// nothing wider.
	VerifyUnlinked bool
	// VerifyLinked fails the command when no scanned manifest carries one,
	// which is how a pipeline proves its link step actually landed. It is
	// asked of the selection as a whole: a single manifest with no workspace
	// dependencies legitimately carries no directive.
	VerifyLinked bool
	// ForbidRanges fails the command for every declared dependency range
	// matching one of these patterns (literal text, * as a wildcard).
	ForbidRanges []string
	// RequireRanges fails the command for every pattern here that no
	// declared dependency range matches. Each pattern is asked on its own.
	RequireRanges []string
}

// WriteOptions is one `dispat writer` invocation.
type WriteOptions struct {
	// Root is the folder Paths resolve against.
	Root string
	// Paths are the manifest files to edit, relative to Root.
	Paths []string
	// Version, when set, rewrites each manifest's own version field.
	Version string
	// Build, when set, writes each manifest's build counter where its format
	// keeps one.
	Build string
	// Edits set declared dependency ranges.
	Edits []writer.Edit
	// Links point dependencies at local folders, or remove the redirect when
	// their Path is empty.
	Links []writer.Link
	// DropLinks removes every local-link directive each manifest carries,
	// without being told the dependencies' names.
	DropLinks bool
	// Strict turns an edit the manifest does not declare into a failed
	// command. Skipped edits never do: they are the normal state of a
	// healthy manifest.
	Strict bool
	// JSON renders one event per manifest through Log instead of a listing.
	JSON bool
	// Out receives the listing.
	Out io.Writer
	// Log carries the per-manifest events in JSON mode.
	Log zerolog.Logger
	// Writer applies the edits; nil means the filesystem writer.
	Writer writer.Writer
}

// ReplaceOptions is one `dispat replacer` invocation.
type ReplaceOptions struct {
	// Root is the folder Paths resolve against.
	Root string
	// Paths are the files to edit, relative to Root. Any file at all: the
	// replacer parses nothing, so nothing has to be a manifest.
	Paths []string
	// Replacements are the literal find/write pairs, applied to each file in order.
	Replacements []writer.Replacement
	// Strict turns a replacement that matched nothing anywhere into a failed
	// command, which is the CI gate for a pattern that has gone stale.
	Strict bool
	// JSON renders one event per file through Log instead of a listing.
	JSON bool
	// Out receives the listing.
	Out io.Writer
	// Log carries the per-file events in JSON mode.
	Log zerolog.Logger
	// Writer applies the replacements; nil means the filesystem writer.
	Writer writer.Writer
}

// depView is one dependency declaration as the JSON output spells it.
type depView struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Range     string `json:"range,omitempty"`
	LocalPath string `json:"localPath,omitempty"`
}

// editView is one requested edit as the JSON output spells it.
type editView struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Range string `json:"range"`
}

// replacementView is one requested replacement as the JSON output spells it.
type replacementView struct {
	Find  string `json:"find"`
	Write string `json:"write"`
}

// linkView is one requested link as the JSON output spells it.
type linkView struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Path    string `json:"path,omitempty"`
}

// outcomeView splits a result the three ways the writer package reports it.
type outcomeView[T any] struct {
	Applied []T `json:"applied,omitempty"`
	Skipped []T `json:"skipped,omitempty"`
	Missing []T `json:"missing,omitempty"`
}

// ScanManifests reads every manifest under the scanned folder and reports
// them. A manifest that fails to parse is reported and skipped, and the ones
// that parsed are still listed, which is pkg/scanner's own partial-result
// contract; with Strict set, those failures also fail the command. An
// unreadable folder always does.
//
// Like every other operation here it reports its own findings through Log, so
// the caller only has to map the returned error onto an exit code.
func ScanManifests(ctx context.Context, opts ScanOptions) error {
	dir, err := existingDir(opts.Root, opts.Dir)
	if err != nil {
		opts.Log.Error().Err(err).Msg("cannot scan the folder")
		return err
	}
	out := listing(opts.Out)
	sc := opts.Scanner
	if sc == nil {
		sc = scanner.New()
	}
	opts.Log.Debug().Str("dir", dir).Bool("rootOnly", opts.RootOnly).Msg("scanning")

	var mans []scanner.Manifest
	if opts.RootOnly {
		mans, err = sc.ScanRoot(ctx, dir)
	} else {
		mans, err = sc.Scan(ctx, dir)
	}
	// An interrupted scan is an interruption, not a parse failure.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	failed := unwrapJoined(err)
	for _, e := range failed {
		opts.Log.Warn().Err(e).Msg("manifest failed to parse")
	}

	deps := 0
	for _, m := range mans {
		deps += len(m.Deps)
		for _, entry := range m.Dropped {
			opts.Log.Debug().Str("manifest", m.Path).Str("entry", entry).
				Msg("declaration dropped")
		}
		if opts.JSON {
			logManifest(opts.Log, m)
			continue
		}
		opts.Log.Debug().Str("manifest", m.Path).Str("ecosystem", string(m.Ecosystem)).
			Int("dependencies", len(m.Deps)).Msg("manifest read")
		printManifest(out, m)
	}
	if opts.JSON {
		opts.Log.Info().Int("manifests", len(mans)).Int("dependencies", deps).
			Int("failed", len(failed)).Msg("scan complete")
	} else {
		fmt.Fprintf(out, "%d manifest(s), %d dependency declaration(s)\n", len(mans), deps)
	}

	gateErr := verifyScan(opts, dir, mans)
	if opts.Strict && len(failed) > 0 {
		err := fmt.Errorf("%d manifest(s) failed to parse", len(failed))
		opts.Log.Error().Err(err).Msg("scan is not clean")
		return errors.Join(err, gateErr)
	}
	return gateErr
}

// verifyScan runs the scanner command's gates over what the scan found. Each
// finding is its own error event carrying a diagnostic code, so a pipeline
// asserts on the code and a reader sees every offender rather than only the
// count that failed the run.
func verifyScan(opts ScanOptions, dir string, mans []scanner.Manifest) error {
	var errs []error
	if opts.VerifyUnlinked || opts.VerifyLinked {
		w := opts.Writer
		if w == nil {
			w = writer.New()
		}
		present := 0
		for _, m := range mans {
			if !writer.SupportsLink(m.Path) {
				continue
			}
			links, err := w.Links(filepath.Join(dir, filepath.FromSlash(m.Path)))
			if err != nil {
				opts.Log.Error().Err(err).Str("manifest", m.Path).Msg("cannot read the link directives")
				errs = append(errs, err)
				continue
			}
			present += len(links)
			if !opts.VerifyUnlinked {
				continue
			}
			for _, l := range links {
				opts.Log.Error().Str("code", plan.CodeLinkPresent).
					Str("manifest", m.Path).Str("dependency", l.Name).Str("path", l.Path).
					Msg("local link present")
			}
		}
		if opts.VerifyUnlinked && present > 0 {
			errs = append(errs, fmt.Errorf("%d local link(s) present", present))
		}
		if opts.VerifyLinked && present == 0 {
			opts.Log.Error().Str("code", plan.CodeLinkAbsent).Msg("no local link present")
			errs = append(errs, errors.New("no local link present"))
		}
	}
	for _, pattern := range opts.ForbidRanges {
		matched := 0
		for _, m := range mans {
			for _, d := range m.Deps {
				if !plan.GlobMatch(pattern, d.Range) {
					continue
				}
				matched++
				opts.Log.Error().Str("code", plan.CodeRangeForbidden).
					Str("manifest", m.Path).Str("dependency", d.Name).
					Str("range", d.Range).Str("pattern", pattern).
					Msg("forbidden range")
			}
		}
		if matched > 0 {
			errs = append(errs, fmt.Errorf("%d range(s) match %q", matched, pattern))
		}
	}
	for _, pattern := range opts.RequireRanges {
		found := false
		for _, m := range mans {
			for _, d := range m.Deps {
				if plan.GlobMatch(pattern, d.Range) {
					found = true
				}
			}
		}
		if !found {
			opts.Log.Error().Str("code", plan.CodeRangeMissing).Str("pattern", pattern).
				Msg("required range missing")
			errs = append(errs, fmt.Errorf("no declared range matches %q", pattern))
		}
	}
	return errors.Join(errs...)
}

// printManifest writes one manifest's listing entry: a title line, then one
// line per declared dependency, in columns sized to that manifest.
func printManifest(out io.Writer, m scanner.Manifest) {
	fmt.Fprintln(out, manifestTitle(m))
	kindWidth, nameWidth := 0, 0
	for _, d := range m.Deps {
		kindWidth = max(kindWidth, len(d.Kind.String()))
		nameWidth = max(nameWidth, len(d.Name))
	}
	for _, d := range m.Deps {
		fmt.Fprintf(out, "  %-*s  %-*s  %s", kindWidth, d.Kind.String(), nameWidth, d.Name, d.Range)
		if d.LocalPath != "" {
			fmt.Fprintf(out, "  -> %s", d.LocalPath)
		}
		fmt.Fprintln(out)
	}
}

// manifestTitle is a manifest's one-line identity: where it is, what it is,
// and what it calls itself.
func manifestTitle(m scanner.Manifest) string {
	title := m.Path + "  " + string(m.Ecosystem)
	switch {
	case m.Name != "" && m.Version != "":
		title += "  " + m.Name + "@" + m.Version
	case m.Name != "":
		title += "  " + m.Name
	case m.Version != "":
		title += "  " + m.Version
	}
	if m.BuildNumber != "" {
		title += "  build " + m.BuildNumber
	}
	return title
}

// logManifest emits one manifest as a structured event.
func logManifest(log zerolog.Logger, m scanner.Manifest) {
	deps := make([]depView, 0, len(m.Deps))
	for _, d := range m.Deps {
		deps = append(deps, depView{
			Kind: d.Kind.String(), Name: d.Name, Range: d.Range, LocalPath: d.LocalPath,
		})
	}
	ev := log.Info().
		Str("path", m.Path).
		Str("ecosystem", string(m.Ecosystem)).
		Bool("root", m.Root)
	if m.Name != "" {
		ev = ev.Str("name", m.Name)
	}
	if m.Version != "" {
		ev = ev.Str("version", m.Version)
	}
	if m.BuildNumber != "" {
		ev = ev.Str("buildNumber", m.BuildNumber)
	}
	if len(m.Dropped) > 0 {
		ev = ev.Strs("dropped", m.Dropped)
	}
	ev.Interface("deps", deps).Msg("manifest")
}

// WriteManifests applies the requested edits to each named manifest: the
// dependency ranges and the manifest's own version first, then the local-path
// redirects. Every manifest is attempted even after one fails, since each
// file's write is atomic and independent, and the failures are joined into the
// returned error so one run reports the whole picture.
func WriteManifests(ctx context.Context, opts WriteOptions) error {
	var (
		errs                      []error
		applied, skipped, missing int
	)
	out := listing(opts.Out)
	edit := manifestEdit{
		Version: opts.Version, Build: opts.Build, Edits: opts.Edits, Links: opts.Links,
		DropLinks: opts.DropLinks, Writer: opts.Writer,
	}
	for _, rel := range opts.Paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(opts.Root, filepath.FromSlash(rel))
		opts.Log.Debug().Str("manifest", rel).Msg("editing manifest")
		res, linkRes, err := edit.apply(path)
		if err != nil {
			opts.Log.Error().Err(err).Str("manifest", rel).Msg("manifest edit failed")
			errs = append(errs, err)
			continue
		}
		applied += len(res.Applied) + len(linkRes.Applied)
		skipped += len(res.Skipped) + len(linkRes.Skipped)
		missing += len(res.Missing) + len(linkRes.Missing)
		if opts.JSON {
			logWrite(opts.Log, rel, res, linkRes)
			continue
		}
		printWrite(out, rel, res, linkRes)
	}

	if opts.JSON {
		opts.Log.Info().Int("manifests", len(opts.Paths)).
			Int("applied", applied).Int("skipped", skipped).Int("missing", missing).
			Msg("write complete")
	} else {
		fmt.Fprintf(out, "%d manifest(s): %d applied, %d skipped, %d missing\n",
			len(opts.Paths), applied, skipped, missing)
	}

	if err := errors.Join(errs...); err != nil {
		// Each one was already reported against the manifest it belongs to.
		return err
	}
	if opts.Strict && missing > 0 {
		err := fmt.Errorf("%d edit(s) target a dependency the manifest does not declare", missing)
		opts.Log.Error().Err(err).Msg("edits are not clean")
		return err
	}
	return nil
}

// manifestEdit is one invocation's whole edit set, ready to be applied to a
// file. It is the unit `dispat writer` builds once from its flags and `dispat
// autowriter` builds once per covered package, so both spell "what a write
// does to a manifest" the same way.
type manifestEdit struct {
	// Version, when set, rewrites the manifest's own version field.
	Version string
	// Build, when set, writes the manifest's build counter.
	Build string
	// Edits set declared dependency ranges.
	Edits []writer.Edit
	// Links point dependencies at local folders, or remove the redirect
	// when their Path is empty.
	Links []writer.Link
	// DropLinks removes every local-link directive the manifest carries.
	DropLinks bool
	// Writer applies the edits; nil means the filesystem writer.
	Writer writer.Writer
}

// empty reports an edit set with nothing in it, which is what makes a manifest
// not worth opening at all.
func (e manifestEdit) empty() bool {
	return e.Version == "" && e.Build == "" && len(e.Edits) == 0 && len(e.Links) == 0 && !e.DropLinks
}

// apply writes one manifest's share of the invocation. The rewrite runs before
// the redirects so a range and the redirect for the same dependency end up in
// one consistent file, and each step is skipped entirely when nothing asked
// for it.
func (e manifestEdit) apply(path string) (writer.Result, writer.LinkResult, error) {
	var (
		res     writer.Result
		linkRes writer.LinkResult
		err     error
	)
	w := e.Writer
	if w == nil {
		w = writer.New()
	}
	if e.Version != "" || len(e.Edits) > 0 {
		if res, err = w.Rewrite(path, e.Version, e.Edits); err != nil {
			return res, linkRes, err
		}
	}
	if e.Build != "" {
		built, err := w.SetBuild(path, e.Build)
		if err != nil {
			return res, linkRes, err
		}
		res.BuildWritten = built.BuildWritten
	}
	if len(e.Links) > 0 {
		if linkRes, err = w.Relink(path, e.Links); err != nil {
			return res, linkRes, err
		}
	}
	if e.DropLinks {
		dropped, err := w.DropLinks(path)
		if err != nil {
			return res, linkRes, err
		}
		linkRes.Path = dropped.Path
		linkRes.Applied = append(linkRes.Applied, dropped.Applied...)
		linkRes.Missing = append(linkRes.Missing, dropped.Missing...)
		linkRes.Skipped = append(linkRes.Skipped, dropped.Skipped...)
	}
	return res, linkRes, nil
}

// ReplaceFiles applies the literal replacements to each named file. Every
// file is attempted even after one fails, since each file's write is atomic
// and independent, and the failures are joined into the returned error so one
// run reports the whole picture.
//
// Strict fails on a replacement that matched nothing in any of the files,
// rather than in each of them: a run over twenty files where the pattern only
// belongs in one is the ordinary case, and a pattern found nowhere at all is
// the stale one worth catching.
func ReplaceFiles(ctx context.Context, opts ReplaceOptions) error {
	var (
		errs                      []error
		applied, skipped, missing int
		occurrences               int
		found                     = make(map[writer.Replacement]bool, len(opts.Replacements))
	)
	out := listing(opts.Out)
	w := opts.Writer
	if w == nil {
		w = writer.New()
	}
	for _, rel := range opts.Paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(opts.Root, filepath.FromSlash(rel))
		opts.Log.Debug().Str("file", rel).Msg("replacing in file")
		res, err := w.Replace(path, opts.Replacements)
		if err != nil {
			opts.Log.Error().Err(err).Str("file", rel).Msg("replace failed")
			errs = append(errs, err)
			continue
		}
		for _, s := range res.Applied {
			found[s] = true
		}
		for _, s := range res.Skipped {
			found[s] = true
		}
		applied += len(res.Applied)
		skipped += len(res.Skipped)
		missing += len(res.Missing)
		occurrences += res.Count
		if opts.JSON {
			logReplace(opts.Log, rel, res)
			continue
		}
		printReplace(out, rel, res)
	}

	if opts.JSON {
		opts.Log.Info().Int("files", len(opts.Paths)).Int("occurrences", occurrences).
			Int("applied", applied).Int("skipped", skipped).Int("missing", missing).
			Msg("replace complete")
	} else {
		fmt.Fprintf(out, "%d file(s), %d occurrence(s): %d applied, %d skipped, %d missing\n",
			len(opts.Paths), occurrences, applied, skipped, missing)
	}

	if err := errors.Join(errs...); err != nil {
		// Each one was already reported against the file it belongs to.
		return err
	}
	if opts.Strict {
		var stale int
		for _, s := range opts.Replacements {
			if !found[s] {
				opts.Log.Error().Str("find", s.Find).Msg("replacement matched nothing")
				stale++
			}
		}
		if stale > 0 {
			err := fmt.Errorf("%d replacement(s) matched nothing", stale)
			opts.Log.Error().Err(err).Msg("replacements are not clean")
			return err
		}
	}
	return nil
}

// printReplace writes one file's listing entry: one line per replacement
// with its outcome, and the occurrence count for the ones that landed.
func printReplace(out io.Writer, rel string, res writer.ReplaceResult) {
	fmt.Fprintln(out, rel)
	for _, group := range []struct {
		outcome string
		reps    []writer.Replacement
	}{
		{"applied", res.Applied}, {"skipped", res.Skipped}, {"missing", res.Missing},
	} {
		for _, s := range group.reps {
			fmt.Fprintf(out, "  %-7s  %s -> %s\n", group.outcome, s.Find, s.Write)
		}
	}
	if res.Count > 0 {
		fmt.Fprintf(out, "  %d occurrence(s) replaced\n", res.Count)
	}
}

// logReplace emits one file's result as a structured event.
func logReplace(log zerolog.Logger, rel string, res writer.ReplaceResult) {
	msg := "file unchanged"
	if len(res.Applied) > 0 {
		msg = "file updated"
	}
	log.Info().
		Str("path", rel).
		Int("occurrences", res.Count).
		Interface("replacements", outcomeView[replacementView]{
			Applied: replacementViews(res.Applied),
			Skipped: replacementViews(res.Skipped),
			Missing: replacementViews(res.Missing),
		}).
		Msg(msg)
}

func replacementViews(reps []writer.Replacement) []replacementView {
	if len(reps) == 0 {
		return nil
	}
	out := make([]replacementView, 0, len(reps))
	for _, s := range reps {
		out = append(out, replacementView{Find: s.Find, Write: s.Write})
	}
	return out
}

// printWrite writes one manifest's listing entry: what the version field did,
// then one line per edit and link with its outcome.
func printWrite(out io.Writer, rel string, res writer.Result, linkRes writer.LinkResult) {
	fmt.Fprintln(out, rel)
	if res.VersionWritten {
		fmt.Fprintf(out, "  version written\n")
	}
	if res.BuildWritten {
		fmt.Fprintf(out, "  build number written\n")
	}
	for _, group := range []struct {
		outcome string
		edits   []writer.Edit
	}{
		{"applied", res.Applied}, {"skipped", res.Skipped}, {"missing", res.Missing},
	} {
		for _, e := range group.edits {
			fmt.Fprintf(out, "  %-7s  %s  %s  %s\n", group.outcome, e.Kind.String(), e.Name, e.Range)
		}
	}
	for _, group := range []struct {
		outcome string
		links   []writer.Link
	}{
		{"applied", linkRes.Applied}, {"skipped", linkRes.Skipped}, {"missing", linkRes.Missing},
	} {
		for _, l := range group.links {
			target := l.Path
			if target == "" {
				target = "(removed)"
			}
			fmt.Fprintf(out, "  %-7s  link     %s  %s\n", group.outcome, l.Name, target)
		}
	}
}

// logWrite emits one manifest's result as a structured event.
func logWrite(log zerolog.Logger, rel string, res writer.Result, linkRes writer.LinkResult) {
	changed := res.VersionWritten || res.BuildWritten || len(res.Applied) > 0 || len(linkRes.Applied) > 0
	msg := "manifest unchanged"
	if changed {
		msg = "manifest updated"
	}
	log.Info().
		Str("path", rel).
		Bool("versionWritten", res.VersionWritten).
		Bool("buildWritten", res.BuildWritten).
		Interface("edits", outcomeView[editView]{
			Applied: editViews(res.Applied),
			Skipped: editViews(res.Skipped),
			Missing: editViews(res.Missing),
		}).
		Interface("links", outcomeView[linkView]{
			Applied: linkViews(linkRes.Applied),
			Skipped: linkViews(linkRes.Skipped),
			Missing: linkViews(linkRes.Missing),
		}).
		Msg(msg)
}

func editViews(edits []writer.Edit) []editView {
	if len(edits) == 0 {
		return nil
	}
	out := make([]editView, 0, len(edits))
	for _, e := range edits {
		out = append(out, editView{Kind: e.Kind.String(), Name: e.Name, Range: e.Range})
	}
	return out
}

func linkViews(links []writer.Link) []linkView {
	if len(links) == 0 {
		return nil
	}
	out := make([]linkView, 0, len(links))
	for _, l := range links {
		out = append(out, linkView{Name: l.Name, Version: l.Version, Path: l.Path})
	}
	return out
}

// listing is the writer the human-readable output goes to, matching how the
// compute command treats its own: a caller that wants no listing at all (a
// JSON run, a test) may leave Out unset rather than having to supply a sink.
func listing(out io.Writer) io.Writer {
	if out == nil {
		return io.Discard
	}
	return out
}

// existingDir resolves rel against root and proves it is a folder, so a typo
// is reported as the typo it is instead of surfacing as an empty scan.
func existingDir(root, rel string) (string, error) {
	dir := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s: not a folder", dir)
	}
	return dir, nil
}

// unwrapJoined splits an errors.Join result back into its parts, so each
// failed manifest is reported on its own line instead of as one wall of text.
func unwrapJoined(err error) []error {
	if err == nil {
		return nil
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		return joined.Unwrap()
	}
	return []error{err}
}
