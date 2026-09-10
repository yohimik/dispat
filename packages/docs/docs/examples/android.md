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

The criterion is independent of the engine that produced the app: inspect every 64-bit `.so` that reaches the SDK or
final package, including middleware and plugin wrappers. To pass Android's documented ELF check, every `PT_LOAD` segment needs `p_align`
of at least `0x4000`; a passing core library says nothing about the wrappers beside it.

The reviewed
[`v4.8.1` FMOD archive](https://github.com/ValveSoftware/steam-audio/releases/download/v4.8.1/steamaudio_fmod_4.8.1.zip)
demonstrates that boundary. Its Android arm64 `libphonon.so` has three `PT_LOAD` alignments of `0x4000`, while
`libphonon_fmod.so` has three of `0x1000`. The latter fails Android's documented
[16 KB ELF alignment check](https://developer.android.com/guide/practices/page-sizes), as recorded with the exact
archive and program headers in [#548](https://github.com/ValveSoftware/steam-audio/issues/548#issuecomment-5612174680).
The report came through a Unity integration, but the ELF criterion applies to any build that packages the library.

This proves only the alignment of those released arm64 binaries. It does not identify the build setting that caused
the difference or test the final app. ELF segment alignment and APK ZIP alignment are separate checks. Run both, then
exercise the packaged app on a 16 KB device or emulator. A 4 KB-aligned library may enter a platform compatibility
mode, so the static result alone does not establish a universal crash or store rejection.

Put the static checks and packaged consumer test in the configured build scripts so failure prevents publication.
A manifest version update does not inspect or rebuild a precompiled native wrapper.

## A public repository to compare

[Termux at `3b66f87`](https://github.com/termux/termux-app/blob/3b66f8799635a4dba4a206563048ff0e6792c487/app/src/main/AndroidManifest.xml): The app manifest contains neither a literal package version nor a build number for this writer to change. `--set-version` left it byte-for-byte unchanged. Keep the version source in the build configuration; manifest recognition alone does not establish that `versionName` or `versionCode` was updated.

See the [21-ecosystem audit](./open-source.md) for pinned inputs, reproducible checks and their limits.

See [From one package to many](./one-to-many.md) to add deliverables while preserving existing package identities and release history.
