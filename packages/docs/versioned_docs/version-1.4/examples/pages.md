# A site deployed from the release

You can build and push a documentation or marketing site to a hosting branch as part of a release run. A guard stops a
developer's working tree from ever reaching the live site.

This is how the site you are reading is published. Look at
[`packages/docs/dispat.yaml`](https://github.com/yohimik/dispat/blob/main/packages/docs/dispat.yaml) to see the
original configuration.

## The layout

```
site/package.json     the site
site/build/           what the build produces, gitignored
dispat.json
```

## The configuration

```json title="dispat.json"
{
  "scripts": {
    "build-site": "npm ci && npm run build",
    "deploy-site": "./scripts/deploy.sh"
  },
  "packages": {
    "site": {
      "path": "site",
      "flow": {"build": "build-site", "publish": "deploy-site"},
      "dependencies": [{"provider": "app", "keep": true}]
    }
  }
}
```

The `keep: true` edge on `app` makes the site rebuild after the application it documents. Nothing in the site's files
declares that relationship. You state it once by hand, and `dispat compute` leaves it alone.

## The deploy script

```sh title="site/scripts/deploy.sh"
#!/bin/sh
set -eu

dispat if 'CI!=true' --then 'echo "refusing to deploy outside CI" >&2; exit 1'
: "${GITHUB_TOKEN:?GITHUB_TOKEN is required to push gh-pages}"

touch build/.nojekyll
cd build
git init -q -b gh-pages
git config user.name "github-actions[bot]"
git config user.email "github-actions[bot]@users.noreply.github.com"
git add -A
git commit -qm "docs: deploy ${DISPAT_NEW_VERSION}"
git push -f "https://x-access-token:${GITHUB_TOKEN}@github.com/${GITHUB_REPOSITORY}.git" gh-pages
```

A publish stage is an ordinary shell command. Running `dispat run deploy-site` from a laptop force-pushes a
half-finished working tree over the live site. The first line uses [`dispat if`](../cli/if.md) to prevent this. The
deploy only runs where `CI` is set.

The second line guards against a missing secret. Failing before the first `git` call is cheaper than failing halfway
through a force-push.

## Why a publish stage rather than a separate workflow

A deploy triggered by a push to `main` runs on every commit. It triggers even when nothing changes, and it has no idea
which version it publishes. A publish stage runs only when a release happens. It knows its version. The tag it produces
is the record that this exact site went out.

This approach also orders the site after what it documents. With the `keep: true` edge above, a release that ships both
packages builds the site only once the application is published.

## Other hosts

Only the last two lines of the script are GitHub-specific.

| Host | The publish stage |
|------|-------------------|
| GitHub Pages | force-push `build/` to a `gh-pages` branch, as above |
| Netlify | `netlify deploy --prod --dir build --message "$DISPAT_NEW_VERSION"` |
| Cloudflare Pages | `wrangler pages deploy build --branch main --commit-message "$DISPAT_NEW_VERSION"` |
| S3 and CloudFront | `aws s3 sync build s3://acme-site --delete` then an invalidation |
| A plain server | `rsync -a --delete build/ deploy@acme.example:/srv/site/` |

Each of these commands takes a version string. Pass `$DISPAT_NEW_VERSION` to make the deploy history readable next to
your tags.

## Worth knowing

- **The build is often the link checker.** A static site generator that fails on a broken link turns the build stage
  into a gate. A bad link never reaches the publish stage.
- **Deploying is not idempotent for a person, but it is for a run.** Re-running a failed release repeats the deploy.
  This is safe because the same commit produces the same site.
- **Version the site like everything else.** Its tag says when it was published. Its changelog says what changed.
- **Keep secrets out of the script.** The script reads `GITHUB_TOKEN` from the environment and never echoes it.

## See also

- [`dispat if`](../cli/if.md) for the guard, including file tests and `--changed`.
- [dispat in CI](../reference/ci.md) for the job around this.
- [A game, from one package to many](./game.md), where the landing page is one of the packages.
