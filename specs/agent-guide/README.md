# Using dispat as a coding agent

**Version:** 1.11.0-rc.5

**License:** MIT. See [LICENSE](./LICENSE).

The guide shares dispat’s major/minor release line and can receive its own patches. Released `dispat --help` links to the guide and reference documents captured by that CLI’s immutable release tag. Relative documentation links in this guide preserve the same snapshot.

This guide explains how a coding agent should inspect, test, release, and recover a repository managed by dispat. It complements the [dispat documentation](../../packages/docs/docs/getting-started.md) and the repository's own instructions. It does not replace either one.

dispat can execute arbitrary configured scripts and can publish packages, write files, create commits and tags, push branches, and call external services. Treat the repository configuration as executable release policy.

## Choose compatible guide and documentation patches

Start with the guide linked by `dispat --help`. Check the repository's published `specs/agent-guide/v*` releases for a newer guide patch on the installed CLI's major/minor line, and use the latest available patch on that line. For example, a dispat 1.8.2 installation can use guide 1.8.3: guide patches carry their own version and need not match the CLI's patch number. Do not move to a different major/minor line or a prerelease guide for a stable CLI.

Apply the same versioning rule to documentation: check published `packages/docs/v*` releases and use the latest stable documentation patch on the installed CLI's major/minor line. The CLI, guide and documentation have independent patch numbers; none needs a release merely to match another's patch. On the website, select that major/minor documentation version rather than Next or another line.

Keep the configuration and API references from the installed binary's help pinned to that CLI release when checking its implemented contract. Newer guide and documentation patches can correct advice without adding binary capabilities. If published patches cannot be checked, use the pinned references and report which latest patches could not be verified.

## Working rules

1. Read repository instructions and the effective dispat configuration before choosing commands.
2. Use explicit help commands for discovery. A bare `dispat` starts a release.
3. Keep inspection, testing, configuration changes, and release execution distinct.
4. Preserve the user's authorization across the task. Do not repeatedly ask for the same approval, but do not expand it to unrelated packages, configuration, credentials, or destinations.
5. Preview the exact package selection and environment that will be used for release.
6. Run the repository's required full suite before starting a release, then retain release-time checks for rewritten inputs and built artifacts.
7. Preserve command exit codes and inspect diagnostics, including warnings.
8. Treat publishing as non-atomic. Some packages can publish before another package fails.
9. Serialize normal releases, but interrupt a release immediately when an urgent correction invalidates its contents. The lock must not delay cancellation or local repairs. Verify remote outcomes before retrying.
10. Report what ran, what published, what failed or was skipped, and what remains uncertain.
11. Release through the repository's CI/CD workflow. Configure the release path instead of publishing by hand.
12. When a release delegates work to worker nodes, start those nodes through the pipeline and never from your own shell, and read the per-task summary before reporting what a run did.

## Release through CI/CD

Choose the command by the job it performs:

| Command | Effect | Release lock | Where to use it |
| --- | --- | --- | --- |
| `dispat status` | Compute and display the plan; no release stages, publication, or release records. | Never acquires the lock. | Local inspection and CI/CD plan gates; add `--require-release` to distinguish an empty plan. |
| `dispat` or `dispat release` | Execute the same release lifecycle, including configured version edits, checks, builds, publication, and records. | Acquires the remote `dispat-release-lock` before planning, unless locking is explicitly disabled by the unsafe setting. | The CI/CD release job, after its required gates pass. |

Bare `dispat` is an alias for `dispat release`, not a status check. Even `dispat release --require-release` takes the
lock before it can discover that there is nothing to release. Use `dispat status --require-release` for that CI gate.
A status check does not reserve the repository: the release job acquires its own lock and recomputes the plan.

Set up or repair the repository's CI/CD release workflow when that work is authorized. Put the dispat invocation, package selection, credentials, checks, artifact validation, publication, and recording in that workflow. Keep its configuration in version control so reviewers can inspect the release path before it runs.

Do not run a production release from an agent's local shell, publish directly to a registry, create release tags by hand, or upload release assets outside the workflow. Do not use standalone dispat commands or another tool to bypass this rule. A request to release authorizes using the established pipeline; it does not authorize inventing a manual publication path.

If the repository has no release workflow, configure and validate one first. When a release is authorized, trigger that workflow for the intended revision, wait for its required gates, and verify its published artifacts and records. A manual workflow dispatch is acceptable: the pipeline still performs the release. Local read-only planning and tests in disposable repositories remain useful preparation.

If the pipeline fails, inspect the failure, fix the cause in version control, and retry through the pipeline after resolving any ambiguous publication. Do not finish the release manually to make a failed run appear successful. Exceptional destructive recovery requires a documented procedure and specific authorization; it must not become an alternative publication path. See [dispat in CI](../../packages/docs/docs/reference/ci.md).

## Establish the installed contract

Start with commands that cannot start a release:

```sh
dispat --version
dispat --help
dispat status --help
dispat release --help
dispat run --help
```

Command-specific help is the contract for the installed binary's command names and flags. Do not invent a flag from memory or transfer a flag from one command to another. In particular, there is no release `--dry-run` flag. An unfamiliar command word can be interpreted as the shorthand for `dispat run <script>`; it is not a safe discovery mechanism.

Keep three sources aligned:

- installed `--help` for accepted commands and flags;
- the documentation version matching the installed binary for behavior and configuration;
- repository source and tests when working on dispat itself or resolving a documentation conflict.

If they disagree, stop relying on the disputed behavior. Record the binary version and the conflicting statements, inspect the matching implementation when available, and use the safer read-only command. Do not silently combine current website documentation with an older installed binary.

Use `status --require-release` for a lock-free CI plan gate. A release invocation acquires the release lock before planning, including when `--require-release` eventually reports no work.

Useful references are the [CLI reference](../../packages/docs/docs/cli/README.md), [configuration reference](../../packages/docs/docs/configuration/README.md), [Go API](../../packages/docs/docs/api.md), and [diagnostic codes](../../packages/docs/docs/reference/plan-errors.md).

## Resolve the effective repository

dispat discovers `dispat.json`, `dispat.yaml`, `dispat.yml`, then `dispat.toml`, and may ascend to a parent repository configuration. `--root` changes the starting directory. An explicit `--config` selects that file without discovery fallback.

Configuration can be split through `$ref` files and inherited through root, space, and package declarations. Inspect the effective package names, paths, spaces, groups, dependencies, scripts, hooks, parser settings, version policy, release records, tags, remotes, and lock policy. A root file alone may not show a package override.

The default `.env` is resolved from the invocation directory rather than `--root`. `--env-file` replaces that default and may be repeated. Keep the working directory, `--root`, `--config`, and environment files identical between status checks and the eventual release. Never print an entire environment, `.env` file, token, signing key, or credential-bearing command.

