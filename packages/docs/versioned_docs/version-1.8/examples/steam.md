# Publishing to Steam

Upload a build to Steam with `steamcmd`. Put the version in the build description and let the release channel decide
which Steam branch goes live. Steam has no version field.

A build is a numbered upload with a description attached, and players get whichever build a branch points at. This maps
onto dispat cleanly once you decide what the description says and which branch a given release sets live.

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

dispat reads the `project.godot` manifest. The `autoVersion` field finds the game's version and writes it back with
nothing else configured. Read [Godot](./godot.md) for the detail.

Set `flow.login` to run your authentication script once before the first publish of the space. It runs once per space,
not once per package. A repository with one game still uses a space to get this behavior.

See [registry login, once per space](./login.md).

## The build script

SteamPipe reads a VDF file. Keep it as a template, because two values in it change per release:

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

Fill the template in using the environment dispat provides:

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

`$DISPAT_CHANNEL` holds the release channel. This is `stable` for an ordinary release, or `beta` or `rc` for a
prerelease. Name your Steam branches after your channels to make the mapping a single line.

## A release

Mark a commit with `%beta` to put the game on the beta line. The script sends it to the `beta` branch:

```console
$ git commit -m "feat(adventure)%beta: new boss fight"
$ dispat
12:54:14 INF release started root=.
12:54:14 INF ● changed baselineFromInitials=true bump=minor channel="stable -> beta" dueToProviders=[] ownCommits=1 package=adventure reason=direct space=games version="0.2.0 -> 0.3.0-beta.0"
12:54:14 INF release plan ready held=0 packages=1 releasing=1
12:54:14 INF manifest reconciled manifest=project.godot package=adventure ranges=0 stage=version version=0.3.0-beta.0 versionWritten=true
12:54:14 INF version succeeded package=adventure stage=version version=0.3.0-beta.0
12:54:14 INF build started package=adventure stage=build version=0.3.0-beta.0
12:54:14 INF build succeeded package=adventure stage=build version=0.3.0-beta.0
12:54:14 INF login started space=games stage=login
12:54:14 INF publish started package=adventure stage=publish version=0.3.0-beta.0
12:54:16 INF would upload 0.3.0-beta.0 to branch 'beta' package=adventure stage=publish version=0.3.0-beta.0
12:54:16 INF published package=adventure stage=publish tag=adventure@0.3.0-beta.0 version=0.3.0-beta.0
12:54:16 INF summary channel=beta package=adventure status=published tag=adventure@0.3.0-beta.0 took=1.4s version="0.2.0 -> 0.3.0-beta.0"
```

Watch the login happen once, after the build succeeds and before the first publish. A build that fails never reaches
Steam.

Testers on the beta branch get the build. Graduate the same work when it is ready for everybody, rather than rebuilding
a different version of it:

```console
$ git commit -m "release(adventure)%beta>stable: ship it"
$ dispat
12:54:16 INF ● changed baselineFromInitials=true bump=minor channel="beta -> stable" dueToProviders=[] ownCommits=1 package=adventure reason=direct space=games version="0.3.0-beta.0 -> 0.3.0"
12:54:16 INF would upload 0.3.0 to branch 'default' package=adventure stage=publish version=0.3.0
12:54:16 INF summary channel=stable package=adventure status=published tag=adventure@0.3.0 took=1.0s version="0.3.0-beta.0 -> 0.3.0"
```

The `0.3.0-beta.0` release became `0.3.0` and the branch became the default one. Both uploads sit on the same store
page. You can read their descriptions in the Steamworks build list.

## Several platforms in one release

A Steam app usually has one depot per platform. Export them all in the build stage, then upload once. SteamPipe takes
every depot in the same run, which keeps the platforms on one build number.

```json
{
  "scripts": {
    "export": "godot --headless --export-release Windows build/windows/game.exe && godot --headless --export-release Linux build/linux/game.x86_64"
  }
}
```

Give the build stage a [list of scripts](../configuration/scripts.md) instead of one command to see each export as its
own line in the log.

## Authentication in CI

Steam Guard makes a plain password login impossible on a fresh runner. Log in once on a machine you control. Carry the
resulting `config.vdf` into CI as a secret and place it before dispat runs:

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

The `steam-login` script then succeeds without a prompt. It runs once, and every publish in the space reuses that
session.

## Worth knowing

- **Uploading is not releasing.** A build sitting on a branch nobody is on changes nothing for players. This makes
  automation safe. Set the default branch live to release the game, which happens in the `%beta>stable` graduation
  above.
- **Steam build numbers are Valve's, versions are yours.** The tag `adventure@0.3.0` and the changelog are your record.
  The build description is how you find that release again in the Steamworks list.
- **A failed upload costs you nothing but time.** No version was consumed. The next run re-does the leg that failed.
  See [recovering from a failed run](../reference/releasing/recovery.md).
- **Keep the export in the build stage.** The run stops before `steamcmd` launches if `godot --headless` fails.
- **Test the whole thing on a private branch first.** Set `SetLive` to a branch only you are on. Run a release and look
  at what arrives.

## See also

- [Publishing to itch.io](./itch.md) to send the same build to a second store.
- [A game, from one package to many](./game.md) for the repository around this.
- [Prerelease branches](../reference/releasing/prerelease-branches.md) to map git branches onto channels.
