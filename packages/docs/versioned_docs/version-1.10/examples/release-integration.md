# Integrating an existing release pipeline

Start with the artifacts your users install, then trace what each build reads. A local workspace dependency and a
package fetched from a registry need different ordering. Keep your working build tools and publisher commands;
dispat supplies the release graph, version plan and package completion records.

This guide draws on public release incidents and isolated script reproductions reviewed on **10 September 2026**.
The linked projects have not been migrated to dispat as part of this review. A repaired incident is evidence for a
regression test, not a claim that the project's current release is broken.

## Choose the build boundary

For each consumer, identify whether it needs its provider's local build output or its published artifact.

| Input to the consumer | Integration |
|---|---|
| An npm workspace package or a locally built SDK | Keep `isBuildWaitingPublish` false on the provider. The consumer can build after the provider builds, while publication proceeds. |
| A base image pulled from a registry, a remote Maven artifact, or a downloaded release binary | Set `isBuildWaitingPublish: true` on the **provider package or its space**. Verify availability in the provider's publish script before it succeeds. |
| Infrastructure needed only when deploying | Keep the provider's build-wait flag false. Application builds can finish before the infrastructure publish; their publish stages wait for it. |
| An external CI workflow | Wait for that exact workflow invocation and propagate its final status. A successful dispatch only means the request was accepted. |