Read-only inspection does not authorize configuration repair. Change configuration only when the requested task includes that change. This includes `dispat init`, `dispat compute --write`, release hooks, publish commands, channels, credentials, record settings, and lock settings. Once the user has authorized a particular scoped change, carry it through without unnecessary reconfirmation. Ask again only if new evidence materially changes the target or effect.

## Inspect a polyrepository workspace

A control repository can combine the independent Git histories of linked source repositories. It activates this mode
with `polyrepo: true`, `--polyrepo`, a non-empty `configs` list, or repeatable `--configs` flags. A repository with its
own non-empty `repository` identity activates the same multi-history profile automatically; its optional
`repositories` roster names other peers. If none is
present, keep treating submodule pointer changes as ordinary control-repository changes.

Before trusting a plan, inspect `.gitmodules`, the exact source names, current gitlinks, source checkouts, imported
config paths, and effective commit policy per repository. Every active source must be initialized at the commit pinned
by the control checkout and have complete history. Treat the control repository as the reserved identity `control` and
each source's exact `.gitmodules` name as its identity. A commit is identified by repository plus full SHA; equal SHA
text from two repositories says nothing about ancestry.

Composition captures the source head while it verifies the control gitlink and retains that same full commit as the
plan's initial pin boundary. A head move between composition and history loading is drift, not a new implicit input.

Source configuration is never activated by a central path accidentally entering a nested repository. A file named in
`configs` or `--configs` establishes that source's normal local root, space, and package configuration layers. Keep
`--config`, every import, `--polyrepo`, the control checkout, and all source pins identical between status, gates, and
release.
Control-owned folder configuration and ignore/exclude files retain their ordinary behavior. Source-owned folder
configuration, `.dispatignore`, and `.dispatexclude` apply only through an explicitly imported source configuration.

Review ownership before running scripts. A package must live wholly inside one Git repository. In this mode, a
control-owned wrapper may not point `src`, manifests, changelogs, or version writes across a source boundary. Paths in
an imported configuration are source-local; paths in the control configuration retain their control-relative spelling.
Check the closest Git worktree containing each package and its `src`; an unlisted nested repository is an ownership
error rather than an implicitly discovered source.
Package names form one graph. Spaces and groups from an imported configuration stay repository-local. A shared space
or group declared centrally keeps ordinary monorepository semantics and can span sources. An unqualified CLI space or
group selector can match local declarations in several sources.

Read source commits as local direct intent. A source commit can name only its repository's packages directly; its
propagation may cross the combined dependency graph. An explicit control commit can address packages across the fleet
and is evaluated against that control revision's gitlinks. Do not count a gitlink move as a second package change in
polyrepository mode. Do not resolve precedence between incomparable source commits by timestamp, traversal order, or
SHA spelling; use an applicable control directive or stop on the reported conflict.
An applicable direct channel directive takes precedence over conflicting propagated channels; an unmatched or
otherwise inert direct directive does not resolve their conflict.

Read `repositoryOverrides` before trusting a package list. A source whose `enabled` is `false` takes no part in the
run: it supplies no package, commit, tag, baseline, script, record or lock, it needs no initialized checkout, and the
control space paths inside it stop contributing. The composed and excluded repositories are named in the log line
`polyrepo workspace composed`. An excluded repository still owns its paths, so a control package declared inside one
is an ownership error, and a required dependency on one of its packages is refused by name. Do not restore a
repository's participation to make a plan look complete; ask whether the exclusion was intended.

An `external: true` dependency may name a provider omitted from the current imports. Confirm that the skipped-provider
diagnostic is expected. If that provider is present, review the edge as an ordinary one: it affects cycles,
propagation, ordering, failure blocking, reconciliation, `--consumers`, and scripts.

Cross-repository catch-up needs a proven consumer boundary. Accept automatic reconstruction only from an ordinary
control release checkpoint whose message identifies the exact consumer source tag, whose same commit moves that
consumer gitlink to the tag's commit, and whose other gitlinks pin the incorporated source revisions. A matching SHA
alone is not evidence: the tag may have been attached later, or the same pointer may span several provider moves. If
the checkpoint is missing, custom and ambiguous, or conflicting, require an explicit `repositoryBaselines` entry with
`consumer`, `releaseTag`, repository identity, and reachable revision. Never guess from dates.

Resolve a repository boundary only when that history can affect the tagged package. A tag-only consumer release needs
no control boundary when no applicable control intent affects it. When an explicit control directive does affect the
package and must be ordered across the tag, require the ordinary checkpoint association or an explicit baseline whose
repository is `control`; otherwise stop on `E333`.

Source and control commits remain optional. A present `repositoryOverrides.<source>.commit` completely replaces the
inherited commit object and omitted fields take ordinary defaults; it applies only to centrally configured sources.
An imported source owns its commit policy. A detached source needs `commit.branch` only when it will push a release
branch. No mode forces an empty commit or a control checkpoint.

After publication, verify source recording before the control gitlink advances. If the source tag and revision succeed
but the control checkpoint fails, retain that success and stop its consumers. The error names the source, full revision,
and tag. Inspect the source remote, then explicitly reconcile and commit the normal control gitlink to that durable
revision, or restore the intended pin. Until that repair, another run correctly refuses the unpinned checkout. Do not
republish the source or force an automatic checkpoint.

The release lock must cover the combined fleet, including standalone packages. dispat acquires participating remote
locks in exact repository-name order and releases them in reverse; `control` has no special first position. Within one
process, dispat serializes its native Git transactions per repository, takes several repositories in canonical Git
common-directory order, and gives them back in reverse; it claims no exclusion against other processes. Hooks and
scripts run outside those transactions.
Partial publication remains non-atomic:
preserve successful source tags, block consumers of failures, allow independent work, and re-plan from durable records.
After all `beforeAll` hooks, dispat rechecks the whole fleet. After each `beforePublish` hook, it rechecks that package's
owner plus its transitive provider and shared-version-group repository closure, and any applicable control input. Avoid
direct Git commits and release-tag writes in build or hook scripts: a relevant unplanned change stops that package with
`E330` before publication. A native record step can advance the owner when it exports the exact full lowercase 40- or
64-hex package commit. The outer release shares an admitted pin with already-running nested commands through private
coordination bound to that run and removes it afterwards; this does not become a durable baseline or release ledger.
Packages in one repository publish and record in a deterministic order; repositories can still publish concurrently.
These checks observe drift at the validation points but cannot exclude arbitrary external Git writers after the final
check.
For `--since <control-revision>`, verify that the command projects that revision's gitlinks into one source range per
repository; it must not scan the control history once per consumer or count pointer moves again. Package scripts and
nested hooks share the combined workspace while retaining the triggering source context.

