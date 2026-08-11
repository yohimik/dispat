# Updating dispat

dispat ships as a single binary. There is nothing to uninstall, no package
database to keep in step, and no dependency to resolve. The flip side is that
nothing updates it for you, which is what `dispat self-update` is for.

```console
$ dispat self-update
downloading dispat-darwin-arm64 (13.3 MiB)
installed dispat 1.1.0 at /usr/local/bin/dispat
the previous binary is at /usr/local/bin/dispat.backup, removed on its own after a week
put it back with "dispat self-update --rollback"
```

The binary you were running is not thrown away. It is renamed to sit beside the
new one, so if the new version turns out to be wrong for you, one command puts
the old one back.

## What it does, step by step

It is worth knowing exactly what happens to the file you depend on.

1. **It finds the release.** dispat's own GitHub releases are listed, the ones
   tagged for the CLI are picked out, and the highest stable version wins.
2. **It downloads the binary for your platform.** Linux, macOS and Windows, on
   Intel and on ARM: six binaries, one per platform, named after it.
3. **It checks what arrived.** The size has to match what the release
   advertised, and the checksum has to match the one GitHub published for that
   file. A download that was cut short or altered on the way is refused here.
4. **It runs the new binary once.** A file can arrive perfectly intact and
   still be the wrong thing. Asking it for its version before trusting it is
   cheap, and finding out afterwards would mean finding out with no working
   dispat.
5. **Only then does it swap.** Your current binary is renamed to
   `dispat.backup`, and the new one takes its place.

Nothing moves until every check has passed. If anything at all goes wrong, the
binary you were running is still exactly where it was, still working, and there
is no half-installed anything to clean up.

The path never changes, so whatever put `dispat` on your `PATH` still points at
it. Nothing needs re-linking and no shell needs restarting.

## Checking without installing

`--check` answers one question, changes nothing, and exits `1` when there is
something to install. That makes it usable as a gate in a script.

```console
$ dispat self-update --check
current   dispat 1.0.0 (darwin_arm64)
available dispat 1.1.0 (services/dispat/v1.1.0)

install it with: dispat self-update

$ echo $?
1
```

When you are already current it prints the same two lines, says `nothing to
install`, and exits `0`.

## Going back

The copy left behind is a real, working binary, and `--rollback` puts it back
without touching the network at all.

```console
$ dispat self-update --rollback
rolled back to dispat 1.0.0 at /usr/local/bin/dispat
dispat 1.1.0 is now the backup, so another --rollback returns to it
```

Notice the second line. A rollback does not throw away the version it replaced,
it swaps the two, so rolling back twice puts you back where you started. You do
not have to be certain before running it.

Before restoring anything it runs the kept binary, exactly as it does with a
download. A backup that will not start is refused rather than installed.

`dispat self-update --check --rollback` says which version the backup holds
without moving anything.

### The backup does not stay forever

A week after it was created, the next dispat command you run deletes it. That
is long enough to notice a bad update and go back, and short enough that a
15 MiB copy of an old binary is not still sitting in `/usr/local/bin` a year
later.

Nothing else is ever touched, and the check costs a single look at the
filesystem. Once the backup is gone, `--release` is how you get an older
version:

```sh
dispat self-update --release 1.0.0
```

## Choosing a version

By default you get the highest stable release. Three flags change that.

| Flag                  | Effect                                                                                       |
|-----------------------|----------------------------------------------------------------------------------------------|
| `--release <version>` | Install exactly that version, whether it is newer or older than the one you are running.       |
| `--prerelease`        | Consider release candidates too.                                                              |
| `--force`             | Install the selected release even when it is not newer.                                       |

`--prerelease` means "consider them too", not "prefer them". Ordering still
decides, and `1.2.0` is above `1.2.0-rc.1`, so a released version wins over the
candidates that led to it. Once a stable release exists, `--prerelease` stops
changing the answer on its own.

Nothing ever downgrades by itself. If the release dispat found is older than
what you are running, it says you are already current and stops. Naming a
version with `--release`, or asking with `--force`, is how you say you meant
it. That is also the way off a prerelease line:

```console
$ dispat self-update --force
installed dispat 1.1.0 at /usr/local/bin/dispat
```

## Being told there is an update

You do not have to remember to check. Any command will mention a newer stable
release on its way out:

```console
$ dispat status
...

a newer stable release is available: 1.1.0 (you have 1.0.0)
run "dispat self-update" to install it
```

