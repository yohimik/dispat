# The release announcement

Dispat's own release workflow renders a card and posts it to configured Instagram, LinkedIn, and Discord destinations.
It uses the [`flow.announce`](../configuration/spaces.md#flowannounce) stage and the
[release notes variables](../reference/environment.md#release-notes-data), which other projects can use with their own
announcement scripts.

## RC and stable announcements

The files live in [`services/dispat/announce/`](https://github.com/yohimik/dispat/tree/main/services/dispat/announce),
beside the CLI they announce. There is no announcement script. The `announce` entry of
[`services/dispat/dispat.yaml`](https://github.com/yohimik/dispat/blob/main/services/dispat/dispat.yaml) is a short
[`dispat if`](../cli/if.md) chain that selects a crier configuration by channel:

```sh
dispat if 'DISPAT_STAGE!=announce' --then 'echo "announce: not the announce stage, nothing is posted"' \
  --elif '!ANNOUNCE' --then 'echo "announce: announcements are off for this run"' \
  --elif 'DISPAT_CHANNEL=rc' --then 'crier publish --config announce/rc/crier.yaml' \
  --elif 'DISPAT_CHANNEL=stable' --then 'python3 announce/notes.py | crier publish --config announce/stable/crier.yaml' \
  --else 'echo "announce: no announcement for the $DISPAT_CHANNEL channel"'
```

A release candidate announces only what a person wrote, with every link to that candidate, and no generated notes. A
stable release announces the notes dispat generated for it. Any other prerelease channel has no announcement.

| File | Purpose |
|---|---|
| `rc/crier.yaml` | The release-candidate configuration. Its caption is a `$ref` of `rc/notes.yaml`. Its data is `env:DISPAT_`, so it takes only the version from the release. |
| `rc/notes.yaml` | The human-written release-candidate caption: the headline, every link to the candidate, and the notes. |
| `rc/template.html` | The release-candidate card: a faint lattice on near-black, carrying the same notes and links as the caption. |
| `stable/crier.yaml` | The stable configuration. Its caption prints the generated notes under a fixed introduction. Its data is the document `notes.py` writes to standard input. |
| `stable/template.html` | The stable card: the same layout as print, a pine wash inside a ruled frame, with changelog pages. |
| `rc/anthem-*.mp3` | Release-candidate music: the William Tell galop and In the Hall of the Mountain King. |
| `stable/anthem*.mp3` | Stable music: the 1812 Overture finale and The Stars and Stripes Forever. |
| `publish.yaml` | The cross-posting both configurations pull in: the three destinations and their limits. |
| `notes.py` | JSON data for a stable release: version, changelog sections, and version-pinned install commands. |
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

## What posts where

The renderer is [crier](https://github.com/yohimik/crier). One `crier publish` invocation renders the release and
handles every destination:

| Destination | Output |
|---|---|
| Instagram feed | One photo post: the cover, then the RC notes page or the stable changelog pages. |
| Instagram story | The cover held for sixteen seconds with music from the channel's audio pool. |
| LinkedIn | One photo post with the release caption. |
| Discord | One photo post with the release caption and an explicit `@everyone` mention. |

Rendering is capped at ten pages. Set `ANNOUNCE_COVER_ONLY=1` to use only the stable cover image while retaining the
changelog in captions. The sample pages show each channel's card:

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

The release workflow sets `ANNOUNCE` when the repository holds any of these secrets and the dispatch did not select
`skip_announcements`. Without `ANNOUNCE` the announce script posts nothing, which also keeps a release run from a laptop
quiet. Skipping announcements leaves the test suite, package publication, release records, and installation checks in
place. To run without one destination, set `CRIER_PUBLISH_<NAME>_ENABLED=false` in the environment.

With `ANNOUNCE` set, the workflow checks the destinations with `crier ping` before publishing packages and puts the same
verified executable first on `PATH` for the announcement. The check runs against both channel configurations, because
each channel names music of its own and the gate runs before the release has a version. A missing or revoked credential
stops the release at that gate. Instagram fetches its media from a public URL, so the release job defaults
`CRIER_STAGE_MODE` to `server` behind an ngrok tunnel, and the repository variable can select `url` or `s3` instead.
crier starts the tunnel only when a destination needs a URL.

## Preview and test without posting

Render the RC card, or generate the stable data, from the repository root:

```sh
DISPAT_NEW_VERSION=1.11.0-rc.1 crier render --config services/dispat/announce/rc/crier.yaml

DISPAT_NEW_VERSION=1.11.0 \
DISPAT_FEATURES="Minimal or star topology for identity-linked repositories" \
  python3 services/dispat/announce/notes.py | python3 -m json.tool
```

Run `python3 scripts/announce-test.py` to check the generated stable notes, that the two configurations share their
cross-posting and keep separate cards and music, that an RC links to its own release, that the RC card and its caption
say the same words, and that the RC caption fits each platform's limit. When a `dispat` binary is available it also runs
the announce script with a fake publisher. This test contacts no social platform. The Docker shell gate runs it too,
without a `dispat` binary, so there the announce script is read but not run.

`dispat run announce -p dispat` sets the stage to `run:announce`, so the announce script stops at its first condition.
Actual posting requires the release's `announce` stage and `ANNOUNCE`.

## Recover an incomplete announcement

An incomplete announcement returns failure. There is no automatic retry after an ambiguous publishing result: inspect
the destination first so a replay does not duplicate a successful post.

The [replay workflow](https://github.com/yohimik/dispat/blob/main/.github/workflows/announce.yml) accepts a channel
(`rc` or `stable`) and a script filename inside that channel folder. Write the replay script for the published version
and the destination that needs recovery. It sets `DISPAT_NEW_VERSION`, disables the destinations that already have the
post with `CRIER_PUBLISH_<NAME>_ENABLED=false`, and calls `crier publish` with the channel's `crier.yaml`. A stable
replay pipes `notes.py` with the release-notes variables. The workflow puts the released crier first on `PATH`.
