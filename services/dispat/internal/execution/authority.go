// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

// Package execution is the distributed execution profile: which role a node
// plays in a release, what authority one assigned task carries, and the
// diagnostics both are reported through.
//
// The package deliberately knows nothing about releases. It answers two
// questions the release path asks it — may this process start a release, and
// what does this failure have to be called — so that the answer is the same
// wherever it is asked from, and so that a later gate can add the mailbox,
// the manifests and the pool beside it without threading that knowledge back
// into the planner.
package execution

import "strings"

// AuthorityEnv marks a process that is executing somebody else's task rather
// than running a release of its own. A worker's task runner sets it on every
// command of an assignment, and it is deliberately internal: it is not a
// setting an operator writes, it is how one dispat process tells the dispat
// processes it starts what scope they are in. Anything it reaches is under
// worker authority however deeply it was nested, because a build script that
// shells out inherits it exactly as it inherits the rest of the environment.
const AuthorityEnv = "DISPAT_INTERNAL_EXECUTION_AUTHORITY"

// NodeEnv names the node a task is running on. It travels beside the marker
// so a script, and a log line written by a nested command, can say where the
// work happened; nothing decides anything from it, because the name on a
// branch is a routing hint and never an authority.
const NodeEnv = "DISPAT_EXECUTION_NODE"

// WorkerAuthority is the one value AuthorityEnv takes today, matched exactly.
// A different value is not worker authority: the marker is written by dispat
// for dispat, so a value nothing here wrote is a value nothing here should
// act on, and guessing at it would be guessing at the one thing that decides
// whether a release may start.
const WorkerAuthority = "worker"

// IsWorkerAuthority reports whether these environment pairs put the process
// under worker authority, which is the state that refuses release initiation.
//
// It takes the pairs rather than reading the process environment so that both
// callers ask the same question of whatever environment they mean: the
// command line asks before any configuration is read, and the release path
// asks before any lock is taken. The first statement of a name wins, which is
// what the C library and the Go runtime both do with a duplicated name.
func IsWorkerAuthority(env []string) bool {
	for _, pair := range env {
		name, value, _ := strings.Cut(pair, "=")
		if name != AuthorityEnv {
			continue
		}
		return value == WorkerAuthority
	}
	return false
}

// FormatWorkerAuthorityEnv is what a worker's task runner appends to every
// command of an assignment: the authority marker and the node the work is
// running on.
//
// It lives here, beside the reader, because the two have to agree exactly. A
// runner that spelled the marker itself would be one rename away from a
// worker that silently regained the right to start releases.
func FormatWorkerAuthorityEnv(node string) []string {
	return []string{AuthorityEnv + "=" + WorkerAuthority, NodeEnv + "=" + node}
}