The flag belongs to the provider and affects all its consumers. It is not an option on an individual edge. If one
artifact needs a different publication boundary, consider giving it a separate package. See
[space options](../configuration/spaces.md#space-options) and [dependencies](../configuration/dependencies.md).

A Docker build that uses BuildKit shared contexts can consume local output without a registry hop. A build that pulls
`FROM registry.example.com/base:1.2.3` needs that exact remote image and its required platform manifest. Ordering does
not make a mutable `latest` tag reproducible. Keep the version or digest tied to the reviewed release plan.

## Pin a runtime snapshot before downloading

A mutable directory alias can change between requests, including during a resumed download. In
[Steam Runtime #842](https://github.com/ValveSoftware/steam-runtime/issues/842), different responses for the same
`latest` archive had different lengths and ETags, breaking downloads and a downstream package build. The maintainer
explained that the alias design permits inconsistent reads and documented the supported pinning procedure.

Resolve the runtime's `latest-*.txt` pointer once, such as scout's
[general-availability pointer](https://repo.steampowered.com/steamrt1/images/latest-steam-client-general-availability.txt),
then fetch the archive and its checksum manifest from the resulting
immutable snapshot directory. Retain that snapshot and checksum in your downloader's receipt so a retry resumes the
same object. Re-reading the mutable pointer for each file can mix releases even when every individual request succeeds.
The publisher or download script owns this check; stage ordering alone does not make a mutable URL reproducible.

For a distribution package, also retain its source descriptor and applied patch series. The
[Sniper/PipeWire report](https://github.com/ValveSoftware/steam-runtime/issues/853#issuecomment-5612189158) prompted a check of the exact
published source, not just its upstream version: the checked downstream patches do not carry the cited IO-buffer
synchronization fixes. That establishes source contents; reproducing the reported Bluetooth-triggered crash remains
a separate consumer test. A version comparison alone cannot tell whether a distribution backported a fix.

Validate compatibility at the other end too: the [Steam example](./steam.md#validate-the-host-and-plugin-together)
checks an installed host/plugin combination, while the [Android example](./android.md#inspect-every-native-wrapper-for-16-kb-alignment)
checks each native wrapper even when the core SDK passes. These require different consumer tests.

## Make success mean available to the next stage

A publisher must return failure when any required upload or finalization step fails. Check the files built in this
run, the destinations that accepted them, and the exact versions consumers can resolve.

The review found these useful test cases:

| Evidence | Integration lesson |
|---|---|
| [JupyterLab's release discussion](https://github.com/jupyterlab/jupyterlab/issues/16055#issuecomment-5610400425): an August run uploaded Python artifacts before an npm dist-tag operation failed. Later npm versions are available. | Treat a version upload and a channel update as separate operations inside the publisher. Reconcile both on retry. |
| [RxSwift packaging reproduction](https://github.com/ReactiveX/RxSwift/issues/2733): injected build and signing failures still produced a ZIP containing stale framework outputs. | Build in a clean output directory, propagate subprocess failures, and validate the final archive rather than only the source tree. |
| [Defold upload reproduction](https://github.com/defold/defold/issues/13158): an injected failed upload was followed by a success message and a normal return. | Do not let an HTTP helper swallow a required upload failure. Check every asset result before reporting package completion. |
| [Apache Arrow's workflow watcher](https://github.com/apache/arrow/issues/50363): the helper selected an older successful run instead of the newly dispatched one. | Retain the invocation ID. Test delayed visibility, concurrent dispatches, failure, cancellation and a bounded wait. |
| [NemoClaw's candidate-image discussion](https://github.com/NVIDIA/NemoClaw/issues/11282#issuecomment-5610427164): a new tag changed the version derived during a rerun of the same commit. Stable promotion was skipped in that run. | Pass the planned version into every build and publisher. Do not recalculate it from changing remote tags between stages. |

These are different failure boundaries. A package record cannot describe every destination hidden inside one shell
command. If a publisher uploads a gem, an npm package and release assets, it must recover its own partial completion.
Alternatively, model independently versioned deliverables as separate packages with explicit dependencies.

After a successful external upload with a lost response, inspect the destination before retrying. Compare the intended
version and artifact identity; do not treat every duplicate-version response as proof that the correct bytes arrived.
Never delete and recreate an immutable version as a routine retry. See [release recovery](../reference/releasing/recovery.md).

## Keep the release tied to its input commit

A commit can arrive while another release is running. Test this separately from a registry failure: serialization,
artifact identity and recovery after a rejected push protect different boundaries.

[SvelteKit's concurrency fix](https://github.com/sveltejs/kit/pull/15160) documents overlapping release jobs that could
leave a release unpublished. Its workflow now serializes those jobs. [SWC's misc npm tagging change](https://github.com/swc-project/swc/pull/12173)
ties tags to the checked-out commit and tests conflicting and concurrent tag creation. Its
[release-recovery discussion](https://github.com/swc-project/swc/issues/12338) also shows why a green rerun is not proof
that every matrix leg ran again: GitHub can carry successful jobs from an earlier attempt. Retain each leg's run and
attempt identity. Preserve existing protections when integrating dispat; these examples do not establish an unresolved
race in those projects.

For a regression test, start a release from commit A, then push unrelated commit B while publishing is paused. After
the release finishes, verify that its package versions, artifacts, tags and changelog describe the intended input.
A later branch tip must not silently become the identity of an earlier build. Include a rerun after publication but
before Git finalization, and check that completed artifacts are retained.

This matters for one-package repositories too. A single package can have several destinations, platform assets,
release notes and a notification step. In the [Astro retry report](https://github.com/withastro/astro/issues/17956),
one attempt published four npm packages and then failed before either marketplace publisher ran; its green retry found
no unpublished npm versions and skipped both marketplaces. Record those destinations separately so a retry can resume
them without another npm version. This is a post-publication recovery case, not evidence that a concurrent commit
caused it. See [recovery](../reference/releasing/recovery.md) for dispat's recording boundary and the limits of retrying
external publishers.

## Apply the pattern to your ecosystem

The review covered all 21 manifest ecosystems. This table is an integration map, not a claim of a reproduced failure
in every ecosystem. Where only an intentional omission or a repaired incident was found, no new release defect is asserted.

| Ecosystem | What to keep in the integration |
|---|---|
| npm | Workspace builds, package-specific registry credentials, dist-tags and a clean consumer install. See [npm](./npm.md). |
| Go modules | Module paths and module-specific Git tags; verify the published module from outside local `replace` directives. See [Go](./go.md). |
| Cargo | Local workspace builds plus registry-resolvable packaged dependencies; recover per crate after partial publication. See [Cargo](./rust.md). |
| Python | Both sdist and supported wheels, with an explicit platform/Python matrix. See [Python](./python.md). |
| Composer | Component repository tags, Packagist visibility and generated API docs as distinct outputs. See [Composer](./php.md). |
| Maven | POMs, classifiers, signatures and staging promotion; test a clean consumer against the promoted coordinates. See [Maven](./java.md). |
| NuGet | Managed packages, native runtime assets and any separate editor extension publisher. See [.NET](./dotnet.md). |
| Dart/pub | Compatible published dependency versions, SDK constraints and a fresh consumer solve. See [Flutter](./flutter.md). |
| Apple plist | Platform-valid bundle versions, signing and store processing. See [Apple](./apple.md). |
| CocoaPods | Podspec/source consistency and the exact binary archive referenced by a pod. See [Apple](./apple.md). |
| Xcode | Fresh XCFramework outputs and target-platform consumer tests. See [Apple](./apple.md). |
| Android | AAR/native ABI contents, application version codes and the project's intended publication list. See [Android](./android.md). |
| Gradle | Published metadata and version catalogs as well as JARs; a local project build does not test repository resolution. See [Gradle](./gradle.md). |
| RubyGems | The gem, any paired JavaScript package, channel metadata and release notes. See [Ruby](./ruby.md). |
| Docker | Registry-backed provider chains, exact image identities and platform manifests. See [Docker](./docker.md). |
| Aqua | Tool pins and compatible asset selectors; registry authoring and tool installation stay with Aqua. See [Aqua tool pins](./aqua.md). |
| Unity | Native UPM `package.json` files, `.unitypackage` exports and optional NuGet artifacts. See [Unity](./unity.md). |
| Godot | Addon/native-library compatibility, engine-version matrices and resumable asset publication. See [Godot](./godot.md). |
| Unreal | Packaged plugins, supported engine/platform combinations and native library inventories. See [Unreal](./unreal.md). |
| Defold | Engine/editor/SDK outputs and the separate extension-service deployment. See [game development](./game.md). |
| O3DE | Gem/project manifests and installer payloads tied to an immutable build identity. See [game development](./game.md). |

Do not add an npm wrapper just to make a non-JavaScript deliverable releasable. Use its native manifest where supported,
or an explicit package with your own scripts and version policy. Unity's UPM `package.json` is already its native
manifest, not a workaround.

## Test the distributed artifact

Several findings concerned consumers outside the source checkout:

- [Cesium for Unreal's packaging discussion](https://github.com/CesiumGS/cesium-unreal/issues/1430) records a missing native
  library and asks for non-Windows package tests. Its existing Windows artifact-based test is a useful model: install
  the packaged plugin into a clean project and build it on each supported platform.
- [O3DE's preview installer report](https://github.com/o3de/o3de/issues/18917) describes an older bootstrapper fetching
  newer payloads. The maintainer distinguishes this preview behavior from stable releases. Keep an immutable payload
  prefix per build, then update a separate discovery pointer after the full set is available.
- [Dartway's publication checker](https://github.com/dartway/dartway/issues/143#issuecomment-5610468146) checks only the
  registry's latest version. An older compatible published version can still satisfy a constraint. Separate dependency
  availability from a policy requiring the newest version, and let a real consumer solve check transitive compatibility.

Run the same release script on the operating systems it supports. Shared
[referenced configuration](../configuration/refs.md) can hold the graph while platform files select shells and commands.
This avoids duplicating the release policy; it does not translate Bash into PowerShell or replace Windows testing.

## Prioritize failures that affect consumers

A GitHub release appearing before npm is not sufficient evidence of a broken release. Check the intended publication
order and maintainer responses first. Missing optional assets, an unused version constant and an old release page
are different from a package that fails to compile or an artifact that corrupts a supported workload.

Useful acceptance checks come from demonstrated failures:

- [release-please's component parsing report](https://github.com/googleapis/release-please/issues/2801#issuecomment-5611677658) and
  [related crash](https://github.com/googleapis/release-please/issues/2884) were reproduced with its published 17.11.2
  parser. Inline code containing `<path>` dropped a component from parsed release data; `<details>` caused a
  `TypeError`. Escaped controls passed. Compare the intended component set with the parsed plan and final records;
  rendered notes must not silently become a smaller release plan. The reproduction exercised parsing, not publication.
- [Firebase Auth's dependency report](https://github.com/firebase/firebase-android-sdk/issues/8557) includes a maintainer
  reproduction of Kotlin compilation failure. The published AAR references an annotation missing from its POM.
  Test a clean consumer of the packaged API; another dependency in a large development checkout can hide the omission.
- [VMAF's backpressure regression](https://github.com/Netflix/vmaf/issues/1587) has a confirmed single-commit correction
  outside the latest release. A bounded queue probe corroborated the mechanism without repeating the reporter's
  memory-exhausting video workload. Verify that the artifact delivered to users contains the tested correction,
  rather than treating corrected mainline source as a completed release.

Put project-specific checks in the configured build or publish script, where a failure stops that package's stage.
dispat orders those scripts and records their outcome; it does not supply a video-quality, Kotlin or platform-specific
acceptance suite. The [npm](./npm.md), [Gradle](./gradle.md), [pnpm](./pnpm.md), [Cargo](./rust.md),
[Apple](./apple.md) and [binary](./binaries.md) examples describe the corresponding consumer boundaries.

A harmful published artifact may call for a maintenance release, a downstream backport or explicit withdrawal.
Those choices differ from retrying an interrupted publish. [CCME 3's rollback specification](https://github.com/yohimik/dispat/blob/main/specs/ccme-spec/ROLLBACK.md)
describes a version-bound request and withdrawal receipts while preserving release history. It does not turn an
ordinary failure into an automatic rollback; dispat's current parser still implements CCME 2. See
[release recovery](../reference/releasing/recovery.md) for the implemented retry boundary.

## Version documentation and agent instructions too

Give a guide, protocol schema or specification its own package when consumers need a versioned contract. Choose
independent versions or a [version group](../configuration/spaces.md#versioning-groups) according to compatibility, rather than
forcing every document change to release every binary.

Check the whole published document set. [Playwright CLI's skill-reference issue](https://github.com/microsoft/playwright-cli/issues/463)
shows why comparing only a root skill file can miss changed referenced guides. Test links from the released CLI to the
intended snapshot, include referenced files in the artifact inventory, and distinguish current advice from historical
contracts. dispat's [agent guide](https://github.com/yohimik/dispat/tree/main/specs/agent-guide) is an example of a separately
released guide; it is not evidence that another project's migration has been tested.

## What this review does not establish

A missing registry entry may be intentional. AndroidX maintainers explain that
[`media3-effect-ndk` is not published to Google Maven](https://github.com/androidx/media/issues/3363); that is not a failed
upload. Aqua's [Scenarigo update discussion](https://github.com/aquaproj/aqua-registry/issues/59576) concerns a version and
Go-toolchain selector that must identify an existing asset. Rewriting a version alone cannot choose a compatible selector.

Registry quotas, credentials, signatures, platform compatibility and store approval remain the responsibility of the
configured tools and checks. dispat can order those commands and record their successful completion; it cannot repair
a registry outage or validate a binary that the publisher never tested.

The [release experiments](../internals/experiments.mdx) provide controlled evidence for the scenarios described there.
They are not a certification of migrations for every ecosystem in this table. Before adopting the setup, run a
throwaway release with failure after the first publication and another with a lost response. Confirm that the retry
preserves completed artifacts and refuses ambiguous or mismatched ones.

## Choose the authoring and document contract

The same recovery questions apply to [a single package](./single-package.md#one-package-can-still-have-a-partial-release).
Count the external destinations, not just the number of manifests. Preserve release-it's existing record-retention
behavior when comparing a post-publish Git failure; a different coordinator still needs safe reconciliation.

For Changesets users, [the npm integration example](./npm.md#moving-release-intent-out-of-changeset-files) separates
per-change input files from generated changelogs and release commits. It includes a tested commit-based alternative,
explicit dependency propagation, outstanding-intent review and merge-message requirements.

[Skills, specifications and TeX documentation](./document-artifacts.md) need their own artifact inventory and
compatibility policy. A missing uploaded ZIP can be intentional when templates moved into a wheel; a date-based
specification revision can differ from every SDK version. Verify the declared destination before reporting an anomaly.
