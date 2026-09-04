# The release announcement

Read this page to understand how a dispat release posts itself to Instagram and LinkedIn, what it takes to turn that
on, and what happens when it is left off. It documents dispat's own release, not a feature of the tool: what makes it
possible is the [`flow.announce`](../configuration/spaces.md#flowannounce) stage and the
[release notes variables](../reference/environment.md#release-notes-data) every stage receives, which any repository
can use the same way.

The announcement is rendered and posted by the release itself, from the GitHub Actions run that cut it, out of the
changelog it just wrote. Nobody schedules it, and no draft is prepared in advance.

## What posts where

The renderer is [crier](https://github.com/yohimik/crier), a single Go binary that renders an HTML template to an image
or a video and publishes it. dispat's announce stage runs it several times over one release:

| Surface | What goes out |
|---|---|
| Instagram feed | One carousel: a sixteen-second video of the cover under the anthem, then the changelog pages as images. |
| Instagram stories | The same clip as the first story, then one story per changelog page, fitted into 1080x1920. |
| LinkedIn | One post: a reel that holds the cover under the fanfare and then leafs through the changelog pages, with the changelog repeated as text. |

LinkedIn takes one video or many images and never both, so the reel is the one post that can carry the whole release.
When the token does not carry the video product, the same card goes out as one multi-image album instead.

## The card

The card is a paginated document. Page one is the cover; a long changelog carries on across the pages behind it, and
each surface turns those pages into what it takes. A release with one fix in it is two pages. A release with a dozen
entries is four, and none of that is configured: a template that overflows paginates.

Page one carries the mark and the wordmark, a `$ dispat release` prompt, the version badge a thumbnail has to carry, a
two-line lede, and the three install routes pinned to the version being announced. The pages behind it carry the
changelog in the sections the changelog itself uses, plus `PICKS UP` for the providers the release rewrote, which on a
monorepo is half of what a run did.

![The announcement card's cover page](https://raw.githubusercontent.com/yohimik/dispat/main/announce/preview-1.png)

![The card's first changelog page](https://raw.githubusercontent.com/yohimik/dispat/main/announce/preview-2.png)

There are two layouts, and a release wears one of them: a faint lattice on near-black, and a radial pine wash inside a
ruled frame. Both draw the site's own palette, both paginate identically, and crier picks between them from the run's
seed. The seed is the version, so a release has a face and a soundtrack of its own and re-running one reproduces it to
the pixel.

## The files

Everything lives in [`announce/`](https://github.com/yohimik/dispat/tree/main/announce) at the repository root, which
is deliberate: the fonts and the audio come to three megabytes and the root deny-all `.dockerignore` keeps them out of
every image build context. The dispat package's announce stage reaches them through `git rev-parse --show-toplevel`.

| File | What it is |
|---|---|
| `announce.sh` | The whole flow: the guard, the seed, the missing-secret check, staging, the clips, and one pass per surface. Every decision is an `announce: ...` line on stderr. |
| `notes.sh` | The card's data document, built from the `DISPAT_*` release notes variables and written to standard output as JSON. It runs without a release, a network or a binary. |
| `crier.yaml` | The card as crier draws it: the template pool, the page size, the hermetic fonts, the anthem pool, and one caption per platform. |
| `template.html`, `template-b.html` | The two layouts. Everything that carries meaning is shared letter for letter; only the look differs. |
| `fonts/` | Poppins and Space Mono with their OFL licences. Hermetic, so a release renders the same on a runner as on a laptop. |
| `anthem*.mp3`, `anthem.md` | Four public-domain clips and their provenance. Instagram takes no audio file and no track id, so a held cover page over a clip is the only way sound reaches it. |
| `preview-1.png`, `preview-2.png` | The two pages above, rendered from a sample release. |

`notes.sh` is a script of its own rather than a function inside `announce.sh` so that the data document can be checked
on its own:

```sh
DISPAT_NEW_VERSION=1.7.2 DISPAT_FIXES="close a leak" sh announce/notes.sh | python3 -m json.tool
```

## Turning it on

Five repository secrets, all optional:

| Secret | What it is |
|---|---|
| `CRIER_PUBLISH_INSTAGRAM_TOKEN` | A long-lived token from "API setup with Instagram business login". A token of that flavour parses only on `graph.instagram.com`, which is why the config names that host. |
| `CRIER_PUBLISH_INSTAGRAM_USER_ID` | The Instagram professional account id. |
| `CRIER_PUBLISH_LINKEDIN_TOKEN` | An OAuth token carrying `w_member_social` or `w_organization_social`, plus `openid profile` for the ping to work. |
| `LINKEDIN_URN` | `urn:li:person:...` or `urn:li:organization:...`. The secret keeps the name crier's own repository uses, and the workflows map it onto `CRIER_PUBLISH_LINKEDIN_AUTHOR_URN`, the key crier reads. |
| `NGROK_AUTHTOKEN` | An ngrok agent token on a paid plan. Instagram fetches the media from a public URL of its own accord and a runner has none, so the stage server needs a tunnel; the free plan's interstitial page breaks Meta's fetch. |

The repository variable `CRIER_STAGE_MODE` is the escape hatch from the tunnel. Set it to `url` or `s3` and the ngrok
token is neither wanted nor asked for.

The release workflow's `ping` job checks the credentials before anything is built, with `crier ping`, which is
read-only and posts nothing. A revoked or expired token fails the run in a minute instead of surfacing as a skipped
announcement after the release has already spent twenty. Without the secrets the job says so and the release proceeds.

## What happens without the secrets

Nothing red. `announce.sh` collects every reason it cannot post into one message and exits 0, because a missing social
media token must not turn a good release into a failed build:

```console
announce: not announcing v1.7.2: no CRIER_PUBLISH_INSTAGRAM_TOKEN CRIER_PUBLISH_INSTAGRAM_USER_ID NGROK_AUTHTOKEN
announce: set them as repository secrets, or set CRIER_STAGE_MODE to stage without a tunnel
```

The reasons are collected rather than reported one at a time, so somebody setting this up is told everything that is
missing at once instead of one secret per release. The same holds further in: a failed post is a line in the log and a
count at the end, and the passes that did work still went out.

Two more things are optional in the same way. Without `ffmpeg` the release goes out silent, as images alone. Without
the LinkedIn pair, Instagram still gets the release.

## Rehearsing and replaying

The script refuses to post unless it is really a release. `dispat run announce -p dispat` sets `DISPAT_STAGE` to
`run:announce` rather than `announce`, and that is the whole check, so a rehearsal on a laptop with the secrets
exported says what it would have done and stops. `ANNOUNCE_FORCE=1` is the way back in.

`ANNOUNCE_ONLY=linkedin` replays that pass alone: no Instagram secrets wanted, no tunnel, no feed and no stories. It
exists because a platform can refuse a post for reasons of its own, and re-running a whole release to say one thing
again is not an option once the tags exist. A replay that fails exits non-zero, unlike a release: a person asked for
one thing, and a green run would hide that it did not happen.

`ANNOUNCE_CRIER_BIN` names the binary to use, for a machine where crier is not on `PATH`. Everywhere else it comes from
the [install manifest](../reference/ci.md#the-other-tools-a-job-needs), which installs its newest release.

The replay itself is a dispatch rather than a laptop holding the repository's secrets:
[`announce.yml`](https://github.com/yohimik/dispat/blob/main/.github/workflows/announce.yml) takes the name of a script
under `announce/` and runs it with the released crier. The replay scripts are written on the day one is needed, and
each is a record of what went out again and why.

## Follow-ups

- **Staging without a tunnel.** `CRIER_STAGE_MODE=s3` against an S3-compatible bucket removes ngrok from the release
  entirely, which is the more robust arrangement: a tunnel is a moving part in the middle of a release.
- **The video product on LinkedIn.** The reel needs a token carrying LinkedIn's Community Management API. Without it
  the album fallback is what posts, which is the whole card but not the film.
