# Script environment variables

Every `DISPAT_*` variable a stage script receives, and what each one holds. A script sees three layers, in this order:

1. the parent environment, which the [`.env` file](../configuration/dotenv.md) fills in where nothing else set a name;
2. any [static `env` variables the configuration sets](../configuration/env.md);
3. the variables in the table below, computed from the release plan.

The order matters when a name appears twice. The static variables are placed first, so a computed `DISPAT_*` variable
always wins, and a static value referring to one, such as `custom_$DISPAT_VERSION`, is expanded against the values in
this table before the script starts.

The same variables expand in [record text](../configuration/records.md#variables-in-record-text), so a changelog footer
and a publish script name a release the same way.

These variables come from the release plan, so ordinarily a release is what produces them.
[`dispat exec --env both`](../cli/exec.md#what-the-script-gets) computes the same plan on demand, which is how a
script written against `$DISPAT_VERSION` is run on its own without releasing anything.

| Variable                      | Example              | Meaning                                                                                                                                                                                 |
|-------------------------------|----------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `DISPAT_PACKAGE`              | `core`               | Package name.                                                                                                                                                                           |
| `DISPAT_SPACE`                | `libs`               | Space name.                                                                                                                                                                             |
| `DISPAT_OLD_VERSION`          | `1.2.3`              | The version the package last published (`0.0.0` for a first release).                                                                                                                   |
| `DISPAT_NEW_VERSION`          | `1.3.0-beta.4`       | Version being released: version + channel + counter, SemVer spelling.                                                                                                                   |
| `DISPAT_VERSION`              | `1.3.0`              | The core version alone: `MAJOR.MINOR.PATCH`, channel and counter stripped.                                                                                                              |
| `DISPAT_MAJOR`                | `1`                  | The first number of `DISPAT_VERSION`, on its own.                                                                                                                                       |
| `DISPAT_MINOR`                | `3`                  | The second number of `DISPAT_VERSION`, on its own.                                                                                                                                      |
| `DISPAT_PATCH`                | `0`                  | The third number of `DISPAT_VERSION`, on its own.                                                                                                                                       |
| `DISPAT_TAG_VERSION`          | `1.3.0-beta4`        | Version + channel + counter as the space's `tagFormat` spells them; see the note below.                                                                                                 |
| `DISPAT_STABLE_BASELINE`      | `1.2.3`              | The last release with no prerelease component: what versions are computed from.                                                                                                         |
| `DISPAT_BASELINE`             | `1.3.0-beta.3`       | The latest baseline: the newest tag of any kind, prereleases included. **Unset** when the package has never released; see the note below.                                               |
| `DISPAT_BUMP`                 | `minor`              | `none`, `patch`, `minor` or `major`. `none` on a channel-only release.                                                                                                                  |
| `DISPAT_CHANNEL`              | `beta`               | Channel being released on: `stable` or a prerelease identifier.                                                                                                                         |
| `DISPAT_OLD_CHANNEL`          | `stable`             | Channel of the previous release, so a graduation is distinguishable from an ordinary release.                                                                                           |
| `DISPAT_COUNTER`              | `4`                  | Prerelease counter of the version being released. **Unset** on a stable release.                                                                                                        |
| `DISPAT_OLD_COUNTER`          | `3`                  | Prerelease counter of the previous release. **Unset** when the previous release was stable.                                                                                             |
| `DISPAT_IS_PRERELEASE`        | `true`               | `true` when `DISPAT_NEW_VERSION` carries a prerelease component. Handy for choosing a dist-tag.                                                                                         |
| `DISPAT_TAG`                  | `core@v1.3.0-beta4`  | Tag that will be created on success: name + version + channel + counter, rendered with the space's `tagFormat`.                                                                         |
| `DISPAT_SEMVER_TAG`           | `core@1.3.0-beta.4`  | The same name + version + channel + counter under the normative `{name}@{version}` SemVer format, whatever `tagFormat` encodes: the spelling a script can rely on across spaces.        |
| `DISPAT_STAGE`                | `build`              | What is currently running; the spellings are listed below the table.                                                                                                                    |
| `DISPAT_OUTPUT`               | *(a temp file path)* | Where the script appends `NAME=value` (or `DISPAT_OUTPUT_NAME=value`, the same output) lines to [export outputs](#script-outputs) for everything that runs after it.                    |
| `DISPAT_OUTPUT_<NAME>`        | *(exported value)*   | One variable per accumulated [script output](#script-outputs); `DISPAT_OUTPUTS` lists the exported names (set even when empty).                                                         |
| `DISPAT_OUTPUT_SOURCE_<NAME>` | `core:build`         | The script that exported (or last re-exported) `<NAME>`: `<package>:<stage>`, or `<space>:login` for a login export.                                                                    |
| `DISPAT_EXPORT_GITHUB`        | `/pkg/dist/app.tgz`  | Set once a script [exported it](#script-outputs): the opt-in for the package's GitHub release, its value the asset list. Travels under its full name and stays out of `DISPAT_OUTPUTS`. |

`DISPAT_STAGE` carries `version`, `build`, `publish` or `announce` for a stage script; the hook's name (`beforeBuild`,
`postPublish`, `postAll`, ...) for a hook; `login` for the login; `syncLock` for an
[`autoVersion.syncLock`](../configuration/autoversion.md) script; and `run:<name>` for
[`dispat run <name>`](../configuration/spaces.md#scripts-and-dispat-run).

`DISPAT_TAG_VERSION` is the version section of `DISPAT_TAG` without the name and its decoration (no `v` prefix, no
path). It equals `DISPAT_NEW_VERSION` under formats that leave the prerelease inside `{version}`.

`DISPAT_MAJOR`, `DISPAT_MINOR` and `DISPAT_PATCH` split `DISPAT_VERSION` so a script never has to cut a version string
apart. They are what a moving series tag is written from, the way the container images write `image:1` and `image:1.4`
beside `image:1.4.2`. All three are always set, including when they are `0`, and they describe the *core* version: a
`1.3.0-beta.4` release reports `1`, `3` and `0`, the stable release the train is heading for.

`DISPAT_BASELINE` is what the computed version must exceed and where the channel is read from. Because it is unset (not
empty) for a package that has never released, `${DISPAT_BASELINE+x}` detects a first release; when set, it equals
`DISPAT_OLD_VERSION`.

`DISPAT_OLD_VERSION` and `DISPAT_STABLE_BASELINE` differ only on a prerelease train: a package on `1.3.0-beta.1`
whose last stable release was `1.2.3` reports both, because the first is what it shipped and the second is what the next
version is computed from.

The counters are left **unset** (not empty) when there is nothing to report, so a shell's `${DISPAT_COUNTER+x}`
distinguishes "a stable release" from "a prerelease whose counter happens to be empty text", which an empty string
cannot. An exact `Release-As` may carry more than the bare number: `2.0.0-rc.1.hotfix` reports a counter of
`1.hotfix`, since the counter is everything after the channel.

## Workspace data

Every stage additionally receives two per-package listings, readable from any shell without a parser. The version stage
is where manifests are reconciled, but a build baking versions into artefacts and a publish choosing dist-tags read the
same state, and identical environments keep a script movable between stages.

Both listings address packages through a `<KEY>`: the package name uppercased with everything outside `[A-Z0-9]`
replaced by `_` (`@acme/ui` becomes `_ACME_UI`), because a package name may contain bytes a variable name cannot. The
raw name always travels in the `_NAME` field; a lookup by name is `for k in $DISPAT_WORKSPACE_PACKAGES`, compare
`_NAME`, read the fields. Should two names sanitise to the same key (`core-utils` / `core.utils`), the first in plan
order keeps it and the loser is omitted from the listings with a warning; rename one of the pair if you hit this.

The **workspace listing** covers **every** workspace package with the version it will carry at the end of the run:
its planned version where it is releasing, its baseline otherwise.

```sh
DISPAT_WORKSPACE_PACKAGES="CORE UTILS"        # keys in plan order: for k in $DISPAT_WORKSPACE_PACKAGES
DISPAT_WORKSPACE_CORE_NAME="core"             # the raw package name
DISPAT_WORKSPACE_CORE_VERSION="1.3.0"
DISPAT_WORKSPACE_CORE_CHANNEL="stable"
DISPAT_WORKSPACE_CORE_RELEASING="true"
```

The breadth matters. dispat has no manifest model (reconciling declared dependency ranges is the version script's job),
and a correct reconciliation cannot be restricted to "released in the same run": a dependency may have been published by
an *earlier* run whose dependent leg failed, which is exactly the catch-up case. `_RELEASING=false`
with a version newer than the range you declared is that situation. Reconciling against every workspace dependency
closes it, and is a no-op whenever the narrow rule would already have been right.

The **updated-provider listing** covers every provider whose version this package picks up in this run:

```sh
DISPAT_UPDATED_PACKAGES="CORE"                # empty (not unset) when nothing was updated
DISPAT_UPDATED_CORE_NAME="core"
DISPAT_UPDATED_CORE_SPACE="libs"
DISPAT_UPDATED_CORE_OLD_VERSION="1.2.3"
DISPAT_UPDATED_CORE_NEW_VERSION="1.3.0"
DISPAT_UPDATED_CORE_CHANNEL="stable"
```

"Picks up" is deliberately wider than "was bumped by". A provider that releases alongside this package, for its own
reasons and with no propagation between them, is listed: the two ship together and the consumer's manifests still have
to name the new version. That is the ordinary case, because [propagation](./commits.md#inline-directives) reaches
nobody unless a commit or the configuration asks it to. A provider released by an *earlier* run whose consumer leg failed is
listed too, with `OLD_VERSION` equal to `NEW_VERSION`: the version is already out and this run is only now picking it
up. A provider that is not releasing at all is not listed, since it published nothing to pick up.

Providers that failed or were skipped are filtered out (their versions were never released), and the listing is resolved
per stage: a provider can fail between this package's build and its publish, and each stage sees the truth of its own
moment. If a package has providers to pick up and none of them survive, the *version* script specifically is not
executed at all: there is nothing to sync manifests to.

A package released on `stable` whose dependency currently carries a prerelease version is the one case no range can make
honest; the remedy is to graduate the provider too, or not to graduate the consumer yet.

## Release notes data

Every stage and hook of a package also receives its release notes, grouped exactly as the changelog file and the GitHub
release group their sections: units bumping major are breaking changes, minor are features, patch are fixes:

```sh
DISPAT_BREAKING_CHANGES="drop the old API"    # one headline per line
DISPAT_FEATURES="add streaming
add retries"
DISPAT_FIXES="close a leak"
```

Entries are the unit descriptions, newline-separated, in history order; a group with no entries is empty text (set, not
unset), so a line-wise loop iterates zero times. Bodies are omitted (they are multiline prose that would destroy the
line-per-entry contract) and stay in the changelog and the GitHub release. The groups follow the
[release-notes windowing](../configuration/records.md#changelog): on a prerelease they carry only the release's own
changeset, on a stable release (a graduation included) the whole pending window. The dependencies section travels the
same way:

```sh
DISPAT_DEPENDENCIES="core: 1.2.3 -> 1.3.0"    # one "name: old -> new" line per live provider update
```

matching the changelog's rendering (`From` equals `To` on a catch-up, whose provider version is already out); the
`DISPAT_UPDATED_*` listing carries the same data field by field for scripts that want it addressable. The
[`flow.announce`](../configuration/spaces.md#flowannounce) stage is the natural consumer, but like every listing the
variables reach every stage, keeping scripts movable.

## Script outputs

Every per-package script and hook (the stages, their hooks, the announce frame, `onFail`/`onSkip`, the space's
`login`) receives `DISPAT_OUTPUT`: the path of a file it may append `NAME=value` lines to, `GITHUB_OUTPUT`-style, to
export values for everything that runs after it. The name may be written bare or already carrying the
`DISPAT_OUTPUT_` prefix; both spellings address the same output:

```sh
echo "DISPAT_OUTPUT_IMAGE_DIGEST=$(docker inspect --format='{{index .RepoDigests 0}}' img)" >> "$DISPAT_OUTPUT"
echo "IMAGE_DIGEST=..." >> "$DISPAT_OUTPUT"     # the same output, bare spelling
echo "DISPAT_EXPORT_GITHUB=$PWD/dist/app.tgz $PWD/dist/SHA256SUMS" >> "$DISPAT_OUTPUT"
```

Outputs accumulate across the package's pipeline into one store. Every later script and hook of the package receives
each export as `DISPAT_OUTPUT_<NAME>`, and that includes the outcome scripts `onFail` and `onSkip`, so a notifier can
report with them.

Two more variables come along. `DISPAT_OUTPUTS` lists the exported names, space-separated, and is set but empty when
nothing was exported. `DISPAT_OUTPUT_SOURCE_<NAME>` names the script each export came from, as `<package>:<stage>`
(`core:build`, `base:run:lint`) or `<space>:login` for the login.

How far an export travels depends on which script made it. Hooks export exactly like stage scripts, so a `beforeBuild`
export reaches the build, the publish and everything after. The **login script's** exports are space-scoped, reaching
every package of the space from its publish stage, the one that waits for the login, onward. In
[`dispat run`](../configuration/spaces.md#scripts-and-dispat-run) outputs additionally carry across packages, from a
provider's script to its consumers'. In a release run they stay within the package, because a consumer's release
scripts read a provider's new version from the `DISPAT_UPDATED_*` listing rather than from the provider's outputs.

Re-exporting a name overrides its earlier value and source, like a shell re-assignment.

The name must be a valid environment variable name; other `DISPAT_`-prefixed names are reserved (an export cannot shadow
the `DISPAT_*` environment), and a malformed line fails a release-gating sequence (and only warns in a warn-only one). A
sequence that fails still surrenders whatever it exported before failing, which is how `onFail`
gets to see it.

One export is a directive to the [GitHub recorder](../configuration/records.md#github): **`DISPAT_EXPORT_GITHUB`**. A
package whose scripts exported it gets a GitHub release, and a package that never exported it is skipped by the
recorder.

Its value is a whitespace-separated list of absolute paths to existing files, each uploaded as an asset of the release
and named after the file. `$PWD` inside a script resolves to the package folder, which makes absolute paths easy. An
empty value creates the release with no assets, and an invalid entry, meaning a relative path, a missing file or a
directory, is skipped with a warning while the release and the sound entries go through.

Unlike ordinary outputs, this export travels to later scripts under its full name, so appending to it reads
`echo "DISPAT_EXPORT_GITHUB=$DISPAT_EXPORT_GITHUB $PWD/more.tgz" >> "$DISPAT_OUTPUT"`, and it does not appear in
`DISPAT_OUTPUTS`. Because it reaches later scripts as a plain environment variable, the
[`dispat github`](../cli/github.md) step command run from one of them reads the same opt-in and the same asset list out
of its own environment.

The other export with a consumer inside dispat is **`PACKAGE_<KEY>`**, where `<KEY>` is the exporting package's own key
under the [scheme above](#workspace-data). A release script that exports `PACKAGE_<KEY>=<commitHash>` pins the package's
release to that commit: the tag is created there instead of at HEAD (or at the release commit in
[commit mode](../configuration/records.md#commit)), and the package's GitHub release carries the hash as its commit and
`target_commitish`. It is meant for packages whose release scripts produce their own commit (a subtree push, a generated
repository) that the tag should point at. Like any output it reaches later scripts, as
`DISPAT_OUTPUT_PACKAGE_<KEY>`:

```sh
echo "PACKAGE_CORE=$(git rev-parse HEAD)" >> "$DISPAT_OUTPUT"
```

## Run outcome data

The [run-level hooks](../configuration/run-hooks.md) additionally receive the run's outcome, rendered with
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

The five list variables are set even when empty, so a shell for-loop iterates zero times instead of reading an unset
variable; `_FAILED_STAGE` and `_BLOCKED_BY`, conversely, are **unset** when there is nothing to report. Unplanned
packages carry no `DISPAT_RESULT_*` block; their state is the workspace listing's baseline entry. A `cancelled` status
means the run was interrupted (Ctrl-C, a killed CI job) before the package ran: nothing about it failed, and the next
run picks it up unchanged.

## Size

One package costs ~250 bytes across its listing variables, so a 500-package monorepo puts roughly 125 KB into each
script's environment. Each individual variable is tiny, so the ceiling is total environment size (~2 MiB on Linux, 1 MiB
on macOS), good for a few thousand packages, far beyond the size at which one dispat workspace has usually become
several.
