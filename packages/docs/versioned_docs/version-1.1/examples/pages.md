# A site deployed from the release

A documentation or marketing site that is built and pushed to a hosting branch as part of the run, with a guard that
stops a developer's working tree ever reaching the live site.

This is how the site you are reading is published. Its configuration is in
[`packages/docs/dispat.yaml`](https://github.com/yohimik/dispat/blob/main/packages/docs/dispat.yaml) if you want the
original.

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

The `keep: true` edge on `app` is what makes the site rebuild after the thing it documents. Nothing in the site's
files declares that relationship, so it is stated once, by hand, and `dispat compute` leaves it alone.

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

The first line is the important one. A publish stage is an ordinary shell command, so `dispat run deploy-site` would
happily run it from a laptop and force-push a half-finished working tree over the live site.
[`dispat if`](../cli/if.md) turns that into a refusal: the deploy only runs where `CI` is set.

The second line is the same idea for a missing secret. Failing before the first `git` call is cheaper than failing
halfway through a force-push.

## Why a publish stage rather than a separate workflow

A deploy triggered by a push to `main` runs on every commit, including the ones that changed nothing the site cares
about, and it has no idea which version it is publishing. As a publish stage it runs when a release happens, it knows
its version, and the tag it produces is the record that this exact site went out.

It also means the site is ordered after what it documents. With the `keep: true` edge above, a release that ships both
builds the site only once the application it describes is published.

## Other hosts

Nothing here is GitHub-specific except the last two lines of the script.

| Host | The publish stage |
|------|-------------------|
| GitHub Pages | force-push `build/` to a `gh-pages` branch, as above |
| Netlify | `netlify deploy --prod --dir build --message "$DISPAT_NEW_VERSION"` |
| Cloudflare Pages | `wrangler pages deploy build --branch main --commit-message "$DISPAT_NEW_VERSION"` |
| S3 and CloudFront | `aws s3 sync build s3://acme-site --delete` then an invalidation |
| A plain server | `rsync -a --delete build/ deploy@acme.example:/srv/site/` |

Each of them takes a version string somewhere. Pass `$DISPAT_NEW_VERSION` and the deploy history becomes readable
next to the tags.

## Worth knowing

- **The build is often the link checker.** A static site generator that fails on a broken link turns the build stage
  into a gate, and a bad link never reaches the publish stage.
- **Deploying is not idempotent for a person, but it is for a run.** Re-running a failed release repeats the deploy,
  which is safe because the same commit produces the same site.
- **Version the site like everything else.** Its tag says when it was published; its changelog says what changed.
- **Keep secrets out of the script.** The script reads `GITHUB_TOKEN` from the environment and never echoes it.

## See also

- [`dispat if`](../cli/if.md) for the guard, including file tests and `--changed`.
- [dispat in CI](../reference/ci.md) for the job around this.
- [A game, from one package to many](./game.md), where the landing page is one of the packages.
