# Composer packages

You can release PHP packages from one repository to Packagist. Composer takes its versions from git tags. This means
the tag is the release, and `composer.json` only has to name the right constraints.

## The layout

```
packages/core/composer.json    acme/core
packages/web/composer.json     acme/web, requires acme/core
dispat.json
```

## The configuration

```json title="dispat.json"
{
  "tagFormat": "{name}/v{version}",
  "scripts": {
    "test": "vendor/bin/phpunit",
    "notify": "curl -sf -XPOST -H 'content-type:application/json' \"https://packagist.org/api/update-package?username=$PACKAGIST_USER&apiToken=$PACKAGIST_TOKEN\" -d '{\"repository\":{\"url\":\"https://github.com/acme/acme\"}}'"
  },
  "spaces": {
    "packages": {
      "path": "packages",
      "flow": {"build": "test", "publish": "notify"},
      "autoVersion": {"enabled": true}
    }
  }
}
```

You have nothing to upload. Packagist reads the repository and its tags, so the publish stage is at most a webhook
telling it to look now. Drop the `publish` slot entirely if your repository already has the Packagist webhook
installed. The tag becomes the whole release.

The `{name}/v{version}` format puts the package name in the tag. This makes per-package tags in a monorepo readable. It
is also what a Composer monorepo split tool expects.

## A release

```console
$ git commit -m "feat(core)^: psr-18 client"
$ dispat status
12:44:57 INF ● changed baselineFromInitials=true bump=minor channel=stable dueToProviders=[] ownCommits=1 package=core reason=direct space=packages version="1.2.0 -> 1.3.0"
12:44:57 INF ● changed baselineFromInitials=true bump=patch channel=stable dependsOn=["core"] dueToProviders=["core"] ownCommits=0 package=web reason="propagated from core" space=packages version="0.4.1 -> 0.4.2"
12:44:57 INF release plan ready held=0 packages=2 releasing=2

$ dispat autoversion
12:44:57 INF manifest reconciled manifest=composer.json package=web ranges=1 stage=version versionWritten=false
12:44:57 INF auto-versioning finished failed=0 ran=2 skipped=0 stage=autoversion
```

Expect to see `versionWritten=false` as the normal and correct outcome here. A `composer.json` usually declares no
`version` field because Composer derives it from the tag. dispat writes a version field only where one exists.

The `core` package produced no line at all because nothing in its manifest needed changing. Check the consumer to see
the updated constraint:

```json
{
  "name": "acme/web",
  "require": {
    "php": ">=8.2",
    "acme/core": "^1.3.0",
    "guzzlehttp/guzzle": "^7.9"
  }
}
```

## What dispat reads and writes

dispat reads the `name` field as `vendor/package`. It maps `require` to dependencies and `require-dev` to dev
dependencies. It skips platform requirements because nobody publishes `php` or `ext-mbstring` as packages.

```console
$ dispat scanner
packages/web/composer.json  composer  acme/web
  dependencies     acme/core          ^1.2.0
  dependencies     guzzlehttp/guzzle  ^7.9
  devDependencies  phpunit/phpunit    ^11.3
1 manifest(s), 3 dependency declaration(s)
```

Look at the identity line. It carries no `@version` because this manifest declares none. dispat writes to both
`require` sections, and it updates the `version` field only if the file already has one.

## Worth knowing

- **A tag is permanent in practice.** Packagist caches what it saw. Moving a tag creates two different archives with
  one version number, so fix forward instead.
- **`composer.lock` belongs to applications, not libraries.** Add `composer update --lock` as a
  [`syncLock`](../configuration/autoversion.md) script if a deployable application in the repository commits one. This
  ensures the lockfile follows the manifest.
- **Path repositories are for development.** Prevent a `"repositories": [{"type": "path", ...}]` entry pointing next
  door from reaching a tag. The [`--verify-unlinked` gate](../editing/manifests.md#verifying-the-tree) catches the
  manifest formats dispat links. Use a `dispat if` check to catch the rest.
- **Branch aliases are unnecessary here.** Every release is a real version on a real tag. This is what `^1.3.0`
  resolves against.

## See also

- [A Go module workspace](./go.md) shows the other ecosystem where the tag is the whole publish.
- [Release records](../configuration/records.md) explains pushing tags and writing the changelog.
- [Commit message reference](../reference/commits.md) covers scopes, propagation, and channels.
