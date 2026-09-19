# Release announcements

This folder belongs to the Dispat CLI package. Shared scripts, templates, fonts, and audio live here; announcement copy lives in `rc/announcement.md` and `stable/announcement.md`. Edit the appropriate file before its release.

`notes.sh` selects RC copy for a version such as `1.11.0-rc.1` and stable copy for a version such as `1.11.0`. Other prerelease channels fail before publishing until they have an explicit announcement policy. The selected copy is included in each caption before the generated changelog. Install commands name the exact announced version.

Preview the RC data without rendering or posting:

```sh
DISPAT_NEW_VERSION=1.11.0-rc.1 sh services/dispat/announce/notes.sh | python3 -m json.tool
```

The RC draft is in [rc/announcement.md](rc/announcement.md). Its [cover preview](rc/preview-1.jpg) and [changelog preview](rc/preview-2.jpg) use sample notes for the planned `1.11.0-rc.1` release. They are local renders, not publication records.

The announcement replay workflow selects `rc` or `stable` and a replay script within that folder. Shared publishing remains in `announce.sh`.

The release uses one `crier publish` command to render the cover and changelog pages and send one photo post to each configured destination: Instagram, LinkedIn, and Discord. Every post includes the changelog in its caption, with a release-notes link near the beginning. Discord explicitly enables `@everyone` notifications. Platform text limits still apply to long captions.

The same command also publishes an Instagram story: the first page, shown for sixteen seconds with music from the configured audio pool. It fits the cover into a vertical frame. Instagram's feed keeps its photo carousel and changelog caption; the story carries the music.

Rendering is capped at ten pages, so each destination receives one photo post. If an account cannot accept multiple photos, set `ANNOUNCE_COVER_ONLY=1` to show only the cover on all destinations while keeping the changelog in their captions.

The release workflow runs `crier ping` before publishing any packages. Failed credentials or required story inputs block the release. The publishing command reports each destination's result and returns failure if an announcement is incomplete. It does not retry an ambiguous publishing failure automatically. Inspect the destination before replaying to avoid duplicate posts. `ANNOUNCE_ONLY=linkedin` or `ANNOUNCE_ONLY=discord` limits a replay to that destination and needs no public staging tunnel.

Select `skip_announcements` when dispatching the Release workflow to publish packages without posting to social platforms. This leaves the full test suite, package publication, release records, and installation checks enabled. Announcements remain enabled by default; the opt-out applies only to that workflow run.

Set the repository secret `CRIER_PUBLISH_DISCORD_WEBHOOK_URL` to an incoming Discord webhook URL. No separate Discord bot token is required. Keep that URL out of configuration files and logs.

This flow requires a Crier release that supports `publish.instagram.cover-story` and `publish.discord.mention-everyone`. Earlier versions fail configuration validation at the ping gate.

Run `python3 scripts/announce-test.py` from the repository root to check single-call orchestration with a fake publisher. It never contacts a social platform. The Docker shell gate also runs this check.
