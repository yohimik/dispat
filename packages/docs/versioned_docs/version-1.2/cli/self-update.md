# The self-update command

Run `dispat self-update` to replace your running binary with the latest stable release of dispat. You do not need a
config file or a git repository. This command updates the tool itself, regardless of the directory you are in.

dispat downloads the binary for your platform from the GitHub release, verifies the published size and checksum, and
runs it once to prove it works. It keeps your old binary as `<name>.backup` and removes it during a future run a week
later. Because nothing moves until every check passes, a failed update leaves your working binary exactly where it was.

After the new binary is in place, dispat prints the changes and links to the changelog for the installed tag. Run with
`--check` to see this information before you download anything.

```console
$ dispat self-update --check
current   dispat 1.0.0 (darwin_arm64)
available dispat 1.1.0 (services/dispat/v1.1.0)

what changed in 1.1.0

  Features
    - self-update reads out what it installed

full changelog: https://github.com/yohimik/dispat/blob/refs/tags/services/dispat/v1.1.0/services/dispat/CHANGELOG.md

install it with: dispat self-update
```

Use `--check` as a script gate, because it changes nothing on disk and exits `1` when an update is available. Pass
`--prerelease` to include release candidates, `--release <version>` to install a specific version, or `--force` to
install your selection even if it is older. dispat never downgrades on its own, so it reports an older release as
"already the latest" unless you provide one of those two flags.

Run with `--rollback` to restore the backup binary without downloading anything. This command rotates the files instead
of overwriting them. The binary you replace becomes the new backup, so a second `--rollback` returns you to where you
started.

dispat refuses to replace a binary installed with `go install`, because your next `go install` would undo the update.
Instead, `--check` prints the exact `go install` command you need. dispat also refuses to update a local `dev` build
for the same reason.

Most other dispat commands check for a newer stable release and report it before exiting, without ever waiting for
the network response. The shell helpers `if` and `exec` never start the check, because they are glue that may run
dozens of times in one script, and neither does a command printing JSON. To disable the check elsewhere, set
[`updateCheck`](../configuration/README.md) to `false` in your config file, or set `DISPAT_UPDATE_CHECK=0` in your
environment. Read the full guide at [Updating dispat](../reference/self-update.md).

## Flags

These flags apply alongside the [global flags](./README.md#global-flags):

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--check`             |             | `compute` and `self-update`: report only, change nothing, and exit `1` when there is something to do. For `self-update`, this means a release is available to install.  |
| `--force`             |             | `self-update` only: install the selected release even if it is not newer. This repairs a damaged binary and leaves a prerelease line.                                    |
| `--prerelease`        |             | `self-update` only: consider prereleases too. Standard ordering still decides, so a released `1.2.0` wins over `1.2.0-rc.1`.                                                   |
| `--release`           |             | `self-update` only: install exactly this version, including downgrades. A leading `v` is fine.                                                                              |
| `--rollback`          |             | `self-update` only: restore the binary the last update replaced, without downloading anything. This refuses to run alongside release selection flags, but it combines with `--check`.              |
| `--owner`, `--repo`, `--api-url`, `--token-env` | from config | `self-update`: point dispat at another repository or a GitHub Enterprise endpoint instead of its own releases.  |
