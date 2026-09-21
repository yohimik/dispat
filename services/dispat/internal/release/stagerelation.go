// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

// What a provider's relation decides about the consumers of its packages: the
// ordering edges the task graph gets, and the sentence a consumer skipped
// because of it is told.
//
// Both answers live here rather than inline at the three call sites because
// the relation has three values and each of them is a different combination of
// the same two questions. Written out at the call site they read as nested
// conditions the reader has to re-derive; written here each value of the
// relation answers from its own branch.

import (
	"github.com/yohimik/dispat/services/dispat/internal/model"
)

// resolveFirstTaskWaits answers which of a changed provider's tasks a
// consumer's first task (its version task when it has one, its build
// otherwise) has to wait for.
//
// The three relations are a chain rather than three unrelated rules: nothing,
// the provider's build, the provider's build and its publish. The publish case
// names both edges rather than relying on the provider's own build-before-
// publish edge to imply the first, because the graph is read by people as well
// as by the scheduler and an edge nobody can see is an ordering nobody can
// check.
func resolveFirstTaskWaits(relation model.StageRelation, provider string) []task {
	if !relation.IsBuildWaitingBuild() {
		return nil
	}
	if relation.IsBuildWaitingPublish() {
		return []task{{provider, taskBuild}, {provider, taskPublish}}
	}
	return []task{{provider, taskBuild}}
}

// formatSkipReason renders why a package the run planned produced nothing. The
// sentence has to stay true for each relation separately, because it is what a
// CI log offers an operator in place of the release they expected.
func formatSkipReason(blocker string, relation model.StageRelation, isRecordBlocked bool) string {
	if isRecordBlocked {
		return "provider " + blocker +
			" has incomplete release records; repair its records before releasing dependents"
	}
	if relation.IsBuildWaitingPublish() {
		return "provider " + blocker +
			" failed or was skipped, and this package's build takes its publish as input"
	}
	if relation.IsBlocking {
		return "provider " + blocker +
			" failed or was skipped, and its space declares that its consumers publish only after it published"
	}
	return "provider " + blocker + " failed or was skipped, and the package has no changes of its own"
}
