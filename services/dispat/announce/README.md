# Release announcements

This folder belongs to the Dispat CLI package. Each channel has a folder of its own, `rc/` and `stable/`, holding its Crier configuration (`crier.yaml`), its card (`template.html`), and its music. The release candidate's words are in `rc/notes.yaml`. `publish.yaml` and the fonts are shared.

There is no announcement script and nothing builds data first. The `announce` entry of [../dispat.yaml](../dispat.yaml) is one Crier command whose configuration is named by the channel, behind the `ANNOUNCE` switch:

```sh
dispat if ANNOUNCE --then 'crier publish --config "announce/$DISPAT_CHANNEL/crier.yaml"' --else 'echo "announce: announcements are off for this run"'
```

Both configurations read their data from the stage's environment (`render.data: env:DISPAT_`), which dispat fills for every stage.

| Channel | Configuration | What it says |
|---|---|---|
| `rc` | `rc/crier.yaml` | Only what a person wrote: the caption in `rc/notes.yaml` and the same words on `rc/template.html`, with every link to this candidate. It reads `DISPAT_NEW_VERSION` and no generated notes. |
| `stable` | `stable/crier.yaml` | The notes dispat generated for the release, under a fixed introduction: `DISPAT_BREAKING_CHANGES`, `DISPAT_FEATURES`, `DISPAT_FIXES`, and `DISPAT_DEPENDENCIES`, one entry per line. |

Any other prerelease channel has no folder, so Crier reports the configuration it looked for and the announce stage only warns.

The step outputs the release workflow reads, `cli-released` and `cli-version`, are written by the package's `postPublish` hook, so they never wait on a social network.

## Same cross-posting, separate cards and music

Both configurations take their `publish` section from `publish.yaml`, so a release candidate and a stable release post to the same destinations with the same limits: Instagram, LinkedIn, and Discord, all enabled. Each channel writes only its caption beside that reference. They differ in the card and the music: the candidate uses the lattice card with the William Tell galop or In the Hall of the Mountain King, and the stable release uses the print card with the 1812 Overture or The Stars and Stripes Forever. [anthem.md](anthem.md) records where the clips came from.

## Writing a release candidate

Edit two places before a candidate ships, and keep them the same. Neither is the configuration: `rc/crier.yaml` pulls the caption in with a `$ref` and never changes between candidates.

1. [rc/notes.yaml](rc/notes.yaml), the caption: the headline, the links, the notes.
2. The words on [rc/template.html](rc/template.html): the first line of the notes is the cover's lede, the links are printed on the cover above the install commands, and the remaining paragraphs fill the notes page.

The only value either takes from the release is `{{ .new_version }}`, which Crier builds from `DISPAT_NEW_VERSION` (`render.data: env:DISPAT_`). The links therefore point at this exact candidate: its GitHub release under `releases/tag/services/dispat/v<version>` and its documentation under `https://dispat.dev/next/`.

Crier posts a caption whole, and the platforms refuse a long one: Discord at 2000 characters, Instagram at 2200. The caption keeps every link on every platform. On Discord it follows the lede with a pointer to the pictures instead of the long paragraphs, because the pictures attached to the same message carry the notes in full.

`python3 scripts/announce-test.py` fails when a paragraph or a link of the caption is missing from the card, when the card says something the caption does not, or when the caption is over a platform's limit.

The [cover preview](rc/preview-1.jpg) and [notes preview](rc/preview-2.jpg) show the draft for the planned `1.11.0-rc.1` release:

```sh
DISPAT_NEW_VERSION=1.11.0-rc.1 crier render --config services/dispat/announce/rc/crier.yaml
```

## Stable releases

The stable card prints each release-notes group as dispat wrote it, one entry per line, and paginates: rendering is capped at ten pages, so each destination receives one photo post. Preview it without posting:

```sh
DISPAT_NEW_VERSION=1.11.0 DISPAT_FEATURES="a feature" DISPAT_FIXES="a fix" crier render --config services/dispat/announce/stable/crier.yaml
```

The pictures always carry the groups in full. The caption carries a group only while it is under 330 characters and otherwise points at the pictures, which keeps the worst case inside Instagram's 2200 characters. Discord leaves the groups to the pictures attached to the same message. A caption reads all four groups, and dispat sets each of them, empty when it has no entries; a hand-run `crier publish` needs them set too.

The stable card has a [cover preview](stable/preview-1.jpg) and a [changelog preview](stable/preview-2.jpg) with sample notes for `1.11.0`. The previews are local renders, not publication records.

## What is posted

One `crier publish` command renders the pages and sends one photo post to each destination. Discord explicitly enables `@everyone` notifications, and the captions write the mention for Discord only. The same command also publishes an Instagram story: the cover, shown for sixteen seconds with music from the channel's audio pool, fitted into a vertical frame.

## The release workflow

The workflow sets `ANNOUNCE` when the repository holds any of the announcement's secrets and the dispatch did not select `skip_announcements`. Without it nothing is posted, which is also what keeps a release run from a laptop quiet. Skipping leaves the full test suite, package publication, release records, and installation checks enabled.

With `ANNOUNCE` set, the workflow runs `crier ping` against both channel configurations before publishing any packages. Every destination in `publish.yaml` is checked, so a missing or revoked credential, or a missing story input, blocks the release. The release job then puts that verified `crier` first on `PATH`. Instagram fetches its media from a public URL, so the job sets `CRIER_STAGE_MODE` (default `server`, through an ngrok tunnel; the repository variable can select `s3` or `url` instead). Crier starts the tunnel only when a destination needs a URL.

Set the repository secret `CRIER_PUBLISH_DISCORD_WEBHOOK_URL` to an incoming Discord webhook URL. No separate Discord bot token is required. Keep that URL out of configuration files and logs. To run without one destination, set `CRIER_PUBLISH_<NAME>_ENABLED=false` in the environment.

## Recovering an incomplete announcement

The publishing command reports each destination's result and returns failure if an announcement is incomplete. It does not retry an ambiguous publishing failure. Inspect the destination before replaying to avoid duplicate posts.

The announcement replay workflow selects `rc` or `stable` and a replay script within that folder. A replay script is a few lines: it sets `DISPAT_NEW_VERSION`, switches off the destinations that already have the post with `CRIER_PUBLISH_<NAME>_ENABLED=false`, and calls `crier publish` with the channel's `crier.yaml` (with the release-notes variables set for a stable replay). The replay workflow holds no Instagram credentials, so a replay there disables Instagram.

## Checks

Run `python3 scripts/announce-test.py` from the repository root. It checks that the two configurations share their cross-posting while keeping separate cards and music, the release candidate's words and limits, the stable caption's worst case against each platform's limit, where the step outputs are written, and, when a `dispat` binary is available, the announce script itself with a fake publisher. It never contacts a social platform. The Docker shell gate also runs this check; it has no `dispat` binary, so there the announce script is read but not run.

This flow requires a Crier release that supports `publish.instagram.cover-story` and `publish.discord.mention-everyone`. Earlier versions fail configuration validation at the ping gate.
