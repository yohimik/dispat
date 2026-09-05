# Release announcements

The release posts one image carousel to Instagram and one to LinkedIn when their credentials are configured. The first photo is the cover; the remaining photos contain the changelog. Both platforms receive the changelog in their text caption, with a release-notes link before it. Platform text limits still apply to long captions.

There are no video or story posts. Instagram rendering is capped at ten pages and LinkedIn at twenty, matching each post's attachment limit. Rendering beyond the limit fails before publication instead of creating several posts.

For an account that cannot accept multiple images, set `ANNOUNCE_INSTAGRAM_COVER_ONLY=1` or `ANNOUNCE_LINKEDIN_COVER_ONLY=1`. `ANNOUNCE_COVER_ONLY=1` uses only the cover on both platforms. This removes changelog pages from the pictures while preserving the changelog in the caption data.

A failed publishing request is not retried automatically: a lost response may mean the post already exists. Check the destination before replaying. `ANNOUNCE_ONLY=linkedin` replays LinkedIn without Instagram or a tunnel.

Run `python3 scripts/announce-test.py` from the repository root to check the flow with a fake publisher. It never contacts a social platform. The Docker shell gate also runs this check.
