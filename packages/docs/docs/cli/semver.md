# The semver command

Use `dispat semver` to parse or construct a [SemVer 2.0.0](https://semver.org/spec/v2.0.0.html) version without a
configuration file or Git repository. Both forms write exactly one JSON object to standard output.

```sh
dispat semver parse '1.2.3-rc.1+sha.001'
dispat semver build 1 2 3 --pre-release rc.1 --build-metadata sha.001
```

Both examples print:

```json
{"version":"1.2.3-rc.1+sha.001","major":"1","minor":"2","patch":"3","prerelease":["rc","1"],"build":["sha","001"]}
```

The core components are decimal **strings** so JSON consumers do not lose large integers to floating-point rounding.
The CLI accepts arbitrarily long core numbers. The `version` field retains the complete spelling, including build
metadata; `prerelease` and `build` are arrays, empty when absent. `--pre-release` and `--build-metadata` belong only to
`build` and take dot-separated identifiers. An explicitly empty flag value is invalid.

The parser rejects a leading `v`, missing components, leading zeros in core numbers or numeric prerelease identifiers,
empty identifiers, non-ASCII letters and other characters outside the SemVer grammar. Build metadata may contain a
numeric identifier such as `001`. Malformed input exits `2`, writes a diagnostic to standard error, and writes no JSON.
Output failures exit `1`.

This utility does not choose a release version or modify one. Dispat's release engine uses a separate `uint64` core
representation, so a CLI version with larger core numbers cannot be used as an exact release version. The release
engine's `Version.String` intentionally omits build metadata from computed versions; `dispat semver` preserves it.
`dispat --version` still reports the installed Dispat binary's version.
