# A site deployed from the release

Put the site build in `build` and its deployment command in `publish`. This example deploys the generated files to a
GitHub Pages branch and refuses to deploy unless `CI=true`.

For this repository's site configuration, see
[`packages/docs/dispat.yaml`](https://github.com/yohimik/dispat/blob/main/packages/docs/dispat.yaml).

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

The dependency on `app` orders the site after the application it documents. `keep: true` preserves that hand-written
edge when you run `dispat compute`. To release the site when only the app changes, request propagation in the app's
commit. The edge alone does not request a site release.

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

You can invoke a publish script directly with `dispat run deploy-site`, so the script needs its own guard. Here,
[`dispat if`](../cli/if.md) refuses deployment unless `CI` equals `true`. The next line checks `GITHUB_TOKEN` before
any Git operations begin.

## Why a publish stage rather than a separate workflow

A publish stage runs when the site has a planned release and receives that version as `$DISPAT_NEW_VERSION`. Its tag
records the deployment alongside the other packages in the run.

When both packages release, the site's build waits for the app's build and its deployment waits for the app's publish.
If generating the site needs the app's published artifact, set `isBuildWaitingPublish: true` on `app` as well.

## Other hosts

Keep the build and release graph, then replace the deployment script for your host:

| Host | The publish stage |
|------|-------------------|
| GitHub Pages | force-push `build/` to a `gh-pages` branch, as above |
| Netlify | `netlify deploy --prod --dir build --message "$DISPAT_NEW_VERSION"` |
| Cloudflare Pages | `wrangler pages deploy build --branch main --commit-message "$DISPAT_NEW_VERSION"` |
| S3 and CloudFront | `aws s3 sync build s3://acme-site --delete` then an invalidation |
| A plain server | `rsync -a --delete build/ deploy@acme.example:/srv/site/` |

Where the host supports a deployment message, include `$DISPAT_NEW_VERSION` so its history can be matched to your tags.

## Worth knowing

- **The build is often the link checker.** A static site generator that fails on a broken link turns the build stage
  into a gate. A bad link never reaches the publish stage.
- **A retry can repeat the deployment.** Keep the same source revision and built files, and make the deployment
  command safe to repeat. Check the host first if a previous deployment's result is unclear.
- **Version the site like everything else.** Its tag says when it was published. Its changelog says what changed.
- **Keep secrets out of the script.** The script reads `GITHUB_TOKEN` from the environment and never echoes it.

## See also

- [`dispat if`](../cli/if.md) for the guard, including file tests and `--changed`.
- [dispat in CI](../reference/ci.md) for the job around this.
- [From one package to many](./one-to-many.md), where the landing page is one of the packages.
