# A game, from one package to many

Start with a repository that holds nothing but the game. Add the landing page, the docs, the modding SDK, and the
dedicated server later, one block at a time. This page covers both halves of that story.

Game engines keep their version in a file no package manager understands. dispat reads and writes those files. A game
is an ordinary package, so something builds it and something publishes it.

## What this buys you

**One version number, everywhere.** dispat computes the version from your commits and writes it straight into
`project.godot`. That version becomes the git tag, the Steam build description, the live branch, the itch
`--userversion`, the changelog heading, and the GitHub release name. You never type it twice, so nothing can disagree
about what `0.3.0` contains. When a player reports a bug against the version on their title screen, that string leads
straight to a tag and a diff.

**The changelog writes itself, per package.** The commits that caused the release become the release notes:

```markdown
# Changelog

## adventure@0.3.0 (2026-08-18)

### Features

- co-op lobby

### Fixes

- stop the save file corrupting on quit
```

The same text becomes the GitHub release body. dispat also hands it to the announce stage as `DISPAT_FEATURES` and
`DISPAT_FIXES`. Patch notes stop being a chore you remember to write on release day.

**Announcements go out with the facts already in them.** [`flow.announce`](../configuration/spaces.md#flowannounce)
runs after the publish succeeds. It has the release notes and the channel in its environment. It only warns if it fails
because the release is already out:

```console
13:44:35 INF announce started package=adventure stage=publish version=0.3.0
13:44:36 INF channel=stable prerelease=false package=adventure stage=publish version=0.3.0
13:44:36 INF features: co-op lobby package=adventure stage=publish version=0.3.0
13:44:36 INF fixes: stop the save file corrupting on quit package=adventure stage=publish version=0.3.0
```

Write a `case` on `$DISPAT_CHANNEL` in that script to send a Discord post to players on `stable` and a quieter one to
testers on `beta`.

**A patch is not a special procedure.** A `fix:` commit is a patch release. You get the same build, the same upload,
the same tag, changelog, and announcement from the same one command. You do not have to remember a hotfix path under
pressure.

**The interface does not grow with the project.** The command is `dispat` for one package or twenty. Which packages
move, in what order, and what can run in parallel are problems for the tool. That is why the growth in part two costs a
few lines of configuration rather than a migration.

**And the project does grow.** Today the repository is one game binary, but it rarely stays that way. You might add a
landing page, a docs site, a modding SDK, a level editor, or a dedicated server image. Each of those has its own
version, its own release rhythm, and its own idea of "published". Each one is a place where a number can drift out of
step with the game it belongs to.

The usual path is that the second deliverable gets a hand-written script, and the third gets another. By the fifth
deliverable, nobody can say which versions were tested together. Starting on dispat means the second deliverable is a
config block instead.

It joins the same graph, takes the same commit conventions, and gets the same changelog, tag, and announcement the game
already had. Part one below is a complete setup for a single game with none of that machinery in sight. Part two adds
four packages to it without touching the game's own entry.

## Part one: just the game

```
game/project.godot     the Godot project, with config/version
dispat.json
```

```json title="dispat.json"
{
  "scripts": {
    "export": "godot --headless --export-release Linux build/acme-adventure.x86_64",
    "publish": "butler push build acme/adventure:linux --userversion $DISPAT_NEW_VERSION"
  },
  "packages": {
    "game": {
      "path": "game",
      "flow": {"build": "export", "publish": "publish"},
      "autoVersion": {"enabled": true}
    }
  },
  "initials": {"game": "0.1.0"}
}
```

One package, no spaces. `project.godot` is a manifest dispat reads. `autoVersion` finds the version and writes it back
with nothing else configured. `initials` tells dispat where the version numbering starts because there is no tag yet.

```console
$ git commit -m "feat(game): co-op mode"
$ dispat
12:50:45 INF release started root=.
12:50:45 INF ● changed baselineFromInitials=true bump=minor channel=stable dueToProviders=[] ownCommits=1 package=game reason=direct space=game version="0.1.0 -> 0.2.0"
12:50:45 INF release plan ready held=0 packages=1 releasing=1
12:50:45 INF manifest reconciled manifest=project.godot package=game ranges=0 stage=version version=0.2.0 versionWritten=true
12:50:45 INF version succeeded package=game stage=version version=0.2.0
12:50:45 INF build started package=game stage=build version=0.2.0
12:50:45 INF build succeeded package=game stage=build version=0.2.0
12:50:45 INF publish started package=game stage=publish version=0.2.0
12:50:45 INF published package=game stage=publish tag=game@0.2.0 version=0.2.0
12:50:45 INF summary channel=stable package=game status=published tag=game@0.2.0 took=1.4s version="0.1.0 -> 0.2.0"
12:50:45 INF done cancelled=0 failed=0 held=0 published=1 skipped=0 took=1.4s unchanged=0
```

Three things exist afterwards that did not before. The version in `project.godot` reads `0.2.0`, the tag `game@0.2.0`
records the build, and `game/CHANGELOG.md` says what changed.

```markdown
# Changelog

## game@0.2.0 (2026-08-18)

### Features

- co-op mode
```

That is a complete setup. Nothing below is required to keep using it.

### The same thing in Unity

Unity keeps the version in `ProjectSettings/ProjectSettings.asset`, one folder down from the package. That is the only
difference. Add `manifests: all` to cover it:

```json title="dispat.json (Unity)"
{
  "autoVersion": {"enabled": true, "manifests": "all"}
}
```

```console
$ dispat
12:51:53 INF manifest reconciled manifest=ProjectSettings/ProjectSettings.asset package=client ranges=0 stage=version version=0.3.0 versionWritten=true
12:51:53 INF summary channel=stable package=client status=published tag=client@0.3.0 took=1.1s version="0.2.0 -> 0.3.0"
```

Unreal is the same again, with its version in `Config/DefaultGame.ini`. Read the specific page for your engine to get
the details: [Unity](./unity.md), [Godot](./godot.md), or [Unreal](./unreal.md).

If dispat does not read your engine, the [replace strategy](../configuration/autoversion.md) still works. Point a rule
at the line that holds the number. Everything else on this page stays exactly the same.

## Part two: the repository grows

Six months later there is more than a game. You have a landing page, a modding SDK, a dedicated server, and a bit of
shared netcode for the server and the SDK. You do not need to restructure anything. The game package stays exactly as
it is, and you add the new parts beside it.

```
game/         the Godot client            published to itch and Steam
protocol/     shared netcode              an npm package
sdk/          the modding SDK             an npm package, depends on protocol
server/       the dedicated server        a Docker image, depends on protocol
site/         the landing page            static hosting, follows the game
dispat.json
```

Each new package is one entry that says where it lives and what builds it. The `game` entry is abbreviated below. Its
`autoVersion` block from part one is untouched:

```json title="dispat.json (added, not rewritten)"
{
  "packages": {
    "game": {"path": "game", "flow": {"build": "export-game", "publish": "push-itch"}},
    "protocol": {"path": "protocol", "flow": {"build": "test", "publish": "npm-publish"}},
    "sdk": {"path": "sdk", "flow": {"build": "test", "publish": "npm-publish"}},
    "server": {"path": "server", "flow": {"build": "build-image", "publish": "push-image"}},
    "site": {
      "path": "site",
      "flow": {"build": "build-site", "publish": "deploy-site"},
      "dependencies": [{"provider": "game", "keep": true}]
    }
  },
  "dependencies": {
    "server": ["protocol"],
    "sdk": ["protocol"]
  }
}
```

The two `dependencies` edges are the only new idea. Run [`dispat compute`](../cli/compute.md) to read the manifests and
write them for you. Declare the `site` edge by hand with `keep: true` because nothing in the site's files says it
depends on the game.

The site shows the current version, so it should build after the game is out. `keep: true` means you meant to add this
edge, and dispat will not offer to remove it.

Now one commit can move as much or as little of the repository as it needs to:

```console
$ git commit -m "feat(protocol)^^: entity interpolation"
$ git commit -m "feat(game)^: co-op lobby"
$ dispat status
12:51:36 INF ● changed baselineFromInitials=true bump=minor channel=stable dueToProviders=[] ownCommits=1 package=game reason=direct space=game version="0.2.0 -> 0.3.0"
12:51:36 INF ● changed baselineFromInitials=true bump=minor channel=stable dueToProviders=[] ownCommits=1 package=protocol reason=direct space=protocol version="1.0.0 -> 1.1.0"
12:51:36 INF ● changed baselineFromInitials=true bump=patch channel=stable dependsOn=["protocol"] dueToProviders=["protocol"] ownCommits=0 package=sdk reason="propagated from protocol" space=sdk version="0.3.0 -> 0.3.1"
12:51:36 INF ● changed baselineFromInitials=true bump=patch channel=stable dependsOn=["protocol"] dueToProviders=["protocol"] ownCommits=0 package=server reason="propagated from protocol" space=server version="0.5.0 -> 0.5.1"
12:51:36 INF ● changed baselineFromInitials=true bump=patch channel=stable dependsOn=["game"] dueToProviders=["game"] ownCommits=0 package=site reason="propagated from game" space=site version="0.1.0 -> 0.1.1"
12:51:36 INF release plan ready held=0 packages=5 releasing=5
```

The `^^` on the protocol commit reaches everything that declares it, so the SDK and the server come along. The `^` on
the game commit reaches the site. Without those marks, each commit releases its own package alone, which is often
exactly what you want on a Tuesday.

The run builds and publishes all five packages in dependency order. It runs in parallel where the graph allows. The
output ends with one line per package:

```console
$ dispat
12:51:36 INF summary channel=stable package=game status=published tag=game@0.3.0 took=1.8s version="0.2.0 -> 0.3.0"
12:51:36 INF summary channel=stable package=protocol status=published tag=protocol@1.1.0 took=1.9s version="1.0.0 -> 1.1.0"
12:51:36 INF summary channel=stable package=sdk status=published tag=sdk@0.3.1 took=2.4s version="0.3.0 -> 0.3.1"
12:51:36 INF summary channel=stable package=server status=published tag=server@0.5.1 took=2.6s version="0.5.0 -> 0.5.1"
12:51:36 INF summary channel=stable package=site status=published tag=site@0.1.1 took=2.4s version="0.1.0 -> 0.1.1"
12:51:36 INF done cancelled=0 failed=0 held=0 published=5 skipped=0 took=3.1s unchanged=0
```

Every package keeps its own version, its own tag, and its own changelog. Nothing forces the SDK to carry the game's
version number. The game does not jump a major version because the protocol did.

### What did not change when it grew

- The `game` package entry is the same one from part one.
- No file moved.
- The tags already published are still the baselines everything counts from.
- The docs site, a demo build, an asset pipeline, and a Discord bot are each just one more entry.

If several of the new packages build the same way, group them into a [space](../configuration/spaces.md). They will
share one flow instead of repeating it. That is a tidying step, not a migration.

## Worth knowing

- **Game builds are large and slow.** Keep the export in the build stage and the upload in the publish stage. A failed
  build then costs nothing, and no half-finished upload ever reaches a store.
- **Store versions and semantic versions are different audiences.** `0.3.0` is what your changelog and your SDK
  consumers see. What players see is up to the store page, and nothing stops the build script from deriving
  `Early Access 0.3` from `$DISPAT_NEW_VERSION`.
- **Assets do not belong in the version graph.** If a package is only art, give it
  [`versioning: none`](../reference/releasing/versioning.md#packages-that-never-release-none). It takes part in the
  ordering without ever taking a version.
- **Playtest builds want a channel, not a version scheme.** Write `feat(game)%beta: ...` to put the game on a beta
  line. The [prerelease branches](../reference/releasing/prerelease-branches.md) page maps that onto a branch model.

## See also

- Read [Publishing to Steam](./steam.md) for depots, branches, and `steamcmd`.
- Read [Publishing to itch.io](./itch.md) for `butler` and per-platform channels.
- Read [A Docker image chain](./docker.md) for the dedicated server image.
- Read [A single package, no monorepo](./single-package.md) for the general form of part one.
