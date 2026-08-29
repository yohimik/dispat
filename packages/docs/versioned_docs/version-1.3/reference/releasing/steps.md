# Release steps

A standard `dispat` release runs every stage in a fixed order. It calculates new versions, executes your build and
publish scripts, updates each package's changelog, creates git commits and tags, and publishes GitHub releases. Most
repositories can rely on this default sequence without extra configuration.

Step commands give you direct control when that order does not fit your workflow. You might need the changelog written
*before* the commit is made so that the commit includes the changelog file. You might also want your own announce
script to publish the GitHub release right after uploading an artifact.

Each step command isolates one part of the release process:

| Command             | What it does                                                       |
|---------------------|--------------------------------------------------------------------|
| `dispat changelog`  | Writes each package's pending changelog entry                       |
| `dispat autoversion`| Rewrites manifests to the planned versions                          |
| `dispat commit`     | Makes each package's release commit, and can tag and push it        |
| `dispat github`     | Creates each package's GitHub release                               |

The [`dispat autowriter`](../../editing/autowriter.md) command is not a step command because release runs never execute
it automatically. It selects target packages using the same rules, so the coverage flags described here work there as
well.

Every step command calculates a fresh release plan before taking action. They agree on version calculations and run
independently without requiring an active release process.

## Four rules that make them safe

