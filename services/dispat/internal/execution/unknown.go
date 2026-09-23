// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// What a run remembers about an effect it cannot account for, and what that
// costs the exclusion it holds (CCME §28.6, §28.9 `publication-unknown`).
//
// A publication this run authorized and cannot establish the outcome of is the
// one condition of this profile that outlives the run. Everything else a
// distributed release gets wrong is a package that failed: the bytes were
// wrong, a node went away, a hook refused. This is different because the thing
// that may have happened is a write to somebody else's registry, and no
// evidence available here says whether it did. The specification's answer is
// not to guess in either direction, and the two halves of that are what this
// file holds.
//
// The first half is the report: the package failed at its publish stage, with
// the identities an operator needs and no tag, record or second attempt.
//
// The second half is the lock. A publisher that acknowledged the withdrawal is
// quiesced, and locks go back in reverse order as they always do. A publisher
// that never answered may still be running, so handing the repository it was
// publishing into to the next owner would be handing over an exclusion that
// does not exclude. That one lock is therefore retained for an operator, with
// the order of recovery named, and the run never reports successful cleanup.

import "sort"

// unknownPublication is one authorized publication whose outcome this run
// could not establish: which work it was, where it ran, whose repository it
// was publishing into, and whether the publisher is known to have stopped.
type unknownPublication struct {
	Task       string
	Attempt    int
	Node       string
	Branch     string
	Repository string
	// IsQuiesced says the node acknowledged the withdrawal, so nothing of the
	// attempt is running any more. It is the whole difference between a lock
	// that goes back and a lock that is left for an operator: §28.6 releases
	// exclusion only once the authorized operations have quiesced or been
	// safely fenced, and a publisher is the one attempt a revoked ref does not
	// fence.
	IsQuiesced bool
}

// rememberUnknownPublication records one such outcome. It is a list rather
// than a map because the summary prints them in the order the run decided
// them and the lock path asks a question about the set.
func (c *Coordinator) rememberUnknownPublication(unknown unknownPublication) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.unknownPublications = append(c.unknownPublications, unknown)
}

// UnknownPublications are the authorized publications this run could not
// establish the outcome of, in the order it decided them.
//
// It is exported because the two readers are outside this package: the run
// summary, which must distinguish an unknown external outcome from a failure
// (§28.9), and the release path, which has to know whether it may report a
// clean cleanup.
func (c *Coordinator) UnknownPublications() []unknownPublication {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]unknownPublication(nil), c.unknownPublications...)
}

// RetainedRepositories are the repositories whose release lock this run must
// not give back, by name, sorted.
//
// One repository is named per unfenced publisher and no others: the exclusion
// that has to survive is the one covering the effect nobody can account for,
// and retaining anything else would be taking a repository out of service for
// a fact about a different one. A run whose every unknown publisher
// acknowledged retains nothing, because an acknowledged publisher has
// provably stopped.
//
// It is asked of the coordinator by the unlock path rather than pushed to it,
// because the question is the lock's: which of the locks I hold may I release.
func (c *Coordinator) RetainedRepositories() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	retained := map[string]bool{}
	for _, unknown := range c.unknownPublications {
		if unknown.IsQuiesced {
			continue
		}
		retained[unknown.Repository] = true
	}
	names := make([]string, 0, len(retained))
	for name := range retained {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
