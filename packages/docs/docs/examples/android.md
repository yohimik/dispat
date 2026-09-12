# An Android app

You can release an Android app from a monorepo using Gradle builds driven by the computed version. This setup requires
a monotonic `versionCode` and attaches a bundle to the GitHub release.

Gradle projects fit the same build and publish slots as every other ecosystem. Pass the version through environment
variables into your Gradle properties. The publish step for an app is whatever your delivery requires, like a Play
upload, an artifact repository, or an APK attached to a GitHub release.

```json
{
  "scripts": {
    "build": "../../gradlew -p . assembleRelease -PversionName=$DISPAT_NEW_VERSION",
    "publish": "../../gradlew -p . publishReleaseBundle -PversionName=$DISPAT_NEW_VERSION"
  },
  "spaces": {
    "apps": {
      "path": "android",
      "flow": {
        "build": "build",
        "publish": "publish"
      }
    }
  }
}
```

Keep two Gradle-specific details in mind:

- Scripts run inside the package folder, which is `android/<app>` in this example. Call the Gradle wrapper at the
  repository root by going two levels up with `../../gradlew`.
- The `versionCode` must be a monotonically increasing integer. Derive this directly from the semantic version in your
  script. Compute `-PversionCode=$((MAJOR * 10000 + MINOR * 100 + PATCH))` using `$DISPAT_NEW_VERSION`.

Attach the built bundle to the GitHub release by exporting it as an asset from the build script:

```sh
echo "DISPAT_EXPORT_GITHUB=$PWD/app/build/outputs/bundle/release/app-release.aab" >> "$DISPAT_OUTPUT"
```

## Check the published library set

A source-tree Gradle build does not prove that an external Android project can resolve the published AARs. Check exact
Maven coordinates and the declared set of publishable modules. Keep a clean consumer check for required ABIs, metadata
and transitive dependencies inside the release gates, using packed artifacts or a staging repository before publication.
dispat does not compare AAR bytecode with Maven metadata. Check production coordinates separately after native release
records exist; see [artifact testing](./release-integration.md#test-the-distributed-artifact).

Read the project's publication policy before treating an absent coordinate as a failure. Set the provider's
`isBuildWaitingPublish` only when the consumer really fetches that provider from a registry; local Gradle project
dependencies can build from the checkout.

## Inspect every native wrapper for 16 KB alignment

The criterion is independent of the engine that produced the app: inspect every 64-bit `.so` that reaches the SDK or
final package, including middleware and plugin wrappers. To pass Android's documented ELF check, every `PT_LOAD` segment needs `p_align`
of at least `0x4000`; a passing core library says nothing about the wrappers beside it.

Use Android's documented [16 KB page-size guidance](https://developer.android.com/guide/practices/page-sizes) for the
exact checks. ELF segment alignment and APK ZIP alignment are separate. Run both, then
exercise the packaged app on a 16 KB device or emulator. A 4 KB-aligned library may enter a platform compatibility
mode, so the static result alone does not establish a universal crash or store rejection.

Put the static checks and packaged consumer test in the configured build scripts so failure prevents publication.
A manifest version update does not inspect or rebuild a precompiled native wrapper.

See [From one package to many](./one-to-many.md) to add deliverables while preserving existing package identities and release history.
