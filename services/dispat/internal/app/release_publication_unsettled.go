// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package app

import (
	"context"

	"github.com/yohimik/dispat/services/dispat/internal/plan"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

const (
	publicationUnsettledIncludeWithheld = true
	publicationKnownRecordFound         = true
	publicationKnownRecordAbsent        = false
)

type publicationUnsettledRecording struct {
	plan    *plan.Plan
	results map[string]*release.Result
	hooks   *runHooks
	github  *ghDispatch
	fleet   *workspaceRecorder
}

// recordKnownPublishedWhileUnsettled completes records for packages whose
// publication was already verified before another package's external publish
// became uncertain. The uncertain package has no publish outcome to record.
// Run hooks and shared include regeneration could start new operator effects
// or consume its unverified source, so this path does neither. The caller's
// release exclusion remains held after these known records finish.
func (a *App) recordKnownPublishedWhileUnsettled(ctx context.Context, recording publicationUnsettledRecording) error {
	if !isAnyReleasePublished(recording.results) {
		return nil
	}
	recordCtx, stopRecording := context.WithTimeout(context.WithoutCancel(ctx), release.RecordTimeout)
	defer stopRecording()
	crit := &criticals{}
	if recording.fleet == nil {
		// A cancelled observer skips every commit/push bracket hook while the
		// native records use the live, bounded recording context.
		hookCtx, stopHooks := context.WithCancel(recordCtx)
		stopHooks()
		a.finalize(recordCtx, finalizer{gh: recording.github, remote: a.pushRemote(), hooks: recording.hooks, crit: crit,
			observed:          recordHooks{ctx: hookCtx, observer: hookCtx},
			isIncludeWithheld: publicationUnsettledIncludeWithheld}, recording.plan, recording.results)
	}
	a.reportOwedAfterPublication(recordCtx,
		publicationOutcome{plan: recording.plan, results: recording.results, fleet: recording.fleet}, crit)
	return crit.err()
}

func isAnyReleasePublished(results map[string]*release.Result) bool {
	for _, result := range results {
		if result != nil && result.Status == release.StatusPublished {
			return publicationKnownRecordFound
		}
	}
	return publicationKnownRecordAbsent
}
