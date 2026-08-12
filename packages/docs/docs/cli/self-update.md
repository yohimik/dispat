# The self-update command

`dispat self-update` replaces the running binary with the latest stable release of dispat itself. It needs no config
file and no git repository: it is about the binary, not about the repository it is standing in.

The binary for the running platform is downloaded from the GitHub release, checked against the size and checksum the
release published, run once to prove it works, and only then moved into place. The binary it replaces is kept beside it
as `<name>.backup` and removed by a later run a week on. Nothing moves until every check has passed, so a failed update
leaves the working binary exactly where it was.

```console
$ dispat self-update --check
current   dispat 1.0.0 (darwin_arm64)
available dispat 1.1.0 (services/dispat/v1.1.0)

install it with: dispat self-update
```

`--check` changes nothing and exits `1` when there is something to install, which makes it a gate. `--prerelease`
considers the release candidates too, `--release <version>` installs one named version including a downgrade, and
`--force` installs the selection even when it is not newer. Nothing downgrades on its own: a release older than the
running binary is reported as "already the latest" unless one of those two flags says otherwise.

`--rollback` restores the kept binary and downloads nothing. It rotates rather than moves, so the binary it replaces
becomes the new backup and a second `--rollback` returns.

A binary installed with `go install` is not replaced, since the next `go install` would undo it, and `--check` prints the
`go install` command that does update it. A local build (`dev`) is refused for the same reason.

Every other command reports a newer stable release on its way out, without ever waiting for the answer. Set
[`updateCheck`](../configuration/README.md) to `false`, or `DISPAT_UPDATE_CHECK=0` in the environment, to switch that
off. The full guide is [Updating dispat](../reference/self-update.md).

## Flags

Beside the [global flags](./README.md#global-flags):

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--check`             |             | `compute` and `self-update`: report only, change nothing, and exit `1` when there is something to do. For `self-update`, a release it would install.  |
| `--force`             |             | `self-update` only: install the selected release even when it is not newer, which repairs a damaged binary and leaves a prerelease line.                                    |
| `--prerelease`        |             | `self-update` only: consider prereleases too. Ordering still decides, so a released `1.2.0` still wins over `1.2.0-rc.1`.                                                   |
| `--release`           |             | `self-update` only: install exactly this version, downgrades included. A leading `v` is fine.                                                                              |
| `--rollback`          |             | `self-update` only: restore the binary the last update replaced, downloading nothing. Refuses beside the flags that select a release; combines with `--check`.              |
| `--owner`, `--repo`, `--api-url`, `--token-env` | from config | `self-update`: point it at another repository or a GitHub Enterprise endpoint instead of dispat's own releases.  |
