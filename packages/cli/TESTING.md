# npm CLI test plan

This plan covers the `@dispat/cli` npm wrapper and its release tooling. It does
not change or re-test unrelated Go runtime behavior. Tests use local files,
processes, HTTP servers, proxies, and a disposable npm registry; they never
publish to the public registry, create Git tags, or push commits.

## Required behavior

| Area | Cases |
| --- | --- |
| Package metadata | Exact native version and immutable release tag, all six platform assets, byte-size and SHA-256 validation, missing and oversized API responses, response cleanup, and a total request deadline. |
| Installer | Existing valid and invalid binaries, streamed and corrupt downloads, wrong versions, truncated and oversized bodies, symlinks, directories, concurrent installs, cleanup, executable mode, HTTPS redirects, proxy and custom CA settings, and request timeouts. |
| Launcher | Literal arguments, value-taking flags, last boolean value, environment, directory, streams, exit status and signal propagation, spawn failures, npm-managed self-update, and listener cleanup. |
| Artifact | npm 11 array and npm 12 keyed pack output, one artifact record, exact package name and version, streaming SHA-512 verification, release-output identity, global and local npm installation, npm exec, pnpm repair, and a disposable repository status command. |
| Publication | Exactly one `npm publish` invocation, stable and prerelease tags, access and provenance flags, npm failure propagation, a real disposable-registry upload, and successful process exit when npm accepts the upload while registry metadata remains unavailable. |
| Workspace | Release compilation cannot trigger an implicit pnpm reinstall or lifecycle script from a stale workspace lock. |

The package coverage gate requires at least 95% executable line and branch
coverage. Tests and generated declarations are excluded; production branches do
not use coverage-ignore directives.

## Release boundary

The release build obtains exact native release metadata, compiles the wrapper,
packs once, and runs the artifact smoke test. Before publication the smoke test
checks that `artifact.json` is complete, matches the rewritten npm manifest, and
still names the same tarball and streaming SHA-512 digest. It also checks any
`DISPAT_OUTPUT_*` values supplied to later stages. The install exercises use that
same tarball path.

The publish script then invokes npm once:

```text
npm publish <tarball> --access public --provenance --tag <channel>
```

It maps dispat's `stable` channel to npm's `latest` tag. It does not call `npm
view`, compare registry versions or integrity, reconcile a response, or invoke
`npm dist-tag`. npm stdout and stderr remain visible, successful completion is
reported on stdout, and an npm error remains a failing process status. The npm
subprocess has a two-minute deadline and a 2 MiB output bound.

## Found after initial testing

- The first artifact-verifier test packed into a custom output directory while
  the verifier assumed `dist`. `verifyArtifact` now accepts the same output
  directory abstraction as the packer, and the regression verifies both a valid
  custom path and a tarball changed after packing.
- The process-level publication regression initially enabled provenance against
  a disposable local registry. npm correctly refused automatic provenance
  outside a supported CI provider. The fixture now removes that single flag in
  its test-only npm shim while retaining the production command unchanged; the
  direct command-boundary test still asserts the production provenance flag.
- Successful publication was written to stderr by the earlier implementation.
  That channel can be classified as a warning by the release runner. The entry
  point now writes success to stdout, with a process regression that checks the
  channel. Errors remain on stderr and preserve a nonzero exit.

## Verification

`pnpm --filter @dispat/cli test` passes 54 tests on macOS ARM64. Executable
package logic reaches 99.82% line coverage and 96.59% branch coverage; release
scripts reach 100% lines and 99.04% branches. The suite includes real entrypoint
processes, local HTTP and HTTPS servers, a CONNECT proxy, a disposable npm
registry, and the stale-pnpm-workspace fixture.

Historical packed-artifact and Linux results remain useful background, but are
not presented as fresh evidence for this recovery. Windows and native x64
execution still require their configured CI runners. The parent agent is
responsible for independent Docker, documentation, release-plan, and final
workflow verification.

## Recovery constraints

Release run `34584443988` published `@dispat/cli@1.10.0` with the expected
SHA-512 artifact, then failed before a repository release tag was recorded. A
public npm version remains reserved after publication even when it is
unpublished, so recovery uses npm version `1.10.1` and preserves native provider
version `1.10.0`. No manifest version is hand-edited; the final CCME release
record supplies the version.

Removal was verified at 2026-09-11 09:57 UTC: the public package document and
original tarball both returned 404. npm requires waiting 24 hours after complete
package removal before publishing a new version, so 2026-09-12 09:57 UTC is the
conservative earliest release time; the exact deletion moment remains
user-managed. Publication, tag creation, and pushes remain outside these local
tests.