### Identity-linked fleets

Identify the configuration owner before anything else. A non-empty `repository` identity means
there is no control repository: every participant carries its own configuration and release records and is joined to
its neighbours named by the optional `repositories` roster through two-sided submodule links. An omitted or empty
roster is valid for a one-member fleet; a non-empty roster without identity is `E339`. Reading the fleet starts from the repository you are standing in, not
from a central inventory, so record which peer a plan was composed from; the `polyrepo workspace composed` line names
the entry and participants.

Never run `git submodule update --recursive` in a linked fleet. Every peer carries a back-link to the repository that
linked it, deliberately left as an empty folder, and recursion fills it with a second copy of the repository you are
in. Update one declared path at a time. A link path holding no repository is `E330` and names the command that repairs
exactly that link; a run started inside another peer's linked checkout meets the same diagnostic, which is expected
rather than a broken fleet.

`dispat compute --write` in an identity-linked fleet touches Git and the network: it creates a checkout for one half of
each link, declares the other half inside it, and stages `.gitmodules` and the gitlinks in both repositories. It also
writes the missing half of a link only one end declares, which needs no fetch because that checkout already exists.
That is a configuration change and needs the same authorization as any other; read-only inspection does not authorize
it. The command never commits, never removes a link, and never recurses, so review and commit the staged result in
each repository yourself. A roster entry with no url is reported rather than written, and a link URL carrying user
information is refused. A repository with no remote has the half that would point at it withheld with a warning,
because a pin has to be a revision its peer can fetch.

Treat a settlement as a release record. Before a cross-repository consumer publishes, each repository on the route
records the revision of its next hop as an ordinary commit carrying the release's own message, which is why two
`chore(release): <tag>` commits can sit on top of each other. That commit's tree is the only evidence the next plan
reads, so never rewrite, squash, amend or drop one, and never delete a link to tidy a history. A commit that only moved
a fleet link is not a change to any package; do not report one as pending work.

`E338` means the links do not form a tree and a pair is joined twice. Ask which link to remove rather than removing
one: a link records what a release incorporated. `E339` means an identity cannot be trusted, usually because a peer's
own `repository` value, the rosters naming it and the submodule name linking it do not agree. `W332` (a one-sided link)
and `W333` (a roster missing a fleet
member) do not stop a run and are what `dispat compute` repairs; report them rather than working around them.

`--polyrepo=false` on an identity-linked configuration releases that repository alone and composes no fleet. A consumer
released that way writes its release commit without settling any link, so the next fleet-wide plan reports `E333` for
that tag and needs an explicit `repositoryBaselines` tuple. Do not use the flag to get a release past a fleet problem.

For link repair, `dispat compute --topology minimal` is the default: it preserves existing links and proposes the
fewest additions needed to connect the roster. `--topology star` proposes a direct link from the entry repository to
every peer and errors if existing links cannot fit that shape. Neither mode removes or converts links. `--write` and
`--interactive` apply the selected suggestions under the same authorization rule described above.
Known incompatible links are rejected before writes. A peer fetched by `--write` can expose a previously unseen link;
if that link conflicts with the requested topology, the command fails and leaves staged edits for review. Do not
describe or treat that state as an automatic rollback, and do not remove a link without explicit authorization.

## Share configuration across Windows and Linux

A developer can work on Windows while CI releases on Linux. Keep the shared graph, scripts, and lifecycle in `global.yaml`. Give each platform a small configuration that references it and overrides the shell:

```yaml
# global.yaml (repository root)
scripts:
  build: cmake --build build
flow:
  build: [build]
packages:
  app:
    path: app
```

```yaml
# linux.yaml (repository root)
$ref: ./global.yaml
shell: ["/bin/sh", "-c"]
```

```yaml
# windows.yaml (repository root)
$ref: ./global.yaml
shell: ["powershell.exe", "-NoProfile", "-NonInteractive", "-Command"]
```

This example assumes CMake is available and `app/build` has already been configured. The shared build command works in either shell; platform-specific commands may need separate referenced script files. Configure the project's actual test and publish stages before using this as a release configuration.

`$ref` paths are relative to the file containing the reference. Keys beside `$ref` replace whole keys from the referenced object. This is not a deep merge: adding a `scripts` map to `windows.yaml` replaces the entire shared scripts map. Reference and override at the level you intend to change. See [configuration references](../../packages/docs/docs/configuration/refs.md).

Preview locally with `dispat --config windows.yaml status`. On the Linux runner, use `dispat --config linux.yaml status`, then use that same configuration for every gate and the authorized release. The release workflow invokes `dispat --config linux.yaml` after its gates; do not run that production release locally.

Where `/bin/sh` is available, a shared preview wrapper can select the file:

```sh
dispat if OS=Windows_NT \
  --then 'dispat --config windows.yaml status' \
  --else 'dispat --config linux.yaml status'
```

`OS=Windows_NT` compares an environment value; it does not detect the host operating system. The `else` branch here assumes Linux. In dispat 1.8.2, `if` starts the selected command through `/bin/sh -c` without loading a configuration. Only the child dispat invocation reads `windows.yaml` or `linux.yaml`. Setting a PowerShell shell in the Windows file does not remove the outer wrapper's `/bin/sh` requirement. On native Windows without that executable, invoke `dispat --config windows.yaml status` directly. Do not claim Windows execution was tested based on a Linux run with `OS=Windows_NT`.

## Preview the exact release

`status` computes a release plan without running release stages or acquiring the release lock. `preview` renders pending release notes.

```sh
dispat status --log-format json
dispat status --package api --strict --log-format json
dispat preview --package api --changelog --github
```

Use the same selection for preview and release. Explicit `--package` (`-p`), `--space` (`-s`), or `--group` (`-g`) selection overrides implicit narrowing from the current folder. Quote globs, for example `-p '@acme/*'`. `-p '*'` includes standalone packages that a space selection may omit.

Selection narrows which planned packages execute; the complete dependency graph still participates in version calculation. Review warnings about withheld consumers and split version groups. Use `--strict` when those conditions must fail the gate. Do not suppress them by editing configuration or selecting only the convenient half of a group.

`E201` means the selection would release a provider on the very commit a consumer it still owes was released on, which happens after the consumer published a change of its own while the provider failed or was held. Two releases on one commit cannot be ordered afterwards, so `release` refuses before anything runs and `status` prints the same error while exiting `0`. Never move or delete tags to get past it: select the consumer too (`--package <provider>,<consumer>`), or commit first and release the provider alone, after which the next full run catches the consumer up with `W193`. An `E201` reported by a failed run names a `Release-As` remedy instead: the consumer can only be delivered by committing `release(<consumer>)` with exactly that footer.

