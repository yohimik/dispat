# The replacer

Use the replacer for version text that the manifest writer cannot update: a coordinate assembled in a build script,
an installation example in a README, or an image reference in a workflow.

It replaces literal text without parsing the file. Include enough context to identify the intended value, such as
`com.acme:core:1.2.0`. A bare `1.2.0` can match unrelated versions too.

Use the `dispat replacer` command for one-off edits and scripts. Use the `replace` block of
[`autoVersion`](../configuration/autoversion.md) during a release.

## The command

```console
$ dispat replacer --replace 'com.acme:core:1.2.0=>com.acme:core:1.3.0' build.gradle README.md
build.gradle
  applied  com.acme:core:1.2.0 -> com.acme:core:1.3.0
  2 occurrence(s) replaced
README.md
  applied  com.acme:core:1.2.0 -> com.acme:core:1.3.0
  1 occurrence(s) replaced
2 file(s), 3 occurrence(s): 2 applied, 0 skipped, 0 missing
```

Pass one `--replace` flag per replacement, written as `find=>write`. Use `=>` as the separator rather than `=`. Version
strings often contain `=` characters like `>=1.0` or `VERSION=1.2.3`. Splitting on a single equals sign would refuse
perfectly sensible edits.

dispat splits the string at the first `=>`. A `=>` inside the replacement text survives.

Pass file paths as positional arguments, relative to `--root`. They do not need to be manifests.

Three rules determine the result:

**Every occurrence is replaced, not just the first.** A version usually appears in a file more than once. Replacing
only one occurrence leaves the file disagreeing with itself.

**Replacements apply in the order you wrote them, each over what the one before it left.** Write your flags carefully.
This command really does end at `2.0.0`:

```console
$ dispat replacer --replace '1.0.0=>1.1.0' --replace '1.1.0=>2.0.0' notes.txt
```

**A write only happens when something changed.** Run the same command twice and dispat leaves the file untouched the
second time. The permissions and modification time stay intact.

### Outcomes

Every replacement ends up in one of three buckets. These are the same three the writer command uses:

| Outcome     | Meaning                                                                                   |
|-------------|-------------------------------------------------------------------------------------------|
| **applied** | The text was found and the file changed.                                                  |
| **missing** | The text does not occur in the file.                                                      |
| **skipped** | The text occurs, but `find` and `write` are the same, so there was nothing to do.          |

A missing replacement is not an error by itself. You will usually run one command over twenty files where the pattern
belongs in only one of them.

Watch for a pattern that matched **nowhere at all**. This usually means the pattern has gone stale. Pass `--strict` to
turn an unmatched pattern into a failed command:

```console
$ dispat replacer --strict --replace 'com.acme:core:1.2.0=>com.acme:core:1.3.0' build.gradle
$ echo $?
1
```

### Files it refuses

dispat declines two kinds of file rather than rewriting them:

- Anything over 16 MiB. This is the same read cap the manifest tools use.
- Anything that looks binary. dispat checks for a NUL byte in the first 8 KiB, matching the test git and grep use. This
  keeps a replacement out of a PNG that happens to contain the version text.

Name a binary file on the command line and the command fails. A glob reaching a binary file during a release skips it
quietly. Reaching an image with a glob is ordinary, so failing the release over it would be a mistake.

### Machine-readable output

Pass `--log-format json` to get one event per file plus a summary. This prints to the same stream the rest of dispat
writes to:

```console
$ dispat replacer --log-format json --replace '1.2.0=>1.3.0' build.gradle
{"level":"info","path":"build.gradle","occurrences":2,"replacements":{"applied":[{"find":"1.2.0","write":"1.3.0"}]},"message":"file updated"}
{"level":"info","files":1,"occurrences":2,"applied":1,"skipped":0,"missing":0,"message":"replace complete"}
```

### Exit codes

Expect `0` when dispat does everything you asked. Expect `1` for a file that cannot be read or written, or a `--strict`
run with a pattern that matched nothing. Expect `2` for a bad command line. This includes naming no file, providing no
`--replace` flag, or passing a `--replace` with no separator or nothing to find.

## Replacing during a release

Use the command on its own for scripts. The replacer exists primarily for the version stage. Add a `replace` list to a
space's `autoVersion` block and every release reconciles those files for you.

