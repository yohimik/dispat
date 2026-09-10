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

Read the project's publication policy before treating an absent coordinate as a failure. For example,
[AndroidX explains that `media3-effect-ndk` is not published to Google Maven](https://github.com/androidx/media/issues/3363).
Set the provider's `isBuildWaitingPublish` only when the consumer really fetches that provider from a registry;
local Gradle project dependencies can build from the checkout.
