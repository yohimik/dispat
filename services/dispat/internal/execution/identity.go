// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

// Who this process is, as every log line it writes and every event it sends
// has to say (§28.3).
//
// A release spread over several machines is read from several logs at once,
// and the first question of any reading is which machine wrote the line. The
// answer is two fields, `role` and `node`, and they name the writer rather
// than the work: a line about another node names that node separately.
//
// The decision is here, in one function, because three callers ask it and
// they have to agree: the command line, which derives the run logger; the
// application, which stamps the events; and a nested dispat inside a task,
// which is on a worker whatever the checkout it was handed says.

import (
	"cmp"
	"os"
	"strings"

	public "github.com/yohimik/dispat/pkg/models"

	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// UnnamedNode is what a node that can state no name of its own is called: no
// execution.name in the file and a machine that cannot say what it is called.
// It is a last resort rather than a default, because an empty field would be
// a line that answers the first question with nothing.
const UnnamedNode = "unnamed-node"

const (
	senderOrchestrator = "orchestrator"
	senderWorker       = "worker"
)

// ResolveSender answers who this process is when it takes part in distributed
// execution, and the zero sender when it does not.
//
// The three branches are three different questions and are answered in the
// order they overrule each other. A process executing a task or serving a
// mailbox is a worker for log purposes. The first node's authority is
// verified from its run and locks, never from this log identity.
func ResolveSender(settings *public.ExecutionConfig, isServingTasks bool, env []string) release.Sender {
	if IsWorkerAuthority(env) {
		return release.Sender{Role: senderWorker, Node: resolveTaskNode(env)}
	}
	if isServingTasks {
		return release.Sender{Role: senderWorker, Node: resolveNodeName(statedName(settings))}
	}
	if settings == nil {
		return release.Sender{}
	}
	return release.Sender{Role: senderOrchestrator, Node: resolveNodeName(settings.Name)}
}

// resolveTaskNode is the node a task is running on, as its command
// environment names it (NodeEnv).
//
// It reads the pairs rather than the process environment for the reason
// IsWorkerAuthority does: both questions are asked of the same environment at
// the same moment, and an answer taken from two sources could disagree with
// itself. A marker that arrived without its node is a node that cannot say
// its name, which is what the host name answers.
func resolveTaskNode(env []string) string {
	for _, pair := range env {
		name, value, _ := strings.Cut(pair, "=")
		if name != NodeEnv {
			continue
		}
		return resolveNodeName(value)
	}
	return resolveNodeName("")
}

// statedName is the name a serving node's settings carry, for the settings
// that are absent altogether: `dispat worker` refuses a node with no name by
// that name, and the refusal is a line this identity is already on.
func statedName(settings *public.ExecutionConfig) string {
	if settings == nil {
		return ""
	}
	return settings.Name
}

// resolveNodeName is what a node calls itself: the name it states, else the
// machine it runs on, else UnnamedNode.
//
// The host name's error is not consulted because it carries the same answer
// as the empty name it comes with, and one fallback is easier to read than
// two that say the same thing.
func resolveNodeName(stated string) string {
	if stated != "" {
		return stated
	}
	host, _ := os.Hostname() //nolint:errcheck // an unreadable host name is the empty one
	return cmp.Or(host, UnnamedNode)
}
