# npm CLI test plan

This plan covers the `@dispat/bin` npm wrapper and its release tooling. It does
not change or re-test unrelated Go runtime behavior. Tests use local files,
processes, HTTP servers, proxies, and a disposable npm registry; they never
publish to the public registry, create Git tags, or push commits.

## Run the tests

```sh
pnpm install --ignore-scripts
pnpm --filter @dispat/bin test
```

## Required behavior

| Area | Cases |
| --- | --- |
| Package metadata | Exact native version and immutable release tag, all six platform assets, byte-size and SHA-256 validation, missing and oversized API responses, response cleanup, and a total request deadline. |
| Installer | Existing valid and invalid binaries, streamed and corrupt downloads, wrong versions, truncated and oversized bodies, symlinks, directories, concurrent installs, cleanup, executable mode, HTTPS redirects, proxy and custom CA settings, and request timeouts. |
| Launcher | Literal arguments, value-taking flags, last boolean value, environment, directory, streams, exit status and signal propagation, spawn failures, npm-managed self-update, and listener cleanup. |
| Artifact | npm 11 array and npm 12 keyed pack output, one artifact record, exact package name and version, streaming SHA-512 verification, release-output identity, a script-disabled global npm install with exact-path repair, local npm installation, npm exec, pnpm repair, and a disposable repository status command. |
| Publication | Exactly one `npm publish` invocation, stable and prerelease tags, access and provenance flags, npm failure propagation, a real disposable-registry upload, and successful process exit when npm accepts the upload while registry metadata remains unavailable. |
| Workspace | Release compilation cannot trigger an implicit pnpm reinstall or lifecycle script from a stale workspace lock. |
| Post-release readiness | Missing or incomplete exact-version metadata, HTTP 404/429/5xx, network errors, bounded retries, stalled response headers and bodies, permanent failures, and execution from TypeScript source without installed dependencies. |

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

## Issues found after development

- The independent Docker gate showed that selecting Node 24.18 alone did not select npm 12. The first npm-12 gate
  therefore failed its version assertion. The gate and native post-release checks now install npm 12.0.1 explicitly;
  the Docker gate also verifies that exact version before running the default-denial regression.
- The launcher previously suggested a project-relative command after a global install. From a home directory that
  command looked under `~/node_modules` and failed before reaching the installed package. The launcher and failed
  postinstall now print the exact script path with shell-safe quoting; the packed-artifact smoke test installs under a
  disposable global prefix with scripts disabled, checks that launch fails with that path, repairs it, and invokes it.
- A barrel/entrypoint regression relied on the checkout never containing a native binary. Workspace installation can
  legitimately create one, masking the missing-binary branch. The test now runs a copied wrapper from an isolated
  package fixture and always exercises the intended failure.
- npm 12 blocks dependency install scripts by default. Global and one-off installs must pass
  `--allow-scripts=@dispat/bin`; local projects retain approval in `package.json`. The repair path deliberately runs
  only when the user invokes it and does not bypass npm's policy or make ordinary launches download code.
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
- An inherited npm registry environment could redirect the disposable-registry
  test. The fixture now pins its localhost registry in both the command and
  environment.
- A post-publication registry lookup could fail after npm had accepted the
  tarball, preventing release records from being written. The publisher now
  returns success after npm succeeds; a process regression covers unavailable
  registry metadata after an accepted upload.
- Open: the ordinary anonymous GitHub metadata request returned HTTP 403 during
  artifact validation. A smoke test using authenticated immutable metadata does
  not establish that the production anonymous request succeeds.
- Validation gap: native Windows and x64 artifact execution still requires the
  configured CI runners. Platform-mapping tests do not verify native execution.
- Validation gap: production trusted-publisher authorization and provenance
  require CI verification; the disposable registry cannot establish GitHub OIDC
  configuration.
- npm's script-approval behavior is version-specific. The package keeps its supported Node baseline gate and adds a
  pinned Node 24/npm 12 fixture that proves default denial and explicit recovery. The pre-publish artifact smoke also
  runs under npm 12 and retains a separately approved global installation. The first trusted-publisher run in that
  updated environment remains CI validation.
- Follow-up: historical release planning currently reports 174 warnings and 29 errors for old commit records,
  including retired scopes and already-released corrections, plus a pre-existing split in the unchanged `ccme`
  version group. Those repository-history diagnostics predate this npm repair and need separate cleanup.

- Post-release installations previously attempted npm resolution once. The
  independent check now polls exact-version install metadata every 10 seconds
  for at most five minutes, limits each request to 15 seconds, and then installs
  with fresh metadata. Authentication and malformed-response failures fail
  immediately. This wait never runs inside the publish stage.
- npm rejected a new publication with E409 while the package was within the
  24-hour restriction after complete removal. Post-publication readiness retries
  cannot resolve that restriction; recovery must wait before retrying through CI.

## Feature review

The independent review found that the self-update refusal and package README still suggested global updates without
script approval. Those commands could recreate the same missing-binary failure after an upgrade. They now include
the scoped approval, and the process-level entrypoint regression checks both suggested global update commands.

The repair command is derived once from the wrapper's resolved package root and Node executable. It does not search
the filesystem, invoke a shell, retry a launch, or download during ordinary command execution. Existing argument,
stream, working-directory, environment, exit-code, signal, self-update, and listener-cleanup behavior stays on the
same spawn path. Quoting is POSIX-shell safe on Unix and explicitly PowerShell safe on Windows.

The repair itself retains the bounded, streaming installer: binary size is capped, hashing does not buffer the
artifact, request and executable validation have deadlines, temporary files are removed, and response bodies are
destroyed. This change adds no resident resources or unbounded work. Native Windows execution and npm trusted
publishing remain CI-only validation gaps as recorded above.
