# Keeping configuration beside the code

You have three ways to keep a folder's release configuration in the folder itself. You can write a space configuration
file for a space's own exceptions, or a package configuration file for one package. Use a `.dispatexclude` file when a
migration leaves two config files in one place.

## Keeping a space's exceptions inside its folder

Put a `dispat.json` into a space folder to give that space its own configuration file. This helps when you have a
sub-team's area or a vendored tree and you want to avoid editing the root config. The top-level object in this file is
the space, and it overrides the root file field by field.

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

Every package of `libs` now tags as `libs/<name>@<version>`, and `reports` rolls its folder back when it fails. The
root config never learns either fact, but you still must define the space at the root because that is where its `path`
and name live. Commands run from inside the space keep resolving to the monorepo root, so
`cd packages/reports && dispat status` works as before.

You can put these same entries in the root file instead. Place them under the space rather than at the top level:

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

Use whichever approach keeps the exception where you will look for it. The nearer configuration wins when both files
name the same package. You can read the full order in
[the override ladder](../configuration/packages.md#the-override-ladder).

## One space over several folders

Pass a list to a space's `path` when packages that release the same way sit in different root folders. Every direct
sub-folder in that list becomes a package of the one space. Each folder can carry its own space configuration file, and
later entries override earlier ones:

```json title="dispat.json (root)"
{
  "scripts": { "build": "npm run build", "publish": "npm publish" },
  "spaces": {
    "libs": { "path": ["packages", "plugins"], "flow": { "build": "build", "publish": "publish" } },
    "ops": { "path": "tools", "versioning": "none" }
  }
}
```

The `packages/` and `plugins/` folders now share one release configuration without moving. The `tools/` folder holds
scripts-only packages that [never release](../reference/releasing/versioning.md#packages-that-never-release-none). The
first listed folder is the primary one for the space, meaning the login script runs there and
`dispat exec --in space:libs` resolves there.

## Keeping a package's exceptions inside its folder

Create a config file inside a package folder to configure that specific package. The top-level object is exactly a
package entry, making it the most local layer of the ladder. It beats every entry above it that names the same package,
and the file travels with the package so a moved package keeps its exceptions.

Move the `reports` exception from the space file into the package itself:

```json title="packages/reports/dispat.json"
{
  "revertOnFail": true,
  "versionGroup": "platform"
}
```

The folder now declares everything unusual about itself, rolling back on failure and versioning with the `platform`
group. A package file cannot carry a `path` key because a file cannot move its own folder. dispat refuses a package
file that declares `spaces` or `packages`, since a folder holding its own monorepo root belongs behind a
`.dispatexclude`.

Resolution stays root-first here too. Run the CLI from inside the package and dispat ascends past the package's own
file, and past its space's, to the monorepo root. This means `cd packages/reports && dispat lint` works no matter what
the folders on the way carry, and you can read the full rules in
[in-folder configuration files](../configuration/packages.md#in-folder-configuration-files).

## Two config files in one folder

Migrating from JSON to YAML, or generating one config while hand-writing another, leaves two files where dispat expects
to choose one. Without a hint, dispat takes the first name in its list (`dispat.json`, then `dispat.yaml`,
`dispat.yml`, `dispat.toml`). During a migration, this default is usually the file you want to retire.

Name the file to skip in a `.dispatexclude` right next to it:

```
# dispat.json is generated; the checked-in config is dispat.yaml
dispat.json
```

You can use this exclusion in the repository root, a space folder, or a package folder. It applies to that folder
alone, so you can migrate one folder at a time. Run with an explicit `--config dispat.json` to load the excluded file
anyway, letting you compare the two side-by-side.
