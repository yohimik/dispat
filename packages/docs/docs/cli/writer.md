# The writer command

`dispat writer <manifest>...` applies `--set-version`, `--set` and `--link` to each named manifest. Every edit ends
as applied, skipped (a version deferring to something outside the file, which is normal and never fails the command)
or missing (a dependency the manifest does not declare, which fails only under `--strict`). A path no writer covers
always exits `1`.

`dispat scanner`, `dispat writer` and `dispat replacer` expose the manifest libraries directly: the first prints what a
folder's manifests declare, the second edits a declaration in place while preserving the file's formatting, and the
third replaces literal text in any file at all. All three need no config file, no git repository and no release plan,
so they work on any checkout. Positional paths resolve against `--root`, and `--log-format json` swaps each command's
listing for one event per file.

The full guide, with worked examples and the format list, is [Manifest tools](../editing/manifests.md).

## Flags

Beside the [global flags](./README.md#global-flags):

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--set-version`       |             | `writer` and `autowriter`: rewrite the manifest's own version field. For `autowriter`, `{version}` writes the covered package's planned version, and only its root manifests are touched. |
| `--set-build`         |             | `writer` only: write the manifest's build counter where its format keeps one: `CFBundleVersion`, `android:versionCode`, `CURRENT_PROJECT_VERSION`, Gradle's `versionCode`, a pubspec version's `+` suffix, Unity's `AndroidBundleVersionCode` and every platform entry under `buildNumber`, Godot's `version/code` in every export preset, an Unreal plugin's `Version` and the Android `StoreVersion`. A counter the file does not declare is not created (the pubspec suffix, part of the version scalar, is the one exception); every integer counter refuses a non-integer before the file is opened. |
| `--set`               |             | `writer` and `autowriter`: set one dependency's declared range, `[kind:]name=range`; repeatable. For `autowriter`, `{version}` in the range is the planned version of the package the edit names. |
| `--link`              |             | `writer` and `autowriter`: point a dependency at a local folder, `name=path`; an empty path removes the redirect. Repeatable. |
| `--drop-links`        |             | `writer` only: remove every local-link directive the named manifests carry, without being told the dependencies' names. Cannot be combined with `--link`. |
| `--strict`            |             | Turns a tolerated finding into a failure. `scanner`, `writer` and `replacer`: a manifest that failed to parse, an edit the manifest does not declare, or a `--replace` that matched nothing. |
