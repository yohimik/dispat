# Release steps

A normal `dispat` release does everything itself, in a fixed order. It works out the new versions, runs your build and
publish scripts, writes each package's changelog, makes a commit, creates the tags, and publishes the GitHub releases.
For most repositories that order is the right one and you never have to think about it.

Sometimes it is not. You may need the changelog written *before* the commit is made, so that the commit contains it.
You may want the GitHub release published from your own announce script, right after the artifact is uploaded, rather
than at the very end of the run. That is what the step commands are for.

A step command takes one thing the release normally does and lets you ask for it yourself, at the moment you choose.
There are four:

| Command             | What it does                                                       |
|---------------------|--------------------------------------------------------------------|
| `dispat changelog`  | Writes each package's pending changelog entry                       |
| `dispat autoversion`| Rewrites manifests to the planned versions                          |
| `dispat commit`     | Makes each package's release commit, and can tag and push it        |
| `dispat github`     | Creates each package's GitHub release                               |

[`dispat autowriter`](../../editing/autowriter.md) is not one of them, since it does something a release never does on its own, but
it covers packages by the same rules, so everything on this page about choosing what a command covers applies to it
too.

Every one of them plans first, exactly like a release would, so they always agree with each other about what the new
versions are. None of them needs the release to be running.

## Two rules that make them safe

