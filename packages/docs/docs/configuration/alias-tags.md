# Alias tags

A package gets one release tag. dispat writes it using the package's [`tagFormat`](./versions.md#tagformat). In a
monorepo that tag usually carries a path prefix to show which package released and which version:

```
services/dispat/v1.4.2
```

That is the right name for a record, but it is the wrong name for a *pointer*. Consumers often want to follow a release
line instead of naming an exact version. A GitHub Action is the clearest example because the Marketplace only accepts
this ref shape:

```yaml
uses: yohimik/dispat@v1
```

Use `aliasTags` to give a package extra names beside its real tag. Every release writes all of them.

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

A release of `dispat` 1.4.2 now writes three refs at the same commit: `services/dispat/v1.4.2`, `v1.4.2` and `v1`.
dispat writes the first two once and never touches them again. It re-points `v1` on every stable 1.x release, so `@v1`
always means the newest 1.x version.

## Options

| Key        | Default            | Meaning                                                                                                                |
|------------|--------------------|------------------------------------------------------------------------------------------------------------------------|
| `format`   | required           | The template. See [Placeholders](#placeholders).                                                                        |
| `moving`   | `false`            | Re-point the alias on every matching release instead of writing it once.                                          |
| `channels` | every channel      | Only write the alias for releases on these channels.                                                                    |
| `force`    | [`commit.force`](./records.md#force) | Whether this alias may overwrite an existing ref. A `moving` alias cannot set this to `false`. |

You can set `aliasTags` at the repository level, on a space, in a space folder's config file, and on a package. This
works exactly like `tagFormat`. A list declared at a nearer level **replaces** the inherited one, so a package opts out
of its space's aliases with an empty list:

```json
{ "packages": { "internal-tool": { "aliasTags": [] } } }
```

## Placeholders

`format` accepts everything `tagFormat` accepts, plus the three parts of the version on their own:

| Placeholder  | Example       |
|--------------|---------------|
| `{name}`     | `dispat`      |
| `{version}`  | `1.4.2`       |
| `{major}`    | `1`           |
| `{minor}`    | `4`           |
| `{patch}`    | `2`           |
| `{channel}`  | `rc`          |
| `{counter}`  | `1`           |

`{major}`, `{minor}` and `{patch}` are available **only** here. A release tag must be readable back into the version
that produced it. Because `v1` names no release in particular, dispat refuses a `tagFormat` that uses these
placeholders at load.

A format must name some part of the version. If it does not, every release of every package writes the same ref.
`latest` is not a valid alias for that reason.

## Channels

Use `channels` to restrict a moving alias. Without it, `v1` follows whatever released last, including release
candidates. A project publishing `1.5.0-rc.1` would move `v1` onto a prerelease, and every consumer pinning `@v1` would
receive it.

```json
{ "format": "v{major}", "moving": true, "channels": ["stable"] }
```

Now `v1` only ever follows stable releases. A prerelease still gets its own exact alias if you configure one. This
means `uses: yohimik/dispat@v1.5.0-rc.1` works for anyone testing it, while `v1` stays where it was.

The `channels` field takes the same values everywhere it appears in the configuration file. Naming nothing selects
every release, `stable` selects the stable line, and `*` selects any prerelease. A bare name like `beta` selects that
specific channel and ignores case. You also use this field to configure
[which channels record](./records.md#choosing-the-channels-that-record) and
[which releases a changelog line reaches](./records.md#choosing-which-releases-a-line-reaches).

## Aliases are never read back

dispat finds a package's history by listing the tags its `tagFormat` matches. It writes aliases but never reads them,
so they take no part in that history.

This distinction is load-bearing, and dispat enforces it. A moving alias is always the newest tag by creation date, so
if dispat could read one back as a release tag it would find it first while looking for the package's baseline, and a
bare `v1.4.2` beside a `v{version}` release format would quietly become that package's released version.

dispat refuses the configuration at load if any package's alias reads back as a release tag of any package:

```
config: package "dispat": alias tag "v1.4.2" would be read back as a release tag of package "cli"
(tagFormat "v{version}"); an alias must never be readable as a release tag, or it becomes that package's history
```

Reads back, not looks alike. A name that carries no version is never a release of anything, so it is legal even when
it shares the release format's prefix. This is what lets a single-package repository releasing as `v1.4.2` publish the
`v1` a GitHub composite is consumed through:

```json
{
  "tagFormat": "v{version}",
  "aliasTags": [{ "format": "v{major}", "moving": true, "channels": ["stable"] }]
}
```

dispat recognises `v1` as an alias when it lists tags and leaves it out of the history. It knows every package's alias
formats, not only the listing owner's, because an alias belongs to whoever writes it and lands in whichever listing its
shape matches: one package's `v1` sits in another's `v{version}` listing looking exactly like a release nobody can
parse. A tag that matches the format and carries no version but is nobody's alias, such as a mistyped `v1.0.0.0`,
stays in and is still what the [`initials`](./versions.md#initials) fallback is for.

The same check refuses two packages that write the same alias name. A shared-version group would trigger this if every
member declared `v{major}`.

If two packages tag as `{name}@{version}` and both want bare aliases, give the aliases their own prefix
(`action-v{major}`). Alternatively, give the packages a path-prefixed `tagFormat`.

## Failures

An alias is a convenience ref, not the record of a release. If dispat cannot write one, it warns (`W232`) and carries
on because the release tag is already there. You or the next release can re-point the alias later, which differs from
the release tag itself, where a failure is a [critical](../internals/architecture.md#after-the-point-of-no-return).

## Pushing

dispat pushes aliases with the release tags, so it never leaves behind a ref nobody can fetch. A moving alias needs to
replace the copy the remote already has. This is what [`commit.force`](./records.md#force) does, and it is why the flag
defaults to on.
