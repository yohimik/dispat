package app

import (
	"context"
	"fmt"
	"os"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/workspaceenv"
)

func (p *workspacePins) nativeScope() workspaceenv.NativeTransitionScope {
	if p == nil {
		return workspaceenv.NativeTransitionScope{}
	}
	p.mu.Lock()
	root, configPath := p.root, p.config
	p.mu.Unlock()
	return workspaceenv.NativeTransitionScope{Root: root, ConfigPath: configPath, Environment: os.Environ()}
}

func (p *workspacePins) beginNative(transition workspaceenv.NativeTransition) (string, error) {
	if p == nil || !p.inherited {
		return "", nil
	}
	return workspaceenv.BeginNativeTransition(p.nativeScope(), transition)
}

func (p *workspacePins) completeNative(token string, transition workspaceenv.NativeTransition) error {
	if token == "" {
		return nil
	}
	return workspaceenv.CompleteNativeTransition(p.nativeScope(), token, transition)
}

func (p *workspacePins) failNative(token, owner string) {
	if token == "" {
		return
	}
	if err := workspaceenv.FailNativeTransition(p.nativeScope(), token, owner); err != nil {
		p.log.Error().Err(err).Str("repository", owner).Msg("could not mark nested native record failed")
	}
}

func (p *workspacePins) nativeTransitions(owner string) ([]workspaceenv.NativeTransitionRecord, error) {
	if p == nil {
		return nil, nil
	}
	p.mu.Lock()
	live := p.live
	p.mu.Unlock()
	if live == nil {
		return nil, nil
	}
	return live.NativeTransitions(owner)
}

func (p *workspacePins) nativeTransition(owner, token string) (workspaceenv.NativeTransitionRecord, bool, error) {
	if p == nil {
		return workspaceenv.NativeTransitionRecord{}, false, nil
	}
	p.mu.Lock()
	live := p.live
	p.mu.Unlock()
	if live == nil {
		return workspaceenv.NativeTransitionRecord{}, false, nil
	}
	return live.NativeTransition(owner, token)
}

func (p *workspacePins) nativeRevision(owner string) (workspaceenv.NativeJournalRevision, error) {
	if p == nil {
		return workspaceenv.NativeJournalRevision{}, nil
	}
	p.mu.Lock()
	live := p.live
	p.mu.Unlock()
	if live == nil {
		return workspaceenv.NativeJournalRevision{}, nil
	}
	return live.NativeJournalRevision(owner)
}

type nativeTransitionSpec struct {
	release      *plan.Release
	owner        string
	priorHead    string
	tagRequested bool
	pushRequired bool
}

func nativeTransitionFor(spec nativeTransitionSpec) workspaceenv.NativeTransition {
	return workspaceenv.NativeTransition{
		Owner: spec.owner, Package: spec.release.Pkg.Name, Version: spec.release.Next.String(), PriorHead: spec.priorHead,
		TagRequested: spec.tagRequested, PushRequired: spec.pushRequired,
	}
}

type nativeTagQuery struct {
	git     *gitx.LocalGitx
	release *plan.Release
	tag     string
	head    string
}

func nativeTags(ctx context.Context, query nativeTagQuery) ([]workspaceenv.NativeTagRef, error) {
	names := []struct {
		name   string
		moving bool
	}{{name: query.tag}}
	for _, alias := range query.release.AliasTags() {
		names = append(names, struct {
			name   string
			moving bool
		}{name: alias.Name, moving: alias.Force})
	}
	refs := make([]workspaceenv.NativeTagRef, 0, len(names))
	for index, candidate := range names {
		object, err := query.git.TagObject(ctx, candidate.name)
		if err != nil {
			if index > 0 { // Alias creation is advisory; only an observed alias is admitted.
				continue
			}
			return nil, fmt.Errorf("reading native release tag %s: %w", candidate.name, err)
		}
		commit, err := query.git.ResolveCommit(ctx, "refs/tags/"+candidate.name)
		if index > 0 && (err != nil || commit != query.head) {
			continue // An advisory alias that did not move is not a native effect.
		}
		if err != nil || commit != query.head {
			return nil, fmt.Errorf("native tag %s does not name source revision %s", candidate.name, query.head)
		}
		refs = append(refs, workspaceenv.NativeTagRef{
			Name: candidate.name, Object: object, Commit: commit, Moving: candidate.moving,
		})
	}
	return refs, nil
}

func (w *workspaceRecorder) plannedNativeRelease(name string) *plan.Release {
	if w == nil || w.snapshot == nil {
		return nil
	}
	return w.snapshot.nativePlan[name]
}

func nativeOwnerName(rel *plan.Release) string {
	if rel == nil || rel.Pkg == nil {
		return config.ControlRepository
	}
	return rel.Pkg.Repository
}
