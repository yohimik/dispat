# An Android app

Releasing an Android app from a monorepo: Gradle builds driven by the computed version, a monotonic `versionCode`, and
a bundle attached to the GitHub release.

Gradle projects fit the same two slots. The version travels through environment variables into Gradle properties, and
the "publish" of an app is whatever your delivery is: a Play upload, an artifact repository, an APK attached to a GitHub
release.

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

Two Gradle-specific notes:

- Scripts run inside the package folder (here `android/<app>`), so a Gradle wrapper at the repository root sits two
  levels up: `../../gradlew`.
- `versionCode` must be a monotonically increasing integer. A simple derivation from the semantic version:
  `-PversionCode=$((MAJOR * 10000 + MINOR * 100 + PATCH))` computed in the script from `$DISPAT_NEW_VERSION`.

To attach the built bundle to the GitHub release instead, export it as an asset from the build script:

```sh
echo "DISPAT_EXPORT_GITHUB=$PWD/app/build/outputs/bundle/release/app-release.aab" >> "$DISPAT_OUTPUT"
```
