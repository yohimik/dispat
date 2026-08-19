# A game, from one package to many

Start with a repository that holds nothing but the game, and add the landing page, the docs, the modding SDK and the
dedicated server later, one block at a time. This page is both halves of that story.

Game engines keep their version in a file no package manager understands. dispat reads and writes those files, so
there is nothing to sort out first: a game is an ordinary package, and something builds it and something publishes
it.

## What this buys you

**One version number, everywhere.** The version dispat computes from your commits is written straight into
`project.godot`,
becomes the git tag, becomes the Steam build description and the branch that goes live, becomes the itch
`--userversion`, heads the changelog entry and names the GitHub release. Nobody types it twice, so nothing can
disagree about what `0.3.0` contains. When a player reports a bug against the version on their title screen, that
string leads straight to a tag and a diff.

**The changelog writes itself, per package.** The commits that caused the release are the notes:

```markdown
# Changelog

## adventure@0.3.0 (2026-08-18)

### Features

- co-op lobby

### Fixes

- stop the save file corrupting on quit
```

The same text becomes the GitHub release body, and is handed to the announce stage as `DISPAT_FEATURES` and
`DISPAT_FIXES`. Patch notes stop being a thing somebody remembers to write on release day.

**Announcements go out with the facts already in them.** [`flow.announce`](../configuration/spaces.md#flowannounce)
runs after the publish succeeded, with the release notes and the channel in its environment, and it only warns if it
fails, because the release is already out:

```console
13:44:35 INF announce started package=adventure stage=publish version=0.3.0
13:44:36 INF channel=stable prerelease=false package=adventure stage=publish version=0.3.0
13:44:36 INF features: co-op lobby package=adventure stage=publish version=0.3.0
13:44:36 INF fixes: stop the save file corrupting on quit package=adventure stage=publish version=0.3.0
```

A Discord post to players on `stable` and a quieter one to testers on `beta` is a `case` on `$DISPAT_CHANNEL` in that
script.

**A patch is not a special procedure.** A `fix:` commit is a patch release: the same build, the same upload, the same
tag, changelog and announcement, from the same one command. There is no hotfix path to remember under pressure, which
is when a hotfix path is normally used for the first time.

**The interface does not grow with the project.** One package or twenty, the command is `dispat`. Which packages move,
in what order, and what can run in parallel are the tool's problem. That is what makes the growth in part two cost a
few lines of configuration rather than a migration.

**And the project does grow.** Today the repository is one game binary. It rarely stays that way: a landing page with
a buy button, a docs site for the wiki, a modding SDK, a level editor or asset pipeline somebody else on the team
runs, a dedicated server image, a Discord bot reading your own release feed. Each of those has its own version, its
own release rhythm and its own idea of "published", and each one is a place where a number can drift out of step with
the game it belongs to.

The usual path is that the second deliverable gets a hand-written script, the third gets another, and by the fifth
nobody can say which versions of these things were ever tested together. Starting on dispat means the second one is a
config block instead: it joins the same graph, takes the same commit conventions, and gets the same changelog, tag and
announcement the game already had. Part one below is a complete setup for a single game with none of that machinery
in sight, and part two adds four packages to it without touching the game's own entry.

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

One package, no spaces. `project.godot` is a manifest dispat reads, so `autoVersion` finds the version and writes it
back with nothing else configured. `initials` tells dispat where the version numbering starts, since there is no tag
yet.

```console
$ git commit -m "feat(game): co-op mode"
$ dispat
12:50:45 INF release started root=.
12:50:45 INF ● changed baselineFromInitials=true bump=minor channel=stable dueToProviders=[] ownCommits=1 package=game reason=direct space=game version="0.1.0 -> 0.2.0"
12:50:45 INF release plan ready held=0 packages=1 releasing=1
12:50:45 INF manifest updated manifest=project.godot package=game stage=version versionWritten=true version=0.2.0
12:50:45 INF version succeeded package=game stage=version version=0.2.0
12:50:45 INF build started package=game stage=build version=0.2.0
12:50:45 INF build succeeded package=game stage=build version=0.2.0
12:50:45 INF publish started package=game stage=publish version=0.2.0
12:50:45 INF published package=game stage=publish tag=game@0.2.0 version=0.2.0
12:50:45 INF summary channel=stable package=game status=published tag=game@0.2.0 took=1.4s version="0.1.0 -> 0.2.0"
12:50:45 INF done cancelled=0 failed=0 held=0 published=1 skipped=0 took=1.4s unchanged=0
```

Three things exist afterwards that did not before: the version in `project.godot` reads `0.2.0`, the tag `game@0.2.0`
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
difference, and `manifests: all` covers it:

```json title="dispat.json (Unity)"
{
  "autoVersion": {"enabled": true, "manifests": "all"}
}
```

```console
$ dispat
12:51:53 INF manifest updated manifest=ProjectSettings/ProjectSettings.asset package=client stage=version versionWritten=true
12:51:53 INF summary channel=stable package=client status=published tag=client@0.3.0 took=1.1s version="0.2.0 -> 0.3.0"
```

Unreal is the same again, with its version in `Config/DefaultGame.ini`. Each engine has a page of its own for the
detail: [Unity](./unity.md), [Godot](./godot.md), [Unreal](./unreal.md).

If your engine is one dispat does not read, the [replace strategy](../configuration/autoversion.md) still works:
point a rule at the line that holds the number, and everything else on this page is unchanged.

## Part two: the repository grows

Six months later there is more than a game. A landing page, a modding SDK, a dedicated server, and a bit of shared
netcode both the server and the SDK use. None of that requires restructuring anything: the game package stays exactly
as it is, and the new parts are added beside it.

```
game/         the Godot client            published to itch and Steam
protocol/     shared netcode              an npm package
sdk/          the modding SDK             an npm package, depends on protocol
server/       the dedicated server        a Docker image, depends on protocol
site/         the landing page            static hosting, follows the game
dispat.json
```

Each new package is one entry, saying where it lives and what builds it. The `game` entry is abbreviated below; its
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

The two `dependencies` edges are the only new idea, and [`dispat compute`](../cli/compute.md) writes them for you by
reading the manifests. The `site` edge is declared by hand with `keep: true`, because nothing in the site's files says
it depends on the game; the site shows the current version, so it should be built after the game is out. `keep: true`
means "I meant this, do not offer to remove it".

Now one commit can move as much or as little of the repository as it should:

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

`^^` on the protocol commit reaches everything that declares it, so the SDK and the server come along. `^` on the
game commit reaches the site. Without those marks each commit would release its own package alone, which is often
exactly what you want on a Tuesday.

The run builds and publishes all five in dependency order, in parallel where the graph allows, and ends with one line
per package:

```console
$ dispat
12:51:36 INF summary channel=stable package=game status=published tag=game@0.3.0 took=1.8s version="0.2.0 -> 0.3.0"
12:51:36 INF summary channel=stable package=protocol status=published tag=protocol@1.1.0 took=1.9s version="1.0.0 -> 1.1.0"
12:51:36 INF summary channel=stable package=sdk status=published tag=sdk@0.3.1 took=2.4s version="0.3.0 -> 0.3.1"
12:51:36 INF summary channel=stable package=server status=published tag=server@0.5.1 took=2.6s version="0.5.0 -> 0.5.1"
12:51:36 INF summary channel=stable package=site status=published tag=site@0.1.1 took=2.4s version="0.1.0 -> 0.1.1"
12:51:36 INF done cancelled=0 failed=0 held=0 published=5 skipped=0 took=3.1s unchanged=0
```

Every package keeps its own version, its own tag and its own changelog. Nothing forces the SDK to carry the game's
version number, and the game does not jump a major because the protocol did.

### What did not change when it grew

- The `game` package entry is the same one from part one.
- No file moved.
- The tags already published are still the baselines everything counts from.
- The docs site, a demo build, an asset pipeline, a Discord bot: each is one more entry.

If several of the new packages build the same way, group them into a [space](../configuration/spaces.md) so they share
one flow instead of repeating it. That is a tidying step, not a migration.

## Worth knowing

- **Game builds are large and slow.** Keep the export in the build stage and the upload in the publish stage: a
  failed build then costs nothing, and no half-finished upload ever reaches a store.
- **Store versions and semantic versions are different audiences.** `0.3.0` is what your changelog and your SDK
  consumers see. What players see is up to the store page, and nothing stops the build script deriving
  `Early Access 0.3` from `$DISPAT_NEW_VERSION`.
- **Assets do not belong in the version graph.** If a package is only art, give it
  [`versioning: none`](../reference/releasing/versioning.md#packages-that-never-release-none) so it takes part in the
  ordering without ever taking a version.
- **Playtest builds want a channel, not a version scheme.** `feat(game)%beta: ...` puts the game on a beta line, and
  the [prerelease branches](../reference/releasing/prerelease-branches.md) page maps that onto a branch model.

## See also

- [Publishing to Steam](./steam.md) for depots, branches and `steamcmd`.
- [Publishing to itch.io](./itch.md) for `butler` and per-platform channels.
- [A Docker image chain](./docker.md) for the dedicated server image.
- [A single package, no monorepo](./single-package.md) for the general form of part one.
