# The release announcement

Dispat's own release workflow renders a card and posts it to configured Instagram, LinkedIn, and Discord destinations.
It uses the [`flow.announce`](../configuration/spaces.md#flowannounce) stage and the
[release notes variables](../reference/environment.md#release-notes-data), which other projects can use with their own
announcement scripts.

## RC and stable announcements

The files live in [`services/dispat/announce/`](https://github.com/yohimik/dispat/tree/main/services/dispat/announce),
beside the CLI they announce. There is no announcement script, and nothing builds data first. The `announce` entry of
[`services/dispat/dispat.yaml`](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.yaml) is one crier
command whose configuration is named by the channel, behind a [`dispat if`](../cli/if.md) switch:

```sh
dispat if ANNOUNCE \
  --then 'crier publish --config "announce/$DISPAT_CHANNEL/crier.yaml"' \
  --else 'echo "announce: announcements are off for this run"'
```

Both configurations read their data from the stage's environment with `render.data: env:DISPAT_`. A release candidate
announces only what a person wrote, with every link to that candidate, and reads nothing but `DISPAT_NEW_VERSION`. A
stable release announces the notes dispat generated for it, which are the [release-notes
groups](../reference/environment.md#release-notes-data). Any other prerelease channel has no folder, so crier reports
the configuration it looked for and the announce stage only warns.

The step outputs the release workflow reads, `cli-released` and `cli-version`, are written by the package's
[`flow.postPublish`](../configuration/spaces.md) hook, so they never wait on a social network.

| File | Purpose |
|---|---|
| `rc/crier.yaml` | The release-candidate configuration. Its caption is a `$ref` of `rc/notes.yaml`. Its data is `env:DISPAT_`, so it takes only the version from the release. |
| `rc/notes.yaml` | The human-written release-candidate caption: the headline, every link to the candidate, and the notes. |
| `rc/template.html` | The release-candidate card: a faint lattice on near-black, carrying the same notes and links as the caption. |
| `stable/crier.yaml` | The stable configuration. Its caption prints the generated notes under a fixed introduction. Its data is `env:DISPAT_` too: the version and the four release-notes groups. |
| `stable/template.html` | The stable card: the same layout as print, a pine wash inside a ruled frame, with changelog pages. |
| `rc/anthem-*.mp3` | Release-candidate music: the William Tell galop and In the Hall of the Mountain King. |
| `stable/anthem*.mp3` | Stable music: the 1812 Overture finale and The Stars and Stripes Forever. |
| `publish.yaml` | The cross-posting both configurations pull in: the three destinations and their limits. |
| `fonts/`, `anthem.md` | Shared fonts, licences, and audio provenance. |

Both configurations set their `publish` section to a `$ref` of `publish.yaml`, so the two channels cannot post to
different destinations. Each writes only its `caption` beside the reference. The channels differ in `render`: the
template, the data source, and the audio pool. The two cards share their page geometry, running header and footer, so a
release paginates the same way on either. Fonts and audio stay outside the CLI and Go test image build contexts.

## Writing a release candidate

The words of a release candidate are written by hand in two places, and the two must match: `rc/notes.yaml`, which
`rc/crier.yaml` pulls in as its caption with a `$ref`, and the text of `rc/template.html`. Announcing a new candidate
therefore never edits the configuration. The first line of the notes is the cover's lede. The links are printed on the
cover above the install commands, where a reader can copy an address a picture cannot make clickable. The remaining
paragraphs fill the notes page.

The only value either takes from the release is `{{ .new_version }}`, which crier builds from `DISPAT_NEW_VERSION`. The
links therefore name this exact candidate: its GitHub release under `releases/tag/services/dispat/v<version>` and its
documentation under `https://dispat.dev/next/`.

crier posts a caption whole, and the platforms refuse a long one: Discord at 2000 characters and Instagram at 2200. The
caption keeps every link on every platform. On Discord it follows the lede with a pointer to the pictures instead of the
long paragraphs, because the pictures attached to the same message carry the notes in full. RC captions label the
version as ready for testing.

## Stable releases

The stable card prints each release-notes group as dispat wrote it, one entry per line, across as many pages as it needs
up to ten. The pictures always carry the groups in full. The caption carries a group only while it is under 330
characters and otherwise points at the pictures, which keeps the worst case inside Instagram's 2200 characters. Discord
leaves the groups to the pictures attached to the same message. A caption reads all four groups, and dispat sets each of
them, empty when it has no entries.

## What posts where

The renderer is [crier](https://github.com/yohimik/crier). One `crier publish` invocation renders the release and
handles every destination:

| Destination | Output |
|---|---|
| Instagram feed | One photo post: the cover, then the RC notes page or the stable changelog pages. |
| Instagram story | The cover held for sixteen seconds with music from the channel's audio pool. |
| LinkedIn | One photo post with the release caption. |
| Discord | One photo post with the release caption and an explicit `@everyone` mention. |

Rendering is capped at ten pages. The sample pages show each channel's card:

![Sample release-candidate cover](https://raw.githubusercontent.com/yohimik/dispat/main/services/dispat/announce/rc/preview-1.jpg)

![Sample release-candidate notes page](https://raw.githubusercontent.com/yohimik/dispat/main/services/dispat/announce/rc/preview-2.jpg)

![Sample stable cover](https://raw.githubusercontent.com/yohimik/dispat/main/services/dispat/announce/stable/preview-1.jpg)

![Sample stable changelog page](https://raw.githubusercontent.com/yohimik/dispat/main/services/dispat/announce/stable/preview-2.jpg)

## Configure destinations

All three destinations are enabled in `publish.yaml`, and each needs its credentials:

| Repository secret | Purpose |
|---|---|
| `CRIER_PUBLISH_INSTAGRAM_TOKEN` and `CRIER_PUBLISH_INSTAGRAM_USER_ID` | Instagram publishing credentials. |
| `CRIER_PUBLISH_LINKEDIN_TOKEN` and `LINKEDIN_URN` | LinkedIn token and author identity. The workflow maps the URN to `CRIER_PUBLISH_LINKEDIN_AUTHOR_URN`. |
| `CRIER_PUBLISH_DISCORD_WEBHOOK_URL` | Incoming Discord webhook. Keep it out of files and logs. |
| `NGROK_AUTHTOKEN` | Public media staging for Instagram when `CRIER_STAGE_MODE=server`. |

The release workflow sets `ANNOUNCE` when the repository holds any of these secrets. Without `ANNOUNCE` the announce
script posts nothing, which also keeps a release run from a laptop quiet. To run without one destination, set
`CRIER_PUBLISH_<NAME>_ENABLED=false` in the environment.

With `ANNOUNCE` set, the workflow checks the destinations with `crier ping` before publishing packages and puts the same
verified executable first on `PATH` for the announcement. The check runs against both channel configurations, because
each channel names music of its own and the gate runs before the release has a version. A missing or revoked credential
stops the release at that gate. Instagram fetches its media from a public URL, so the release job defaults
`CRIER_STAGE_MODE` to `server` behind an ngrok tunnel, and the repository variable can select `url` or `s3` instead.
crier starts the tunnel only when a destination needs a URL.

## Preview and test without posting

Render either card from the repository root:

```sh
DISPAT_NEW_VERSION=1.11.0-rc.1 crier render --config services/dispat/announce/rc/crier.yaml

DISPAT_NEW_VERSION=1.11.0 \
DISPAT_FEATURES="Minimal or star topology for identity-linked repositories" \
  crier render --config services/dispat/announce/stable/crier.yaml
```

Nothing checks the release candidate's two copies or a caption's length by machine. After a rewrite, read the card
against the caption, and print each platform's resolved caption with `crier publish --dry-run --json`, which makes no
network calls.

Without `ANNOUNCE` the announce script posts nothing, so `dispat run announce -p dispat` and a release run from a laptop
stay quiet.

## Recover an incomplete announcement

An incomplete announcement returns failure. There is no automatic retry after an ambiguous publishing result: inspect
the destination first so a replay does not duplicate a successful post.

The [replay workflow](https://github.com/yohimik/dispat/blob/main/.github/workflows/announce.yml) accepts a channel
(`rc` or `stable`) and a script filename inside that channel folder. Write the replay script for the published version
and the destination that needs recovery. It sets `DISPAT_NEW_VERSION`, disables the destinations that already have the
post with `CRIER_PUBLISH_<NAME>_ENABLED=false`, and calls `crier publish` with the channel's `crier.yaml`. A stable
replay sets the release-notes variables too. The workflow puts the released crier first on `PATH`.
