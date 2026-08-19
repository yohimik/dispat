# Publishing to Steam

Uploading a build to Steam with `steamcmd`, with the version in the build description and the release channel
deciding which Steam branch goes live.

Steam has no version field. A build is a numbered upload with a description attached, and what players get is
whichever build a branch points at. That maps onto dispat cleanly once you decide two things: what the description
says, and which branch a given release sets live.

## The layout

```
games/adventure/project.godot                  the game itself
games/adventure/scripts/app_build.template.vdf the SteamPipe build script, with two placeholders
games/adventure/scripts/steam-publish.sh       fills the template in and runs steamcmd
dispat.json
```

## The configuration

```json title="dispat.json"
{
  "scripts": {
    "steam-login": "steamcmd +login \"$STEAM_USERNAME\" +quit",
    "export": "godot --headless --export-release Windows build/windows/acme-adventure.exe",
    "steam": "./scripts/steam-publish.sh"
  },
  "spaces": {
    "games": {
      "path": "games",
      "flow": {"login": "steam-login", "build": "export", "publish": "steam"},
      "autoVersion": {"enabled": true}
    }
  }
}
```

`project.godot` is a manifest dispat reads, so `autoVersion` finds the game's version and writes it back with
nothing else configured. [Godot](./godot.md) has the detail.

`flow.login` runs once before the first publish of the space, not once per package, which is what you want from
anything that authenticates. It is a space-level slot for exactly that reason and cannot be set on a single package;
a repository with one game still uses a space to get it. See
[registry login, once per space](./login.md).

## The build script

SteamPipe reads a VDF file. Two values in it change per release, so keep it as a template:

```
"AppBuild"
{
	"AppID" "1234560"
	"Desc" "{{DESC}}"

	"ContentRoot" "../build/"
	"BuildOutput" "../out/"
	"SetLive" "{{BRANCH}}"

	"Depots"
	{
		"1234561" "depot_windows.vdf"
	}
}
```

And fill it in from the environment dispat provides:

```sh title="games/adventure/scripts/steam-publish.sh"
#!/bin/sh
set -eu

# A prerelease goes to its own Steam branch; a stable release goes to the
# default branch, which is what an empty SetLive means.
branch="$DISPAT_CHANNEL"
[ "$branch" = "stable" ] && branch=""

sed -e "s|{{DESC}}|$DISPAT_NEW_VERSION|" \
    -e "s|{{BRANCH}}|$branch|" \
    scripts/app_build.template.vdf > /tmp/app_build.vdf

steamcmd +login "$STEAM_USERNAME" +run_app_build /tmp/app_build.vdf +quit
```

`$DISPAT_CHANNEL` is the release channel: `stable` for an ordinary release, `beta` or `rc` for a prerelease. Name your
Steam branches after your channels and the mapping is this one line.

## A release

A commit marked `%beta` puts the game on the beta line, and the same script sends it to the `beta` branch:

```console
$ git commit -m "feat(adventure)%beta: new boss fight"
$ dispat
12:54:14 INF release started root=.
12:54:14 INF ● changed baselineFromInitials=true bump=minor channel="stable -> beta" dueToProviders=[] ownCommits=1 package=adventure reason=direct space=games version="0.2.0 -> 0.3.0-beta.0"
12:54:14 INF release plan ready held=0 packages=1 releasing=1
12:54:14 INF manifest updated manifest=project.godot package=adventure stage=version versionWritten=true version=0.3.0-beta.0
12:54:14 INF version succeeded package=adventure stage=version version=0.3.0-beta.0
12:54:14 INF build started package=adventure stage=build version=0.3.0-beta.0
12:54:14 INF build succeeded package=adventure stage=build version=0.3.0-beta.0
12:54:14 INF login started space=games stage=login
12:54:14 INF publish started package=adventure stage=publish version=0.3.0-beta.0
12:54:16 INF would upload 0.3.0-beta.0 to branch 'beta' package=adventure stage=publish version=0.3.0-beta.0
12:54:16 INF published package=adventure stage=publish tag=adventure@0.3.0-beta.0 version=0.3.0-beta.0
12:54:16 INF summary channel=beta package=adventure status=published tag=adventure@0.3.0-beta.0 took=1.4s version="0.2.0 -> 0.3.0-beta.0"
```

The login happens once, after the build succeeded and before the first publish. A build that fails never reaches
Steam at all.

Testers on the beta branch get it. When it is ready for everybody, graduate the same work rather than rebuilding a
different version of it:

```console
$ git commit -m "release(adventure)%beta>stable: ship it"
$ dispat
12:54:16 INF ● changed baselineFromInitials=true bump=minor channel="beta -> stable" dueToProviders=[] ownCommits=1 package=adventure reason=direct space=games version="0.3.0-beta.0 -> 0.3.0"
12:54:16 INF would upload 0.3.0 to branch 'default' package=adventure stage=publish version=0.3.0
12:54:16 INF summary channel=stable package=adventure status=published tag=adventure@0.3.0 took=1.0s version="0.3.0-beta.0 -> 0.3.0"
```

`0.3.0-beta.0` became `0.3.0`, the branch became the default one, and both uploads are on the same store page with
descriptions you can read in the Steamworks build list.

## Several platforms in one release

A Steam app usually has one depot per platform. Export them all in the build stage, then upload once: SteamPipe takes
every depot in the same run, which is what keeps the platforms on one build number.

```json
{
  "scripts": {
    "export": "godot --headless --export-release Windows build/windows/game.exe && godot --headless --export-release Linux build/linux/game.x86_64"
  }
}
```

If you would rather see each export as its own line in the log, give the build stage a
[list of scripts](../configuration/scripts.md) instead of one command.

## Authentication in CI

Steam Guard makes a plain password login impossible on a fresh runner. The usual approach is to log in once on a
machine you control, then carry the resulting `config.vdf` into CI as a secret and place it before dispat runs:

```yaml title=".github/workflows/release.yml"
      - name: Restore the Steam session
        run: |
          mkdir -p ~/Steam/config
          echo "$STEAM_CONFIG_VDF" | base64 -d > ~/Steam/config/config.vdf
        env:
          STEAM_CONFIG_VDF: ${{ secrets.STEAM_CONFIG_VDF }}
      - run: dispat
        env:
          STEAM_USERNAME: ${{ secrets.STEAM_USERNAME }}
```

The `steam-login` script then succeeds without a prompt, once, and every publish in the space reuses that session.

## Worth knowing

- **Uploading is not releasing.** A build sitting on a branch nobody is on changes nothing for players, which is what
  makes this safe to automate. Setting the default branch live is the moment that matters, and the
  `%beta>stable` graduation above is where it happens.
- **Steam build numbers are Valve's, versions are yours.** The tag `game@0.3.0` and the changelog are your record; the
  build description is how you find that release again in the Steamworks list.
- **A failed upload costs you nothing but time.** No version was consumed, and the next run re-does the leg that
  failed. See [recovering from a failed run](../reference/releasing/recovery.md).
- **Keep the export in the build stage.** If `godot --headless` fails, the run stops before `steamcmd` is even
  launched.
- **Test the whole thing on a private branch first.** Set `SetLive` to a branch only you are on, run a release, and
  look at what arrives.

## See also

- [Publishing to itch.io](./itch.md), which is often the same build going to a second store.
- [A game, from one package to many](./game.md) for the repository around this.
- [Prerelease branches](../reference/releasing/prerelease-branches.md) for mapping git branches onto channels.