A version group's sharing rule decides how far a directive reaches. Where its `channels` axis is `independent`, only the packages a directive names enter or leave a prerelease train; the others stay on the line they are on, and no later run catches them up. Name every package you intend to move, and confirm the resulting channels and versions with `dispat status` before releasing. Where its `counter` axis is `independent`, each member continues its own prerelease counter, so members of one group legitimately sit at different counters and a retry releases only the legs that failed.

`preview` cannot predict values produced only by build or publish scripts. A successful status is evidence about the plan, not proof that scripts, credentials, registries, or artifacts will succeed.

## Keep tool-heavy pipelines reproducible

When a release pipeline needs many toolchains, prefer containerized build and test stages with pinned tool versions. On a runner that supports it, use Docker-in-Docker (DinD) so dispat can run those stages through a dedicated Docker daemon. The runner must provide the daemon and its required permissions; selecting an image alone does not configure DinD. If the runner already supplies a suitable Docker or remote BuildKit service, use that service. See the [official Docker image guidance](https://hub.docker.com/_/docker).

Configure BuildKit cache import and export for ephemeral CI runners. Use `--cache-from` and `--cache-to` with the CI cache backend or a dedicated registry cache, scoped by package and branch to avoid competing writes. Keep credentials in BuildKit secrets, outside image layers. Check both a cold run and a cached run: cache availability must not determine correctness or replace release records. See [Docker's cache backend documentation](https://docs.docker.com/build/cache/backends/).

## Preserve structured logs and exit status

Use `--log-format json` when a program or agent consumes dispat logs. Parse each JSON line and its diagnostic `code`; do not scrape colored console text. `--log-level debug` is useful for configuration resolution. Review trace output before sharing it because scripts and environments can expose sensitive data.

The logging flag does not convert help, version output, rendered release notes, or arbitrary configured scripts into JSON. Capture stderr and the original process status. Avoid `|| true` and pipelines that replace dispat's status with a parser's status.

For `status` and `release`:

| Exit | Meaning |
| --- | --- |
| `0` | The command succeeded. Without `--require-release`, the plan may be empty. |
| `1` | Planning, configuration, execution, refusal, interruption, or recording failed. A release may already have published packages. |
| `2` | The command line is invalid. |
| `3` | `--require-release` found nothing to release. |

Helper commands and configured scripts can return their own exit codes. Check their command-specific help.

A lock-free CI plan gate can distinguish no work from failure:

```sh
rc=0
dispat status --require-release --log-format json \
  > dispat-status.jsonl 2> dispat-status.stderr || rc=$?
case "$rc" in
  0) printf '%s\n' 'Release work is pending.' ;;
  3) printf '%s\n' 'Nothing to release.' ;;
  *) cat dispat-status.stderr >&2; exit "$rc" ;;
esac
```

## Test before and during release

The status gate answers whether work is pending. A separate full-suite gate checks the proposed revision across the workspace, including consumers that did not change directly.

Use the repository's declared scripts and CI workflow. A typical dispat sweep is:

```sh
dispat run tests --since all
```

`tests` is a configured script name, not a built-in command. A package without that script may do nothing. Confirm that repository-wide lint, type checking, unit, integration, and packaging checks are actually represented. Run from the repository root or use an explicit selection so the current directory does not narrow the suite.

`--on-error continue` lets dependent and independent script work continue after a failure, but the overall `dispat run` still fails. It is useful for collecting results, not for weakening a gate. Preserve cleanup with an `always` condition in CI when the suite temporarily edits manifests or links.

When consumers must exercise provider code from the checkout, use the ecosystem's existing workspace mechanism. If the repository temporarily rewrites dependency links, do so in a disposable checkout, clean them up even after failures, and verify cleanup. A release does not automatically remove local links. pnpm repositories should normally retain their existing `workspace:*` declarations; those are persistent workspace policy, not temporary redirects.

Keep three layers of evidence:

| Layer | Placement | Purpose |
| --- | --- | --- |
| Full workspace suite | Before release starts | Exercise the proposed checkout and cross-package behavior. |
| Package checks | After version and lockfile reconciliation | Exercise the inputs the release actually uses. |
| Artifact smoke checks | After build and before publish | Exercise the exact files that will be published. |

Version reconciliation may change manifests, dependency ranges, replacement text, generated files, and lockfiles after the pre-release suite. Put checks that require the final reconciled inputs in `flow.beforeBuild` or later. `flow.postVersion` runs before lockfile synchronization and cannot validate the final installation.

For binaries, invoke the newly built artifact by its explicit path. Check its version, executable permissions, platform and architecture, runtime dependencies, and at least one bounded representative operation. Unpack archives or install packages into a temporary environment to catch missing files. Record which target platforms actually executed.

Publish the same artifact that passed. If signing, packing, or another transform changes the delivered artifact, validate the result of that transform. A rebuild inside `publish` breaks the evidence from an earlier smoke test.

Finish artifact identity, version, digest, install, destination, and channel checks before the publish stage. The
publication command must be the final external action in the publish stage. Do not follow a successful upload with a
registry read, integrity or version lookup, status gate, or channel mutation: a failure there converts an accepted
immutable upload into a failed package leg before dispat can write its native records. After the publication command
succeeds, return success immediately so dispat can write its native release records. Forward the command's stdout and
stderr at their proper severity, and propagate a nonzero publication exit.

Recurring findings from the ecosystem review suggest these integration checks. Follow the references for evidence and case-specific limits.

| Finding | Integration check | References |
| --- | --- | --- |
| Local builds can hide missing packaged files or published dependencies. | Test the final archive in a clean consumer across supported runtimes; keep native checks in build scripts. | [Artifact tests](../../packages/docs/docs/examples/release-integration.md#test-the-distributed-artifact) |
| Consumers may need a provider’s published artifact rather than its local build. | Set the provider’s publication boundary from what consumers actually fetch; verify the exact version and platform. | [Build boundaries](../../packages/docs/docs/examples/release-integration.md#choose-the-build-boundary) |
| One package can partially publish across registries, assets and channels. | Declare the required destinations and file/platform set; propagate failures and reconcile receipts and artifact identity before retrying. | [Partial release](../../packages/docs/docs/examples/single-package.md#one-package-can-still-have-a-partial-release) |
| Mutable tags, download pointers and workflow searches can select different inputs on retry. | Retain the planned version, source revision, artifact digest and exact workflow invocation; preserve published tags. | [Release identity](../../packages/docs/docs/examples/release-integration.md#keep-the-release-tied-to-its-input-commit), [workflow completion](../../packages/docs/docs/examples/release-integration.md#make-success-mean-available-to-the-next-stage) |
| Generated release output and editable release intent are separate records. | Preserve pending notes and dependency policy during migration; review merge text and use `Edits:` for unreleased corrections. | [Authoring](../../packages/docs/docs/examples/npm.md#moving-release-intent-out-of-changeset-files), [corrections](../../packages/docs/docs/examples/npm.md#edit-pending-notes-without-rewriting-shared-history) |
| A skill, specification or manual includes more than its root document. | Validate referenced files and the delivered artifact; preserve native revision schemes, compatibility policy and publication tools. | [Document artifacts](../../packages/docs/docs/examples/document-artifacts.md) |

## Pass artifacts through script outputs

Scripts in a stage sequence can append `NAME=value` lines to the file named by `DISPAT_OUTPUT`. dispat injects accumulated values into later sequences as `DISPAT_OUTPUT_<NAME>`.

At the end of a build script:

```sh
binary="$PWD/dist/my-cli"
test -f "$binary" || exit 1
printf 'BINARY=%s\n' "$binary" >> "${DISPAT_OUTPUT:?Missing dispat output file}"
```

In a later `postBuild` or `beforePublish` script:

```sh
binary=${DISPAT_OUTPUT_BINARY:?Build did not export BINARY}
test -x "$binary" || exit 1
"$binary" --version
```

Printing the assignment to stdout or exporting a shell variable does not populate the next sequence. Outputs are captured after the whole sequence completes, so a value appended during one command is not immediately available as `DISPAT_OUTPUT_*` to the next command in that same sequence. Use a shell variable within one script or consume the captured value in a later hook.

An output's presence does not prove its producing sequence succeeded. Preserve the original failure and require the smoke gate to pass. Output values are paths or strings, not transported file contents; files must remain accessible to later scripts and containers. See [script outputs](../../packages/docs/docs/reference/environment.md#script-outputs).

## Select script work deliberately

`dispat run <name>` sweeps selected packages in dependency order. It resolves the script through package, space, then root configuration and runs it in each package's folder.

```sh
# Current release window and transitive consumers
dispat run tests --consumers

# Commits in HEAD~1..HEAD and their consumers
dispat run tests --since HEAD~1 --consumers

# Every package, regardless of changes
dispat run tests --since all
```

`HEAD~1` covers only the latest commit. In CI, use the workflow's actual base revision for a multi-commit push and ensure the checkout contains it. Successful script sweeps do not advance release tags or remove packages from the release window.

`dispat exec <name>` runs one declared script once. `--for` chooses the configuration subject; `--in` chooses the working directory. They are independent. Use `--fallback` only when package-to-space-to-root script lookup is intended. `--env both` can add computed plan variables, but it does not reconcile versions, produce artifacts, or run surrounding release hooks.

```sh
dispat exec tests --for pkg:core --in pkg:core --fallback
```

`dispat if` executes shell text chosen by a condition. A false condition without `--else` succeeds, so make a missing required prerequisite fail explicitly.

`dispat for` executes shell text sequentially for each item. Selecting packages does not change directories, and `--do 'tests'` asks the shell for an executable named `tests`; it does not resolve a dispat script. Prefer `dispat run` for dependency scheduling and package script lookup.

All helpers can run mutating or publishing scripts. They are execution tools, not previews. Their `--on-failure` handlers can replace the original exit status, so a successful notification handler must not hide a failed gate.

See [`run`](../../packages/docs/docs/cli/run.md), [`exec`](../../packages/docs/docs/cli/exec.md), [`if`](../../packages/docs/docs/cli/if.md), and [`for`](../../packages/docs/docs/cli/for.md).

## Understand release intent

dispat uses Conventional Commits: Monorepo Extension (CCME). Read the repository's parser settings and the [commit reference](../../packages/docs/docs/reference/commits.md) before predicting versions.

| Commit | Default intent |
| --- | --- |
| `fix(api): handle an empty response` | Patch `api`. |
| `feat(api,web): add pagination` | Minor bump for both packages. |
| `feat(api)!: remove an endpoint` | Breaking change for `api`. |
| `feat(api)^: add a consumer-facing field` | Release `api` and direct consumers. |
| `feat(api)^^: update a shared protocol` | Reach transitive consumers. |
| `feat(api)%beta: introduce an endpoint` | Put the addressed package on beta. |

Explicit scopes name configured packages. With no scope, ownership can derive from changed files. Propagation and channel syntax change release intent; do not add them as decorative prose. Accurate scopes matter for both releases and `--since` script sweeps.

### Confirm release intent before authoring commits

Confirm the expected packages, versions, channels, and consumer propagation with the operator before authoring final release intent.
Include documentation and guide patches in that confirmation when they are part of the requested release. An explicit
operator selection already given for the task is confirmation; do not repeatedly ask for it. Ask for clarification
when the scope is ambiguous or a read-only plan selects unexpected packages, versions, channels, consumers, or
major/minor version-group changes. An inspection or implementation request does not authorize publication unless the
operator also authorized the release.

Compose distinct release records as distinct CCME units using the package scopes and propagation described below.
Validate the proposed message with `dispat diagnostics`, create the commit through the repository's authoring path,
then run a full-workspace `dispat status` and the exact CI selection with the same root, configuration, environment,
and credentials policy the release will use. Review every diagnostic: status exit `0` does not prove strict release
policy or publication will succeed. Confirm that the complete package set, exact versions, channels, consumers, and
version-group side effects match the operator-approved intent.

If the plan differs, correct the release intent with supported CCME corrections and inspect it again. Do not edit
manifests, create or move tags, narrow CI selection, or change release configuration merely to hide the mismatch.
Keep preparatory commits as non-releasing records when the repository requires one final CCME release commit. Ensure
commit CI selects every module touched by the pushed commit range, including release-plan integration tests.

### Write commits for the task

Apply the repository's versioning policy to documentation and guide commits too. In this repository, corrections and
new explanations of existing behavior use patch intent (`fix:`), preserving the shared major/minor line and each
document package's independent patch counter. Do not use `feat:` merely because a page or section is new: that would
request a minor release for the version group. Do not hand-edit version declarations to match the CLI. Update the
targeted `versioned_docs/version-MAJOR.MINOR` pages alongside their current copies when the correction applies to
that line, then inspect the plan to confirm the intended document patches and any required consumer releases.

When the edited files share the same release record (the same change description, type, and directives), omit the
scope. This applies whether the files belong to one package or several: let file ownership select the packages.
For example, use `fix: handle an empty response` when that record describes the change in every affected package.
Check the configured package paths and `src` boundaries so the intended files resolve to their owners.

When the task requires different release records for different packages, write explicitly scoped CCME units, one
per distinct record, separated by a line containing `---`. One Git commit can carry all of them:

```text
fix(api): handle an empty response

---

feat(web): show an empty-state message
```

Here `api` receives the fix record and `web` receives the feature record. Use configured package names in the scopes;
do not assign every package the same record when the task requires different release intent or notes.

Add a separate test commit only when the push task contains more than one commit. For a single-commit push task,
include the test changes with the implementation and describe the task's main change in the commit message. Do not
split out a test commit merely to turn a single-commit task into a multi-commit push. In a multi-commit task, a
separate test commit may describe actual test changes using the repository's configured type (normally `test:`).
This is a commit organization rule; required tests still run for every task. A `---` separator adds a CCME unit,
not another Git commit, so multiple records in one commit do not satisfy the multi-commit condition.

Manifest versions alone do not determine the next release. Tags, commits, dependency propagation, channels, groups, and parser policy contribute to the plan. Use `status` for the computed result. The implemented syntax is described by the [CCME 2.0.0 specification](https://github.com/yohimik/dispat/blob/specs/ccme-spec/v2.0.0/specs/ccme-spec/SPEC.md).

The current CCME 3 specification includes the optional polyrepository Git profile, external VCS adapters, and explicit
rollback. Current dispat implements the polyrepository profile as planning and repository behavior without changing
its published message grammar. It does not execute `rollback(scope)` or accept the adapter/rollback configuration.
Do not use those specification-only examples as runtime commands; the parser can treat the directive as an unknown
type without withdrawing anything.

### Choose when dependents release

Releasing a package often also requires releasing the packages that depend on it, especially when they bundle its
code, embed its binary, or ship an application built from it. Determine that project's policy before authoring the
commit. A package scope selects the starting package; propagation describes which dependents (consumers) also need
a release. It follows edges toward consumers, not toward the starting package's own dependencies (providers).

Before choosing `^`, `^^`, or `^+N`:

1. Read recent Git history, including full commit bodies and release records, for comparable changes to the package
   and its consumers. Use `git log --format=full` and path-specific history together: an explicitly scoped release
   directive can address a package without changing files in its folder. Check how prior releases handled consumers.
2. Read the effective dispat configuration, including referenced files, package dependencies, version groups, and
   `parser.propagation.depth`, `bump`, and `kinds`. Check manifests for the actual consumer graph. A configured depth
   can already propagate a commit without a caret, and excluded dependency kinds are not traversed.
3. Read repository instructions, package READMEs, release documentation, and CI/CD workflows for requirements to
   rebuild, republish, or deploy dependents after a provider changes. Use history as evidence; reconcile it with the
   current configuration and documented policy instead of copying an old directive blindly.

| Directive | Reach | Use when |
| --- | --- | --- |
| `^` | Direct consumers, one dependency edge away. | The changed package and its immediate consumers need releases, but consumers further away do not. |
| `^^` | All transitive consumers along eligible dependency edges. | Project policy requires dependent releases through the whole chain, such as a library change that must reach shipped applications. |
| `^+N` (or `+N`) | Consumers up to `N` edges away; replace `N` with a number, for example `^+2`. | The project has a deliberate release boundary after a known number of dependency layers. |
| No directive | The configured default depth; `0` if unset. | That default matches the task's required reach. With depth `0`, this unit adds no consumer releases. |

For a chain where `sdk` depends on `core`, `cli` depends on `sdk`, and `image` depends on `cli`, a change to `core`
with `^` reaches `sdk`; `^+2` reaches `sdk` and `cli`; `^^` reaches all three. In `^+2`, the explicit depth replaces
the caret's implied depth of one. Do not combine `^^` with a finite depth: `^^+2` contradicts its all-consumers intent.
The consumer bump defaults to patch and can be configured or stated separately, for example `^minor+2`.

Keep the scope rule above: use `fix^^: refresh bundled dependency` when file ownership identifies the starting
packages and they share a release record. Use scoped units when the task needs different records or propagation
policies for different packages. If the project normally releases dependents with a provider, preserve that reach
unless the task or documented policy justifies a narrower release.

Validate the message with `dispat diagnostics`, then inspect `dispat status` after the source commit. Check the
whole plan and the exact CI/CD selection: `--package core` does not automatically select every consumer reached by
`^^`. Include the required consumers or use the appropriate space, group, or full-workspace selection. Review holds,
channel constraints, and version-group effects; a directive alone does not guarantee every intended package will
publish. Run the authorized release through CI/CD with the verified selection.

## Build commit tooling around diagnostics

When making an editor integration, commit-message generator, or other tool for dispat commits, use `dispat diagnostics` to check the proposed text. Do not duplicate CCME parsing in the tool or create a temporary commit just to obtain diagnostics.

```sh
dispat diagnostics --config dispat.yaml --log-format json 'fix(core): close the stream'
```

Omit `--config` to check with parser defaults without a repository. An explicit file applies the project's parser settings and normal configuration validation. Pass the message as one literal process argument; do not interpolate generated text into a shell command. Capture the exit status, JSON diagnostics on stdout, and errors on stderr. Exit `0` means no parser errors, `1` means parser or configuration failure, and `2` means invalid usage. Warnings remain visible and need review.

Check `dispat diagnostics --help` for availability on the installed version. Syntax validation does not validate package selection or authorize a release; inspect `dispat status` before proceeding. See the [diagnostics reference](../../packages/docs/docs/cli/diagnostics.md).

## Check source commit messages

When the installed `dispat commit --help` lists authoring flags, stage only the intended files and use
`dispat commit -m "fix(core): close the stream"` to validate the proposed message before Git creates the commit.
A file or editor can supply the message instead. Read the [command reference](../../packages/docs/docs/cli/commit.md)
for supported options. Older dispat versions provide only the per-package release-step command under this name.

Parser errors in any unit reject the source commit; warnings follow the repository's parser policy. Successful syntax
validation does not prove that scopes identify the intended packages or that the release has approval. Inspect the
plan with `dispat status` and keep the CI gates. Do not add `--tag` or `--push` to a source-authoring invocation, and do
not use a standalone release-step command to bypass CI/CD.

## Know the gating boundary

Planning chooses versions before package work starts. Native `autoVersion` reconciliation, an optional `flow.version`, and lockfile synchronization prepare inputs. The version stage is also called the propagate stage: `autoPropagate` and `flow.propagate` (with `flow.beforePropagate` and `flow.postPropagate`) are the same settings under that name, one spelling per object, and the stage still reports itself as `version` at runtime. Build produces artifacts. Login authenticates once per configured space. Publish performs external publication. Native records then create the configured tags, changelog, GitHub release, commit, and push. Announce happens after publication.

| Work | Failure behavior |
| --- | --- |
| `run.beforeAll` | Gates the task graph; the release lock has already been acquired. |
| Package hooks through `postVersion`, native reconciliation, and lock sync | Gate that package before build. |
| `beforeBuild`, `build`, `postBuild` | Gate that package before publish. |
| Space `login` | Gates publishes in that space. |
| `beforePublish`, `publish` | Last gating package work. |
| `postPublish`, announce hooks, and `announce` | Warn after a successful publish. |
| `onFail`, `onSkip`, and post-run/commit/push hooks | Warn-only observers. |
| Native records and pushes | Critical after-publish work; failure makes the command fail while preserving the published state. |

Independent graph branches can continue after a package fails. Consumers may be skipped. A hook named `before...` is not necessarily gating: commit, push, and announce observers occur after publication. Put required artifact and policy checks before `publish` so the publish stage contains only its authorized external action.

Choose the retry policy for each destination before releasing. When a publish response is lost or native records are
missing, reconcile the exact package, version, artifact digest, source revision, and destination outside the publish
script before another workflow run. If the destination does not permit an accepted version to be reused, recover
forward with a new version when the original upload has no dispat record. Verification that remote bytes match is
evidence for the operator's recovery decision. It does not authorize a manual release tag, channel mutation, local
publication, or any bypass of the reviewed CI workflow.

After `publish` succeeds, failure to write a tag, changelog, GitHub record, release commit, or push does not make the package unpublished. Treat the result as published with incomplete recording. See [release steps](../../packages/docs/docs/reference/releasing/steps.md) and [recovery](../../packages/docs/docs/reference/releasing/recovery.md).

## Run a distributed release

A repository whose root configuration carries an `execution` object with a non-empty `workers` list runs its build stages, and sometimes its publish stages, on other machines. The machine the release is started on keeps the release locks, the plan, every publication authorization and every release record. Read [distributed execution](../../packages/docs/docs/distributed-execution.md) before working on such a repository, and its security section before touching the signing secret or the repository's branch and tag rules. The mailbox is the repository being released: a worker link with no `endpoint` reaches the remote the release takes its lock on, a worker started in a checkout of the repository with no `endpoint` of its own polls the same remote, and an `endpoint` names another mailbox.

Before a release:

1. Read the `execution` object of the entry configuration, and the pipeline's `--worker name` or `--worker name=endpoint` flags, which add links for one invocation. It is a node-startup setting, so a space, a package, an imported configuration or a linked repository never contributes one, and a peer's own object is ignored.
2. Confirm the pipeline starts the worker nodes. `dispat worker` belongs in a CI job or a cluster workload beside the release job, not in your shell: a node you start locally executes the commands of any authentic assignment with your machine's credentials.
3. Confirm the signing secret reaches every node from the secret store, and never read, print or copy its value. Report a secret written literally in a configuration file rather than fixing it silently.
4. Run `dispat status`. With workers configured it names the plan it fixed in one `plan fixed` line, which is the plan every assignment of the run states.
5. Expect the run to refuse rather than proceed when any release-lock bypass is configured, when `commit.verify` is off, when the release remote a link with no endpoint reaches has a push URL carrying a credential, when a node cannot be reached, or when a package's `buildPlatforms` no node satisfies. These are `E225`, and no command runs: the first three are refused before any lock, the last two after the locks and the plan, before any hook or stage, with the locks given back.

After a release, read the per-task summary rather than the last line of the log. One line per task states where it ran, and keeps four outcomes apart: computation, outputs, publication and recording. A completed task is not a released package. The `execution metrics` line beside it is measurement only and is not a claim about speed.

An unknown publication outcome is the one result that needs a person:

1. `E228` with the category `publication-unknown` means the run authorized a publication on a node and cannot establish what became of it. The package failed at its publish stage, its dependents are blocked, and no second attempt is made in that run.
2. Report it as unknown. Never describe it as published or as failed, and never retry the publish by hand.
3. The run retains the uncertain publication's authorization ref, even if the node acknowledged after starting publish. It may also retain that repository's release lock when the node never acknowledged. Do not delete a retained lock. Clearing it is an operator's decision and needs the documented order: read the run id from the lock tag's `run` line, list the `dispat-worker-*` refs of the repository (`git ls-remote --heads origin 'dispat-worker-*'`) and take the ones whose messages carry that run, find the authorization with no result beside it, confirm on that node that the publisher has stopped, check the registry for the version, delete the run's refs, and only then delete the lock tag.
4. A run may end with a lock retained and no release record at all, so check the registry rather than the tags.

Before deleting a coordination ref, verify that its current tip still belongs to that run's authenticated chain. Investigate a changed or unauthenticated tip instead of treating it as cleanup residue.

`dispat run` uses the same pool when the invocation has links: each package's task runs where its `runOnly` places a build, and the folders the entry configuration's `runOutputs` names for the script are carried back and merged into the orchestrator's checkout. A sweep takes no release lock and records nothing, so it neither waits for a release nor stops one; a sweep with no link runs every task locally, as it always has.

Leftover `dispat-worker-*` branches on the repository, or on a mailbox a link names, are coordination state, not release records. A completed run deletes its own; an uncertain publication retains its authorization as evidence until the operator follows the recovery order above. They carry full source and command text, so report other leftovers for cleanup after checking that their current tips still belong to the run's authenticated chain and no process uses them. Investigate a changed or unauthenticated tip instead of deleting it as residue. Never write a `dispat-worker-*` branch yourself, and never run a release from a node whose `execution.role` is `worker`: a node refuses a message nobody signed, and a worker refuses the release with `E226`.

A worker state folder has one serving process, and its `worker.lock` holds that process's id. A crashed worker's
successor takes the folder over from the id of a process that is gone, or from its own id left by an earlier process
with that id (a container worker is process 1 on every restart), so do not delete or rename `worker.lock` to restart a
worker; start it normally and investigate an `E225` refusal if another process still owns the folder. A serving worker
that finds another process's id in the file stops with `E225`.

## Respect the release lock

`dispat release` and bare `dispat` use a remote `dispat-release-lock` tag to serialize releases for the repository. During normal work, do not start a second release, push unrelated commits to the release branch, move release tags, delete the lock, or disable locking while a run may be active. Urgent corrections follow the interruption procedure below; the lock is not a reason to let a known-invalid release finish.

Check the configured release remote. For a repository using `origin`:

```sh
git ls-remote --refs origin refs/tags/dispat-release-lock
```

A returned ref can be active or abandoned. An empty result is only a point-in-time observation, and a lookup error proves nothing. dispat's lock acquisition resolves the race.

If a lock appears abandoned, inspect its annotated tag and confirm that the owning process or CI job has ended. Delete it only with authorization for that cleanup. Lock deletion can admit a concurrent publisher and is not routine recovery. See [release locking](../../packages/docs/docs/reference/releasing/release-lock.md).

The lock covers dispat releases, not ordinary Git pushes or other deployment tools. dispat may recover a push that arrives during release by merging it (`W242`) or preserve conflicts on a `release-conflicts/...` branch (`W243`). Review either outcome even if the command exits successfully. If the remote already contains a release tag that the run would overwrite, recovery refuses; update the checkout and plan again.

### Interrupt a release for an urgent correction

If the user identifies incorrect release content, corrects the required scope, requests removal, or otherwise invalidates the revision being released, stop that release immediately. Do not wait for normal completion, defer the correction to a later patch, or treat earlier release authorization as permission to ship the rejected revision. A passing test suite does not override the corrected requirements.

Warn the user when the correction requires interrupting an actual queued or running release: identify the run, explain why it must stop, and note that some outputs may already be published and need reconciliation. Give this brief warning before cancellation when possible; do not delay an urgent stop or turn the warning into another approval request. Local edits or changes that do not invalidate the active release do not by themselves require interruption or a warning.

1. Cancel the exact queued or running workflow through its CI controls immediately. Cancellation and local preparation of the fix do not require the release lock to disappear or another confirmation of the user's instruction.
2. Confirm that the publishing process has stopped, including cancellation finalization. If cancellation is not taking effect, use the CI provider's supported force-cancellation control and report what remains active. Pushing a fix alone does not change the checkout an existing publisher is using.
3. Inspect the destinations, tags, release records and branch. Cancellation can still finish records for an already-published package. Report what shipped; never infer that cancellation undid publication.
4. Apply the urgent correction and any explicitly requested deletion or history repair once the publisher has stopped. Preserve unrelated work and verify remote outcomes. Reuse the user's existing authorization for the specified cleanup. Clear a leftover lock only after verifying its owner has stopped and cleanup is authorized; deleting an active lock does not stop its publisher.
5. Validate the corrected content and recompute the release plan before considering a new CI run. Respect any instruction to stop publishing or keep changes local. Do not automatically restart the cancelled revision or ship an incomplete intermediate patch.

## Recover from failure or interruption

A release is a saga rather than an all-or-nothing transaction. Tags and other configured records let the next plan skip packages already recorded as published and continue with remaining work.

After a failure:

1. Capture the command exit status, diagnostics, package summaries, and CI logs.
2. Identify packages marked published, failed, skipped, cancelled, or held.
3. Inspect local and remote release records.
4. For any publish whose response was lost or whose record is missing, query the registry or destination directly.
5. Repair the cause and rerun status with the same configuration and environment.
6. Retry through the CI/CD release workflow only after resolving ambiguous publications and any stale lock.

Before a retry that can create release commits or revert failed package edits, inspect the selected package folders and configured `commit.include` paths for pre-existing work. Commit or stash only the intended owner's changes through the repository's normal workflow; never let an automatic release commit capture unrelated files.

A nonzero release exit never proves that nothing published. Conversely, a successful remote upload without its dispat record can cause an unsafe duplicate attempt unless the publish script verifies the existing artifact.

On interruption, dispat stops new package work but completes durable records for publishes it knows succeeded, whenever the interrupt arrives, including during `postAll`, a commit or push hook, or the release push. A user hook that is running when the interrupt arrives is stopped and later hooks are skipped. The records get five minutes from the interrupt; a recording that outlasts them is a critical failure. Inspect the final summaries and records rather than inferring state from where the visible log stopped.

Do not manually rewrite tags, force a push, reset files, or invoke standalone record commands as a shortcut unless the recovery procedure specifically requires that action and it is within the authorized task. Standalone `github`, `commit`, `writer`, `replacer`, auto-edit, `install`, and `self-update` commands can mutate local or external state.

## External approval and optional policy tools

dispat itself does not define who may authorize a release. Follow the repository's governance, CI environment protections, branch rules, registry controls, and the user's established authorization. An inspection request does not authorize publishing. A clear request to perform a scoped release remains valid through ordinary pipeline retries and necessary implementation steps unless the scope or consequences materially change. Keep those retries within the CI/CD release path described above.

Some environments add a policy tool that reviews shell commands, including [HOL Guard](https://github.com/hashgraph-online/hol-guard). If one is installed, follow its actual local policy and approval results. Do not assume it exists, claim that it is integrated with dispat, or use helper and standalone commands to route around it. Policy enforcement supplements dispat's release lock and pipeline gates; it does not replace them.

## Final report

For inspection or testing, report:

- installed dispat version and the configuration/root used;
- package selection and release plan, including warnings;
- exact suites and artifact targets exercised;
- skipped checks, platform gaps, and unresolved documentation conflicts;
- whether any files or external state changed.

For a release, also report:

- command exit status and package outcomes;
- packages and versions confirmed published;
- tags, commits, pushes, changelog, and GitHub records created or missing;
- lock cleanup status;
- ambiguous registry outcomes, recovery branches, and required operator action.

Do not describe a repository-wide release as rolled back merely because one package failed. Do not claim an artifact was tested when only source tests or metadata inspection ran. State the limits of the evidence plainly.

## Reference map

- [CLI reference](../../packages/docs/docs/cli/README.md)
- [Configuration](../../packages/docs/docs/configuration/README.md)
- [Go API](../../packages/docs/docs/api.md)
- [Packages](../../packages/docs/docs/configuration/packages.md), [spaces](../../packages/docs/docs/configuration/spaces.md), and [dependencies](../../packages/docs/docs/configuration/dependencies.md)
- [Scripts](../../packages/docs/docs/configuration/scripts.md) and [run hooks](../../packages/docs/docs/configuration/run-hooks.md)
- [Environment and script outputs](../../packages/docs/docs/reference/environment.md)
- [Partial releases](../../packages/docs/docs/reference/releasing/partial-releases.md)
- [Release lock](../../packages/docs/docs/reference/releasing/release-lock.md)
- [Distributed execution](../../packages/docs/docs/distributed-execution.md) and the [`execution` object](../../packages/docs/docs/configuration/execution.md)
- [The worker command](../../packages/docs/docs/cli/worker.md)
- [Recovery](../../packages/docs/docs/reference/releasing/recovery.md)
- [Diagnostic codes](../../packages/docs/docs/reference/plan-errors.md)
- [CCME specification](https://github.com/yohimik/dispat/blob/specs/ccme-spec/v2.0.0/specs/ccme-spec/SPEC.md)

## Distribution

This guide is versioned independently from the dispat binary. Version `0.0.0` is the unreleased baseline; its first release is `1.0.0` and uses the independent tag `specs/agent-guide/v1.0.0`. Later guide versions do not imply a matching dispat CLI version. The guide is distributed under [MIT](LICENSE).
