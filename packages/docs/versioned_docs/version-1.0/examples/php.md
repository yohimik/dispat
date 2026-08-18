# Composer packages

PHP packages in one repository, released to Packagist. Composer takes its versions from git tags, so the tag is the
release and `composer.json` only has to name the right constraints.

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

There is nothing to upload. Packagist reads the repository and its tags, so the publish stage is at most a webhook
telling it to look now. If your repository already has the Packagist webhook installed, drop the `publish` slot
entirely and let the tag be the whole release.

`{name}/v{version}` puts the package name in the tag, which is what makes per-package tags in a monorepo readable and
what a Composer monorepo split tool expects.

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

`versionWritten=false` is the normal and correct outcome here. A `composer.json` usually declares no `version` field,
because Composer derives it from the tag, and dispat writes a version field only where one exists. `core` produced no
line at all: nothing in its manifest needed changing.

The constraint in the consumer did move:

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

The name is the `name` field, `vendor/package`. `require` becomes dependencies and `require-dev` becomes dev
dependencies, and platform requirements are skipped, because `php` and `ext-mbstring` are not packages anybody
publishes:

```console
$ dispat scanner
packages/web/composer.json  composer  acme/web
  dependencies     acme/core          ^1.2.0
  dependencies     guzzlehttp/guzzle  ^7.9
  devDependencies  phpunit/phpunit    ^11.3
1 manifest(s), 3 dependency declaration(s)
```

The identity line carries no `@version` because this manifest declares none. Writes go to both `require` sections, and
to the `version` field only if the file already has one.

## Worth knowing

- **A tag is permanent in practice.** Packagist caches what it saw; moving a tag after the fact is how you get two
  different archives with one version number. Fix forward instead.
- **`composer.lock` belongs to applications, not libraries.** If a deployable application in the repository commits
  one, add `composer update --lock` as a [`syncLock`](../configuration/autoversion.md) script so it follows the
  manifest.
- **Path repositories are for development.** A `"repositories": [{"type": "path", ...}]` entry pointing next door must
  not reach a tag; the [`--verify-unlinked` gate](../editing/manifests.md#verifying-the-tree) catches the manifest
  formats dispat links, and a `dispat if` check catches the rest.
- **Branch aliases are unnecessary here.** Every release is a real version on a real tag, which is what
  `^1.3.0` resolves against.

## See also

- [A Go module workspace](./go.md) for the other ecosystem where the tag is the whole publish.
- [Release records](../configuration/records.md) for pushing tags and writing the changelog.
- [Commit message reference](../reference/commits.md) for scopes, propagation and channels.
