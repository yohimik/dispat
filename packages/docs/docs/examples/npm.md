# An npm monorepo

The smallest real npm monorepo release setup: one space of packages, a build and a publish command, and versions
computed from your commits. This page also covers registry login, which belongs to the space rather than to any one
package.

The smallest real setup: one space of npm packages, a build and a publish command.

```json
{
  "scripts": {
    "build": "npm ci --silent && npm run build --silent",
    "publish": "npm publish --access public"
  },
  "spaces": {
    "libs": {
      "path": "packages",
      "flow": {
        "build": "build",
        "publish": "publish"
      }
    }
  }
}
```

Commit work with the package name in the scope, then look at the plan before releasing anything:

```console
$ git commit -m "feat(logger): first version of the logger"
$ dispat status
12:04:05 INF ● changed bump=minor channel=stable dueToProviders=[] ownCommits=1 package=logger reason=direct space=libs version="0.0.0 -> 0.1.0"
12:04:05 INF release plan ready held=0 packages=1 releasing=1
```

`status` changes nothing; it shows what a release would do. The release itself:

```console
$ dispat
12:04:05 INF ● changed bump=minor channel=stable dueToProviders=[] ownCommits=1 package=logger reason=direct space=libs version="0.0.0 -> 0.1.0"
12:04:05 INF release plan ready held=0 packages=1 releasing=1
12:04:05 INF build started package=logger stage=build version=0.1.0
12:04:05 INF added 42 packages in 1s package=logger stage=build version=0.1.0
12:04:05 INF build succeeded package=logger stage=build version=0.1.0
12:04:05 INF publish started package=logger stage=publish version=0.1.0
12:04:05 INF + logger@0.1.0 package=logger stage=publish version=0.1.0
12:04:05 INF published package=logger stage=publish tag=logger@0.1.0 version=0.1.0
12:04:05 INF summary channel=stable package=logger status=published tag=logger@0.1.0 took=1.2s version="0.0.0 -> 0.1.0"
12:04:05 INF done cancelled=0 failed=0 held=0 published=1 skipped=0 took=1.2s unchanged=0

$ git tag
logger@0.1.0
```

The annotated tag is the record that the publish happened. Push it (or let dispat push it, see
[release records](../configuration/records.md)) and the release is done.

One thing to know about versions: your `package.json` version field does not drive anything. dispat computes the version
from commits and tags, and hands it to your scripts as `$DISPAT_NEW_VERSION`. A typical build script therefore stamps it
in before packing:

```sh
npm version "$DISPAT_NEW_VERSION" --no-git-tag-version && npm ci && npm run build
```

## Registry login, once per space

Authentication belongs to the space, not to any one package. A `login` entry runs once before the space's first publish;
every other publish of the space waits for it, and if it fails, every publish of the space fails (none of them could
have succeeded). The login runs in the space folder, so a space-local config file is always found at the same place.

```json
{
  "scripts": {
    "docker-login": "echo \"$REGISTRY_TOKEN\" | docker login registry.example.com -u ci --password-stdin"
  },
  "spaces": {
    "images": {
      "path": "images",
      "isBuildWaitingPublish": true,
      "flow": {
        "build": "img-build",
        "publish": "img-publish",
        "login": "docker-login"
      }
    }
  }
}
```

For npm the same slot typically writes an `.npmrc`:

```sh
echo "//registry.npmjs.org/:_authToken=$NPM_TOKEN" >> ~/.npmrc
```

A login script can also pass values forward (a short-lived token, say) by appending
`DISPAT_OUTPUT_<NAME>=value` lines to `$DISPAT_OUTPUT`; the space's publish scripts then read
`$DISPAT_OUTPUT_<NAME>`. See [Script outputs](../reference/environment.md#script-outputs).
