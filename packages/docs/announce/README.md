# Release announcements

This folder belongs to the documentation package, which announces each dispat release once the site it links to is live. Each channel has a folder of its own, `rc/` and `stable/`, holding its Crier configuration (`crier.yaml`), its words (`notes.yaml`), its card (`template.html`), and its music. `publish.yaml` and the fonts are shared.

The `announce` entry of [../dispat.yaml](../dispat.yaml) runs after the site deploy. It posts only when `ANNOUNCE` is set and the docs release picks up a new dispat version, which it reads from the updated-provider listing (`DISPAT_UPDATED_DISPAT_NEW_VERSION` and `DISPAT_UPDATED_DISPAT_CHANNEL`). dispat's channel names the configuration, and the stage hands crier the version as `ANNOUNCE_NEW_VERSION` (`render.data: env:ANNOUNCE_`).

| Channel | Configuration | What it says |
|---|---|---|
| `rc` | `rc/crier.yaml` | What a person wrote in `rc/notes.yaml` and on `rc/template.html`, with every link to this candidate. |
| `stable` | `stable/crier.yaml` | What a person wrote in `stable/notes.yaml` and on `stable/template.html`, with every link to this release. |

Nothing generated is posted on either channel. Any other prerelease channel has no folder, and the stage says so and posts nothing.

The step outputs the release workflow reads, `cli-released` and `cli-version`, are written by the CLI package's `postPublish` hook, so they never wait on a social network.

## What is posted

The stage runs crier four times with one seed, the version's `cksum`, so every picture and the clip share accent colours and music:

1. Discord and LinkedIn: the whole card, cover first. Discord's body is the full text without the hashtags, with `@everyone`.
2. Instagram feed: a carousel that opens with the cover held for sixteen seconds under the channel's music, then the notes pages as photos.
3. Instagram stories: the same clip first.
4. Instagram stories: one story per notes page, fitted into 1080x1920.

The Instagram passes lay the card out without its cover (`ANNOUNCE_NOCOVER=true`), because the clip already is the cover. Each call runs whatever the one before it did, and the stage fails at the end if any post did.

## Same cross-posting, separate cards and music

Both configurations take their `publish` section from `publish.yaml`, so a release candidate and a stable release post to the same destinations with the same limits: Instagram, LinkedIn, and Discord, all enabled. Each channel writes only its caption beside that reference. They differ in the card and the music: the candidate uses the lattice card with the William Tell galop or In the Hall of the Mountain King, and the stable release uses the print card with the 1812 Overture or The Stars and Stripes Forever. [anthem.md](anthem.md) records where the clips came from.

## Writing an announcement

Edit two places before a release ships, and keep them the same. Neither is the configuration: each `crier.yaml` pulls its caption in with a `$ref` and never changes between releases.

1. The channel's `notes.yaml`, the caption: the headline, the links, the notes.
2. The words on the channel's `template.html`: the first line of the notes is the cover's lede, the links are printed on the cover above the install commands, and the remaining paragraphs fill the notes page.

The only value either takes from the release is `{{ .new_version }}`. A candidate's links point at this exact candidate: its GitHub release under `releases/tag/services/dispat/v<version>` and its documentation under `https://dispat.dev/next/`. A stable release links the site's current version. `stable/notes.yaml` was drafted for 1.11.0 from the candidate's notes; read it again before the stable release ships.

Crier posts a caption whole, and the platforms refuse a long one: Discord at 2000 characters, Instagram at 2200. Every platform gets the whole text, and Discord gets it without the hashtags.

Nothing checks the two copies by machine. After a rewrite, read the card's preview against the caption, and print each platform's resolved caption with `crier publish --dry-run --json` to see its length.

The previews beside each configuration are local renders, not publication records. From `packages/docs`:

```sh
ANNOUNCE_NEW_VERSION=1.11.0-rc.4 ANNOUNCE_NOCOVER= crier render --config announce/rc/crier.yaml
ANNOUNCE_NEW_VERSION=1.11.0 ANNOUNCE_NOCOVER= crier render --config announce/stable/crier.yaml
```

Set `ANNOUNCE_NOCOVER=true` to see the pages the Instagram passes post.

## The release workflow

The workflow sets `ANNOUNCE` when the repository holds any of the announcement's secrets. Without it nothing is posted, which is also what keeps a release run from a laptop quiet.

With `ANNOUNCE` set, the workflow runs `crier ping` against both channel configurations before publishing any packages. Every destination in `publish.yaml` is checked, so a missing or revoked credential blocks the release. The release job then puts that verified `crier` first on `PATH`, and installs ffmpeg for the clip when Instagram is configured. Instagram fetches its media from a public URL, so the job sets `CRIER_STAGE_MODE` (default `server`, through an ngrok tunnel; the repository variable can select `s3` or `url` instead). Crier starts the tunnel only when a destination needs a URL.

Set the repository secret `CRIER_PUBLISH_DISCORD_WEBHOOK_URL` to an incoming Discord webhook URL. No separate Discord bot token is required. Keep that URL out of configuration files and logs. To run without one destination, set `CRIER_PUBLISH_<NAME>_ENABLED=false` in the environment.

## Recovering an incomplete announcement

The stage reports each destination's result and returns failure if an announcement is incomplete. It does not retry an ambiguous publishing failure. Inspect the destination before replaying to avoid duplicate posts.

The announcement replay workflow selects `rc` or `stable` and a replay script within that folder. A replay script is a few lines: it sets `ANNOUNCE_NEW_VERSION`, switches off the destinations that already have the post with `CRIER_PUBLISH_<NAME>_ENABLED=false`, and calls `crier publish` with the channel's `crier.yaml`. The replay workflow holds no Instagram credentials, so a replay there disables Instagram.

## Requirements

This flow uses Crier's `render --render-video-frames-input`, `publish --publish-input`, `--publish-instagram-lead-video`, `--publish-instagram-story` and `publish.discord.mention-everyone`, all present in Crier 1.1.1.