The check runs alongside your command rather than in front of it, and **no
command ever waits for it**. If the answer has not come back by the time the
work is done, it is dropped and the command exits. Offline, behind a proxy, out
of API quota, or simply finished first: all of them look the same, and all of
them cost nothing.

That does mean a fast command often finishes first and says nothing. Nothing
is wrong when that happens. `dispat self-update --check` is the way to ask and
be sure of an answer.

Two more things keep it quiet:

- With `logFormat: json` it never runs at all. JSON output is being read by
  something that cannot act on a suggestion, and CI should not be calling
  GitHub on every run.
- The notice is only ever about stable releases. Running a release candidate
  tells you when the stable it leads to lands, and never nags you towards the
  next candidate.

### Turning it off

Set `updateCheck` to `false` in your config file:

```json title="dispat.json"
{
  "updateCheck": false
}
```

That is a refusal to ask, not a refusal to print: with it set, no request is
made at all. For the commands that read no config file, and for turning it off
across a machine, set `DISPAT_UPDATE_CHECK=0` in the environment.

## How you installed it matters

`dispat --version` says which build you are running, because that decides how
it gets updated.

```console
$ dispat --version
dispat 1.1.0 (darwin_arm64)
```

A binary installed with `go install` says so, and `self-update` will not
replace it. Rewriting a file the Go toolchain owns works right up until the
next `go install`, so dispat tells you the command that actually updates it
instead:

```console
$ dispat --version
dispat 1.1.0 (darwin_arm64, go install)

$ dispat self-update --check
current   dispat 1.1.0 (darwin_arm64, go install)
available dispat 1.2.0 (services/dispat/v1.2.0)

update it with: go install github.com/yohimik/dispat/services/dispat@latest
```

A build you compiled yourself reports `dev` and refuses too. It has no released
version to compare against, and overwriting somebody's own build is not
something a tool should do quietly.

The same applies to a package manager. If Homebrew, Scoop or your distribution
installed dispat, update it the way you installed it: replacing the file in
place works, and then the package manager overwrites it again on its next
upgrade.

## On macOS

dispat's macOS binaries are not notarised by Apple. Most of the time this is
invisible, because a file dispat downloaded itself is not quarantined the way a
browser download is. If macOS does refuse to open it, allow it once under
**System Settings → Privacy & Security**, or clear the flag by hand:

```sh
xattr -d com.apple.quarantine /usr/local/bin/dispat
```

dispat prints this reminder itself, both when it suggests an update and after
it installs one, so it never comes as a surprise.

## When it will not work

A few refusals are worth recognising.

**"is not writable"**, when dispat lives somewhere you do not own, such as
`/usr/local/bin` on a machine where that belongs to root. Re-run with the
rights to replace it. The refusal comes before the download, so nothing has
been transferred and nothing has been changed.

**"no matching release"**, when nothing published fits. Usually that means
there is no stable release yet, and `--prerelease` will find the candidates. It
also covers `--release` naming a version that does not exist, which is worth
telling apart from being up to date.

**"carries no dispat-linux-arm64"**, when the release predates your platform
being built for. The message lists the binaries that release does carry.

**"hashes to ..."**, when the download does not match the checksum the release
published. The working binary is untouched. Try again, and if it keeps
happening, something between you and GitHub is changing the file.

## Exit codes

`0` when the command did what was asked, including when there was nothing to
do. `1` when it could not: no matching release, no binary for the platform, a
download that failed a check, a directory it cannot write to, a build it will
not replace, or a `--check` that found something to install. `2` for a command
line that does not make sense, such as `--rollback` next to `--release`, since
a rollback downloads nothing.

## Every flag

| Flag                  | Effect                                                                                                        |
|-----------------------|-----------------------------------------------------------------------------------------------------------------|
| `--check`             | Report what the same invocation would do, change nothing, and exit `1` when there is something to do.            |
| `--force`             | Install the selected release even when it is not newer.                                                         |
| `--prerelease`        | Consider prereleases as well as stable releases.                                                                |
| `--release <version>` | Install exactly this version. A leading `v` is fine.                                                            |
| `--rollback`          | Restore the kept binary and download nothing. Combines with `--check`; refuses the flags that select a release.  |
| `--owner`, `--repo`, `--api-url`, `--token-env` | Point the command at a different repository or a GitHub Enterprise endpoint. A token only raises the API rate limit. |

Needs no config file and no git repository: it is about the binary, not about
whatever project you happen to be standing in.
