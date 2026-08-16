# Keeping configuration beside the code

Three ways to keep a folder's release configuration in the folder itself: a space configuration file for a space's own
exceptions, a package configuration file for one package's, and a `.dispatexclude` for the moment a migration leaves
two config files in one place.

## Keeping a space's exceptions inside its folder

A space that lives on its own (a sub-team's area, a vendored tree, anything you would rather not edit the root config
for) can carry its own configuration file. Drop a `dispat.json` into the space folder; its top-level object is the
space, and it overrides what the root file says field by field.

```json title="dispat.json (root)"
{
  "scripts": { "build": "npm run build", "publish": "npm publish" },
  "spaces": {
    "libs": { "path": "packages", "flow": { "build": "build", "publish": "publish" } }
  }
}
```

```json title="packages/dispat.json"
{
  "tagFormat": "libs/{name}@{version}",
  "packages": {
    "reports": { "revertOnFail": true }
  }
}
```

Now every package of `libs` tags as `libs/<name>@<version>`, `reports` rolls its folder back when it fails, and the root
config never had to learn either fact. The space still has to exist at the root, because that is where its `path` and
its name live; the file only adjusts it. Commands run from inside the space keep resolving to the monorepo root, so
`cd packages/reports && dispat status` works as before.

The same entries can go in the root file instead, under the space rather than at the top level:

```json
{
  "spaces": {
    "libs": {
      "path": "packages",
      "packages": { "reports": { "revertOnFail": true } }
    }
  }
}
```

Use whichever keeps the exception where you will look for it. When both name the same package, the nearer one wins; the
full order is [the override ladder](../configuration/packages.md#the-override-ladder).

## One space over several folders

A space's `path` also takes a list, for the layout where packages that release the same way sit in different root
folders. Every listed folder's direct sub-folders are packages of the one space, and each folder may carry its own
space configuration file, later entries overriding earlier ones:

```json title="dispat.json (root)"
{
  "scripts": { "build": "npm run build", "publish": "npm publish" },
  "spaces": {
    "libs": { "path": ["packages", "plugins"], "flow": { "build": "build", "publish": "publish" } },
    "ops": { "path": "tools", "versioning": "none" }
  }
}
```

Here `packages/` and `plugins/` share one release configuration without either folder having to move, and `tools/`
holds scripts-only packages that [never release](../reference/releasing/versioning.md#packages-that-never-release-none).
The first listed folder is the space's primary one: the login script runs there, and `dispat exec --in space:libs`
resolves there.

## Keeping a package's exceptions inside its folder

One level further down, a package folder can carry a config file of its own. Its top-level object is exactly a package
entry, it is the most local layer of the ladder, so it beats every entry above that names the same package, and the
file travels with the package: a package moved between spaces keeps its exceptions.

The `reports` exception from the space file above, moved into the package itself:

```json title="packages/reports/dispat.json"
{
  "revertOnFail": true,
  "versionGroup": "platform"
}
```

Now the folder says everything unusual about itself: it rolls back on failure, and it versions with the `platform`
group. The one key a package file cannot carry is `path`, because a file cannot move the folder it lives in, and a
package file that declares `spaces` or `packages` is refused with guidance: a folder holding a monorepo root of its
own belongs behind a `.dispatexclude`, not half-merged.

Resolution stays root-first here too. Running the CLI from inside the package ascends past the package's own file, and
past its space's, to the monorepo root, so `cd packages/reports && dispat lint` works whatever the folders on the way
carry. The full rules are [in-folder configuration files](../configuration/packages.md#in-folder-configuration-files).

## Two config files in one folder

Migrating from JSON to YAML, or generating one config while hand-writing another, leaves two files where dispat expects
to choose one. Without a hint it takes the first name in its list (`dispat.json`, then `dispat.yaml`, `dispat.yml`,
`dispat.toml`), which during a migration is usually the file you are trying to retire.

Name the one to skip in a `.dispatexclude` next to it:

```
# dispat.json is generated; the checked-in config is dispat.yaml
dispat.json
```

That works in the repository root, in a space folder and in a package folder, and always applies to that folder alone,
so a migration can move one folder at a time. An explicit `--config dispat.json` still loads the excluded file: the flag
is exact, which is what makes it usable for a side-by-side comparison of the two.
