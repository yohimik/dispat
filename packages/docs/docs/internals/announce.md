# The release announcement

Dispat's own release workflow renders a card and posts it to configured Instagram, LinkedIn, and Discord destinations.
It uses the [`flow.announce`](../configuration/spaces.md#flowannounce) stage and the
[release notes variables](../reference/environment.md#release-notes-data), which other projects can use with their own
announcement scripts.

## RC and stable announcements

The files live in [`services/dispat/announce/`](https://github.com/yohimik/dispat/tree/main/services/dispat/announce),
beside the CLI they announce. Each channel has a folder with a crier configuration, a card, music, and the text to
review before a release:

| File | Purpose |
|---|---|
| `rc/crier.yaml`, `stable/crier.yaml` | One crier configuration per channel: its card, its music, and the shared cross-posting. |
| `rc/template.html` | The release-candidate card: a faint lattice on near-black. |
| `stable/template.html` | The stable card: the same layout as print, a pine wash inside a ruled frame. |
| `rc/anthem-*.mp3` | Release-candidate music: the William Tell galop and In the Hall of the Mountain King. |
| `stable/anthem*.mp3` | Stable music: the 1812 Overture finale and The Stars and Stripes Forever. |
| `rc/announcement.md` | Release-candidate notes. The current draft introduces identity-linked fleets and their minimal or star link shapes. |
| `rc/links.md` | Links to this release candidate: its GitHub release and its guide. |
| `stable/announcement.md`, `stable/links.md` | Stable-release notes and links. |
| `publish.yaml` | The cross-posting both configurations pull in: destinations, limits, and captions. |
| `announce.sh` | Stage guard, platform selection, channel configuration, and the single publishing command. |
| `notes.sh` | JSON data: version, the channel's notes and links, changelog sections, and version-pinned install commands. |
| `fonts/`, `anthem.md` | Shared fonts, licences, and audio provenance. |

A version such as `1.11.0-rc.1` selects the RC configuration and copy. A stable version selects the stable ones. Build
metadata does not change that selection. Other prerelease channels are refused until an announcement policy exists for
them. Missing notes or links fail before rendering or posting. Fonts and audio stay outside the CLI and Go test image
build contexts.

Both configurations set their `publish` section to a `$ref` of `publish.yaml`, so the two channels cannot post to
different destinations. The channels differ only in `render`: the template and the audio pool. The two cards share
their page geometry, running header and footer, and wrapping rules, so a release paginates the same way on either.

## Notes and links

A channel's fixed text is two committed files and nothing else stores it. `announcement.md` holds the notes.
`links.md` holds one `Label: https://...` link per line, and a line of any other shape fails the run before anything
renders. Either file can write the version as `${DISPAT_NEW_VERSION}`, the spelling the `github.footer` lines of
`dispat.yaml` use, and `notes.sh` fills in the announced version. The RC links therefore name this exact candidate:
its GitHub release under `releases/tag/services/dispat/v<version>` and its guide under `https://dispat.dev/next/`.

The RC notes and links are duplicated in the description and in the picture, because both are built from the same two
files. Every caption prints the links first, so they survive a platform's caption limit, then the notes, then the
generated changelog. The RC card prints the first line of the notes as the cover's lede and the links on the cover
above the install commands, where a reader can copy an address a picture cannot make clickable. The remaining notes
open the page after the cover and the changelog follows them. The stable card keeps its fixed lede.

RC captions label the version as ready for testing. The install commands select the announced version, including
prereleases. Platform caption limits still apply to long release notes.

## What posts where

The renderer is [crier](https://github.com/yohimik/crier). One `crier publish` invocation renders the release and handles
every enabled destination:

| Destination | Output |
|---|---|
| Instagram feed | One photo post containing the cover, the RC notes page, and the changelog pages. |
| Instagram story | The cover held for sixteen seconds with music from the channel's audio pool. |
| LinkedIn | One photo post with the release caption. |
| Discord | One photo post with the release caption and an explicit `@everyone` mention. |

Rendering is capped at ten pages. Set `ANNOUNCE_COVER_ONLY=1` to use only the cover image while retaining the notes and
the changelog in captions. The sample pages show each channel's card:

![Sample release-candidate cover](https://raw.githubusercontent.com/yohimik/dispat/main/services/dispat/announce/rc/preview-1.jpg)

![Sample release-candidate notes page](https://raw.githubusercontent.com/yohimik/dispat/main/services/dispat/announce/rc/preview-2.jpg)

![Sample release-candidate changelog page](https://raw.githubusercontent.com/yohimik/dispat/main/services/dispat/announce/rc/preview-3.jpg)

![Sample stable cover](https://raw.githubusercontent.com/yohimik/dispat/main/services/dispat/announce/stable/preview-1.jpg)

![Sample stable changelog page](https://raw.githubusercontent.com/yohimik/dispat/main/services/dispat/announce/stable/preview-2.jpg)

## Configure destinations

Credentials are optional, but each enabled destination must have a complete configuration:

| Repository secret | Purpose |
|---|---|
| `CRIER_PUBLISH_INSTAGRAM_TOKEN` and `CRIER_PUBLISH_INSTAGRAM_USER_ID` | Instagram publishing credentials. |
| `CRIER_PUBLISH_LINKEDIN_TOKEN` and `LINKEDIN_URN` | LinkedIn token and author identity. The workflow maps the URN to `CRIER_PUBLISH_LINKEDIN_AUTHOR_URN`. |
| `CRIER_PUBLISH_DISCORD_WEBHOOK_URL` | Incoming Discord webhook. Keep it out of files and logs. |
| `NGROK_AUTHTOKEN` | Public media staging for Instagram when `CRIER_STAGE_MODE=server`. |

Without any configured destination, the announcement is skipped. An incomplete credential pair fails. LinkedIn-only
and Discord-only runs need no public staging tunnel. Instagram can use an alternative configured staging mode such as
`url` or `s3` instead of the default server tunnel.

The release workflow checks configured destinations with `crier ping` before publishing packages and uses the same
verified executable for the announcement. The check runs against both channel configurations, because each channel
names music of its own and the gate runs before the release has a version. Select `skip_announcements` when
dispatching Release to omit social posts; the test suite, package publication, release records, and installation checks
still run.

## Preview and test without posting

Generate the RC caption data from the repository root:

```sh
DISPAT_NEW_VERSION=1.11.0-rc.1 \
DISPAT_FEATURES="Minimal or star topology for identity-linked repositories" \
  sh services/dispat/announce/notes.sh | python3 -m json.tool
```

Run `python3 scripts/announce-test.py` to check channel selection, install commands, credential handling, and failure
behavior with a fake publisher. It also checks that the two configurations share their cross-posting and keep separate
cards and music, that an RC links to its own release, and that the RC card draws the notes and links its captions
print. This test contacts no social platform. The Docker shell gate runs it too.

`dispat run announce -p dispat` sets the stage to `run:announce`, so the publishing script stops at its stage guard.
Actual posting requires the release's `announce` stage or an explicit `ANNOUNCE_FORCE=1` override.

## Recover an incomplete announcement

An incomplete announcement returns failure. There is no automatic retry after an ambiguous publishing result:
inspect the destination first so a replay does not duplicate a successful post.

The [replay workflow](https://github.com/yohimik/dispat/blob/main/.github/workflows/announce.yml) accepts a channel
(`rc` or `stable`) and a script filename inside that channel folder. Write the replay script for the published version
and destination that needs recovery. `ANNOUNCE_ONLY=linkedin` or `ANNOUNCE_ONLY=discord` limits the shared publisher to
that destination. `ANNOUNCE_CRIER_BIN` selects the verified binary installed by the workflow.
