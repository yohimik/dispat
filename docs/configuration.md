# Configuration & CLI reference

## CLI

```
dispat [command] [flags]
```

| Command             | Effect                                                                                                   |
|---------------------|----------------------------------------------------------------------------------------------------------|
| `release` (default) | Plan, print the graph, then run version/build/publish for every changed package, record releases, tag.   |
| `status`            | Plan and print the graph with computed version bumps, then exit. Nothing is executed, tagged or written. |

| Flag            | Default       | Effect                                                                |
|-----------------|---------------|-----------------------------------------------------------------------|
| `--root`        | `.`           | Monorepo root folder (git repo root).                                 |
| `--config`      | `dispat.json` | Config file name, relative to `--root`.                               |
| `--concurrency` | from config   | Override: one value for both stages (`7`) or `build,publish` (`4,2`). |
| `--log-level`   | from config   | Override: `trace`, `debug`, `info`, `warn`, `error`.                  |
| `--log-format`  | from config   | Override: `pretty` or `json`.                                         |
| `--help`        |               | Print usage.                                                          |

Flag precedence (via viper): explicitly set flag > config file > flag default > built-in default.

Exit codes: `0` success (including "nothing changed"), `1` configuration/planning error or at least one package failed,
`2` bad command line.

## Configuration file

Loaded with viper: the format is inferred from the file extension, so JSON (default `dispat.json`), YAML or TOML all
work. Unknown keys are rejected (typo protection). Viper matches keys case-insensitively and lowercases map keys, so
script and space names are effectively case-insensitive.

### Top level

| Key            | Type                           | Required  | Description                                                                                                                      |
|----------------|--------------------------------|-----------|----------------------------------------------------------------------------------------------------------------------------------|
| `scripts`      | map name → shell command       | no        | Named shell commands, like package.json scripts. Referenced by spaces.                                                           |
| `spaces`       | map name → space               | yes (≥ 1) | Package groups sharing build/publish behaviour.                                                                                  |
| `dependencies` | list of `{consumer, provider}` | no        | Package-level consumer → provider relations. Both must exist; self-dependencies and cycles are rejected; duplicates are ignored. |
| `concurrency`  | int or `[int, int]`            | no        | One value for both stages, or `[build, publish]`. `0` (or omitted) means number of CPUs. More than two values is an error.       |
| `logLevel`     | string                         | no        | Minimum log level: `trace`, `debug`, `info` (default), `warn` or `error`.                                                        |
| `logFormat`    | string                         | no        | Logger output: `pretty` (default; colored console output) or `json` (machine-readable lines for CI ingestion).                   |
| `changelog`    | object                         | no        | Per-package changelog file options; see below.                                                                                   |
| `github`       | object                         | no        | GitHub release options; see below.                                                                                               |
| `initials`     | map package → version          | no        | Baseline versions used when a package's latest tag is missing or unparseable; see below.                                         |
| `commit`       | object                         | no        | End-of-run release commit, tagging and push; see below. Disabled by default.                                                     |
| `shell`        | array of strings               | no        | Command prefix scripts are appended to, e.g. `["bash", "-c"]` or `["cmd", "/C"]`. Default `["/bin/sh", "-c"]`.                   |

### Space options

| Key                     | Type        | Required   | Description                                                                                                                                                                                                                                                                                                                                                                             |
|-------------------------|-------------|------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `path`                  | string      | yes        | Folder relative to the root. Every direct sub-folder is a package named after the folder (hidden folders are skipped). Package names must be unique across all spaces.                                                                                                                                                                                                                  |
| `isBuildWaitingPublish` | bool        | no (false) | When `true`, consumers of packages from this space may only start their version/build stages after the provider is *published*, not merely built. When `false`, consumers may build as soon as the provider is built. In both modes a consumer's own publish always waits for the provider's publish and is skipped if it failed (unless the consumer has a release reason of its own). |
| `revertOnFail`          | bool        | no (false) | When `true`, all local changes inside the package folder are rolled back (tracked files restored from HEAD, untracked files removed) if the package fails at any stage — or is skipped after its version stage already modified files.                                                                                                                                                  |
| `buildScript`           | script name | no         | Build stage command.                                                                                                                                                                                                                                                                                                                                                                    |
| `publishScript`         | script name | no         | Publish stage command.                                                                                                                                                                                                                                                                                                                                                                  |
| `versionScript`         | script name | no         | Manifest-sync stage command; runs exactly before the build, only for packages bumped due to provider updates.                                                                                                                                                                                                                                                                           |

All script references are optional. A stage without a script still runs — ordering, skip semantics, statuses, tags and
release records are fully preserved — it just executes no shell command. Scripts run through the configured `shell`
(default `/bin/sh -c`) with the package folder as the working directory.

### Entry format options (shared by `changelog` and `github`)

| Key                 | Default            | Description                         |
|---------------------|--------------------|-------------------------------------|
| `dateFormat`        | `2006-01-02`       | Go time layout for the entry date.  |
| `breakingTitle`     | `Breaking Changes` | Section title for breaking changes. |
| `featuresTitle`     | `Features`         | Section title for features.         |
| `fixesTitle`        | `Fixes`            | Section title for fixes.            |
| `dependenciesTitle` | `Dependencies`     | Section title for provider updates. |

### `changelog`

