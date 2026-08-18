# Publishing to itch.io

Pushing a build to itch.io with `butler`, one channel per platform, with the release version attached so the itch page
shows what players are downloading.

itch.io is the least ceremonial store to automate. There is one command, it takes a folder, and it uploads only the
parts that changed.

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
      "autoVersion": {
        "enabled": true,
        "manifests": "none",
        "replace": [
          {"files": ["project.godot"], "find": "config/version=\"{previous}\"", "write": "config/version=\"{version}\""}
        ]
      }
    }
  }
}
```

`butler` reads `BUTLER_API_KEY` from the environment, so there is no login step: put the key in the job's environment
and the publish stage is authenticated.

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

Three flags carry the whole integration. `--userversion` is the version itch shows and stores against the build.
`--if-changed` makes a re-run after a partial failure a no-op for the platforms that already landed. The channel name
is what tells itch which platform a build is for, which is why `windows`, `linux` and `osx` are spelled exactly like
that.

## A release

```console
$ git commit -m "fix(adventure): stop the save file corrupting on quit"
$ dispat
12:55:07 INF release started root=.
12:55:07 INF ● changed baselineFromInitials=true bump=patch channel=stable dueToProviders=[] ownCommits=1 package=adventure reason=direct space=games version="0.2.0 -> 0.2.1"
12:55:07 INF release plan ready held=0 packages=1 releasing=1
12:55:07 INF file reconciled file=project.godot occurrences=1 package=adventure stage=version version=0.2.1
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

Everything a player sees now agrees: the download is `0.2.1`, the tag says `adventure@0.2.1`, and the changelog entry
under that heading says the save file no longer corrupts on quit.

## Two things called a channel

They are unrelated, and the script above is where they meet.

An **itch channel** is a download slot on your game's page. It names a platform (`windows`, `linux-demo`, `html5`) and
each one holds one current build.

A **dispat channel** is the release line a version is on: `stable`, `beta`, `rc`. It comes from the commits and shows
up in `$DISPAT_CHANNEL` and in the version itself (`0.3.0-beta.0`).

Mapping one onto the other is a naming choice. The script above appends the dispat channel to the itch channel, so a
beta build lands on `windows-beta` and never replaces the stable download. Uploading prereleases to a separate itch
project instead is equally valid; the only rule is that a build players are not meant to get should not land on a
channel they are subscribed to.

## Worth knowing

- **butler uploads differences, not files.** The second push of a 4 GB game usually transfers a few megabytes, which
  is what makes publishing every release affordable.
- **`butler status acme/adventure` tells you what is live**, and is a good thing to run in a
  [`postPublish` hook](../configuration/spaces.md) if you want the run's log to state what players now see.
- **The version is metadata, not identity.** itch keeps its own build numbers per channel; `--userversion` is what
  makes those numbers mean something to you afterwards.
- **A failed push costs nothing.** Nothing was tagged as published for that platform, and the next run repeats the
  leg with `--if-changed` skipping whatever already arrived.

## See also

- [Publishing to Steam](./steam.md), usually the same build going to a second store.
- [A game, from one package to many](./game.md) for the repository around this.
- [Script environment variables](../reference/environment.md) for everything a stage can read.
