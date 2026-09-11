# Building and testing the npm distribution

Install workspace dependencies without lifecycle scripts when working from a source checkout. The packaged postinstall program does not exist until TypeScript has compiled it.

```sh
pnpm install --ignore-scripts
pnpm --filter @dispat/cli test
```

Both implementation and tests use strict TypeScript. Production code compiles into `build`; tests compile separately and import those same emitted modules. Named ESM exports and `index.ts` barrels expose shared modules. The native Node `#root/` package alias resolves imports without a loader. The launcher and installer entrypoints are kept outside the shared barrels so importing a module does not execute a command.

The coverage gate requires at least 95% lines and branches for executable package logic, including release tooling and entrypoints. Coverage maps back to TypeScript. Test fixtures and generated files are not production coverage. Go coverage is measured separately by the repository's existing gate; adding Node tests does not change its denominator.

## Test cases

| Area | Cases |
| --- | --- |
| Installation | All six platform mappings; digest, size and version verification; existing binary reuse; wrong version; truncated, oversized and corrupt bodies; missing metadata; paths with spaces; symbolic links and directory destinations; concurrent installation and cleanup. |
| Transport | Real local HTTP failures and stalled bodies; HTTPS through a CONNECT proxy and a custom CA; redirects, downgrade rejection and deadlines. |
| Launcher | Argument selection and flag values, `--`, read-only self-update forms, mutation rejection, stdin/stdout/stderr, environment, working directory, exit codes, signals and spawn failures. |
| npm publication | Real npm commands against a disposable local registry, identical retry, lost response recovery, conflicting integrity and channel selection. npm 11 and npm 12 JSON result shapes are accepted only when they identify one result. |
| Release graph | First release, independent npm patch with retained binary pin, explicit provider propagation, shared minor change, prerelease graduation, failed provider publication and consumer retry. These cases are assigned to the Go integration test-plan matrix. |
| Packed artifact | Install the same tarball globally, locally, through npm exec, and with pnpm's script-disabled repair path. Exercise version, help, and status in a disposable Git repository. |

npm 12 matches local tarball approvals by their exact file identity, not the name declared inside the archive. The artifact smoke test approves that file for global/npm-exec installs and records the same file identity in the local fixture’s `allowScripts` policy. Registry users can approve `@dispat/cli` by its package name.

