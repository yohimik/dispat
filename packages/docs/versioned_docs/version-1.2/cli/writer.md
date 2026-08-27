# The writer command

Run `dispat writer <manifest>...` to apply `--set-version`, `--set`, and `--link` to each named manifest. An edit ends
up applied, skipped, or missing. If you provide a path that no writer covers, dispat always exits `1`.

A skipped edit means the version defers to something outside the file. This is normal and never fails the command. A
missing edit means the manifest does not declare that dependency, which fails only if you pass `--strict`.

The `dispat scanner`, `dispat writer`, and `dispat replacer` commands expose the manifest libraries directly.
`dispat scanner` prints what a folder's manifests declare. `dispat writer` edits a declaration in place while
preserving the file's formatting, and `dispat replacer` replaces literal text in any file at all.

These commands need no config file, no git repository, and no release plan. They work on any checkout. Positional paths
resolve against `--root`.

Pass `--log-format json` to swap each command's listing for one event per file.

Read [Manifest tools](../editing/manifests.md) for the full guide, worked examples, and the format list.

## Flags

These options apply alongside the [global flags](./README.md#global-flags):

| Flag                  | Default     | Effect                                                                                                                                                                                                 |
|-----------------------|-------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `--set-version`       |             | `writer` and `autowriter`: rewrite the manifest's own version field. Pass `{version}` to `autowriter` to write the covered package's planned version, which touches only the package's root manifests. |
| `--set-build`         |             | `writer` only: write the manifest's build counter where its format keeps one, such as `CFBundleVersion`, `android:versionCode`, `CURRENT_PROJECT_VERSION`, Gradle's `versionCode`, a pubspec version's `+` suffix, Unity's `AndroidBundleVersionCode` and every platform entry under `buildNumber`, Godot's `version/code` in every export preset, an Unreal plugin's `Version`, and the Android `StoreVersion`. dispat will not create a counter the file does not declare, except for the pubspec suffix because it is part of the version scalar. Every integer counter refuses a non-integer before the file is opened. |
| `--set`               |             | `writer` and `autowriter`: set one dependency's declared range using `[kind:]name=range`. You can repeat this flag. Pass `{version}` in the range to `autowriter` to use the planned version of the package the edit names. |
| `--link`              |             | `writer` and `autowriter`: point a dependency at a local folder using `name=path`. Pass an empty path to remove the redirect. You can repeat this flag. |
| `--drop-links`        |             | `writer` only: remove every local-link directive the named manifests carry. You do not need to provide the dependencies' names. You cannot combine this with `--link`. |
| `--strict`            |             | Turn a tolerated finding into a failure. For `scanner`, `writer`, and `replacer`, this fails on a manifest that failed to parse, an edit the manifest does not declare, or a `--replace` that matched nothing. |