| Key       | Default        | Description                                   |
|-----------|----------------|-----------------------------------------------|
| `enabled` | `true`         | Write a changelog file per published package. |
| `file`    | `CHANGELOG.md` | File name inside the package folder.          |
| `title`   | `# Changelog`  | First line of the file.                       |
| *format*  |                | All entry format options above.               |

New entries are prepended below the title, newest first.

### `github`

| Key        | Default                   | Description                                                                                                                                                                                                                                                                    |
|------------|---------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `enabled`  | `true`                    | Create a GitHub release per published package.                                                                                                                                                                                                                                 |
| `owner`    | from `$GITHUB_REPOSITORY` | Repository owner.                                                                                                                                                                                                                                                              |
| `repo`     | from `$GITHUB_REPOSITORY` | Repository name.                                                                                                                                                                                                                                                               |
| `apiUrl`   | `https://api.github.com`  | REST endpoint; set for GitHub Enterprise.                                                                                                                                                                                                                                      |
| `tokenEnv` | `GITHUB_TOKEN`            | Name of the environment variable holding the API token.                                                                                                                                                                                                                        |
| *format*   |                           | All entry format options above. The release body contains only the sections — the `## pkg@version (date)` header line used in changelog files is omitted, since the release title is already the tag and GitHub shows its own date; `dateFormat` therefore has no effect here. |

The release is named after the tag (`pkg@1.3.0`); its body is the rendered changelog sections. When `enabled` but no
repository or token can be resolved at runtime, GitHub releases are skipped with a warning instead of failing the run.
If the tag has not been pushed yet, GitHub creates it at the default branch head.

### `initials`

A map of package name → `MAJOR.MINOR.PATCH` (validated at load time). The value is the *baseline* the next release bumps
from — it never becomes a release by itself. It applies in exactly two situations:

- the package has no `pkg@*` tag at all (a first release), or
- the newest `pkg@*` tag (by creation date) exists but its version cannot be parsed as strict semver — e.g. a stray
  `core@0.0.1-0.0.0`. In that case older parseable tags are deliberately *not* used, and commits are still scanned from
  the unparseable tag (not the whole history).

Example: `"initials": {"core": "1.0.0"}` with an unparseable newest tag and one `fix(core)` commit since it releases
`core@1.0.1`. Packages without an entry fall back to `0.0.0` as usual. A parseable latest tag always beats initials.
Keys are matched case-insensitively against discovered packages (viper lowercases map keys); entries matching no package
are warned about and ignored.

### `commit`

| Key             | Default                  | Description                                                                                                     |
|-----------------|--------------------------|-----------------------------------------------------------------------------------------------------------------|
| `enabled`       | `false`                  | Create one release commit at the end of a successful run.                                                       |
| `messageFormat` | `chore(release): {tags}` | Template; `{tags}` and `{packages}` become comma-separated lists.                                               |
| `push`          | `false`                  | Push the release commit and tags (`git push --follow-tags <remote> HEAD`). Only applies when `enabled` is true. |
| `remote`        | `origin`                 | Remote to push to.                                                                                              |

When enabled, the run finishes with a *finalize phase*: all published packages' folders are staged and committed in a
single commit (changelog files, version-script manifest changes — add build outputs to `.gitignore` or they get
committed too), release tags are created **on that commit** instead of during each publish, and GitHub releases move to
the end of the run. Every GitHub release body then documents the release commit SHA and the tag in a `### Release`
section — whether or not they were pushed. With `push` on, releases are created after the push and the tag is
additionally pinned to the release commit via `target_commitish`; without `push`, the SHA cannot be sent to GitHub (it
does not exist on the remote yet), so GitHub creates the tag ref at the default branch head until you push — the true
commit and tag remain recorded in the release body. If nothing changed on disk (e.g. changelogs disabled), no empty
commit is created but tags are still placed.

Pushing requires a checked-out branch (not a detached HEAD — use `actions/checkout` with a `ref`). When `push` is
enabled, remote access is **verified before any release work starts** (`git ls-remote`), and likewise an enabled GitHub
configuration is verified against the API (`GET /repos/{owner}/{repo}`) — misconfigured credentials fail the run
immediately, before anything is built. A failure during the finalize phase itself (commit, tag, push, GitHub release)
exits 1, but already-published registry artifacts stay published.

## Script environment variables

Every script receives, on top of the parent environment:

| Variable             | Example      | Meaning                                               |
|----------------------|--------------|-------------------------------------------------------|
| `DISPAT_PACKAGE`     | `core`       | Package name.                                         |
| `DISPAT_SPACE`       | `libs`       | Space name.                                           |
| `DISPAT_OLD_VERSION` | `1.2.3`      | Version being replaced (`0.0.0` for a first release). |
| `DISPAT_NEW_VERSION` | `1.3.0`      | Version being released.                               |
| `DISPAT_BUMP`        | `minor`      | `patch`, `minor` or `major`.                          |
| `DISPAT_TAG`         | `core@1.3.0` | Tag that will be created on success.                  |
| `DISPAT_STAGE`       | `build`      | `version`, `build` or `publish`.                      |

The version stage additionally receives `DISPAT_UPDATED_PROVIDERS`: a JSON array like
`[{"package":"core","space":"libs","oldVersion":"1.2.3","newVersion":"1.3.0"}]` listing this package's changed
providers. Providers that failed or were skipped are filtered out (their versions were never released); if no
successfully updated provider remains — the package proceeds only on its own commits — the version script is not
executed at all.
