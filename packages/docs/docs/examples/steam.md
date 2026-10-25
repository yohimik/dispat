# Publishing to Steam

Steam identifies each upload with a build number. Put dispat's semantic version in the build description, then map
release channels such as `beta` to Steam branches. Players receive whichever build their branch points to.

The example uses Godot for the export and `steamcmd` for the upload. Replace the export command with your engine's
build command if needed.

## The layout

```
games/adventure/project.godot                  the game itself
games/adventure/scripts/app_build.template.vdf the SteamPipe build script, with two placeholders
games/adventure/scripts/depot_windows.vdf      the Windows depot's file mappings
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

`autoVersion` updates the existing version in `project.godot` before export. See [Godot](./godot.md) for the supported
fields and export settings.

`flow.login` runs once before the space's first publish. Even a repository with one game uses a space here to share
that login behavior. See [Registry login, once per space](./login.md).

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

# A prerelease activates its matching Steam branch. Stable releases are
# uploaded without activating a branch; promote them in Steamworks afterward.
branch="$DISPAT_CHANNEL"
[ "$branch" = "stable" ] && branch=""

build_script="$PWD/scripts/app_build.generated.vdf"
trap 'rm -f "$build_script"' EXIT

sed -e "s|{{DESC}}|$DISPAT_NEW_VERSION|" \
    -e "s|{{BRANCH}}|$branch|" \
    scripts/app_build.template.vdf > "$build_script"

steamcmd +login "$STEAM_USERNAME" +run_app_build "$build_script" +quit
```

`$DISPAT_CHANNEL` holds the release channel. This is `stable` for an ordinary release, or `beta` or `rc` for a
prerelease. Create matching Steam branches before uploading prereleases. Empty `SetLive` leaves the upload inactive;
Steam requires you to promote a build to the default branch manually in App Admin. See the
[SteamPipe build options](https://partner.steamgames.com/doc/sdk/uploading?l=english).

The script runs inside `games/adventure` and writes the generated VDF beside the template. Steam resolves
`ContentRoot` relative to that file, so `../build/` points to the game's build directory. The trap removes the
generated file when the script exits.

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
12:54:16 INF published package=adventure stage=publish tag=adventure@0.3.0-beta.0 version=0.3.0-beta.0
12:54:16 INF summary channel=beta package=adventure status=published tag=adventure@0.3.0-beta.0 took=1.4s version="0.2.0 -> 0.3.0-beta.0"
```

The login runs after the build succeeds and before the first publish. If the build fails, the upload does not run.

Once the work is ready for a stable release, graduate it with a release commit:

```console
$ git commit -m "release(adventure)%beta>stable: ship it"
$ dispat
12:54:16 INF ● changed baselineFromInitials=true bump=minor channel="beta -> stable" dueToProviders=[] ownCommits=1 package=adventure reason=direct space=games version="0.3.0-beta.0 -> 0.3.0"
12:54:16 INF summary channel=stable package=adventure status=published tag=adventure@0.3.0 took=1.0s version="0.3.0-beta.0 -> 0.3.0"
```

The `0.3.0-beta.0` release became `0.3.0`. The stable upload is recorded, but players stay on their existing build
until you promote it to the default branch in Steamworks. Both uploads appear in the Steamworks build list, where
their descriptions identify the dispat versions.

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

## Validate the host and plugin together

A plugin and its host can share a private protocol while arriving through different distribution systems. Updating
one without a compatible host can break consumers even when each artifact installs successfully.

Record the host version, layer commit and target runtime channel alongside the build. Test that installed combination
on a private branch before the publishing script promotes it with `SetLive`. Keep the upload receipt and consumer-test
result separately: successful publication does not establish host compatibility.

dispat orders the configured stages. Your native scripts must enforce the compatibility check and retain those
receipts; dispat does not infer compatible versions or coordinate independently owned repositories.

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

- **Uploading and going live are separate decisions.** Check which branch the script updates and who can access it.
  Test the branch mapping privately before using the stable release path.
- **Steam build numbers are Valve's, versions are yours.** The tag `adventure@0.3.0` and the changelog are your record.
  The build description is how you find that release again in the Steamworks list.
- **A failed upload does not consume a semantic version.** Check Steam's result if the response was lost before
  retrying the failed step. See [recovering from a failed run](../reference/releasing/recovery.md).
- **Keep the export in the build stage.** The run stops before `steamcmd` launches if `godot --headless` fails.
- **Test the whole thing on a private branch first.** Set `SetLive` to a branch only you are on. Run a release and look
  at what arrives.

## See also

- [Publishing to itch.io](./itch.md) to send the same build to a second store.
- [From one package to many](./one-to-many.md) for the repository around this.
- [Prerelease branches](../reference/releasing/prerelease-branches.md) to map git branches onto channels.
