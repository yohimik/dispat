# The install command

Run `dispat install https://github.com/owner/repo` to install a tool published as a GitHub release asset. You do not
need a config file or a git repository. This command installs somebody else's binary the way
[`self-update`](./self-update.md) installs dispat's own, so a CI job that already has dispat has a release downloader
as well.

dispat reads the repository's releases, picks the highest stable version, downloads the file you named, verifies the
published size and checksum, and only then moves it into place. Anything already at the destination is kept beside it
as `<name>.backup` and removed during a later run a week afterwards. Because nothing moves until every check passes, a
failed download leaves the folder exactly as it found it.

```console
$ dispat install https://github.com/acme/tool --asset 'tool-{os}-{arch}'
downloading tool-linux-amd64 (8.4 MiB) from acme/tool
installed tool 1.4.0 at /usr/local/bin/tool
```

## Naming the repository

Name the repository however it is already at hand. dispat accepts the page URL, a page inside it, the clone URL, the
SSH remote, and the `owner/repo` shorthand:

```sh
dispat install https://github.com/acme/tool
dispat install https://github.com/acme/tool/releases/tag/v1.4.0
dispat install git@github.com:acme/tool.git
dispat install acme/tool
```

A host that is not `github.com` is treated as a GitHub Enterprise install, and dispat derives its API endpoint as
`https://<host>/api/v3`. Pass `--api-url` when your install serves the API from somewhere else.

dispat sends the conventional `GITHUB_TOKEN` only to `github.com`, because the host here comes from an argument rather
than from a flag you set deliberately. To authenticate against another endpoint, name the variable yourself with
`--token-env`.

## Choosing the file

Most releases attach more than one file, and installing the wrong one globally is worse than typing its name. Use
`--asset` to say which one you want. dispat expands `{os}`, `{arch}`, `{version}`, `{tag}` and `{name}` in the value,
so one invocation keeps working as releases come and go:

```sh
dispat install acme/tool --asset 'tool-{os}-{arch}'
dispat install cli/cli   --asset 'gh_{version}_{os}_{arch}.tar.gz'
```

The value also matches as a glob, which reaches an asset whose exact spelling nobody wants to write out:

```sh
dispat install acme/tool --asset '*linux-amd64'
```

An exact name always wins over a glob. A pattern matching two files is refused with both listed, and so is a release
carrying several files when you named none. A release carrying exactly one file needs no `--asset` at all.

## Choosing the destination

`--bin-dir` says which folder the tool goes into, and `--as` says what it is called there. Without them, dispat
installs into `$DISPAT_BIN_DIR`, then `/usr/local/bin` when that folder accepts a file, then `~/.local/bin`, which is
the same ladder [the install script](../reference/ci.md#the-install-script) climbs. The name defaults to the
repository's own, and on Windows a name with no extension gains `.exe`.

dispat creates the folder if it does not exist, and tells you when it is not on your `PATH`, because a tool your shell
cannot find is a successful install that looks like a failed one.

## Assets that are not binaries

Many projects ship their binary inside an archive, and some ship an install script. Use `--pipe` to hand the verified
file to a command's standard input instead of installing it. The command runs in `--bin-dir`, so whatever it writes
lands where a binary would have:

```sh
dispat install cli/cli --asset 'gh_{version}_{os}_{arch}.tar.gz' \
  --pipe 'tar -xz --strip-components=2 --wildcards "*/bin/gh"'

dispat install acme/tool --asset install.sh --pipe sh
```

The verification is the same either way, so what reaches your command has already been checked against the size and
the checksum the release published. dispat stages the file in a folder of its own and removes it afterwards, so a
command that fails leaves nothing behind on your `PATH`. `$DISPAT_ASSET` names the same file by path, under the name
the release published, for a command that has to seek rather than read a stream; `$DISPAT_ASSET_NAME` is that name on
its own.

## Running it more than once

`dispat install` is idempotent. It hashes the file already at the destination against the checksum the release
published, so running the same line again costs no transfer and says so:

```console
$ dispat install acme/tool --asset 'tool-{os}-{arch}'
tool at /usr/local/bin/tool is already v1.4.0
install it again anyway with --force
```

Use `--check` as a provisioning gate, because it changes nothing on disk and exits `1` when the destination does not
already hold that exact file. Use `--force` to install anyway, which is how a damaged or tampered file is repaired. A
release that publishes no checksum cannot be compared, and dispat says so and installs, rather than guessing that your
machine is up to date.

## Versions and tags

`--release <version>` installs one named version, downgrades included. `--prerelease` considers the prereleases too,
though ordering still decides, so a released `1.2.0` wins over `1.2.0-rc.1`.

`--tag-prefix` says what the repository writes before the version in its tags. It defaults to `v`, which covers nearly
every project. Pass an empty value for a repository tagging `1.2.3`, or the module path for a monorepo publishing one
release per package:

```sh
dispat install acme/tool --tag-prefix ''
dispat install yohimik/dispat --tag-prefix 'services/dispat/v' --asset 'dispat-{os}-{arch}'
```

## Undoing it

Run with `--rollback` to restore the binary the last download replaced, without downloading anything. It rotates the
two files rather than overwriting them, so the binary you replace becomes the new backup and a second `--rollback`
returns you to where you started.

A rollback reads no releases, so it needs only to know which tool: name the repository as usual, or name the file with
`--as`.

```console
$ dispat install acme/tool --rollback
rolled back tool at /usr/local/bin/tool
the binary it replaced is now the backup, so another --rollback returns to it
```

## Flags

These flags apply alongside the [global flags](./README.md#global-flags):

| Flag           | Default              | Effect                                                                                                                                                                                                                 |
|----------------|----------------------|------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--asset`      |                      | Which of the release's files to install, by name or glob. `{os}`, `{arch}`, `{version}`, `{tag}` and `{name}` are expanded. A release carrying exactly one file needs none.                    |
| `--bin-dir`    | see above            | The folder to install into. Without it, `$DISPAT_BIN_DIR`, then `/usr/local/bin` when it is writable, then `~/.local/bin`.                                                                            |
| `--as`         | the repository name  | What to call the installed tool. It takes a file name, not a path.                                                                                                                                        |
| `--pipe`       |                      | Hand the verified file to this command's standard input instead of installing it. The command runs in `--bin-dir`, with `$DISPAT_ASSET` and `$DISPAT_ASSET_NAME` set.                                         |
| `--tag-prefix` | `v`                  | What a release tag carries before its version. An empty value considers every tag whose whole name is a version.                                                                                          |
| `--release`    | the highest stable   | Install exactly this version, including downgrades. A leading `v` is fine.                                                                                                                                    |
| `--prerelease` |                      | Consider prereleases too. Standard ordering still decides, so a released `1.2.0` wins over `1.2.0-rc.1`.                                                                                                     |
| `--check`      |                      | Report only, change nothing, and exit `1` when the destination does not already hold that exact file. With `--pipe`, there is no destination to compare, so this always exits `1`.                        |
| `--force`      |                      | Install even when the destination already carries that file, which repairs a damaged or tampered binary.                                                                                                  |
| `--rollback`   |                      | Restore the binary the last download replaced, without downloading anything. This refuses to run alongside the flags that choose something to download, but it combines with `--check`.                     |
| `--api-url`, `--token-env` | derived  | Point dispat at another API endpoint, and name the variable a token is read from. `GITHUB_TOKEN` is sent to `github.com` alone.                                                                              |
