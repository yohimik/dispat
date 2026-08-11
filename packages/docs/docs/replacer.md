# The replacer

Some versions do not live in a manifest.

A Gradle build script assembles a coordinate out of strings. A README shows the install line people copy. A CI
workflow names a container. A Helm chart pins an image. None of these is a file any parser can read as a package
manifest, and yet every one of them has to move when you release.

The replacer is dispat's answer to that. It finds literal text and writes different literal text in its place. It
parses nothing, understands nothing, and therefore reaches everywhere.

That is the whole trade. Because it does not understand the file, it also cannot protect you from a pattern that
matches somewhere you did not mean. The fix is always the same: give it enough context to be unambiguous. Look for
`com.acme:core:1.2.0`, not for `1.2.0`.

You can reach the replacer two ways: as the `dispat replacer` command, for one-off edits and scripts, and as the
`replace` block of [`autoVersion`](./configuration/spaces.md#autoversion), which is where a release uses it.

## The command

```console
$ dispat replacer --sub 'com.acme:core:1.2.0=>com.acme:core:1.3.0' build.gradle README.md
build.gradle
  applied  com.acme:core:1.2.0 -> com.acme:core:1.3.0
  2 occurrence(s) replaced
README.md
  applied  com.acme:core:1.2.0 -> com.acme:core:1.3.0
  1 occurrence(s) replaced
2 file(s), 3 occurrence(s): 2 applied, 0 skipped, 0 missing
```

Each `--sub` is one substitution, written `find=>write`. The separator is `=>` rather than `=` because both halves are
ordinary text and a version string carries `=` often enough (`>=1.0`, `VERSION=1.2.3`) that splitting on it would
refuse perfectly sensible edits. The split happens at the first `=>`, so a `=>` inside the replacement survives.

Files are named as positional arguments, relative to `--root`. Any file at all: this command has no idea what a
manifest is.

Three things are worth knowing before you use it in anger.

**Every occurrence is replaced, not just the first.** A version usually appears in a file more than once, and
replacing one of them would leave the file disagreeing with itself.

**Substitutions apply in the order you wrote them, each over what the one before it left.** So this really does end at
`2.0.0`:

```console
$ dispat replacer --sub '1.0.0=>1.1.0' --sub '1.1.0=>2.0.0' notes.txt
```

That is occasionally what you want and always worth knowing. Order is yours to choose, so choose it deliberately.

**A write only happens when something changed.** Running the same command twice leaves the file untouched the second
time, with its permissions and its modification time intact.

### Outcomes

Every substitution ends up in one of three buckets, the same three the writer command uses:

| Outcome     | Meaning                                                                                   |
|-------------|-------------------------------------------------------------------------------------------|
| **applied** | The text was found and the file changed.                                                  |
| **missing** | The text does not occur in the file.                                                      |
| **skipped** | The text occurs, but `find` and `write` are the same, so there was nothing to do.          |

A missing substitution is not an error by itself. Running one command over twenty files where the pattern belongs in
one of them is the ordinary case. What is worth catching is a pattern that matched **nowhere at all**, because that
usually means it has gone stale. `--strict` turns exactly that into a failed command:

```console
$ dispat replacer --strict --sub 'com.acme:core:1.2.0=>com.acme:core:1.3.0' build.gradle
$ echo $?
1
```

### Files it refuses

Two kinds of file are declined rather than rewritten:

- Anything over 16 MiB, which is the same read cap the manifest tools use.
- Anything that looks binary, decided by a NUL byte in the first 8 KiB. This is the same test git and grep use, and it
  is what keeps a replacement out of a PNG that happens to contain the version text.

Naming such a file on the command line fails the command, so you find out. A glob reaching one during a release skips
it quietly, because a glob reaching an image is ordinary and failing the release over it would not be.

### Machine-readable output

`--log-format json` gives one event per file plus a summary, on the same stream the rest of dispat writes to:

```console
$ dispat replacer --log-format json --sub '1.2.0=>1.3.0' build.gradle
{"level":"info","path":"build.gradle","occurrences":2,"substitutions":{"applied":[{"find":"1.2.0","write":"1.3.0"}]},"message":"file updated"}
{"level":"info","files":1,"occurrences":2,"applied":1,"skipped":0,"missing":0,"message":"substitution complete"}
```

### Exit codes

`0` when everything asked for was done, `1` for a file that cannot be read or written or a `--strict` run with a
pattern that matched nothing, and `2` for a command line that does not make sense (no file named, no `--sub` given, a
`--sub` with no separator or with nothing to find).

## Replacing during a release

The command is useful on its own, but the reason the replacer exists is the version stage. Add a `replace` list to a
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

The first rule keeps every module's dependency coordinates in step with the versions its providers just released. The
second keeps the module's own README honest about its own version.

### Placeholders

Both `find` and `write` are templates. Six placeholders are filled in before the text is used:

| Placeholder          | Stands for                                                              |
|----------------------|--------------------------------------------------------------------------|
| `{name}`             | The package being released.                                             |
| `{version}`          | The version it is moving to.                                            |
| `{previous}`         | The version it is moving from.                                          |
| `{provider}`         | One package it depends on.                                              |
| `{providerVersion}`  | That provider's version at the end of the run.                          |
| `{providerPrevious}` | The version that provider is moving from.                               |

A `{token}` dispat does not recognise is left exactly as written, on the theory that it is far more likely to be text
your file really contains than a placeholder you meant to exist.

### One rule, many providers

A rule that mentions any of the three provider placeholders is applied **once per provider**. A module depending on
four other modules needs one rule, not four.

Which providers? The ones the [`dependencies`](./configuration/spaces.md) list declares, narrowed by `autoVersion.only`
if you set it. That is deliberate. With `manifests: none` there is no manifest to learn them from, and the declared
edge is in any case what makes the release run this package after its providers, so a rule can never write a version
whose publish nothing waited for.

If a rule mentions no provider placeholder, it is applied once, for the package's own version.

One rule expanded across several providers still substitutes in the order the providers are declared, each over what
the last left. That matters when two providers' versions overlap: a rule finding a bare `{providerPrevious}` for a
provider moving `1.0.0` to `1.1.0` and another moving `1.1.0` to `1.2.0` will run the first result through the second
rule. Name the provider in the pattern, as every example here does, and the two cannot collide.

### Which files a rule reaches

`files` is a list of globs relative to the package folder. `*` matches any run of characters, **separators included**,
which is the same rule [`autoVersion.match`](./configuration/spaces.md#autoversion) and space scopes already follow. So
`*.gradle` reaches a build script three folders down, and no `**` spelling is needed.

The folders a workspace walk never enters stay out of reach whatever the glob says: `node_modules`, `vendor`, `target`,
`dist`, `build`, `out`, virtual environments and every dot-folder. A rule must not rewrite somebody else's code.

Each package folder is walked once per release, however many rules you wrote, and a file several rules select is read
and written once.

### The versions it writes

A provider's version is the one the release actually ships. If the provider is releasing and has not failed, that is
its new version; otherwise it is the version already published. This is the same rule the manifest side follows, so a
provider whose build fails leaves your files naming the version that really exists.

Two warnings narrate what a release did that the commit log cannot explain. `W197` says a rule caught a file up to a
provider that was released in an earlier run. `W203` says a stable release now names a prerelease provider, which is
legal and worth a glance.

### When a rule matches nothing

`W222` says a rule reached files and found its text in none of them. That almost always means a mistyped template or a
pattern that has gone stale, and without the warning it would fail silently for as many releases as it took someone to
notice.

A rule whose globs reach no file at all says nothing. One space-wide rule over `README.md` is the ordinary way to keep
every README that exists in step, and a package without one has nothing for the rule to report.

Re-running a release does not trigger it. After the first pass the text the rule looked for is gone, and dispat checks
whether the file already reads the way the rule wants before deciding the rule is stale.

## Choosing between the strategies

`autoVersion` has two ways of reconciling a package, and they are independent. You can use either, both, or neither.

| You have                                                                    | Use                                                     |
|-----------------------------------------------------------------------------|---------------------------------------------------------|
| A `package.json`, `go.mod`, `Cargo.toml` or another supported manifest       | The parsing strategy: leave `manifests` at its default. |
| A Gradle script, a README, a CI workflow, a Helm chart                       | `replace` rules.                                        |
| Both, in one package                                                         | Both. Manifests are reconciled first.                   |
| Neither, but a lock file to regenerate                                       | `manifests: none`, no `replace`, and a `syncLock` list.  |

The last row is worth spelling out. An `autoVersion` block carrying nothing but `syncLock` is how a space says "run
`go mod tidy` between version and build, one package at a time". Nothing is reconciled, so there is no change to key
the scripts off, and dispat runs them every release rather than never.

For everything the parsing strategy does, see [`autoVersion`](./configuration/spaces.md#autoversion). For the two
commands that read and write manifests directly, see [Manifest tools](./manifests.md).
