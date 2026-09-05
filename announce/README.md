# Release announcements

The release uses one `crier publish` command to render the cover and changelog pages and send one photo post to each configured destination: Instagram, LinkedIn, and Discord. Every post includes the changelog in its caption, with a release-notes link near the beginning. Discord explicitly enables `@everyone` notifications. Platform text limits still apply to long captions.

The same command also publishes an Instagram story: the first page, shown for sixteen seconds with music from the configured audio pool. It fits the cover into a vertical frame. Instagram's feed keeps its photo carousel and changelog caption; the story carries the music.

Rendering is capped at ten pages, so each destination receives one photo post. If an account cannot accept multiple photos, set `ANNOUNCE_COVER_ONLY=1` to show only the cover on all destinations while keeping the changelog in their captions.

The release workflow runs `crier ping` before publishing any packages. Failed credentials or required story inputs block the release. The publishing command reports each destination's result and returns failure if an announcement is incomplete. It does not retry an ambiguous publishing failure automatically. Inspect the destination before replaying to avoid duplicate posts. `ANNOUNCE_ONLY=linkedin` or `ANNOUNCE_ONLY=discord` limits a replay to that destination and needs no public staging tunnel.

Set the repository secret `CRIER_PUBLISH_DISCORD_WEBHOOK_URL` to an incoming Discord webhook URL. No separate Discord bot token is required. Keep that URL out of configuration files and logs.

This flow requires a Crier release that supports `publish.instagram.cover-story` and `publish.discord.mention-everyone`. Earlier versions fail configuration validation at the ping gate.

Run `python3 scripts/announce-test.py` from the repository root to check single-call orchestration with a fake publisher. It never contacts a social platform. The Docker shell gate also runs this check.
