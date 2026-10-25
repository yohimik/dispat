# The diagnostics command

Use `dispat diagnostics` to check a commit message before committing it:

```sh
dispat diagnostics 'feat(core): add streaming'
```

No Git repository or configuration file is required. Without `--config`, dispat uses the CCME parser defaults and ignores nearby configuration files.

## Use project rules

Pass a dispat configuration file to apply its [parser settings](../configuration/parser.md):

```sh
dispat diagnostics --config dispat.yaml 'feat(core): add streaming'
dispat diagnostics --root ../project --config settings/dispat.yaml 'fix(api): close the stream'
```

Use a complete dispat configuration with package or space declarations. If your parser settings live in a separate fragment, include it with `$ref`; the fragment cannot be passed as the configuration on its own.

A relative configuration path starts at `--root`, which defaults to the current directory. Configuration references resolve as usual. A missing or invalid explicit file is an error; dispat does not fall back to defaults.

This command checks message syntax. It does not check whether a scope names an existing package, calculate versions, scan manifests, run scripts, or change Git history. Use [`status`](./status.md) to inspect the release plan and [`commit`](./commit.md) to create a validated source commit.

## Read diagnostics

Each finding gives you a diagnostic code and a location: line, column, and unit number where applicable. Warnings leave the command successful unless your parser settings make them errors. One error in any unit rejects the whole message.

```sh
dispat diagnostics --log-format json 'unlisted(core): check this type'
```

Parser diagnostics are written to standard output. JSON mode emits structured log events. Configuration and usage errors go to standard error. A valid message with no warnings is silent at the default log level.

Diagnostics remain visible even when `parser.quiet`, `--quiet-parser`, or an error-only log level would normally hide warnings. Trace and debug logging can provide additional detail.

| Exit code | Meaning |
| --- | --- |
| `0` | No parser errors; warnings may be present. |
| `1` | A parser error or a configuration error. |
| `2` | Invalid arguments or flags. |

## Supply literal text

Quote the message as one argument. Embedded newlines are preserved, and Git's comment stripping and whitespace cleanup are not applied. An explicitly empty argument is checked by the parser; omitting the argument is a usage error.

Use `--` before text that begins with a hyphen:

```sh
dispat diagnostics -- '--not-a-commit'
```

For a multiline message in a POSIX shell:

```sh
dispat diagnostics 'feat(core): add streaming

---
fix(api): close the stream'
```

If your project already has a script named `diagnostics`, run it explicitly with `dispat run diagnostics`. The bare command name now selects message validation.
