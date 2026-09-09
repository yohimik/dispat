# Using Dispat as a coding agent

**Version:** 1.9.0

**License:** MIT. See [LICENSE](./LICENSE).

The guide shares Dispat’s major/minor release line and can receive its own patches. Released `dispat --help` links to the guide and reference documents captured by that CLI’s immutable release tag. Relative documentation links in this guide preserve the same snapshot.

This guide explains how a coding agent should inspect, test, release, and recover a repository managed by Dispat. It complements the [Dispat documentation](../../packages/docs/docs/getting-started.md) and the repository's own instructions. It does not replace either one.

Dispat can execute arbitrary configured scripts and can publish packages, write files, create commits and tags, push branches, and call external services. Treat the repository configuration as executable release policy.

## Choose the guide patch

Start with the guide linked by `dispat --help`. Check the repository's published `specs/agent-guide/v*` releases for a newer guide patch on the installed CLI's major/minor line, and use the latest available patch on that line. For example, a Dispat 1.8.2 installation can use guide 1.8.3: guide patches carry their own version and need not match the CLI's patch number. Do not move to a different major/minor line or a prerelease guide for a stable CLI.

Keep the configuration and API references from the installed binary's help pinned to that CLI release. A newer guide may link to a newer documentation snapshot, so it does not establish that the older binary supports a feature. If you cannot check published guide releases, use the pinned guide and report that the latest patch could not be verified.

## Working rules

1. Read repository instructions and the effective Dispat configuration before choosing commands.
2. Use explicit help commands for discovery. A bare `dispat` starts a release.
3. Keep inspection, testing, configuration changes, and release execution distinct.
4. Preserve the user's authorization across the task. Do not repeatedly ask for the same approval, but do not expand it to unrelated packages, configuration, credentials, or destinations.
5. Preview the exact package selection and environment that will be used for release.
6. Run the repository's required full suite before starting a release, then retain release-time checks for rewritten inputs and built artifacts.
7. Preserve command exit codes and inspect diagnostics, including warnings.
8. Treat publishing as non-atomic. Some packages can publish before another package fails.
9. Respect the release lock and verify ambiguous remote outcomes before retrying.
10. Report what ran, what published, what failed or was skipped, and what remains uncertain.
11. Release through the repository's CI/CD workflow. Configure the release path instead of publishing by hand.

## Release through CI/CD

Set up or repair the repository's CI/CD release workflow when that work is authorized. Put the Dispat invocation, package selection, credentials, checks, artifact validation, publication, and recording in that workflow. Keep its configuration in version control so reviewers can inspect the release path before it runs.

Do not run a production release from an agent's local shell, publish directly to a registry, create release tags by hand, or upload release assets outside the workflow. Do not use standalone Dispat commands or another tool to bypass this rule. A request to release authorizes using the established pipeline; it does not authorize inventing a manual publication path.

If the repository has no release workflow, configure and validate one first. When a release is authorized, trigger that workflow for the intended revision, wait for its required gates, and verify its published artifacts and records. A manual workflow dispatch is acceptable: the pipeline still performs the release. Local read-only planning and tests in disposable repositories remain useful preparation.

If the pipeline fails, inspect the failure, fix the cause in version control, and retry through the pipeline after resolving any ambiguous publication. Do not finish the release manually to make a failed run appear successful. Exceptional destructive recovery requires a documented procedure and specific authorization; it must not become an alternative publication path. See [Dispat in CI](../../packages/docs/docs/reference/ci.md).

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
- repository source and tests when working on Dispat itself or resolving a documentation conflict.

If they disagree, stop relying on the disputed behavior. Record the binary version and the conflicting statements, inspect the matching implementation when available, and use the safer read-only command. Do not silently combine current website documentation with an older installed binary.

Use `status --require-release` for a lock-free CI plan gate. A release invocation acquires the release lock before planning, including when `--require-release` eventually reports no work.

Useful references are the [CLI reference](../../packages/docs/docs/cli/README.md), [configuration reference](../../packages/docs/docs/configuration/README.md), [Go API](../../packages/docs/docs/api.md), and [diagnostic codes](../../packages/docs/docs/reference/plan-errors.md).

## Resolve the effective repository

Dispat discovers `dispat.json`, `dispat.yaml`, `dispat.yml`, then `dispat.toml`, and may ascend to a parent repository configuration. `--root` changes the starting directory. An explicit `--config` selects that file without discovery fallback.

Configuration can be split through `$ref` files and inherited through root, space, and package declarations. Inspect the effective package names, paths, spaces, groups, dependencies, scripts, hooks, parser settings, version policy, release records, tags, remotes, and lock policy. A root file alone may not show a package override.

The default `.env` is resolved from the invocation directory rather than `--root`. `--env-file` replaces that default and may be repeated. Keep the working directory, `--root`, `--config`, and environment files identical between status checks and the eventual release. Never print an entire environment, `.env` file, token, signing key, or credential-bearing command.