Ordinary tests use fixture servers and disposable registries; they never publish to npm or create production releases. Native process fixtures run on supported local hosts. The post-release workflow defines its own native install matrix. Configured runners and actual executions are reported separately in [Verification](#verification).

## Found after initial testing

The following defects were found after the first test run. Their fixes and regression coverage are recorded here.

- Release retry `34580709934` passed every package test script, then failed during coverage badge generation: the cache change removed `TESTREPORT_COMMIT` from the shared base but left the badge relying on it. The badge now declares its commit input in its own stage. The separate full-suite regression exercises the real badge target in an isolated copy of the source and profiles. Matching profiles pass; stale or missing stamps and coverage below 95% must fail with their specific diagnostics. The valid local fixture is the historical `0c17c284` profile set, not a new measurement of current Go coverage.

- The first remote release deployed docs before npm packaging failed. Docs now declare the npm CLI as a retained provider, and the CLI requires consumers to wait for publication. `TestNPMDistributionGatesDocsOnPublishedCLI` uses the real package policies to cover blocked hooks and deployment, retry after initial and subsequent npm failures, and independent docs patches.
- The docs measurement build inherited the source commit in its shared Docker base, invalidating unchanged dependency and test layers. Commit attestation now follows each test layer, and aggregate reports import the individual gate caches. Experiment campaigns reuse only complete records for the identical harness image. Cache regressions cover input changes, missing or corrupt records, failed dispat cells, and extra cells.
- Cache verification caught a normalized package version leaking into the site bundle. The docs source stage now restores the real manifest and disables implicit pnpm reinstallation; the dependency stage still keys installation on the normalized dependency inputs. The docs build regression rejects the cache-only version in generated output.

- Commit CI invoked the release packaging script on a runner without pnpm. The ordinary `build` now uses the Docker fixture gate, while the release flow explicitly uses `release-build`. `TestNPMDistributionSeparatesCIBuildFromReleasePackaging` exercises the actual package configuration and verifies both command paths without contacting a registry.
- Release auto-versioning made the workspace manifest and installed dependency state differ, so pnpm's pre-run verification implicitly reinstalled with lifecycle scripts and invoked postinstall before TypeScript compilation. The release-build environment now disables only that implicit verification after the workflow's explicit script-disabled install, including nested and later pnpm commands; a real-pnpm regression reproduces the stale-workspace failure and guarded compile sequence.

- An early HTTP header rejection left a stalled response open. The installer and transport now destroy abandoned bodies; a real local server test asserts connection closure and preservation of the old executable.
- The initial command guard missed a later `-h=false` override. Command-selection tests cover explicit false values and the `--` boundary.
- A process signal listener does not receive its signal name as an argument. Forwarding must bind each signal explicitly; otherwise SIGINT is forwarded as the default SIGTERM.
- Searching for the command with `args.indexOf` can find an earlier flag value with the same spelling. Selection must retain the actual command position.
- TypeScript aliases resolved at compile time but remained unresolved in emitted tests. Native package imports now provide the same `#root/` mapping to runtime code and compiled tests.
- The first post-release npm version assertion could fall through to a successful command after a mismatch. A subsequent exact-output comparison also overlooked the native banner and platform suffix. The final workflow compares the version token and fails on a mismatch.
- npm 12 blocks unapproved lifecycle scripts even when `ignore-scripts=false`. The documented registry approval and explicit repair paths preserve npm’s policy. Packed-file tests approve the exact tarball identity, as npm requires for file dependencies.
- Real npm 12 tests exposed different JSON result shapes for `pack` and `view`. The release tools accept one unambiguous result in either npm 11 or npm 12 format. Real local-registry tests verify publication and recovery after a lost response.
- Introducing the package into existing repository history inherited old provider propagation. `TestNPMDistributionStartsOnExistingNativeLine` covers the package-specific baseline record and its first patch release.
- Canonical macOS temporary paths can differ from their `/var` spelling. Entrypoint detection now compares real paths; a symlink-path regression verifies that the installer still executes.
- A failed `latest` lookup could hide a registry failure. Publication now fails closed except for an actual missing version; regression cases cover lookup errors, older stable releases, and semantic versions with build metadata.
- Compiling into an existing directory could leave obsolete JavaScript behind. Both compile commands now clear their output directory. Docker contexts exclude generated output, and the package suite passed from a clean container build.
- Release API response handling needed its own byte bound and shared metadata validation. Packaging regressions cover oversized responses and invalid asset metadata. Structured installer diagnostics now have serialization coverage.
- Commit CI exposed an editor descendant that survived graceful process-group cancellation and held the integration harness pipes for 30 seconds. A TERM-resistant child reproduces the failure deterministically; authoring cancellation now force-kills its editor group, while ordinary package scripts retain graceful TERM cleanup.

## Verification

Before the first push, the TypeScript suite passed all 58 tests on macOS ARM64 and in a clean Linux ARM64 Docker build. Package-owned executable logic reached **99.67% line coverage and 95.46% branch coverage**, including release tools and entrypoints. Coverage excludes tests, generated metadata, third-party code, and the erased, interface-only `types.ts` module. No executable production branches use coverage-ignore directives.

That packed tarball passed global installation, local dependency installation, npm exec, pnpm script-disabled installation and repair, version, help, and a disposable Git repository's `status` operation on macOS ARM64 with Node 24/npm 12. Linux ARM64 with Node 22/npm 10 passed global tarball installation, native version, and help. All six platform mappings have tests. Native x64 and Windows execution was not performed locally; the six-runner post-release matrix is configured but has not run. Those local checks preceded production workflow execution.

The existing Go suite passed with 649 integration cases, repeated with race instrumentation. Combined Go coverage is 95.0%; unit-only coverage is 88.0% and integration-only coverage is 85.9%. These measurements include the first three new release-graph cases and use the starting checkout's measurement label, `0c17c284cb098ec53fa385fee79bd82b0574cff0`. The extra initial-baseline case was added afterward; all four npm release-graph cases then passed separately with race instrumentation. A later commit-CI failure exposed a process-tree cancellation defect; its focused regression passed 20 times normally and 10 times with race instrumentation after the authoring-only force-kill fix.

The docs typecheck, tests, and production build passed. Repository Go vet, formatting, shell checks, and release workflow validation passed. Test-plan validation found 672 referenced tests and 650 integration goal assignments; its duplicate-name notices concern existing test names.

The first remote release deployed docs 1.10.9, then failed npm packaging before publication. Commit CI separately exposed the ordinary-build and editor-cancellation failures recorded above. After the repairs, the TypeScript suite passed **59 tests** on macOS ARM64 and in the Linux ARM64 Docker gate, retaining **99.67% lines and 95.46% branches**. All six npm release-graph tests and editor cancellation passed with race instrumentation; the script and app unit packages passed. Updated test-plan validation covers 674 references and 652 integration assignments. These focused repair checks are separate from the historical full-suite coverage measurement.

## Build the release metadata

`release.json` is generated during the build and ignored by Git, together with compiled output, tarballs, coverage, and downloaded binaries. It records the exact native version, tag, asset names, byte sizes, and SHA-256 digests for all six platforms.

```sh
# Use an already published CLI version from the same major/minor line.
DISPAT_WORKSPACE_DISPAT_VERSION=1.10.0 pnpm --filter @dispat/cli build
```

The build fails when the exact provider release or complete asset metadata is unavailable. It does not substitute `latest`. Ordinary CI runs fixture-based packaging tests rather than fetching an unpublished planned version. The release graph waits for the native provider before this build runs. An npm-only patch receives the provider's existing published version.

A dependency orders publication; it does not automatically propagate every source commit. This repository defaults to propagation depth zero. Use an explicit directive such as `fix(dispat)^` when a native patch must also rebuild its direct distributions, or author distinct package records when their release notes differ. Preserve the `cli` group's `fixedMajorMinor` policy and independent patches.

## Publish and recover

The existing [release workflow](../../.github/workflows/release.yml) builds metadata, packs once, installs that tarball for acceptance, and publishes the same bytes. The pack step records the tarball path, integrity, name, and version for later stages. Stable releases use `latest`; prereleases use their release channel.

Publication checks the local digest and artifact identity before uploading. On a retry, an identical registry integrity is accepted and the channel tag is reconciled. Conflicting bytes fail; a published version is never overwritten. A lost publish response is resolved by reading the registry. A stable retry does not move `latest` backward, and a new older stable publication fails rather than replacing a newer `latest`.

If a stage fails after npm accepted the artifact, retain its recorded tarball and integrity, inspect the registry's exact version, and retry through the release workflow. Do not delete release tags or change the bytes of an existing npm version to work around a conflict. npm publication outputs are independent of native publication outputs, so an npm-only release still gets its install checks.

The first publication requires a temporary repository secret named `NPM_TOKEN`. The workflow exposes it only to the publishing process through runtime npm configuration; no token belongs in a Docker build argument, image layer, source file, or log. After bootstrap:

1. Open the package's trusted publisher settings on npm.
2. Select GitHub Actions, owner `yohimik`, repository `dispat`, and workflow filename `release.yml`.
3. Allow direct publication. Leave the environment unset unless the workflow adopts a matching GitHub environment.
4. Verify an OIDC publication before removing the bootstrap token.

The workflow's Node/npm pair must satisfy [npm's trusted publishing requirements](https://docs.npmjs.com/trusted-publishers/). Production publication remains a CI action; local implementation and fixture tests do not configure credentials or prove OIDC authorization.

## Follow-ups

Design npm download-counter semantics separately: an npm installation also downloads a GitHub asset, so adding both counts may double-count installations. Keep the launcher guard aligned with changes to the native command parser.

The test-plan checker currently relies on `rg` being installed: without it, shell pipelines can report zero validated cases instead of failing. Keep that prerequisite explicit until the checker gains a missing-tool guard.
