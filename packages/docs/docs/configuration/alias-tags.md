# Alias tags

A package gets one release tag, written from its [`tagFormat`](./versions.md#tagformat). In a monorepo that tag usually
carries a path prefix, so it says which package released and which version:

```
services/dispat/v1.4.2
```

That is the right name for a record. It is the wrong name for a *pointer*. Some things that consume a release do not
want to name a version at all: they want to follow a line and get whatever the newest release on it is. A GitHub Action
is the clearest case, because the only ref shape the Marketplace accepts looks like this:

```yaml
uses: yohimik/dispat@v1
```

`aliasTags` gives a package extra names beside its real tag. Each release writes them all.

```json
{
  "packages": {
    "dispat": {
      "aliasTags": [
        { "format": "v{version}" },
        { "format": "v{major}", "moving": true, "channels": ["stable"] }
      ]
    }
  }
}
```

A release of `dispat` 1.4.2 now writes three refs at the same commit: `services/dispat/v1.4.2`, `v1.4.2` and `v1`. The
first two are written once and never touched again. `v1` is re-pointed on every stable 1.x release, which is what makes
`@v1` mean "the newest 1.x".

## Options

| Key        | Default            | Meaning                                                                                                                |
|------------|--------------------|------------------------------------------------------------------------------------------------------------------------|
| `format`   | required           | The template. See [Placeholders](#placeholders).                                                                        |
| `moving`   | `false`            | Re-point the alias on every release it applies to, instead of writing it once.                                          |
| `channels` | every channel      | Only write the alias for releases on these channels.                                                                    |
| `force`    | [`commit.force`](./records.md#force) | Whether this alias may overwrite an existing ref. A `moving` alias may not set this to `false`. |

`aliasTags` can be set at the repository level, on a space, in a space folder's config file, and on a package, exactly
like `tagFormat`. A list declared at a nearer level **replaces** the inherited one rather than adding to it, so a
package opts out of its space's aliases with an empty list:

```json
{ "packages": { "internal-tool": { "aliasTags": [] } } }
```

## Placeholders

Everything `tagFormat` accepts, plus the three parts of the version on their own:

| Placeholder  | Example       |
|--------------|---------------|
| `{name}`     | `dispat`      |
| `{version}`  | `1.4.2`       |
| `{major}`    | `1`           |
| `{minor}`    | `4`           |
| `{patch}`    | `2`           |
| `{channel}`  | `rc`          |
| `{counter}`  | `1`           |

`{major}`, `{minor}` and `{patch}` are available **only** here. A release tag has to be readable back into the version
that produced it, and `v1` names no release in particular, so a `tagFormat` using one is refused at load.

A format has to name some part of the version, or every release of every package would write the same ref. `latest` is
not a valid alias for that reason.

## Channels

`channels` is what keeps a moving alias honest. Without it, `v1` follows whatever released last, release candidates
included, so a project publishing `1.5.0-rc.1` would move `v1` onto a prerelease and every consumer pinning `@v1` would
get it.

```json
{ "format": "v{major}", "moving": true, "channels": ["stable"] }
```

Now `v1` only ever follows stable releases. A prerelease still gets its own exact alias if one is configured, so
`uses: yohimik/dispat@v1.5.0-rc.1` works for anyone testing it, while `v1` stays where it was.

## Aliases are never read back

dispat finds a package's history by listing the tags its `tagFormat` matches. Aliases are written and never read, so
they take no part in that.

This is load-bearing rather than incidental, and dispat enforces it. If an alias could be read back as a release tag,
the next run would find it while looking for the package's baseline, and a moving alias is always the newest tag by
creation date, so it would be found *first*:

- a bare `v1` does not parse as a version, and an unreadable newest tag makes the whole baseline unreadable, so the
  package would look as though it had never released;
- a bare `v1.4.2` does parse, and would quietly become some package's released version.

So the configuration is refused, at load, if any package's alias would match any package's `tagFormat`:

```
config: package "dispat": alias tag "v1.4.2" would be read back as a release tag of package "cli"
(tagFormat "v{version}"); an alias must never be readable as a release tag, or it becomes that package's history
```

The same check refuses two packages that would write one alias name, which is what a shared-version group would do if
every member declared `v{major}`.

If your packages tag as `{name}@{version}` and you want bare aliases, give the aliases a prefix of their own
(`action-v{major}`), or give the packages a path-prefixed `tagFormat`.

## Failures

An alias is a convenience ref, not the record of a release. If one cannot be written, dispat warns (`W232`) and carries
on: the release tag is already there, and the alias is re-pointed by hand or by the next release. That is different
from the release tag itself, whose failure is a [critical](../architecture.md#after-the-point-of-no-return).

## Pushing

Aliases are pushed with the release tags, so a ref nobody can fetch is never left behind. A moving alias needs to
replace the copy the remote already has, which is what [`commit.force`](./records.md#force) does and why it defaults to
on.
