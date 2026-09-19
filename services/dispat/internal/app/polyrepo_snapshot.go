// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"context"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// workspaceSnapshotGuard retains the exact relevant refs read before
// planning. Verifications are fleet-serialised, while repository state stays
// under repositoryRecord.mu so recording and validation cannot observe each
// other halfway through a native Git transaction.
type workspaceSnapshotGuard struct {
	mu        sync.Mutex
	records   []*repositoryRecord
	repoIndex map[string]int
	byRelease map[*plan.Release]*repositorySet
}

type repositorySet struct{ words []uint64 }

func (s *repositorySet) contains(index int) bool {
	return s != nil && index >= 0 && index/64 < len(s.words) && s.words[index/64]&(uint64(1)<<uint(index%64)) != 0
}

type repositoryRefGuard struct {
	matcher  *gitx.TagSnapshotMatcher
	expected gitx.TagSnapshot
	admitted map[string]refAdmission
}

type refAdmission struct {
	target string
	moving bool
}

func tagNamespaces(packages []*model.Package) map[string][]gitx.TagNamespace {
	byRepository := make(map[string][]gitx.TagNamespace)
	for _, pkg := range packages {
		if pkg == nil || pkg.Space == nil {
			continue
		}
		namespace := gitx.TagNamespace{Package: pkg.Name, Release: plan.TagFormatFor(pkg)}
		for _, alias := range pkg.Space.AliasTags {
			namespace.Aliases = append(namespace.Aliases, gitx.AliasFormat(alias.Format))
		}
		byRepository[pkg.Repository] = append(byRepository[pkg.Repository], namespace)
	}
	return byRepository
}

// captureSnapshot reads each repository's configured package/alias namespace
// once. It runs before planning: a ref created during planning is therefore a
// detectable change rather than an input silently accepted by only part of
// the plan.
func (w *workspaceRecorder) captureSnapshot(ctx context.Context, packages []*model.Package) error {
	guard := &workspaceSnapshotGuard{
		records: w.ordered, repoIndex: make(map[string]int, len(w.ordered)),
		byRelease: make(map[*plan.Release]*repositorySet),
	}
	for index, record := range w.ordered {
		guard.repoIndex[record.repo.Name] = index
	}
	namespaces := tagNamespaces(packages)
	for _, record := range w.ordered {
		record.mu.Lock()
		var matcher *gitx.TagSnapshotMatcher
		if len(namespaces[record.repo.Name]) > 0 {
			matcher = gitx.NewTagSnapshotMatcher(namespaces[record.repo.Name])
		}
		unlock, err := gitx.AcquireMutations(ctx, record.git)
		if err == nil {
			var refs gitx.TagSnapshot
			refs, err = record.git.RelevantTagSnapshot(ctx, matcher)
			if err == nil {
				record.snapshot = &repositoryRefGuard{
					matcher: matcher, expected: refs, admitted: make(map[string]refAdmission),
				}
			}
		}
		if unlock != nil {
			unlock()
		}
		record.mu.Unlock()
		if err != nil {
			return config.WithDiagnostic(config.DiagnosticRepositoryInvalid,
				fmt.Errorf("E330: repository %s: capturing fixed tag snapshot: %w", record.repo.Name, err))
		}
	}
	w.snapshot = guard
	w.app.log.Debug().Int("repositories", len(w.ordered)).Msg("captured fixed fleet tag snapshot")
	return nil
}

