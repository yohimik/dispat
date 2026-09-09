# An npm monorepo

Save this configuration to define the smallest real npm monorepo release setup. It contains one space of packages, a
build and a publish command, and versions computed from your commits.

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

Commit your work with the package name in the scope. Run `dispat status` to check the plan before you release anything:

```console
$ git commit -m "feat(logger): first version of the logger"
$ dispat status
12:04:05 INF ● changed bump=minor channel=stable dueToProviders=[] ownCommits=1 package=logger reason=direct space=libs version="0.0.0 -> 0.1.0"
12:04:05 INF release plan ready held=0 packages=1 releasing=1
```

Run `dispat` to execute the release. The `status` command you just ran changes nothing on disk.

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

Push the annotated tag to finish the release. This tag is the record that the publish happened, but you can also
configure dispat to push it for you (see [release records](../configuration/records.md)).

Stamp the computed version into your package before packing. Your `package.json` version field does not drive anything.
dispat computes the version from commits and tags, and hands it to your scripts as `$DISPAT_NEW_VERSION`:

```sh
npm version "$DISPAT_NEW_VERSION" --no-git-tag-version && npm ci && npm run build
```
