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

A source-tree Gradle build does not prove that an external Android project can resolve the published AARs. The
[MapLibre and android-maps-utils audit](./gradle.md#audit-android-coordinates-and-aars) checks exact Maven coordinates
and declared publishable modules. Keep a clean consumer check for required ABIs, metadata and transitive dependencies
inside the release gates.

The [`firebase-auth` 24.2.0 case](./gradle.md#compile-against-the-published-pom-and-aar) illustrates this failure: its POM
omits a dependency for Checker Framework annotations referenced by the AAR, and a Firebase maintainer reproduced the
Kotlin consumer failure. dispat does not compare AAR bytecode with Maven metadata, so add a clean compilation against
the exact published coordinate to the native release checks.

Read the project's publication policy before treating an absent coordinate as a failure. For example,
[AndroidX explains that `media3-effect-ndk` is not published to Google Maven](https://github.com/androidx/media/issues/3363).
Set the provider's `isBuildWaitingPublish` only when the consumer really fetches that provider from a registry;
local Gradle project dependencies can build from the checkout.

## Inspect every native wrapper for 16 KB alignment

Check every 64-bit `.so` in the produced SDK or app, not only its core library. Steam Audio
[`v4.8.1`](https://github.com/ValveSoftware/steam-audio/releases/tag/v4.8.1) demonstrates the boundary: the Android
arm64 `libphonon.so` in its FMOD archive has three `PT_LOAD` alignments of `0x4000`, while `libphonon_fmod.so` has
three of `0x1000`. That confirms the wrapper still fails Android's documented
[16 KB ELF alignment check](https://developer.android.com/guide/practices/page-sizes), as reported in
[#548](https://github.com/ValveSoftware/steam-audio/issues/548#issuecomment-5612174680), even though the core passes it.

ELF segment alignment and APK ZIP alignment are separate checks. Run both, then exercise the packaged app on a 16 KB
device or emulator. A 4 KB-aligned library may enter a platform compatibility mode, so the static result alone does
not establish a universal crash or store rejection.

Put the static checks and packaged consumer test in the configured build scripts so failure prevents publication.
A manifest version update does not inspect or rebuild a precompiled native wrapper.