```yaml
spaces:
  android:
    path: modules
    autoVersion:
      manifests: none            # nothing here parses as a manifest
      replace:
        - files: [ "*.gradle", "*.gradle.kts" ]
          find:  "com.acme:{provider}:{providerPrevious}"
          write: "com.acme:{provider}:{providerVersion}"
        - files: [ "README.md" ]
          find:  "com.acme:{name}:{previous}"
          write: "com.acme:{name}:{version}"
```

The first rule updates each module's dependency coordinates. The second updates the module's own version in its README.

### Placeholders

Write both `find` and `write` as templates. dispat fills in six placeholders before using the text:

| Placeholder          | Stands for                                                              |
|----------------------|--------------------------------------------------------------------------|
| `{name}`             | The package being released.                                             |
| `{version}`          | The version it is moving to.                                            |
| `{previous}`         | The version it is moving from.                                          |
| `{provider}`         | One package it depends on.                                              |
| `{providerVersion}`  | That provider's version at the end of the run.                          |
| `{providerPrevious}` | The version that provider is moving from.                               |

dispat leaves any `{token}` it does not recognise exactly as written. This text is far more likely to be something your
file really contains than a placeholder you meant to exist.

### One rule, many providers

Mention any of the three provider placeholders and the rule applies **once per provider**. A module depending on four
other modules needs one rule, not four.

The replacer uses the providers the [`dependencies`](../configuration/spaces.md) list declares. You can narrow this
list by setting `autoVersion.only`. With `manifests: none` there is no manifest to learn dependencies from.

The declared edge makes the release run this package after its providers. This prevents a rule from writing a version
whose publish nothing waited for.

Omit provider placeholders and the rule applies once for the package's own version.

Expand one rule across several providers and it replaces in the order the providers are declared. Each replacement
writes over what the last left. This matters when two providers' versions overlap.

A rule finding a bare `{providerPrevious}` for a provider moving `1.0.0` to `1.1.0` and another moving `1.1.0` to
`1.2.0` runs the first result through the second rule. Name the provider in the pattern to ensure the two cannot
collide.

### Which files a rule reaches

Provide `files` as a list of globs relative to the package folder. The `*` character matches any run of characters,
**separators included**. This matches the rule [`autoVersion.match`](../configuration/autoversion.md) and space scopes
already follow. A `*.gradle` glob reaches a build script three folders down without needing a `**` spelling.

Folders a workspace walk never enters stay out of reach whatever the glob says. These include `node_modules`, `vendor`,
`target`, `dist`, `build`, `out`, virtual environments, and every dot-folder. A rule must not rewrite somebody else's
code.

dispat walks each package folder once per release, regardless of how many rules you wrote. A file selected by several
rules is read and written exactly once.

### The versions it writes

A provider's version is the one the release actually ships. The new version is used if the provider is releasing and
has not failed. Otherwise, dispat uses the version already published.

This matches the rule the manifest side follows. A provider whose build fails leaves your files naming the version that
really exists.

`W197` reports that a rule caught up to a provider released in an earlier run. `W203` reports that a stable release now
names a prerelease provider; this is allowed, but check that the dependency is intentional.

### When a rule matches nothing

`W222` says a rule reached files and found its text in none of them. This almost always means a mistyped template or a
stale pattern. Without the warning, the rule would fail silently for as many releases as it took someone to notice.

A rule whose globs reach no file at all says nothing. Write one space-wide rule over `README.md` to keep every README
that exists in step. A package without a README has nothing for the rule to report.

Re-running a release does not trigger the warning. The text the rule looked for is gone after the first pass. dispat
checks whether the file already reads the way the rule wants before deciding the rule is stale.

## Choosing between the strategies

The `autoVersion` block has two independent ways of reconciling a package. You can use either, both, or neither.

| You have                                                                    | Use                                                     |
|-----------------------------------------------------------------------------|---------------------------------------------------------|
| A `package.json`, `go.mod`, `Cargo.toml` or another supported manifest       | The parsing strategy: leave `manifests` at its default. |
| A Gradle script, a README, a CI workflow, a Helm chart                       | `replace` rules.                                        |
| Both, in one package                                                         | Both. Manifests are reconciled first.                   |
| Neither, but a lock file to regenerate                                       | `manifests: none`, no `replace`, and a `syncLock` list.  |

For the last case, set `manifests: none`, omit `replace`, and configure `syncLock`. This can run a command such as
`go mod tidy` between versioning and building, one package at a time. With neither rewriting strategy active, dispat
runs the lock scripts on every release.

Read [`autoVersion`](../configuration/autoversion.md) for everything the parsing strategy does. Read
[Manifest tools](./manifests.md) for the two commands that read and write manifests directly.
