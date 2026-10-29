// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package cli

const (
	compiledDistributionModeName         = "compilation"
	distributionModeFlagName             = "distribution-mode"
	distributionModeUsageText            = "release, run and status: compilation (default), interpretation or hybrid"
	invalidInvocationExitCode            = 2
	invalidPlanningConcurrencyErrorText  = "invalid concurrency override"
	invalidReturnChannelAddressErrorText = "invalid return channel address"
	invocationValidationCompleteState    = true
	linkedWorkerFlagName                 = "choreographer"
	linkedWorkerUsageText                = "release, run and status: add a choreographer for this invocation, name[=endpoint] (repeatable); a name alone reaches the repository being released; held to every rule a link in execution.choreographers is, and never part of the plan digest"
	planningConcurrencyFlagName          = "concurrency"
	planningLogFormatFlagName            = "log-format"
	planningLogLevelFlagName             = "log-level"
	planningPolyrepoFlagName             = "polyrepo"
	planningQuietParserFlagName          = "quiet-parser"
	releaseStrictFlagName                = "strict"
	requireReleaseFlagName               = "require-release"
	returnAddressDefaultText             = ""
	returnEndpointFlagName               = "return-endpoint"
	returnEndpointUsageText              = "release and run: reachable HTTPS address of this run's first-node return channel"
	returnListenFlagName                 = "return-listen"
	returnListenUsageText                = "release and run: local host:port binding for the return channel (defaults to the address in --return-endpoint)"
	unsupportedDistributionModeErrorText = "unsupported --distribution-mode value (this build supports compilation, interpretation and hybrid)"
	workerCompiledUsageText              = `Serve the build and validation tasks another node's release assigns to this
one, and nothing else. A choreographer never starts a release, never writes a
release record and never plans: it reads the work addressed to it, runs
what its assignment authorized, and reports back.

The node is described by the execution object of the config file, and three
of its settings are required here: execution.name, which is how this node
recognises the work addressed to it; execution.endpoint, the repository it
reads that work from, which is the repository being released unless the
orchestrator's link names another mailbox; and execution.secretEnv, naming
the environment variable holding the secret every message between the two
nodes is signed with. execution.concurrency (default 1) is how many assigned
command tasks this node takes on at once.

Nothing else about the repository is needed: no packages, no spaces and no
git repository at the root, because a serving node is told what to do rather
than discovering it.

The mailbox is read over Git; claims and results return over the first node's
authenticated HTTPS channel, so a choreographer needs no inbound network address.
--state-dir names the folder this node keeps its object
cache and its record of already-answered work in; two choreographers must not share
one folder for one node name, and the second one to start refuses.
--idle-timeout stops the process after that many seconds with nothing to do,
which is how a node started for one release ends by itself. A SIGINT or a
SIGTERM stops it as soon as what it has claimed is finished; both exits are
exit code 0, because a serving command asked to stop has done its job.`
)