**They only touch packages that are actually releasing.** A step command generates a release plan and modifies only the
packages scheduled for release. If you target a package with no pending changes, dispat reports that and exits cleanly
with code `0`. This keeps custom scripts from failing on unchanged packages, though it introduces
[the pitfall](#the-pitfall-a-tagged-package-drops-off-the-window) covered below.

**Running one twice changes nothing the second time.** Each step checks whether its target changes already exist before
writing:

| Command       | Already done means                               | Reported as |
|---------------|--------------------------------------------------|-------------|
| `changelog`   | the file already has an entry for the tag        | `W226`      |
| `commit`      | the tag already exists at that commit            | `W223`      |
| `github`      | the tag already has a release (a draft included) | `W224`      |
| `autoversion` | the manifests already say the right version      | nothing     |

This idempotency lets later release stages detect completed work and skip duplicate writes or duplicate tag errors. It
also ensures you can safely re-run failed release jobs.

**They fire no [run-level hooks](../../configuration/run-hooks.md).** Running `dispat commit --tag --push` creates
commits, writes tags, and pushes changes without executing `run.beforeCommit` or `run.afterPush`. Those hooks belong to
`dispat release`, where dispat manages the entire lifecycle. You control the execution order directly in your flow
configuration, such as `beforePublish: [notify-before, commit, notify-after]`. Read more about this design in the
[reasoning](../../configuration/run-hooks.md#they-belong-to-dispat-release) section on the hooks page.

**Inside a run, they answer to the run.** A step invoked from a stage script or flow hook calculates a plan, but
mid-run state can differ because earlier steps have already created tags. The
[`DISPAT_*` environment](../environment.md) passed to stage scripts preserves the original release context so
`changelog`, `commit`, and `github` read back those values. When `DISPAT_PACKAGE`, `DISPAT_NEW_VERSION`, and
`DISPAT_TAG` are present, the step scopes to that package unless you pass an explicit filter.

The step ignores tags created during the active run so its plan matches the original baseline. It fixes its records to
the planned version and provider movements listed in `DISPAT_UPDATED_*`. If a plan drifts, dispat corrects it and logs
`W228`, but if a plan cannot align because a package is missing or renders a different tag, dispat halts with `E219`
and writes nothing.

The `dispat github` command reads release attachments from `DISPAT_EXPORT_GITHUB`, whether set by an earlier stage or
appended to `$DISPAT_OUTPUT`. It logs warning `W229` if run before the git tag exists, because GitHub would otherwise
create the tag at the default branch HEAD, so run your commit step first. Running any step twice within a single flow
skips redundant work and reports `W226`, `W223`, or `W224`.

## Choosing what a step covers

Run a step command without arguments to process every package in the release plan in dependency order. You can narrow
the scope using standard selection flags:

```sh
dispat changelog                    # every releasing package
dispat changelog --package core     # just core
dispat changelog --space libs       # every package of the libs space
dispat changelog --group platform   # every package of the platform version group
```

Run a command inside a package directory without flags to target that specific package. This follows the package
selection rules of [`dispat run`](../../cli/run.md#choosing-the-packages).

Passing a package name that does not exist results in an immediate error so typos do not fail silently.

Step commands also accept window filtering flags from `dispat run`:

```sh
dispat changelog --since HEAD~1     # the packages the last commit addressed
dispat changelog --since all        # every package, releasing or not
dispat changelog -p core --consumers  # core, plus everything that depends on it
dispat commit --on-error continue   # a failed package does not skip its dependents
```

The `dispat commit` and `dispat github` commands process packages sequentially to protect the single git index and HEAD
reference. `dispat changelog` and `dispat autoversion` modify files in distinct package directories, so they execute
concurrently up to your configured build concurrency limit.

## The pitfall: a tagged package drops off the window

Every step command recalculates its plan based on existing git tags. Once `dispat commit --tag` tags a package, the
*next* command detects no pending changes and skips that package:

```console
$ dispat commit --package core --tag
$ dispat autoversion --package core
INF package is outside the window, nothing to do  package=core
```

This output is expected and exits with code `0`. If you need to force a step on an already tagged package, specify a
broader window:

```sh
dispat autoversion --package core --since all
```

This situation happens when a flow creates tags early and attempts further modifications on the same package. Run your
other steps before creating tags, or add `--since all` to later commands.

## The commands, one at a time

### `dispat changelog`

Writes the pending changelog entry for each selected package. Run this command before creating release commits to
include the updated changelog in the commit history.

You can override [`changelog`](../../configuration/records.md#changelog) configuration settings using the `--file`,
`--file-title`, `--date-format`, and `--release-name` flags.

The command logs `changelog written` for updated packages, or `W226` when an entry for the tag already exists.

### `dispat autoversion`

Updates package manifest files with their planned release versions. If your space defines `syncLock` scripts, dispat
runs them only for packages with modified manifests.

Running this command again produces no changes because the manifests already match the planned versions.

### `dispat commit`

Creates one commit per package, staging files in the package directory along with any paths listed in
[`commit.include`](../../configuration/records.md#commit). Extend the behavior with optional flags:

```sh
dispat commit --tag          # also create the annotated release tag
dispat commit --tag --push   # ...and push the branch and the tags
```

Packages with no staged changes are skipped cleanly. If a tag already points to the target commit, dispat logs `W223`
and skips it, but a tag pointing to a different commit triggers an error.

Use `--tag-name` to set an explicit tag name instead of calculating one:

```sh
dispat commit --tag --push --tag-name "$DISPAT_TAG"
```

Use this flag when running inside release stages for packages in
[fixed versioning groups](../../configuration/spaces.md#versioning-groups). Earlier packages in the group will have
already advanced the shared tag, so passing `$DISPAT_TAG` keeps the step aligned with the parent run.

The custom tag name applies to both the git tag and the commit message. Setting an explicit tag name while targeting
multiple packages is rejected immediately because packages cannot share a single tag name.

### `dispat github`

Creates GitHub releases for covered packages, using the tag name for the release title and the changelog text for the
body.

A package receives a GitHub release only if it opts in through one of two methods:

1. A script sets `DISPAT_EXPORT_GITHUB` with paths to attached artifacts, or leaves it empty to create a release
   without attachments. See [script outputs](../environment.md#script-outputs).
2. The [`github.allPackages`](../../configuration/records.md#github) setting is enabled to publish releases for all
   packages.

If neither condition is met, the command exits with code `0` without creating releases.

When executed from a stage script, `dispat github` automatically reads `DISPAT_EXPORT_GITHUB` and `DISPAT_PACKAGE` from
the environment. You do not need to pass extra arguments.

Override default [`github`](../../configuration/records.md#github) settings using `--owner`, `--repo`, `--api-url`, and
`--token-env`. Use `--target` to attach the release tag to a specific commit or branch.

## A worked example

Here is a configuration that commits changelogs into the release commit and publishes GitHub releases with build
artifacts during the announce stage:

```json title="dispat.json"
{
  "scripts": {
    "build": "make dist && echo \"DISPAT_EXPORT_GITHUB=$PWD/dist/app.tgz\" >> \"$DISPAT_OUTPUT\"",
    "publish": "make upload",
    "write-changelog": "dispat changelog",
    "release-commit": "dispat commit --tag --push",
    "announce": "dispat github"
  },
  "spaces": {
    "libs": {
      "path": "packages",
      "flow": {
        "build": "build",
        "beforePublish": [ "write-changelog", "release-commit" ],
        "publish": "publish",
        "announce": "announce"
      }
    }
  }
}
```

The flow executes in this sequence:

1. **build** generates `dist/app.tgz` and exports its path, opting the package into GitHub release creation with that
   asset attached.
2. **beforePublish** writes changelog updates, commits the package files, creates a tag, and pushes upstream.
3. **publish** uploads packages to your package registry.
4. **announce** creates the GitHub release and attaches the exported build file.

When the release recording phase runs at the end, it finds the changelog (`W226`), tag (`W223`), and GitHub release
(`W224`) already present. These log codes confirm that dispat safely skipped redundant work during the run.

## What to do when something fails

You can recover from interrupted releases by running `dispat` again. If your publish stage fails after creating commits
and GitHub releases, fix the root cause and rerun the command. dispat detects the existing changelogs, tags, and
releases, skips them, and resumes by retrying the publish stage.

Artifacts already uploaded to external registries cannot be rolled back by dispat. Ensure your publish scripts handle
previously published versions according to your registry's rules.

## Where to look next

- [CLI reference](../../cli/README.md) for every flag each command takes.
- [Stages and hooks](../../configuration/spaces.md#stages-and-hooks) for where in a flow you can put a script.
- [Release records](../../configuration/records.md) for what the changelog, GitHub and commit settings control.
- [Script environment](../environment.md) for the variables a stage script receives.