Read-only inspection does not authorize configuration repair. Change configuration only when the requested task includes that change. This includes `dispat init`, `dispat compute --write`, release hooks, publish commands, channels, credentials, record settings, and lock settings. Once the user has authorized a particular scoped change, carry it through without unnecessary reconfirmation. Ask again only if new evidence materially changes the target or effect.

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

`OS=Windows_NT` compares an environment value; it does not detect the host operating system. The `else` branch here assumes Linux. In Dispat 1.8.2, `if` starts the selected command through `/bin/sh -c` without loading a configuration. Only the child Dispat invocation reads `windows.yaml` or `linux.yaml`. Setting a PowerShell shell in the Windows file does not remove the outer wrapper's `/bin/sh` requirement. On native Windows without that executable, invoke `dispat --config windows.yaml status` directly. Do not claim Windows execution was tested based on a Linux run with `OS=Windows_NT`.

## Preview the exact release

`status` computes a release plan without running release stages or acquiring the release lock. `preview` renders pending release notes.

```sh
dispat status --log-format json
dispat status --package api --strict --log-format json
dispat preview --package api --changelog --github
```

Use the same selection for preview and release. Explicit `--package` (`-p`), `--space` (`-s`), or `--group` (`-g`) selection overrides implicit narrowing from the current folder. Quote globs, for example `-p '@acme/*'`. `-p '*'` includes standalone packages that a space selection may omit.

Selection narrows which planned packages execute; the complete dependency graph still participates in version calculation. Review warnings about withheld consumers and split version groups. Use `--strict` when those conditions must fail the gate. Do not suppress them by editing configuration or selecting only the convenient half of a group.

`preview` cannot predict values produced only by build or publish scripts. A successful status is evidence about the plan, not proof that scripts, credentials, registries, or artifacts will succeed.

## Preserve structured logs and exit status

Use `--log-format json` when a program or agent consumes Dispat logs. Parse each JSON line and its diagnostic `code`; do not scrape colored console text. `--log-level debug` is useful for configuration resolution. Review trace output before sharing it because scripts and environments can expose sensitive data.

The logging flag does not convert help, version output, rendered release notes, or arbitrary configured scripts into JSON. Capture stderr and the original process status. Avoid `|| true` and pipelines that replace Dispat's status with a parser's status.

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

Use the repository's declared scripts and CI workflow. A typical Dispat sweep is:

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

## Pass artifacts through script outputs

Scripts in a stage sequence can append `NAME=value` lines to the file named by `DISPAT_OUTPUT`. Dispat injects accumulated values into later sequences as `DISPAT_OUTPUT_<NAME>`.

At the end of a build script:

