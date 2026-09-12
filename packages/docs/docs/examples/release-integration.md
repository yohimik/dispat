# Integrating an existing release pipeline

Start with the artifacts users install, then trace what each build reads. Keep the working build tools and publisher
commands. dispat supplies the release graph, version plan and package completion records.

## Choose the build boundary

For each consumer, identify whether it needs its provider's local build output or its published artifact.

| Input to the consumer | Integration |
|---|---|
| A workspace package or locally built SDK | Keep `isBuildWaitingPublish` false on the provider. The consumer can build after the provider builds. |
| A base image, remote artifact or release binary | Set `isBuildWaitingPublish: true` on the provider package or space when the consumer must fetch it remotely. |
| Infrastructure needed only for deployment | Keep the provider's build-wait flag false. Builds can finish before infrastructure publication. |
| An external CI workflow | Retain its invocation ID, wait for completion and propagate its final status. A dispatch response is not completion. |

The flag belongs to the provider and affects all its consumers. If one artifact needs a different publication boundary,
give it a separate package. See [space options](../configuration/spaces.md#space-options) and
[dependencies](../configuration/dependencies.md).

A consumer using local output does not need a registry hop. A consumer that pulls
`FROM registry.example.com/base:1.2.3` needs that exact image and platform manifest. A mutable `latest` tag does not
provide reproducible identity; retain the planned version or digest.

## Pin a runtime snapshot before downloading

Resolve mutable discovery pointers once, then download every related file from the resulting immutable snapshot.
Retain the snapshot identity and checksum so a retry resumes the same object. Re-reading a pointer for each file can
mix releases even when every request succeeds.

For a distribution package, retain its source descriptor and applied patch series as well as its upstream version.
A version comparison alone cannot establish whether a fix was backported. Test compatibility at the consumer boundary,
including host/plugin combinations and every native wrapper that reaches the final artifact.

## Make success mean available to the next stage

Validate artifact identity, version, digest, install behavior, destination and channel before publication. The publish
command must be the final external action in the publish stage. After it succeeds, return immediately so dispat can
write its native release records. Do not follow an accepted upload with a registry read or channel mutation that can
turn publication into a failed package leg.

Propagate failures from every build, signing and upload helper. Build in clean output directories so stale files cannot
hide a failed command. When a package has several destinations, either make preflight checks cover the whole attempt or
model independently recoverable deliverables as separate packages.

If an upload response is lost, reconcile the exact version, artifact digest and destination outside the publish
command before another workflow run. Never delete and recreate an immutable version as a routine retry. See
[release recovery](../reference/releasing/recovery.md).

## Keep the release tied to its input commit

Serialize releases and retain the planned source revision, artifact digest, workflow invocation and attempt identity.
A later branch tip must not become the identity of an earlier build. A rerun must distinguish work completed by the
current attempt from successful jobs carried over from an earlier one.

Test this by starting from commit A, advancing the branch to commit B while publication is paused, and confirming that
the resulting versions, artifacts, tags and changelog still describe A. Include recovery after publication but before
Git finalization, and verify that completed artifacts stay in place.

This applies to a single package too. Platform assets, release notes, marketplace updates and notifications can have
different retry boundaries. Record those destinations explicitly so recovery does not require republishing an
immutable package version.

## Apply the pattern to your ecosystem

| Ecosystem | What to keep in the integration |
|---|---|
| npm | Workspace builds, package credentials, dist-tags and a clean consumer install. See [npm](./npm.md). |
| Go modules | Module paths and module-specific tags; test outside local `replace` directives. See [Go](./go.md). |
| Cargo | Local workspace builds plus registry-resolvable packaged dependencies. See [Cargo](./rust.md). |
| Python | Both sdist and supported wheels, with an explicit platform and Python matrix. See [Python](./python.md). |
| Composer | Component tags, Packagist visibility and generated API docs as distinct outputs. See [Composer](./php.md). |
| Maven and Gradle | POMs, classifiers, signatures, metadata and a clean consumer. See [Maven](./java.md) and [Gradle](./gradle.md). |
| .NET | Managed packages, native runtime assets and any separate extension publisher. See [.NET](./dotnet.md). |
| Dart and Flutter | Compatible dependency versions, SDK constraints and a fresh consumer solve. See [Flutter](./flutter.md). |
| Apple | Bundle versions, podspec/source consistency, signing and store processing. See [Apple](./apple.md). |
| Android | AAR and native ABI contents, version codes and the intended publication list. See [Android](./android.md). |
| RubyGems | The gem, paired packages, channel metadata and release notes. See [Ruby](./ruby.md). |
| Docker | Registry-backed provider chains, exact image identities and platform manifests. See [Docker](./docker.md). |
| Aqua | Tool pins and compatible asset selectors; registry authoring stays with Aqua. See [Aqua](./aqua.md). |
| Game engines | Native metadata, platform artifacts, engine compatibility and store delivery. See [game development](./game.md). |

Use native manifests where supported. Add an explicit package with scripts and a version policy for other deliverables;
do not add an npm wrapper merely to make a non-JavaScript artifact releasable.

## Test the distributed artifact

Install the packed files or staging-repository artifacts in a clean consumer before publication, and test every
supported runtime and platform that matters. Check the complete artifact inventory: one successful coordinate, wheel,
image architecture or plugin binary does not prove that the publication set is complete. Smoke checks against the
production registry run separately after dispat has written its native records; they must not turn a successful upload
into a failed publish stage.

Keep immutable payloads under an identity tied to the build, then update a mutable discovery pointer only after the
full set is available. Let a real consumer solve validate transitive compatibility; checking only the registry's newest
version answers a different question.

Shared [referenced configuration](../configuration/refs.md) can hold one graph while platform files select shells and
commands. It does not translate Bash into PowerShell or replace Windows testing.

## Prioritize failures that affect consumers

Compare the intended package set with the parsed plan and final records. Test the packaged API from a clean consumer,
where missing metadata and undeclared dependencies cannot be hidden by the development checkout. Verify that delivered
artifacts contain the source correction that passed testing.

Put required checks in build or pre-publish stages so they gate publication. dispat orders these scripts and records
their outcome; it does not provide ecosystem-specific acceptance tests. A harmful published artifact may require a
maintenance release, backport or documented withdrawal rather than an automatic rollback.

## Version documentation and agent instructions too

Give a guide, protocol schema or specification its own package when consumers need a versioned contract. Choose
independent versions or a [version group](../configuration/spaces.md#versioning-groups) according to compatibility.
Inventory the whole published document tree, including referenced files, and distinguish current advice from historical
contracts.

## Limits of the integration

A missing registry entry may be intentional. Confirm the declared publication set before reporting a failed upload.
Registry quotas, credentials, signatures, platform compatibility and store approval remain the responsibility of the
configured tools and checks. dispat can order commands and record successful completion; it cannot repair a registry
outage or validate an artifact that the publisher never tested.

Run recovery exercises before adopting the setup: one failure after a publication and one lost response. Confirm that
the retry preserves completed artifacts and refuses ambiguous or mismatched ones.

## Choose the authoring and document contract

Count external destinations as well as manifests. For Changesets users, the
[npm example](./npm.md#moving-release-intent-out-of-changeset-files) separates per-change inputs from generated
changelogs and release commits. [Skills, specifications and TeX documentation](./document-artifacts.md) need their own
artifact inventory and compatibility policy.