**They only touch packages that are actually releasing.** A step command computes the release plan, then works on the
packages that plan is releasing. If you point one at a package with no pending changes, it says so and does nothing.
That is a success, not an error, so a script in your flow never breaks because a package happened to have nothing to
release this time. It also has a consequence worth knowing in advance: see
[the pitfall](#the-pitfall-a-tagged-package-drops-off-the-window) below.

**Running one twice changes nothing the second time.** Each step checks whether its work is already done before doing
it:

| Command       | Already done means                          | Reported as |
|---------------|---------------------------------------------|-------------|
| `changelog`   | the file already has an entry for the tag   | `W226`      |
| `commit`      | the tag already exists at that commit       | `W223`      |
| `github`      | the tag already has a release               | `W224`      |
| `autoversion` | the manifests already say the right version | nothing     |

This matters more than it sounds. It is what lets the release stage run after you and find the work done, instead of
writing a second copy of the entry or failing on a duplicate tag. It is also what makes a re-run after a failure safe.

**They fire no [run-level hooks](../../configuration/run-hooks.md).** `dispat commit --tag --push` makes the release
commit, writes the tags and pushes without `run.beforeCommit` or `run.afterPush` running. Those hooks belong to
`dispat release`, which is where dispat rather than you decides when the phase happens. Here you decide, so you
bracket it yourself: `beforePublish: [notify-before, commit, notify-after]`. The
[reasoning](../../configuration/run-hooks.md#they-belong-to-dispat-release) is on the hooks page.

**Inside a run, they answer to the run.** A step invoked from a stage script or flow hook replans, and a fresh plan
mid-run is not the run's plan: earlier legs' tags have landed, and the step's own leg may have created the very tag the
step would read back as published history. The [`DISPAT_*` environment](../environment.md) every stage script inherits
carries the run's own answers, so `changelog`, `commit` and `github` read them back. With `DISPAT_PACKAGE`,
`DISPAT_NEW_VERSION` and `DISPAT_TAG` present, the step narrows to that package (unless you passed a filter of your
own), leaves the run's own tag out of its baseline reading, and holds its record to the run's version. A replan that
drifted anyway is corrected and reported as `W228`; a plan the step cannot align — the package missing from it, or the
run's version rendering a different tag — is refused as `E219` with nothing written, because a failed leg re-runs where
a drifted record does not. `dispat github` additionally reads its attachment list from `DISPAT_EXPORT_GITHUB`, whether
an earlier stage exported it into the environment or the same script just appended it to `$DISPAT_OUTPUT`.

## Choosing what a step covers

With no arguments, a step covers every package the plan is releasing, in dependency order. To narrow it, use the same
flags every other dispat command uses:

```sh
dispat changelog                    # every releasing package
dispat changelog --package core     # just core
dispat changelog --space libs       # every package of the libs space
dispat changelog --group platform   # every package of the platform version group
```

If you run the command from inside a package folder and pass no flags, it narrows to that package. That is the same
rule [`dispat run`](../../cli/run.md#choosing-the-packages) follows.

A name that matches no package at all is an error, because a typo that quietly does nothing is worse than one that
stops you.

The step commands take `dispat run`'s window flags as well, and they mean the same thing:

```sh
dispat changelog --since HEAD~1     # the packages the last commit addressed
dispat changelog --since all        # every package, releasing or not
dispat changelog -p core --consumers  # core, plus everything that depends on it
dispat commit --on-error continue   # a failed package does not skip its dependents
```

`dispat commit` and `dispat github` work through their packages one at a time, since a repository has one index and
one HEAD. `dispat changelog` and `dispat autoversion` write inside each package's own folder, so they run several at
once under the build concurrency budget, in dependency order.

## The pitfall: a tagged package drops off the window

Every step command plans afresh, and the plan is computed from the tags. So the moment `dispat commit --tag` has
tagged a package, the *next* command sees a package with nothing pending and covers nothing at all:

```console
$ dispat commit --package core --tag
$ dispat autoversion --package core
INF package is outside the window, nothing to do  package=core
```

Nothing is wrong there, and the exit code is `0`. It is the same rule that keeps a flow from breaking on a quiet day.
But if you meant to reach the package anyway, say so with the window:

```sh
dispat autoversion --package core --since all
```

The usual shape of this is a flow that tags early and then wants to do more work on the same package. Either do the
rest before the tag, or reach for `--since all` afterwards.

## The commands, one at a time

### `dispat changelog`

Writes the changelog entry each covered package is due, the same entry the release would write. Use it when you want
the entry to be part of the release commit rather than left in your working tree afterwards.

The flags let you override the [`changelog`](../../configuration/records.md#changelog) settings for one invocation:
`--file`, `--file-title`, `--date-format` and `--release-name`.

In the log you will see one `changelog written` line per package, or a `W226` skip for a package whose entry was
already there.

### `dispat autoversion`

Rewrites the version numbers your manifests declare so they match the versions about to be released. If your space has
`syncLock` scripts, they run for the packages whose manifests actually changed, and only those.

Running it again after it has done its work rewrites nothing, because the manifests already say the right thing.

### `dispat commit`

Makes one commit per covered package, staging that package's folder plus anything
[`commit.include`](../../configuration/records.md#commit) lists. Two flags extend it:

```sh
dispat commit --tag          # also create the annotated release tag
dispat commit --tag --push   # ...and push the branch and the tags
```

A package with nothing to stage is a clean no-op. A tag that already exists at the same commit is a `W223` skip; a tag
pointing somewhere else is an error, because that is a real conflict rather than a repeat.

`--tag-name` names the tag instead of letting the command compute it:

```sh
dispat commit --tag --push --tag-name "$DISPAT_TAG"
```

You need it in one situation, and it follows from the rule above that a step command plans afresh. Inside a release
stage the command is planning a second time, and if the package belongs to a
[fixed versioning group](../../configuration/spaces.md#versioning-groups), the group's shared version has already moved by
then, because an earlier member of the same run has tagged. The second plan would compute a different version than the
run is reporting, and tag that. Passing the outer run's `$DISPAT_TAG` keeps the two in agreement.

The name applies to the tag and to the commit message. Naming one tag while covering several packages is refused before
any git work happens, since they cannot share it.

### `dispat github`

Creates the GitHub release for each covered package: named after the tag, with the release notes as its body.

A package is only released if it opted in, the same way it opts in during a normal run. There are two ways to opt in:

1. A script exported `DISPAT_EXPORT_GITHUB`. Its value lists the files to attach, and an empty value means "release me,
   no attachments". See [script outputs](../environment.md#script-outputs).
2. [`github.allPackages`](../../configuration/records.md#github) is on, which opts every published package in.

If neither applies, the command creates nothing and exits `0`. It is not an error to have nothing to publish.

When you run `dispat github` from a stage script, the export is already in the script's environment, because the stage
that exported it put it there. The command picks it up from there, along with `DISPAT_PACKAGE`, which tells it whose
export it is. You do not have to pass anything.

The flags `--owner`, `--repo`, `--api-url` and `--token-env` override the matching
[`github`](../../configuration/records.md#github) settings, and `--target` pins the tag to a specific commit or branch.

## A worked example

Here is the setup the step commands were built for. The goal is a release where the changelog is inside the tagged
commit, and the GitHub release goes out from the announce stage with the built artifact attached.

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

Read the flow from the top and it tells the story:

1. **build** produces `dist/app.tgz` and exports its path, which opts the package into a GitHub release and names the
   file to attach.
2. **beforePublish** writes the changelog entry, then commits the package folder (the entry included), tags that
   commit, and pushes. The tag now marks a commit that contains the changelog.
3. **publish** uploads to your registry.
4. **announce** creates the GitHub release, attaching the file the build stage exported.

By the time the release's own recording phase runs, everything is already done, so it finds the changelog entry
(`W226`), the tag (`W223`) and the release (`W224`) and skips all three. Your log will show those three codes on a
successful run. They are not warnings that something went wrong. They are the steps confirming they did not do the
work twice.

## What to do when something fails

Because every step is repeatable, recovering from a half-finished release is usually just running it again. Say the
publish stage failed after the commit was made and the release was created. Fix whatever broke and run `dispat`. The
plan is the same, the changelog entry is still there, the tag is still there, the GitHub release is still there, and
all three are skipped. Only the publish is retried.

The one thing that is not automatically undone is anything your own scripts pushed to a registry. That is outside
dispat's reach, and your publish script should handle a version that is already published in whatever way your
registry expects.

## Where to look next

- [CLI reference](../../cli/README.md) for every flag each command takes.
- [Stages and hooks](../../configuration/spaces.md#stages-and-hooks) for where in a flow you can put a script.
- [Release records](../../configuration/records.md) for what the changelog, GitHub and commit settings control.
- [Script environment](../environment.md) for the variables a stage script receives.