```sh
binary="$PWD/dist/my-cli"
test -f "$binary" || exit 1
printf 'BINARY=%s\n' "$binary" >> "${DISPAT_OUTPUT:?Missing Dispat output file}"
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

`dispat for` executes shell text sequentially for each item. Selecting packages does not change directories, and `--do 'tests'` asks the shell for an executable named `tests`; it does not resolve a Dispat script. Prefer `dispat run` for dependency scheduling and package script lookup.

All helpers can run mutating or publishing scripts. They are execution tools, not previews. Their `--on-failure` handlers can replace the original exit status, so a successful notification handler must not hide a failed gate.

See [`run`](../../packages/docs/docs/cli/run.md), [`exec`](../../packages/docs/docs/cli/exec.md), [`if`](../../packages/docs/docs/cli/if.md), and [`for`](../../packages/docs/docs/cli/for.md).

## Understand release intent

Dispat uses Conventional Commits: Monorepo Extension (CCME). Read the repository's parser settings and the [commit reference](../../packages/docs/docs/reference/commits.md) before predicting versions.

| Commit | Default intent |
| --- | --- |
| `fix(api): handle an empty response` | Patch `api`. |
| `feat(api,web): add pagination` | Minor bump for both packages. |
| `feat(api)!: remove an endpoint` | Breaking change for `api`. |
| `feat(api)^: add a consumer-facing field` | Release `api` and direct consumers. |
| `feat(api)^^: update a shared protocol` | Reach transitive consumers. |
| `feat(api)%beta: introduce an endpoint` | Put the addressed package on beta. |

Explicit scopes name configured packages. With no scope, ownership can derive from changed files. Propagation and channel syntax change release intent; do not add them as decorative prose. Accurate scopes matter for both releases and `--since` script sweeps.

Manifest versions alone do not determine the next release. Tags, commits, dependency propagation, channels, groups, and parser policy contribute to the plan. Use `status` for the computed result. The implemented syntax is described by the [CCME 2.0.0 specification](https://github.com/yohimik/dispat/blob/specs/ccme-spec/v2.0.0/specs/ccme-spec/SPEC.md).

CCME 3.0.0 specifies external VCS adapters and explicit rollback ahead of implementation. Dispat 1.8.x does not
execute `rollback(scope)` or accept the new adapter/rollback configuration. Do not use specification-only examples as
runtime commands; an older parser can treat the directive as an unknown type without withdrawing anything.

## Check source commit messages

When the installed `dispat commit --help` lists authoring flags, stage only the intended files and use
`dispat commit -m "fix(core): close the stream"` to validate the proposed message before Git creates the commit.
A file or editor can supply the message instead. Read the [command reference](../../packages/docs/docs/cli/commit.md)
for supported options. Older Dispat versions provide only the per-package release-step command under this name.

Parser errors in any unit reject the source commit; warnings follow the repository's parser policy. Successful syntax
validation does not prove that scopes identify the intended packages or that the release has approval. Inspect the
plan with `dispat status` and keep the CI gates. Do not add `--tag` or `--push` to a source-authoring invocation, and do
not use a standalone release-step command to bypass CI/CD.

## Know the gating boundary

Planning chooses versions before package work starts. Native `autoVersion` reconciliation, an optional `flow.version`, and lockfile synchronization prepare inputs. Build produces artifacts. Login authenticates once per configured space. Publish performs external publication. Native records then create the configured tags, changelog, GitHub release, commit, and push. Announce happens after publication.

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

Independent graph branches can continue after a package fails. Consumers may be skipped. A hook named `before...` is not necessarily gating: commit, push, and announce observers occur after publication. Put required checks before or in `publish`, preferably earlier when possible.

Publish scripts must be safe to retry for the same package, version, and artifact. Return success for an existing version only after verifying that it is the exact intended artifact. Never turn every "already exists" response into success: publication may have completed remotely just before a local error or lost response.

After `publish` succeeds, failure to write a tag, changelog, GitHub record, release commit, or push does not make the package unpublished. Treat the result as published with incomplete recording. See [release steps](../../packages/docs/docs/reference/releasing/steps.md) and [recovery](../../packages/docs/docs/reference/releasing/recovery.md).

## Respect the release lock

`dispat release` and bare `dispat` use a remote `dispat-release-lock` tag to serialize releases for the repository. Do not start a second release, push unrelated commits to the release branch, move release tags, delete the lock, or disable locking while a run may be active.

Check the configured release remote. For a repository using `origin`:

```sh
git ls-remote --refs origin refs/tags/dispat-release-lock
```

A returned ref can be active or abandoned. An empty result is only a point-in-time observation, and a lookup error proves nothing. Dispat's lock acquisition resolves the race.

If a lock appears abandoned, inspect its annotated tag and confirm that the owning process or CI job has ended. Delete it only with authorization for that cleanup. Lock deletion can admit a concurrent publisher and is not routine recovery. See [release locking](../../packages/docs/docs/reference/releasing/release-lock.md).

The lock covers Dispat releases, not ordinary Git pushes or other deployment tools. Dispat may recover a push that arrives during release by merging it (`W242`) or preserve conflicts on a `release-conflicts/...` branch (`W243`). Review either outcome even if the command exits successfully. If the remote already contains a release tag that the run would overwrite, recovery refuses; update the checkout and plan again.

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

A nonzero release exit never proves that nothing published. Conversely, a successful remote upload without its Dispat record can cause an unsafe duplicate attempt unless the publish script verifies the existing artifact.

On interruption, Dispat stops new package work but completes durable records for publishes it knows succeeded. User hooks may be skipped during detached finalization. Inspect the final summaries and records rather than inferring state from where the visible log stopped.

Do not manually rewrite tags, force a push, reset files, or invoke standalone record commands as a shortcut unless the recovery procedure specifically requires that action and it is within the authorized task. Standalone `github`, `commit`, `writer`, `replacer`, auto-edit, `install`, and `self-update` commands can mutate local or external state.

## External approval and optional policy tools

Dispat itself does not define who may authorize a release. Follow the repository's governance, CI environment protections, branch rules, registry controls, and the user's established authorization. An inspection request does not authorize publishing. A clear request to perform a scoped release remains valid through ordinary pipeline retries and necessary implementation steps unless the scope or consequences materially change. Keep those retries within the CI/CD release path described above.

Some environments add a policy tool that reviews shell commands, including [HOL Guard](https://github.com/hashgraph-online/hol-guard). If one is installed, follow its actual local policy and approval results. Do not assume it exists, claim that it is integrated with Dispat, or use helper and standalone commands to route around it. Policy enforcement supplements Dispat's release lock and pipeline gates; it does not replace them.

## Final report

For inspection or testing, report:

- installed Dispat version and the configuration/root used;
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
- [Recovery](../../packages/docs/docs/reference/releasing/recovery.md)
- [Diagnostic codes](../../packages/docs/docs/reference/plan-errors.md)
- [CCME specification](https://github.com/yohimik/dispat/blob/specs/ccme-spec/v2.0.0/specs/ccme-spec/SPEC.md)

## Distribution

This guide is versioned independently from the Dispat binary. Version `0.0.0` is the unreleased baseline; its first release is `1.0.0` and uses the independent tag `specs/agent-guide/v1.0.0`. Later guide versions do not imply a matching Dispat CLI version. The guide is distributed under [MIT](LICENSE).
