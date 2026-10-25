# The self-update command

Run `dispat self-update` to install the latest stable dispat release. Use `dispat self-update --check` to see the
available version and changes first. Neither command needs a configuration file or Git repository.

The updater downloads the binary for your platform, checks its published size and checksum, and runs it once before
replacing the installed binary. The previous binary is kept as `<name>.backup` for rollback.

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

## Update notices from other commands

Most other commands check for a newer stable release in the background and report it before exiting if the result
is ready. They do not wait for the network. The `if` and `exec` helpers, and commands printing JSON, skip this check. To disable the check elsewhere, set
[`updateCheck`](../configuration/README.md) to `false` in your config file, or set `DISPAT_UPDATE_CHECK=0` in your
environment. Read the full guide at [Updating dispat](../reference/self-update.md).

## Backups and interrupted updates

A later run removes a backup after one week. Until the new binary is installed, dispat keeps both the current binary
and any existing rollback copy. If the final swap fails, it restores both; if restoration fails too, the error reports
their remaining paths.

If `<name>.backup` is a folder or another non-file entry, the updater stops before downloading and asks you to move
or remove it. `--check` reports the same obstruction.

A cleanup warning after installation means the new binary and its immediate backup are ready, but an older copy needs
manual removal. If a failed download cannot be removed, the warning identifies its staged path. After an interrupted
update, the next update or rollback restores any rollback copy left aside and removes the staged download.

## Flags

These flags apply alongside the [global flags](./README.md#global-flags):

### `--check`

`compute` and `self-update`: report only, change nothing, and exit `1` when there is something to do. For
`self-update`, this means a release is available to install.

### `--force`

`self-update` only: install the selected release even if it is not newer. This repairs a damaged binary and leaves a
prerelease line.

### `--prerelease`

`self-update` only: consider prereleases too. Standard ordering still decides, so a released `1.2.0` wins over
`1.2.0-rc.1`.

### `--release`

`self-update` only: install exactly this version, including downgrades. A leading `v` is fine.

### `--rollback`

`self-update` only: restore the binary the last update replaced, without downloading anything. This refuses to run
alongside release selection flags, but it combines with `--check`.

### `--owner`, `--repo`, `--api-url`, `--token-env`

The default comes from config. `self-update`: point dispat at another repository or a GitHub Enterprise endpoint
instead of its own releases. A private one works too: the token authenticates the releases listing and the download
of the binary alike, so a fork released only inside a company updates itself the same way. `--token-env` names the
variable the token is read from, and `GITHUB_TOKEN` is the default.
