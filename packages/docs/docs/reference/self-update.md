# Updating dispat

Run `dispat self-update` to upgrade your binary in place. Because dispat ships
as a single binary with no external dependencies or package databases, nothing
updates it automatically.

```console
$ dispat self-update
downloading dispat-darwin-arm64 (13.3 MiB)
installed dispat 1.1.0 at /usr/local/bin/dispat
the previous binary is at /usr/local/bin/dispat.backup, removed on its own after a week
put it back with "dispat self-update --rollback"

what changed in 1.1.0

  Features
    - self-update reads out what it installed

  Fixes
    - a truncated release listing says so instead of failing as bad JSON

full changelog: https://github.com/yohimik/dispat/blob/refs/tags/services/dispat/v1.1.0/services/dispat/CHANGELOG.md
```

dispat renames your existing binary instead of deleting it. If the new version
causes problems, you can restore the old binary with a single command.

Use `dispat self-update` to update an existing binary on your machine. To
install dispat for the first time or pin a version in a container image, use
[the install script's](./ci.md#the-install-script) method instead.

## What it does, step by step

Here is what happens to your binary during an update:

1. **It finds the release.** dispat lists its GitHub releases, filters for CLI
   tags, and picks the highest stable version. It reads the release notes
   directly from this response before fetching any files.
2. **It downloads the binary for your platform.** It pulls the matching binary
   from six platform builds across Linux, macOS, and Windows on Intel and ARM.
3. **It checks what arrived.** It verifies that the file size matches the
   release metadata and validates the checksum against the published GitHub
   hash. dispat rejects any download that was altered or cut short.
4. **It runs the new binary once.** It invokes the new binary to check its
   version before committing the change. This prevents broken or mismatched
   binaries from replacing your working tool.
5. **Only then does it swap.** dispat renames your existing binary to
   `dispat.backup` and puts the new one in its place.

No files move until every check passes. If an error occurs, your existing
binary stays intact and ready to run.

Your binary path stays the same, so your `PATH` configuration remains valid.
You do not need to re-link binaries or restart your shell.

## What you just got

After installing the new binary, dispat prints a summary of changes and
provides a link to the complete changelog.

```console
what changed in 1.1.0

  Features
    - self-update reads out what it installed

  Fixes
    - a truncated release listing says so instead of failing as bad JSON

full changelog: https://github.com/yohimik/dispat/blob/refs/tags/services/dispat/v1.1.0/services/dispat/CHANGELOG.md
```

Fetching these notes requires no extra network requests. dispat receives the
release notes in the initial release check before downloading the binary,
ensuring the printed summary always matches the installed version.

Keep these details in mind:

- **It is the release you installed, not everything you skipped.** If you
  upgrade from 1.0.0 directly to 1.3.0, dispat prints notes for 1.3.0. Use the
  changelog link to see notes for all skipped versions.
- **It is a summary, not the page.** dispat strips browser-focused content like
  installation commands and code blocks, printing only release headings and
  bullets. Very long release notes are truncated with a notice.
- **The link points at the tag.** The link targets the changelog at that
  specific Git tag rather than the current repository state, preserving
  historical notes.
- **It never gets in the way.** If release notes are missing or unparseable,
  dispat prints only the link. Update execution never depends on release note
  formatting.

When you configure `logFormat: json`, dispat omits human-readable text. It
includes the summary and link in the `notes` and `changelog` fields of the
`update installed` event instead, allowing CI pipelines to parse updates
cleanly.

## Checking without installing

Run `dispat self-update --check` to inspect available updates without modifying
files. The command exits with code `1` when an update is available, making it
suitable for script gates.

```console
$ dispat self-update --check
current   dispat 1.0.0 (darwin_arm64)
available dispat 1.1.0 (services/dispat/v1.1.0)

what changed in 1.1.0

  Features
    - self-update reads out what it installed

full changelog: https://github.com/yohimik/dispat/blob/refs/tags/services/dispat/v1.1.0/services/dispat/CHANGELOG.md

install it with: dispat self-update

$ echo $?
1
```

`--check` displays the release summary and changelog link without downloading
the binary payload.

If you are already running the latest version, dispat prints the current and
available versions, outputs `nothing to install`, and exits with code `0`. It
skips release notes when no update is needed.

## Going back

Run `dispat self-update --rollback` to restore your previous binary without
using the network.

```console
$ dispat self-update --rollback
rolled back to dispat 1.0.0 at /usr/local/bin/dispat
dispat 1.1.0 is now the backup, so another --rollback returns to it
```

Rolling back swaps the active binary and the backup file. Running `--rollback`
a second time restores the newer version again.

dispat tests the backup binary before restoring it to ensure it executes
properly. If the backup fails to start, dispat aborts the rollback.

Run `dispat self-update --check --rollback` to inspect the backup version
without changing files.

### The backup does not stay forever

dispat deletes the backup file automatically during the first command you run
after seven days. This gives you time to detect issues while preventing old
binaries from accumulating on disk.

The cleanup check inspects only the backup timestamp. If the backup has been
purged, download an older release explicitly using `--release`:

```sh
dispat self-update --release 1.0.0
```

## Choosing a version

dispat installs the latest stable release by default. You can change this
behavior with three flags:

| Flag                  | Effect                                                                                       |
|-----------------------|----------------------------------------------------------------------------------------------|
| `--release <version>` | Install exactly that version, whether it is newer or older than the one you are running.       |
| `--prerelease`        | Consider release candidates too.                                                              |
| `--force`             | Install the selected release even when it is not newer.                                       |

The `--prerelease` flag evaluates prereleases alongside stable releases.
Standard version ordering still applies: because `1.2.0` ranks higher than
`1.2.0-rc.1`, stable versions take precedence when available.

dispat never downgrades your binary automatically. If the latest available
release is older than your current version, dispat reports that you are up to
date. Pass `--release` or `--force` to perform a downgrade or move from a
prerelease back to stable:

```console
$ dispat self-update --force
installed dispat 1.1.0 at /usr/local/bin/dispat
```

## Being told there is an update

Any standard dispat command notifies you if a newer stable release exists:

```console
$ dispat status
...

a newer stable release is available: 1.1.0 (you have 1.0.0)
run "dispat self-update" to install it
```

The update check runs in the background while your command executes, and **no
command waits for it** unless you set `DISPAT_UPDATE_CHECK=1`. If your command
finishes before GitHub responds, dispat discards the check and exits
immediately. Network timeouts, offline environments, or rate limits will not
slow down your workflow.

Fast commands frequently finish before the background check completes. Run
`dispat self-update --check` when you want an immediate, synchronous check.

Notifications are suppressed under two conditions:

- When `logFormat: json` is active, dispat skips update checks entirely to
  avoid corrupting structured logs or making redundant network calls in CI.
- Notifications only report stable releases. If you run a release candidate,
  dispat alerts you when the final stable release arrives, not when subsequent
  release candidates appear.

### Turning it off

Disable update checks across your project by setting `updateCheck` to `false`
in your configuration file:

```json title="dispat.json"
{
  "updateCheck": false
}
```

When disabled, dispat makes no network requests. To disable checks globally or
for commands that do not read config files, set `DISPAT_UPDATE_CHECK=0` in your
environment.

Set `DISPAT_UPDATE_CHECK=1` if you want commands to block until the check
finishes, subject to a two-second timeout. When left unset, commands never
wait.

## How you installed it matters

Run `dispat --version` to check your build type before updating:

```console
$ dispat --version
dispat 1.1.0 (darwin_arm64)
```

If you installed dispat using `go install`, `self-update` refuses to overwrite
the binary. Modifying Go-managed binaries causes conflicts during future
`go install` runs, so dispat prints the required update command instead:

```console
$ dispat --version
dispat 1.1.0 (darwin_arm64, go install)

$ dispat self-update --check
current   dispat 1.1.0 (darwin_arm64, go install)
available dispat 1.2.0 (services/dispat/v1.2.0)

what changed in 1.2.0
...

update it with: go install github.com/yohimik/dispat/services/dispat@latest
```

Locally compiled builds report `dev` and will also refuse updates. These
binaries have no official release baseline to compare against.

Use your package manager if you installed dispat via Homebrew, Scoop, or a
system repository. Upgrading directly through your package manager prevents
local binary replacements from being overwritten later.

## On macOS

dispat's macOS binaries are not notarised by Apple. macOS rarely flags binaries
downloaded through dispat, but if the system blocks execution, allow the binary
under **System Settings → Privacy & Security** or remove the quarantine
attribute directly:

```sh
xattr -d com.apple.quarantine /usr/local/bin/dispat
```

dispat prints this reminder when notifying you of updates and immediately after
installing a new version.

## When it will not work

Here is how to resolve common errors:

**"is not writable"** appears when dispat lacks write permissions for its
binary directory, such as a root-owned `/usr/local/bin`. Re-run the command
with elevated permissions. dispat checks write access before downloading, so
your files remain unchanged.

**"no matching release"** indicates that no release matches your criteria. This
occurs if there are no stable releases yet (in which case pass `--prerelease`)
or if `--release` specifies a non-existent version.

**"carries no dispat-linux-arm64"** occurs when an older release lacks binaries
compiled for your target architecture. The error output lists the available
platform binaries for that release.

**"hashes to ..."** means the downloaded payload failed checksum verification
against the published release hash. Your existing binary remains untouched.
Retry the download; repeated failures point to a proxy or network middlebox
altering files.

## Exit codes

`0` indicates success, including when no updates were needed.

`1` indicates a failed operation or an available update during `--check`.
Causes include missing releases, unsupported platforms, failed checksums, write
permission errors, or unsupported build types.

`2` indicates invalid command-line usage, such as combining `--rollback` with
`--release`.

## Every flag

| Flag                  | Effect                                                                                                        |
|-----------------------|-----------------------------------------------------------------------------------------------------------------|
| `--check`             | Report what the same invocation would do, change nothing, and exit `1` when there is something to do.            |
| `--force`             | Install the selected release even when it is not newer.                                                         |
| `--prerelease`        | Consider prereleases as well as stable releases.                                                                |
| `--release <version>` | Install exactly this version. A leading `v` is fine.                                                            |
| `--rollback`          | Restore the kept binary and download nothing. Combines with `--check`; refuses the flags that select a release.  |
| `--owner`, `--repo`, `--api-url`, `--token-env` | Point the command at a different repository or a GitHub Enterprise endpoint. A token only raises the API rate limit. |

`self-update` operates directly on the binary itself. You can run it anywhere
without a configuration file or Git repository.