// setSnapshotPlan fixes each release's exact planner-provided history-input
// closure. Compact immutable bitsets and interning share equal closures among
// packages. The owner/provider fallback retains small hand-built Plan callers.
func (w *workspaceRecorder) setSnapshotPlan(pl *plan.Plan) {
	if w.snapshot == nil {
		return
	}
	guard := w.snapshot
	guard.byRelease = make(map[*plan.Release]*repositorySet)
	wordCount := (len(guard.records) + 63) / 64
	closures := make(map[string]*repositorySet, len(pl.Releases))
	interned := make(map[string]*repositorySet)
	type plannerSetKey struct {
		first      *uint64
		addControl bool
	}
	plannerSets := make(map[plannerSetKey]*repositorySet)
	matchingOrder := len(pl.RepositoryInputOrder) == len(guard.records)
	if matchingOrder {
		for i, record := range guard.records {
			if pl.RepositoryInputOrder[i] != record.repo.Name {
				matchingOrder = false
				break
			}
		}
	}
	control, hasControl := guard.repoIndex[config.ControlRepository]
	controlRecord := w.byName[config.ControlRepository]
	controlEnabled := hasControl && controlRecord != nil && controlRecord.repo.Commit.IsEnabled()
	for _, name := range pl.Order {
		rel := pl.Releases[name]
		if rel == nil || rel.Pkg == nil {
			continue
		}
		inputWords, planned := pl.RepositoryInputs[name]
		var words []uint64
		plannerWords := planned && wordCount > 0 && matchingOrder && len(inputWords) == wordCount
		addControl := false
		if rel.IsReleasing() && controlEnabled && rel.Pkg.Repository != config.ControlRepository {
			controlWord, controlMask := control/64, uint64(1)<<uint(control%64)
			addControl = !plannerWords || inputWords[controlWord]&controlMask == 0
		}
		var plannerKey plannerSetKey
		if plannerWords {
			plannerKey = plannerSetKey{first: &inputWords[0], addControl: addControl}
			if closure := plannerSets[plannerKey]; closure != nil {
				closures[name] = closure
				if rel.IsReleasing() {
					guard.byRelease[rel] = closure
				}
				continue
			}
			words = inputWords
		} else {
			words = make([]uint64, wordCount)
		}
		if !planned {
			if owner, ok := guard.repoIndex[rel.Pkg.Repository]; ok {
				words[owner/64] |= uint64(1) << uint(owner%64)
			}
			for _, provider := range pl.Providers[name] {
				if closure := closures[provider]; closure != nil {
					for i := range words {
						words[i] |= closure.words[i]
					}
				}
			}
		}
		if planned && !plannerWords {
			for i, repository := range pl.RepositoryInputOrder {
				if i/64 >= len(inputWords) || inputWords[i/64]&(uint64(1)<<uint(i%64)) == 0 {
					continue
				}
				if input, ok := guard.repoIndex[repository]; ok {
					words[input/64] |= uint64(1) << uint(input%64)
				}
			}
		}
		if rel.IsReleasing() && controlEnabled && rel.Pkg.Repository != config.ControlRepository {
			controlWord, controlMask := control/64, uint64(1)<<uint(control%64)
			if words[controlWord]&controlMask == 0 {
				if plannerWords {
					words = append([]uint64(nil), words...)
				}
				words[controlWord] |= controlMask
			}
		}
		closure := internRepositorySet(interned, words)
		if plannerWords {
			plannerSets[plannerKey] = closure
		}
		closures[name] = closure
		if rel.IsReleasing() {
			guard.byRelease[rel] = closure
		}
	}
}

func internRepositorySet(interned map[string]*repositorySet, words []uint64) *repositorySet {
	keyBytes := make([]byte, len(words)*8)
	for i, word := range words {
		binary.LittleEndian.PutUint64(keyBytes[i*8:], word)
	}
	key := string(keyBytes)
	if existing := interned[key]; existing != nil {
		return existing
	}
	set := &repositorySet{words: words}
	interned[key] = set
	return set
}

// verifySnapshot revalidates all repositories after beforeAll, or the current
// release's exact owner/provider/group/control input closure before publish.
func (w *workspaceRecorder) verifySnapshot(ctx context.Context, rel *plan.Release) error {
	if w.snapshot == nil {
		return nil
	}
	return w.snapshot.verify(ctx, rel)
}

