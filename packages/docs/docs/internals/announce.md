# The release announcement

Dispat's own release workflow renders a card and posts it to configured Instagram, LinkedIn, and Discord destinations.
It uses the [`flow.announce`](../configuration/spaces.md#flowannounce) stage and the
[release notes variables](../reference/environment.md#release-notes-data), which other projects can use with their own
announcement scripts.

## RC and stable announcements

The files live in [`services/dispat/announce/`](https://github.com/yohimik/dispat/tree/main/services/dispat/announce),
beside the CLI they announce. The two channel folders hold the text to review before a release:

| File | Purpose |
|---|---|
| `rc/announcement.md` | Release-candidate copy. The current draft introduces identity-linked fleets and their minimal or star link shapes. |
| `stable/announcement.md` | Stable-release copy. |
| `announce.sh` | Stage guard, platform selection, and the single publishing command. |
| `notes.sh` | JSON data: version, selected channel copy, changelog sections, and version-pinned install commands. |
| `crier.yaml` | Shared rendering and platform settings. |
| `template.html`, `template-b.html` | Two card layouts, selected reproducibly from the release version. |
| `fonts/`, `anthem*.mp3`, `anthem.md` | Shared fonts, audio, licences, and provenance. |

A version such as `1.11.0-rc.1` selects RC copy. A stable version selects stable copy. Build metadata does not change
that selection. Other prerelease channels are refused until an announcement policy exists for them. Missing copy
fails before rendering or posting. Shared fonts and audio stay outside the CLI and Go test image build contexts.

The selected text appears before the generated changelog in each caption. RC cards describe identity-linked fleets and their topology choices, and
RC captions label the version as ready for testing. The install commands select the announced version, including prereleases.
Platform caption limits still apply to long release notes.

## What posts where

The renderer is [crier](https://github.com/yohimik/crier). One `crier publish` invocation renders the release and handles
every enabled destination:

| Destination | Output |
|---|---|
| Instagram feed | One photo post containing the cover and changelog pages. |
| Instagram story | The cover held for sixteen seconds with music from the shared audio pool. |
| LinkedIn | One photo post with the release caption. |
| Discord | One photo post with the release caption and an explicit `@everyone` mention. |

Rendering is capped at ten pages. Set `ANNOUNCE_COVER_ONLY=1` to use only the cover image while retaining the changelog
in captions. The two existing sample pages show the shared visual layout:

![Sample announcement cover](https://raw.githubusercontent.com/yohimik/dispat/main/services/dispat/announce/preview-1.png)

![Sample changelog page](https://raw.githubusercontent.com/yohimik/dispat/main/services/dispat/announce/preview-2.png)

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
verified executable for the announcement. Select `skip_announcements` when dispatching Release to omit social posts;
the test suite, package publication, release records, and installation checks still run.

## Preview and test without posting

Generate the RC caption data from the repository root:

```sh
DISPAT_NEW_VERSION=1.11.0-rc.1 \
DISPAT_FEATURES="Minimal or star topology for identity-linked repositories" \
  sh services/dispat/announce/notes.sh | python3 -m json.tool
```

Run `python3 scripts/announce-test.py` to check channel selection, install commands, credential handling, and failure
behavior with a fake publisher. This test contacts no social platform. The Docker shell gate runs it too.

`dispat run announce -p dispat` sets the stage to `run:announce`, so the publishing script stops at its stage guard.
Actual posting requires the release's `announce` stage or an explicit `ANNOUNCE_FORCE=1` override.

## Recover an incomplete announcement

An incomplete announcement returns failure. There is no automatic retry after an ambiguous publishing result:
inspect the destination first so a replay does not duplicate a successful post.

The [replay workflow](https://github.com/yohimik/dispat/blob/main/.github/workflows/announce.yml) accepts a channel
(`rc` or `stable`) and a script filename inside that channel folder. Write the replay script for the published version
and destination that needs recovery. `ANNOUNCE_ONLY=linkedin` or `ANNOUNCE_ONLY=discord` limits the shared publisher to
that destination. `ANNOUNCE_CRIER_BIN` selects the verified binary installed by the workflow.
