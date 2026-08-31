# Publishing to itch.io

Push a build to itch.io with `butler`. You target one channel per platform and attach the release version so the itch
page shows what players download. itch.io is the least ceremonial store to automate. One command takes a folder and
uploads only the parts that changed.

## The layout

```
games/adventure/project.godot        the game itself
games/adventure/scripts/itch-push.sh one butler push per platform
dispat.json
```

## The configuration

```json title="dispat.json"
{
  "scripts": {
    "export": "godot --headless --export-release Windows build/windows/game.exe && godot --headless --export-release Linux build/linux/game.x86_64 && godot --headless --export-release macOS build/osx/game.zip",
    "itch": "./scripts/itch-push.sh"
  },
  "spaces": {
    "games": {
      "path": "games",
      "flow": {"build": "export", "publish": "itch"},
      "autoVersion": {"enabled": true}
    }
  }
}
```

dispat reads `project.godot` as a manifest. The `autoVersion` field finds the game's version and writes it back with
nothing else configured. [Godot](./godot.md) covers the rest of what dispat reads there, including `plugin.cfg` and
`export_presets.cfg`.

Provide `BUTLER_API_KEY` in the job's environment to authenticate the publish stage. `butler` reads this directly, so
you skip the login step.

## The publish script

```sh title="games/adventure/scripts/itch-push.sh"
#!/bin/sh
set -eu

# A prerelease gets its own itch channels, so testers opt in and everyone
# else keeps the stable download.
suffix=""
[ "$DISPAT_CHANNEL" = "stable" ] || suffix="-$DISPAT_CHANNEL"

for target in windows linux osx; do
  butler push "build/$target" "acme/adventure:${target}${suffix}" \
    --userversion "$DISPAT_NEW_VERSION" --if-changed
done
```

Set `--userversion` to tell itch what version to show and store against the build. Add `--if-changed` so a re-run after
a partial failure skips the platforms that already landed. The channel name tells itch which platform a build targets.
This is why you spell `windows`, `linux`, and `osx` exactly like that.

## A release

```console
$ git commit -m "fix(adventure): stop the save file corrupting on quit"
$ dispat
12:55:07 INF release started root=.
12:55:07 INF ● changed baselineFromInitials=true bump=patch channel=stable dueToProviders=[] ownCommits=1 package=adventure reason=direct space=games version="0.2.0 -> 0.2.1"
12:55:07 INF release plan ready held=0 packages=1 releasing=1
12:55:07 INF manifest reconciled manifest=project.godot package=adventure ranges=0 stage=version version=0.2.1 versionWritten=true
12:55:07 INF version succeeded package=adventure stage=version version=0.2.1
12:55:07 INF build started package=adventure stage=build version=0.2.1
12:55:07 INF build succeeded package=adventure stage=build version=0.2.1
12:55:07 INF publish started package=adventure stage=publish version=0.2.1
12:55:08 INF butler push build/windows acme/adventure:windows --userversion 0.2.1 --if-changed package=adventure stage=publish version=0.2.1
12:55:08 INF butler push build/linux acme/adventure:linux --userversion 0.2.1 --if-changed package=adventure stage=publish version=0.2.1
12:55:08 INF butler push build/osx acme/adventure:osx --userversion 0.2.1 --if-changed package=adventure stage=publish version=0.2.1
12:55:08 INF published package=adventure stage=publish tag=adventure@0.2.1 version=0.2.1
12:55:08 INF summary channel=stable package=adventure status=published tag=adventure@0.2.1 took=1.3s version="0.2.0 -> 0.2.1"
12:55:08 INF done cancelled=0 failed=0 held=0 published=1 skipped=0 took=1.3s unchanged=0
```

Everything a player sees now agrees. The download is `0.2.1`, the tag says `adventure@0.2.1`, and the changelog entry
under that heading says the save file no longer corrupts on quit.

## Two things called a channel

These concepts are unrelated. They meet in the script above.

An **itch channel** is a download slot on your game's page. It names a platform (`windows`, `linux-demo`, `html5`) and
each one holds one current build.

A **dispat channel** is the release line a version is on: `stable`, `beta`, `rc`. It comes from the commits and shows
up in `$DISPAT_CHANNEL` and in the version itself (`0.3.0-beta.0`).

Map one onto the other however you choose. The script above appends the dispat channel to the itch channel, so a beta
build lands on `windows-beta` and never replaces the stable download.

Upload prereleases to a separate itch project if you prefer. The only rule is that a build players are not meant to get
must not land on a channel they are subscribed to.

## Worth knowing

- **butler uploads differences, not files.** The second push of a 4 GB game usually transfers a few megabytes. This
  makes publishing every release affordable.
- **`butler status acme/adventure` tells you what is live**. Run this in a
  [`postPublish` hook](../configuration/spaces.md) to make the log state what players now see.
- **The version is metadata, not identity.** itch keeps its own build numbers per channel. The `--userversion` flag
  makes those numbers mean something to you afterwards.
- **A failed push costs nothing.** Nothing gets tagged as published for that platform. The next run repeats the leg,
  and `--if-changed` skips whatever already arrived.

## See also

- [Publishing to Steam](./steam.md) to send the same build to a second store.
- [A game, from one package to many](./game.md) for the repository around this setup.
- [Script environment variables](../reference/environment.md) to see everything a stage can read.