// acquirePublish keeps one repository's publish script and native record in
// one ordered lane. Separate repositories retain full publish concurrency;
// packages sharing a Git HEAD cannot expose a half-admitted nested commit to
// each other's pre-publish guard.
// A choreographed release settles its fleet links here, inside the lane
// acquisition, rather than in the pre-publish hook: the executor takes this
// lane before it calls BeforePublish, so two cross-repository consumers
// settling from there would each hold their own lane while waiting for the
// other's. Taking every lane the settlement needs in repository-name order is
// what makes the wait graph acyclic, and every lane but the package's own is
// released again before publication begins.
func (w *workspaceRecorder) acquirePublish(ctx context.Context, rel *plan.Release) (func(), error) {
	if rel == nil || rel.Pkg == nil {
		return func() {}, nil
	}
	record := w.byName[rel.Pkg.Repository]
	if record == nil {
		return nil, fmt.Errorf("no repository owner for package %s", rel.Pkg.Name)
	}
	lanes, err := w.settleLanes(rel)
	if err != nil {
		return nil, err
	}
	held, err := w.takeLanes(ctx, lanes)
	if err != nil {
		return nil, err
	}
	if err := w.settleLinks(ctx, rel, held); err != nil {
		releaseLanes(held, "")
		return nil, err
	}
	own := held[record.repo.Name]
	releaseLanes(held, record.repo.Name)
	if own == nil {
		return func() {}, nil
	}
	return own, nil
}

// takeLanes reserves the publish lanes of the named repositories, in the
// order they are given, which the caller sorts by name.
func (w *workspaceRecorder) takeLanes(ctx context.Context, names []string) (map[string]func(), error) {
	held := make(map[string]func(), len(names))
	for _, name := range names {
		record := w.byName[name]
		if record == nil {
			releaseLanes(held, "")
			return nil, fmt.Errorf("no repository %s to reserve for publication", name)
		}
		if _, taken := held[name]; taken {
			continue
		}
		select {
		case record.publishGate <- struct{}{}:
			gate := record.publishGate
			var once sync.Once
			held[name] = func() { once.Do(func() { <-gate }) }
		case <-ctx.Done():
			releaseLanes(held, "")
			return nil, ctx.Err()
		}
	}
	return held, nil
}

// releaseLanes gives back every reserved lane but the one named.
func releaseLanes(held map[string]func(), keep string) {
	for name, release := range held {
		if name == keep {
			continue
		}
		release()
	}
}

func (g *workspaceSnapshotGuard) verify(ctx context.Context, rel *plan.Release) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	var selected *repositorySet
	if rel != nil {
		selected = g.byRelease[rel]
	}
	for index, record := range g.records {
		if selected != nil && !selected.contains(index) {
			continue
		}
		if err := g.verifyRepository(ctx, record, rel); err != nil {
			return err
		}
	}
	return nil
}

