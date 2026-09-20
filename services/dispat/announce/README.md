# Release announcements

This folder belongs to the Dispat CLI package. Each channel has a folder of its own, `rc/` and `stable/`, holding its Crier configuration (`crier.yaml`), its card (`template.html`), its music, and its fixed text: the notes (`announcement.md`) and the links (`links.md`). Edit the appropriate notes and links before its release. They are committed files and nothing else stores them. The scripts, the fonts, and `publish.yaml` are shared.

Both configurations take their whole `publish` section from `publish.yaml`, so a release candidate and a stable release post to the same destinations with the same captions and limits. They differ in the card and the music: the candidate uses the lattice card with the William Tell galop or In the Hall of the Mountain King, and the stable release uses the print card with the 1812 Overture or The Stars and Stripes Forever. [anthem.md](anthem.md) records where the clips came from.

`notes.sh` selects RC copy for a version such as `1.11.0-rc.1` and stable copy for a version such as `1.11.0`, and `announce.sh` selects the same channel's `crier.yaml`. Other prerelease channels fail before publishing until they have an explicit announcement policy. Install commands name the exact announced version.

`links.md` holds one `Label: https://...` link per line. Notes and links may write the version as `${DISPAT_NEW_VERSION}`, the spelling `github.footer` uses in `dispat.yaml`, and `notes.sh` fills in the announced version. The RC links therefore point at this exact candidate: its GitHub release under `releases/tag/services/dispat/v<version>` and its guide under `https://dispat.dev/next/`. A line that is not a label and an `https` address fails before rendering or posting.

The RC notes and links are duplicated in the description and in the picture. Every caption prints the links first, then the notes, then the generated changelog. The RC card draws the same two files: the first line of the notes is the cover's lede, the links are printed on the cover above the install commands, and the remaining notes open the page after the cover, ahead of the changelog. The stable card keeps its fixed lede, and its notes and links appear in the captions.

Preview the RC data without rendering or posting:

```sh
DISPAT_NEW_VERSION=1.11.0-rc.1 sh services/dispat/announce/notes.sh | python3 -m json.tool
```

The RC draft is in [rc/announcement.md](rc/announcement.md) and [rc/links.md](rc/links.md). Its [cover preview](rc/preview-1.jpg), [notes preview](rc/preview-2.jpg) and [changelog preview](rc/preview-3.jpg) use a sample changelog for the planned `1.11.0-rc.1` release. The stable card has the same pair: a [cover preview](stable/preview-1.jpg) and a [changelog preview](stable/preview-2.jpg) with sample notes for `1.11.0`. They are local renders, not publication records.

The announcement replay workflow selects `rc` or `stable` and a replay script within that folder. Shared publishing remains in `announce.sh`, which reads the channel from the version the replay script sets.

The release uses one `crier publish` command to render the cover and changelog pages and send one photo post to each configured destination: Instagram, LinkedIn, and Discord. Every post includes the changelog in its caption, with a release-notes link near the beginning. Discord explicitly enables `@everyone` notifications. Platform text limits still apply to long captions.

The same command also publishes an Instagram story: the first page, shown for sixteen seconds with music from the channel's audio pool. It fits the cover into a vertical frame. Instagram's feed keeps its photo carousel and changelog caption; the story carries the music.

Rendering is capped at ten pages, so each destination receives one photo post. If an account cannot accept multiple photos, set `ANNOUNCE_COVER_ONLY=1` to show only the cover on all destinations while keeping the notes and the changelog in their captions. The RC cover still carries its links.

The release workflow runs `crier ping` against both channel configurations before publishing any packages. Failed credentials or required story inputs block the release. The publishing command reports each destination's result and returns failure if an announcement is incomplete. It does not retry an ambiguous publishing failure automatically. Inspect the destination before replaying to avoid duplicate posts. `ANNOUNCE_ONLY=linkedin` or `ANNOUNCE_ONLY=discord` limits a replay to that destination and needs no public staging tunnel.

Select `skip_announcements` when dispatching the Release workflow to publish packages without posting to social platforms. This leaves the full test suite, package publication, release records, and installation checks enabled. Announcements remain enabled by default; the opt-out applies only to that workflow run.

Set the repository secret `CRIER_PUBLISH_DISCORD_WEBHOOK_URL` to an incoming Discord webhook URL. No separate Discord bot token is required. Keep that URL out of configuration files and logs.

This flow requires a Crier release that supports `publish.instagram.cover-story` and `publish.discord.mention-everyone`. Earlier versions fail configuration validation at the ping gate.

Run `python3 scripts/announce-test.py` from the repository root to check single-call orchestration with a fake publisher, the configuration each channel selects, and that the two configurations share their cross-posting while keeping separate cards and music. It never contacts a social platform. The Docker shell gate also runs this check.
