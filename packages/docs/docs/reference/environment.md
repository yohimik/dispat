# Script environment variables

Stage scripts receive package details, planned versions, and outputs from earlier scripts through environment
variables. Values are applied in this order:

1. the parent environment, which the [`.env` file](../configuration/dotenv.md) fills in where nothing else set a name;
2. any [static `env` variables the configuration sets](../configuration/env.md);
3. the variables in the tables below, computed from the release plan.

The order matters when a name appears twice. dispat places the static variables first, so a computed `DISPAT_*`
variable always wins. A static value referring to one, such as `custom_$DISPAT_VERSION`, expands against the values in
these tables before the script starts.

The same variables expand in [record text](../configuration/records.md#variables-in-record-text). A changelog footer
and a publish script name a release the same way.

These variables come from the release plan, so a release ordinarily produces them. Run
[`dispat exec --env both`](../cli/exec.md#what-the-script-gets) to compute the same plan on demand. This lets you run a
script written against `$DISPAT_VERSION` on its own without releasing anything.

[`dispat install --pipe`](../cli/install.md#assets-that-are-not-binaries) has two separate variables: `DISPAT_ASSET`
is the verified download's path, and `DISPAT_ASSET_NAME` is its release filename. These exist only inside the piped
command and do not require a release plan.

## Package identity

| Variable | Example | Meaning |
| --- | --- | --- |
| `DISPAT_PACKAGE` | `core` | Package name. |
| `DISPAT_SPACE` | `libs` | Space name. |
| `DISPAT_GROUP` | `platform` | The versioning group whose versions move together. It is **Unset** when the package versions on its own. Read the note below. |

## Versions

| Variable | Example | Meaning |
| --- | --- | --- |
| `DISPAT_OLD_VERSION` | `1.2.3` | The version the package last published. It holds `0.0.0` for a first release. |
| `DISPAT_NEW_VERSION` | `1.3.0-beta.4` | Version being released. It includes the version, channel, and counter in SemVer spelling. |
| `DISPAT_VERSION` | `1.3.0` | The core version alone. It holds `MAJOR.MINOR.PATCH` with the channel and counter stripped. |
| `DISPAT_MAJOR` | `1` | The first number of `DISPAT_VERSION` on its own. |
| `DISPAT_MINOR` | `3` | The second number of `DISPAT_VERSION` on its own. |
| `DISPAT_PATCH` | `0` | The third number of `DISPAT_VERSION` on its own. |
| `DISPAT_STABLE_BASELINE` | `1.2.3` | The last release with no prerelease component. dispat computes versions from this baseline. |
| `DISPAT_BASELINE` | `1.3.0-beta.3` | The latest baseline. It holds the newest tag of any kind, including prereleases. It is **Unset** when the package has never released. Read the note below. |
| `DISPAT_BUMP` | `minor` | Holds `none`, `patch`, `minor`, or `major`. It holds `none` on a channel-only release. |

## Channels and tags

| Variable | Example | Meaning |
| --- | --- | --- |
| `DISPAT_CHANNEL` | `beta` | The channel being released on. It holds `stable` or a prerelease identifier. |
| `DISPAT_OLD_CHANNEL` | `stable` | The channel of the previous release. This distinguishes a graduation from an ordinary release. |
| `DISPAT_COUNTER` | `4` | The prerelease counter of the version being released. It is **Unset** on a stable release. |
| `DISPAT_OLD_COUNTER` | `3` | The prerelease counter of the previous release. It is **Unset** when the previous release was stable. |
| `DISPAT_IS_PRERELEASE` | `true` | Holds `true` when `DISPAT_NEW_VERSION` carries a prerelease component. Use this to choose a dist-tag. |
| `DISPAT_TAG` | `core@v1.3.0-beta4` | The tag that will be created on success. It includes the name, version, channel, and counter rendered with the space's `tagFormat`. |
| `DISPAT_SEMVER_TAG` | `core@1.3.0-beta.4` | The same name, version, channel, and counter under the normative `{name}@{version}` SemVer format. This ignores what `tagFormat` encodes. A script can rely on this spelling across spaces. |
| `DISPAT_TAG_VERSION` | `1.3.0-beta4` | Version, channel, and counter as the space's `tagFormat` spells them. Read the note below. |

## Stage context and outputs

| Variable | Example | Meaning |
| --- | --- | --- |
| `DISPAT_STAGE` | `build` | What is currently running. The spellings are listed below the table. |
| `DISPAT_EXECUTION_NODE` | `build-a` | The name of the worker node this script is running on. It is set only on a [delegated stage](../distributed-execution.md) or a delegated [`dispat run` task](../distributed-execution.md#running-scripts-on-workers) and is absent everywhere else, so `${DISPAT_EXECUTION_NODE+x}` asks whether the work left the orchestrator. |
| `DISPAT_OUTPUT` | *(a temp file path)* | Where the script appends `NAME=value` or `DISPAT_OUTPUT_NAME=value` lines. This lets you [export outputs](#script-outputs) for everything that runs after it. |
| `DISPAT_OUTPUT_<NAME>` | *(exported value)* | One variable per accumulated [script output](#script-outputs). `DISPAT_OUTPUTS` lists the exported names and is set even when empty. |
| `DISPAT_OUTPUT_SOURCE_<NAME>` | `core:build` | The script that exported or last re-exported `<NAME>`. It holds `<package>:<stage>`, or `<space>:login` for a login export. |
| `DISPAT_EXPORT_GITHUB` | `/pkg/dist/app.tgz` | Set once a script [exported it](#script-outputs). This is the opt-in for the package's GitHub release, and its value is the asset list. It travels under its full name and stays out of `DISPAT_OUTPUTS`. |

## Reading version and stage values

`DISPAT_STAGE` carries `sign`, `version` or `propagate`, `build`, `test`, `publish`, or `announce` for a built-in
stage script, and the stage's own name for a [stage the flow defines](../configuration/spaces.md#stages-of-your-own)
(`scan` for `flow.scan`). It holds the hook's name (`beforeSign`, `beforeBuild`, `beforeTest`, `postPublish`,
`postAll`, ..., and `beforeScan` or `postScan` for a defined stage) for a hook. It holds
`login` for the login, `syncLock` for an [`autoVersion.syncLock`](../configuration/autoversion.md) script, and
`run:<name>` for [`dispat run <name>`](../configuration/spaces.md#scripts-and-dispat-run). The version stage runs under
the name its configuration gave it: a stage configured with `flow.propagate` or `autoPropagate` sees `propagate`, and its
hooks see `beforePropagate` and `postPropagate`; one configured with the version keys sees `version`, `beforeVersion`
and `postVersion`. [Two names for one block](../configuration/autoversion.md#two-names-for-one-block) gives the order
in which the keys decide.

`DISPAT_TAG_VERSION` is the version section of `DISPAT_TAG` without the name and its decoration. It has no `v` prefix
and no path. It equals `DISPAT_NEW_VERSION` under formats that leave the prerelease inside `{version}`.

`DISPAT_MAJOR`, `DISPAT_MINOR`, and `DISPAT_PATCH` split `DISPAT_VERSION` so a script never has to cut a version string
apart. You write a moving series tag from them. This matches how container images write `image:1` and `image:1.4`
beside `image:1.4.2`. All three are always set, even when they are `0`. They describe the *core* version. A
`1.3.0-beta.4` release reports `1`, `3`, and `0`, which is the stable release the train is heading for.

`DISPAT_BASELINE` is what the computed version must exceed. dispat reads the channel from it. It is unset, not empty,
for a package that has never released. Use `${DISPAT_BASELINE+x}` to detect a first release. When set, it equals
`DISPAT_OLD_VERSION`.

`DISPAT_GROUP` identifies the package's versioning group, independently of its package and space names. It holds the
[`versionGroups`](../configuration/spaces.md#versioning-groups) entry the package joined, or the space's own name
where a space versions as a group. A group may span spaces and a space may version independently, so neither of the
other two variables can answer it. It is unset, not empty, for a package that versions on its own. Use
`${DISPAT_GROUP+x}` to ask whether this release moves anything else with it. [`dispat for -g`](../cli/for.md) iterates
over the groups themselves and exports the same variable.

`DISPAT_OLD_VERSION` and `DISPAT_STABLE_BASELINE` differ only on a prerelease train. A package on `1.3.0-beta.1` whose
last stable release was `1.2.3` reports both. The first is what it shipped, and the second is what dispat computes the
next version from.

The counters are left **unset**, not empty, when there is nothing to report. A shell's `${DISPAT_COUNTER+x}`
distinguishes a stable release from a prerelease whose counter happens to be empty text. An empty string cannot do
this. An exact `Release-As` may carry more than the bare number. A version like `2.0.0-rc.1.hotfix` reports a counter
of `1.hotfix`, because the counter is everything after the channel.

## Workspace data

Every stage receives two package listings: all workspace packages and the providers whose versions this package is
picking up. Use them when updating dependency ranges, embedding versions in artifacts, or choosing publish tags.

Both listings address packages through a `<KEY>`. This is the package name uppercased with everything outside
`[A-Z0-9]` replaced by `_`. For example, `@acme/ui` becomes `_ACME_UI`, and a package named `MyLib` becomes `MYLIB`.
This happens because a package name may contain bytes a variable name cannot. The raw name always travels in the
`_NAME` field, spelled exactly as the package is named, so `DISPAT_WORKSPACE_MYLIB_NAME` reads `MyLib`. To look up a
package by name, write `for k in $DISPAT_WORKSPACE_PACKAGES`, compare `_NAME`, and read the fields. Two names can produce
the same key, such as `core-utils` and `core.utils`. The first in plan order keeps it; the other is omitted with a
warning. Rename one package to resolve the collision.

The **workspace listing** covers **every** workspace package with the version it will carry at the end of the run. This
is its planned version where it is releasing, or its baseline otherwise.

```sh
DISPAT_WORKSPACE_PACKAGES="CORE UTILS"        # keys in plan order: for k in $DISPAT_WORKSPACE_PACKAGES
DISPAT_WORKSPACE_CORE_NAME="core"             # the raw package name
DISPAT_WORKSPACE_CORE_VERSION="1.3.0"
DISPAT_WORKSPACE_CORE_CHANNEL="stable"
DISPAT_WORKSPACE_CORE_RELEASING="true"
```

Use the full workspace listing when a custom version script reconciles dependency ranges. A provider may have
published in an earlier run while its consumer failed. In that case, `_RELEASING=false` can still accompany a version
newer than the consumer's declared range. Considering every workspace dependency lets the consumer catch up.

The **updated-provider listing** covers every provider whose version this package picks up in this run:

```sh
DISPAT_UPDATED_PACKAGES="CORE"                # empty (not unset) when nothing was updated
DISPAT_UPDATED_CORE_NAME="core"
DISPAT_UPDATED_CORE_SPACE="libs"
DISPAT_UPDATED_CORE_OLD_VERSION="1.2.3"
DISPAT_UPDATED_CORE_NEW_VERSION="1.3.0"
DISPAT_UPDATED_CORE_CHANNEL="stable"
DISPAT_UPDATED_CORE_TAG="core@1.3.0"          # the provider's own tagFormat spelled it
```

`_TAG` holds the release tag the provider's new version is published under. The provider's space owns the
[`tagFormat`](../configuration/versions.md#tagformat) that renders it, so a consumer cannot derive the tag from the
name and the version alone. Read it whenever a script has to name the provider's release rather than its version, such
as fetching that tag or linking to it. A [step command](./releasing/steps.md) invoked inside a run reads the same
field, which is how a record it writes keeps the `dependencyLink: auto` links of its dependencies section. Every field
of a listed key is set even when it is empty, so addressing a key the listing names never reads an unset variable.

The updated-provider list includes providers releasing alongside the package, even when they did not trigger its
version bump. The consumer still needs their new versions. This is separate from
[propagation](./commits.md#inline-directives), which must be requested by a commit or configuration. A provider released by an *earlier* run whose consumer leg failed is listed
too. Its `OLD_VERSION` equals `NEW_VERSION` because the version is already out, and this run is only now picking it up.
A provider that is not releasing at all is not listed, since it published nothing to pick up.

dispat filters out providers that failed or were skipped, because their versions were never released. The listing is
resolved per stage. A provider can fail between this package's build and its publish. Each stage sees the truth of its
own moment. If a package has providers to pick up and none of them survive, the *version* script specifically does not
execute. There is nothing to sync manifests to.

Before graduating a consumer to `stable`, graduate any provider it needs from a prerelease channel, or keep the
consumer on prerelease until that provider is ready.

## Release notes data

Every stage and hook of a package also receives its release notes. dispat groups them exactly as the changelog file and
the GitHub release group their sections. Units bumping major are breaking changes, minor are features, and patch are
fixes:

```sh
DISPAT_BREAKING_CHANGES="drop the old API"    # one headline per line
DISPAT_FEATURES="add streaming
add retries"
DISPAT_FIXES="close a leak"
```

Each line contains one unit description, in history order. An empty group is set to an empty string. Commit bodies
are omitted so each entry stays on one line; the bodies still appear in the changelog and GitHub release. The groups
follow the [release-notes windowing](../configuration/records.md#changelog). On a prerelease, they carry only the
release's own changeset. On a stable release, including a graduation, they carry the whole pending window. The
dependencies section travels the same way:

```sh
DISPAT_DEPENDENCIES="core: 1.2.3 -> 1.3.0"    # one "name: old -> new" line per live provider update
```

This matches the changelog's rendering. `From` equals `To` on a catch-up, whose provider version is already out. The
`DISPAT_UPDATED_*` listing carries the same data field by field for scripts that want it addressable. The
[`flow.announce`](../configuration/spaces.md#flowannounce) stage is the natural consumer. Like every listing, the
variables reach every stage, keeping scripts movable.

## Script outputs

Every per-package script and hook receives `DISPAT_OUTPUT`. This includes the stages, their hooks, the announce frame,
`onFail`/`onSkip`, and the space's `login`. It holds the path of a file. You can append `NAME=value` lines to it,
`GITHUB_OUTPUT`-style, to export values for everything that runs after it. You can write the name bare or already
carrying the `DISPAT_OUTPUT_` prefix. Both spellings address the same output:

```sh
echo "DISPAT_OUTPUT_IMAGE_DIGEST=$(docker inspect --format='{{index .RepoDigests 0}}' img)" >> "$DISPAT_OUTPUT"
echo "IMAGE_DIGEST=..." >> "$DISPAT_OUTPUT"     # the same output, bare spelling
echo "DISPAT_EXPORT_GITHUB=$PWD/dist/app.tgz $PWD/dist/SHA256SUMS" >> "$DISPAT_OUTPUT"
```

Outputs accumulate along the package's [stage graph](../configuration/spaces.md#the-order-of-the-stages). Every
script and hook of a stage after the exporting one receives each export as `DISPAT_OUTPUT_<NAME>`, and two stages that
may run at once never see each other's exports. The release records and the outcome scripts `onFail` and `onSkip` see
every export of the stages before them, so a notifier can report with them.

Two more variables come along. `DISPAT_OUTPUTS` lists the exported names, space-separated. It is set but empty when
nothing was exported. `DISPAT_OUTPUT_SOURCE_<NAME>` names the script each export came from. It holds
`<package>:<stage>`, like `core:build` or `base:run:lint`, or `<space>:login` for the login.

How far an export travels depends on which script made it. Hooks export exactly like stage scripts. A `beforeBuild`
export reaches the build, the publish, and everything after. The **login script's** exports are space-scoped. They
reach every package of the space from its publish stage onward, because that stage waits for the login. In
[`dispat run`](../configuration/spaces.md#scripts-and-dispat-run), outputs additionally carry across packages from a
provider's script to its consumers'. In a release run, they stay within the package. A consumer's release scripts read
a provider's new version from the `DISPAT_UPDATED_*` listing rather than from the provider's outputs.

Re-exporting a name overrides its earlier value and source. This works like a shell re-assignment.

The name must be a valid environment variable name. Other `DISPAT_`-prefixed names are reserved, so an export cannot
shadow the `DISPAT_*` environment. A malformed line fails a release-gating sequence. It only warns in a warn-only
sequence. Values exported before a sequence fails remain available to `onFail`.

### Opt in to a GitHub release

Export **`DISPAT_EXPORT_GITHUB`** to opt the package into the
[GitHub recorder](../configuration/records.md#github) and provide its attachment list. Packages without this export
are skipped unless configuration opts them in through `github.allPackages`.

Its value is a whitespace-separated list of absolute paths to existing files. dispat uploads each as an asset of the
release and names it after the file. `$PWD` inside a script resolves to the package folder, which makes absolute paths
easy. An empty value creates the release with no assets. dispat skips an invalid entry, like a relative path, a missing
file, or a directory, with a warning. The release and the sound entries still go through.

Unlike ordinary outputs, this export travels to later scripts under its full name. Appending to it reads
`echo "DISPAT_EXPORT_GITHUB=$DISPAT_EXPORT_GITHUB $PWD/more.tgz" >> "$DISPAT_OUTPUT"`. It does not appear in
`DISPAT_OUTPUTS`. It reaches later scripts as a plain environment variable. The [`dispat github`](../cli/github.md)
step command run from one of them reads the same opt-in and the same asset list out of its own environment.

A stage that ran on a [worker node](../distributed-execution.md) exports the same way, and its exports are merged onto
the release when the node reports back, so a later stage on any machine reads them as usual. One thing cannot travel:
`DISPAT_EXPORT_GITHUB` names paths in the checkout the node made for that task, and that checkout is gone when the
task ends. Export assets from a stage that runs on the orchestrator.

### Choose the release commit

Export **`PACKAGE_<KEY>`** when the release tag should point to a commit produced by your script. The `<KEY>` is the exporting package's own key
under the [scheme above](#workspace-data). A release script that exports `PACKAGE_<KEY>=<commitHash>` pins the
package's release to that commit. dispat creates the tag there instead of at HEAD, or at the release commit in
[commit mode](../configuration/records.md#commit). The package's GitHub release carries the hash as its commit and
`target_commitish`. This is meant for packages whose release scripts produce their own commit, like a subtree push or a
generated repository, that the tag should point at. Like any output, it reaches later scripts as
`DISPAT_OUTPUT_PACKAGE_<KEY>`:

```sh
echo "PACKAGE_CORE=$(git rev-parse HEAD)" >> "$DISPAT_OUTPUT"
```

### Nested commands in source-history mode

In source-history mode, the outer release shares the admitted full source commit through private per-run coordination. Nested dispat commands validate that live pin against the same control root, configuration, exact source
identity, and current Git head. The outer release commits a source revision a moment before it publishes it, so a
nested command reads the pin, then the head, then the pin again, and accepts only a head that two agreeing pin reads
admit. Any other reading is repeated for up to a few seconds before the checkout is refused with `E330`, and an
interrupt ends that wait. This lets a command in an already-running sibling script observe a native record completed
elsewhere in the same run. An explicit `--root`, `--config`, `--configs`, or `--polyrepo` suppresses inherited
coordination. The private data is removed when the release ends and is never a release record, baseline, tag payload,
or input to a later plan.

## Run outcome data

The [run-level hooks](../configuration/run-hooks.md) additionally receive the run's outcome. dispat renders these with
the same `<KEY>` scheme:

```sh
DISPAT_PUBLISHED_PACKAGES="CORE"              # keys of published packages
DISPAT_FAILED_PACKAGES="UI"                   # keys of failed packages
DISPAT_SKIPPED_PACKAGES="APP"                 # keys of packages skipped because a provider failed
DISPAT_CANCELLED_PACKAGES=""                  # keys of packages an interrupted run never ran
DISPAT_UNPLANNED_PACKAGES="UTILS"             # keys of packages the plan did not release
                                              # (unchanged, or held by Release-As: none)
DISPAT_RESULT_CORE_NAME="core"                # one block per planned package
DISPAT_RESULT_CORE_STATUS="published"         # published / failed / skipped / cancelled
DISPAT_RESULT_CORE_OLD_VERSION="1.2.3"
DISPAT_RESULT_CORE_NEW_VERSION="1.3.0"
DISPAT_RESULT_CORE_CHANNEL="stable"
DISPAT_RESULT_UI_FAILED_STAGE="build"         # failed packages only
DISPAT_RESULT_APP_BLOCKED_BY="ui"             # skipped packages only: the provider to blame
```

The five list variables are set even when empty. A shell for-loop iterates zero times instead of reading an unset
variable. `_FAILED_STAGE` and `_BLOCKED_BY` are **unset** when there is nothing to report. Unplanned packages carry no
`DISPAT_RESULT_*` block. Their state is the workspace listing's baseline entry. A `cancelled` status means the run was
interrupted, like a Ctrl-C or a killed CI job, before the package ran. Nothing about it failed. The next run picks it
up unchanged.

## Size

One package costs ~250 bytes across its listing variables. A 500-package monorepo puts roughly 125 KB into each
script's environment. Each individual variable is tiny, so the ceiling is total environment size. This is ~2 MiB on
Linux and 1 MiB on macOS. That leaves room for a few thousand packages. Large workspaces should also allow for the runner's inherited environment and check their operating system's limits.