func (g *workspaceSnapshotGuard) verifyRepository(ctx context.Context, record *repositoryRecord, current *plan.Release) error {
	record.mu.Lock()
	defer record.mu.Unlock()
	state := record.snapshot
	if state == nil {
		return nil
	}
	unlock, err := gitx.AcquireMutations(ctx, record.git)
	if err != nil {
		return config.WithDiagnostic(config.DiagnosticRepositoryInvalid,
			fmt.Errorf("E330: repository %s: acquiring snapshot validation lock: %w", record.repo.Name, err))
	}
	defer unlock()

	head, err := record.git.HeadSHA(ctx)
	if err != nil {
		return config.WithDiagnostic(config.DiagnosticRepositoryInvalid,
			fmt.Errorf("E330: repository %s: reading snapshot HEAD: %w", record.repo.Name, err))
	}
	if head != record.expectedHead {
		if !admitCurrentExportedHead(record, current, head) {
			return snapshotError(record.repo.Name, fmt.Sprintf("HEAD moved from %s to %s", record.expectedHead, head))
		}
		record.git.Log.Debug().Str("revision", head).Msg("admitted nested source revision into fleet snapshot")
		record.expectedHead = head
	}

	refs, err := record.git.RelevantTagSnapshot(ctx, state.matcher)
	if err != nil {
		return snapshotError(record.repo.Name, fmt.Sprintf("reading relevant tag refs: %v", err))
	}
	permissions := make(map[string]refAdmission, len(state.admitted)+4)
	for name, admission := range state.admitted {
		permissions[name] = admission
	}
	if current != nil && current.Pkg != nil && current.Pkg.Repository == record.repo.Name {
		if target, ok := currentExportedHead(record, current); ok && target == head {
			admitReleaseRefs(permissions, current, target)
		}
	}

	changed := changedTagRefs(state.expected, refs)
	for _, name := range changed {
		before, existed := state.expected[name]
		after, exists := refs[name]
		admission, admitted := permissions[name]
		if !exists || !admitted || after.Commit() != admission.target || (existed && !admission.moving) {
			return snapshotError(record.repo.Name, describeRefChange(name, before, existed, after, exists))
		}
	}
	state.expected = refs
	for _, name := range changed {
		delete(state.admitted, name)
	}
	for name, admission := range state.admitted {
		if target, ok := refs[name]; ok && target.Commit() == admission.target {
			delete(state.admitted, name)
		}
	}
	record.git.Log.Debug().Int("refs", len(refs)).Int("changes", len(changed)).Msg("verified repository release snapshot")
	return nil
}

func admitCurrentExportedHead(record *repositoryRecord, current *plan.Release, head string) bool {
	exported, ok := currentExportedHead(record, current)
	return ok && exported == head
}

func currentExportedHead(record *repositoryRecord, rel *plan.Release) (string, bool) {
	if rel == nil || rel.Pkg == nil || rel.Pkg.Repository != record.repo.Name {
		return "", false
	}
	exported := rel.ExportedCommit()
	if !fullObjectID(exported) {
		return "", false
	}
	return exported, true
}

func fullObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for i := range len(value) {
		c := value[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func admitReleaseRefs(dst map[string]refAdmission, rel *plan.Release, target string) {
	dst[rel.TagName()] = refAdmission{target: target}
	for _, alias := range rel.AliasTags() {
		dst[alias.Name] = refAdmission{target: target, moving: alias.Force}
	}
}

// admitRecordedRelease is called only after native source tag creation
// succeeds. The next bulk verification can then advance exactly those refs,
// at exactly the source revision the recorder pinned.
func (r *repositoryRecord) admitRecordedRelease(rel *plan.Release) {
	if r.snapshot == nil || rel == nil {
		return
	}
	admitReleaseRefs(r.snapshot.admitted, rel, rel.ExportedCommit())
}

func changedTagRefs(before, after gitx.TagSnapshot) []string {
	var names []string
	for name, target := range before {
		if current, ok := after[name]; !ok || current != target {
			names = append(names, name)
		}
	}
	for name := range after {
		if _, ok := before[name]; !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func describeRefChange(name string, before gitx.TagRefTarget, hadBefore bool, after gitx.TagRefTarget, hasAfter bool) string {
	switch {
	case !hadBefore:
		return fmt.Sprintf("relevant tag %s appeared at %s", name, after.Commit())
	case !hasAfter:
		return fmt.Sprintf("relevant tag %s at %s was deleted", name, before.Commit())
	default:
		return fmt.Sprintf("relevant tag %s moved from %s to %s", name, before.Commit(), after.Commit())
	}
}

func snapshotError(repository, detail string) error {
	detail = strings.TrimSpace(detail)
	return config.WithDiagnostic(config.DiagnosticRepositoryInvalid,
		fmt.Errorf("E330: repository %s changed after planning; re-plan before publishing: %s", repository, detail))
}
