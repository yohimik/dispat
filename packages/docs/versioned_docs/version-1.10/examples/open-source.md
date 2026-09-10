# Open source integration checks

Start from the files your repository actually uses. A Python monorepo can use Pants and pip; a JavaScript
monorepo can use Yarn while still declaring `package.json`. dispat coordinates release versions and commands;
it does not require replacing the build system.

The audit below covers all **21 scanner ecosystem identifiers**, across **36 supported manifest formats**.
It checks one pinned public manifest per ecosystem, rather than every supported format or every package in
those repositories. Apple bundles, CocoaPods and Xcode count separately, as do Android and Gradle. pnpm and
Yarn share the npm identifier. Helm and Terraform are command/replace integrations, not additional native formats.

## What ran locally

On 10 September 2026, dispat **1.10.0** on macOS arm64 scanned each downloaded manifest, attempted the specified
edit, scanned the result, and repeated the edit to check idempotence. The checks assert original and resulting
SHA-256 hashes, exact dependency readback, and expected skips. Missing and indirect versions are tested outcomes,
not successful rewrites. All 21 cases passed. Test versions such as `9.8.7` belong only to disposable copies;
they are not proposed upstream releases.

These projects are integration examples, not claims that their maintainers use dispat. No upstream project was
published. The manifest sweep did not build the full projects, resolve their dependency graphs, sign artifacts,
or exercise their CI credentials. Additional Python packaging checks are described in
[Python without uv and with Pants](./python-pants.md).

Reproduce the manifest sweep from a dispat checkout with Python 3 and dispat on PATH:

```sh
python3 packages/docs/verification/ecosystems/verify.py --output /tmp/dispat-ecosystems.json
```

The [case definitions](https://github.com/yohimik/dispat/blob/main/packages/docs/verification/ecosystems/cases.json)
pin source revisions, content hashes, edit arguments, and expected results. The
[verification runner](https://github.com/yohimik/dispat/blob/main/packages/docs/verification/ecosystems/verify.py)
fetches only those public files and runs scanner/writer commands in temporary folders. It never runs upstream scripts.

## Coverage and integration decisions

| Ecosystem | Pinned public input | Locally checked result and integration decision |
| --- | --- | --- |
| [`npm`](./npm.md) | [JupyterLab: `packages/services/package.json`](https://github.com/jupyterlab/jupyterlab/blob/bf8d11fe21ed78f1f4c4a23851f3e538c22731bb/packages/services/package.json) | The services package has a literal version and internal `@jupyterlab/*` ranges. The local edit changed its version and preserved every dependency. Keep the repository’s Yarn build and publication tooling; selecting an npm-format manifest does not require switching package managers. |
| [`gomod`](./go.md) | [Cobra: `go.mod`](https://github.com/spf13/cobra/blob/adbc8813901bba65827259daa8e22ff94ec1f30e/go.mod) | The module has no package version field. The local check rewrote the `pflag` requirement and read it back. Release identity still comes from Go-compatible Git tags; keep module tidying and consumer checks in the existing workflow. |
| [`cargo`](./rust.md) | [Serde: `serde/Cargo.toml`](https://github.com/serde-rs/serde/blob/a874a1b1bb1cc16cf5ee3b1b7b527af5705742bb/serde/Cargo.toml) | The crate declares both a version requirement and a local path for `serde_core`. The version edit preserved both dependency declarations. Preserve Cargo workspace policy and validate the packaged crate: a working path dependency alone does not establish that its registry version is available. |
| [`python`](./python.md) | [PyPA sampleproject: `pyproject.toml`](https://github.com/pypa/sampleproject/blob/621e4974ca25ce531773def586ba3ed8e736b3fc/pyproject.toml) | The project uses a standard setuptools backend in `pyproject.toml`. The literal version rewrite succeeded. A separate local run built its wheel and sdist with PyPA build, passed Twine’s strict metadata check, installed the wheel, and exercised `sample.simple.add_one` without uv. |
| [`composer`](./php.md) | [Symfony HttpFoundation: `composer.json`](https://github.com/symfony/http-foundation/blob/3d554bb228167df47c29dd55dba5c3d50a324ec7/composer.json) | The manifest has no package version field. Rewriting `symfony/polyfill-mbstring` worked without adding one. Retain tag-based package versions and the existing Composer release process; absence of `version` is not an integration failure. |
| [`maven`](./java.md) | [Apache Commons Lang: `pom.xml`](https://github.com/apache/commons-lang/blob/62620f371b9c6ed337854e066d8916e56942d9af/pom.xml) | The project version is literal, while several dependency versions are `${...}` properties. The local check rewrote the project version and preserved those properties. Keep the parent/property version source and Maven’s existing release checks; scanning this POM does not evaluate an effective POM. |
| [`nuget`](./dotnet.md) | [Newtonsoft.Json: `Src/Newtonsoft.Json/Newtonsoft.Json.csproj`](https://github.com/JamesNK/Newtonsoft.Json/blob/09bb545d72969ad7fb4ea07db0d5c34f4fc07877/Src/Newtonsoft.Json/Newtonsoft.Json.csproj) | The project uses `VersionPrefix` and `VersionSuffix`, rather than `<Version>`, and a `$(MicrosoftSourceLinkGitHubPackageVersion)` dependency. The local writer left the file byte-for-byte unchanged, even with `--strict`: the dependency edit was reported as skipped. Keep its build-time version injection or add a targeted version script; do not infer a successful version update from exit zero. |
| [`pub`](./flutter.md) | [Flutter path_provider: `packages/path_provider/path_provider/pubspec.yaml`](https://github.com/flutter/packages/blob/8a35b1611d677ac0cbbaf26f058a5ac12afa550c/packages/path_provider/path_provider/pubspec.yaml) | The federated plugin declares separate Android, Foundation, Linux and Windows packages. Its literal version rewrite preserved those dependency ranges. Map the components you actually release into the graph and retain Flutter’s platform tests; the manifest check did not run any device or emulator. |
| [`plist`](./apple.md) | [Alamofire Info.plist: `Source/Info.plist`](https://github.com/Alamofire/Alamofire/blob/bda9ed57d72988a3a2ada33d824583541f86eac6/Source/Info.plist) | `CFBundleShortVersionString` points to `$(MARKETING_VERSION)` and the build number points to `$(CURRENT_PROJECT_VERSION)`. A version write left this plist byte-for-byte unchanged. Update the owning Xcode settings and validate the built bundle instead of replacing the substitutions. |
| [`cocoapods`](./apple.md) | [Alamofire podspec: `Alamofire.podspec`](https://github.com/Alamofire/Alamofire/blob/bda9ed57d72988a3a2ada33d824583541f86eac6/Alamofire.podspec) | The podspec’s literal version rewrote successfully. This is independent of its plist and Xcode settings: keep all published metadata consistent, and retain pod validation and publication in the existing Apple workflow. |
| [`xcode`](./apple.md) | [Alamofire Xcode project: `Alamofire.xcodeproj/project.pbxproj`](https://github.com/Alamofire/Alamofire/blob/bda9ed57d72988a3a2ada33d824583541f86eac6/Alamofire.xcodeproj/project.pbxproj) | The local writer updated the project’s literal marketing versions and read the new version back. This check did not build or sign any Apple artifact. Preserve target-specific settings and inspect the final archive before publication. |
| [`android`](./android.md) | [Termux: `app/src/main/AndroidManifest.xml`](https://github.com/termux/termux-app/blob/3b66f8799635a4dba4a206563048ff0e6792c487/app/src/main/AndroidManifest.xml) | The app manifest contains neither a literal package version nor a build number for this writer to change. `--set-version` left it byte-for-byte unchanged. Keep the version source in the build configuration; manifest recognition alone does not establish that `versionName` or `versionCode` was updated. |
| [`gradle`](./gradle.md) | [OkHttp: `gradle/libs.versions.toml`](https://github.com/square/okhttp/blob/1f04bf8028b0fd9471ba9a77eba0ad913f86705a/gradle/libs.versions.toml) | The version catalog maps library coordinates through version references. A local edit of `androidx.activity:activity-ktx` changed the referenced version and read it back without flattening the catalog. Keep Gradle’s dependency resolution and wrapper; the scanner does not execute build logic. |
| [`rubygems`](./ruby.md) | [Rails Active Support: `activesupport/activesupport.gemspec`](https://github.com/rails/rails/blob/9380b46cd448fa3dc6a0ab050c82bfedbfda0d88/activesupport/activesupport.gemspec) | The gemspec computes its version in Ruby. The local writer changed a literal `connection_pool` requirement while leaving the computed version alone. Keep Rails’ version-generation mechanism or explicitly edit its source; dispat does not execute Ruby to resolve the gem version. |
| [`docker`](./docker.md) | [Docker awesome-compose: `nginx-flask-mysql/compose.yaml`](https://github.com/docker/awesome-compose/blob/30f4b7f6a6c3b0c0ecf4d4efb0de203c48d11562/nginx-flask-mysql/compose.yaml) | The nginx/flask/mysql Compose sample is deployment wiring. The scanner selected the literal `mariadb:10-focal` image as its identity, and a package-version write changed that third-party image tag. Do not enable automatic own-version rewriting on an arbitrary deployment Compose file. Inspect the detected identity first and use explicit dependency edits or a dedicated manifest for the image you publish. |
| [`aqua`](./aqua.md) | [Aqua minisign checks: `pkg/minisign/aqua.yaml`](https://github.com/aquaproj/aqua/blob/759a4a4a564922351cb2912ed17519adfb1c2860/pkg/minisign/aqua.yaml) | The configuration contains two pins for `jedisct1/minisign`, one inline and one in a separate version field. The local dependency edit updated both pins. The standard-registry revision and checksum policy stayed unchanged; installation and checksum regeneration remain Aqua’s job. |
| [`unity`](./unity.md) | [Unity Entity Component System samples: `EntitiesSamples/Packages/manifest.json`](https://github.com/Unity-Technologies/EntityComponentSystemSamples/blob/6786a741ee1f118ed14cecfa02beae8e926937b0/EntitiesSamples/Packages/manifest.json) | The package list mixes engine modules and editor packages. The local check rewrote `com.unity.2d.sprite` and preserved all other declarations. This file has no application version: keep that in ProjectSettings, and retain the project’s editor, export and licensing setup. |
| [`godot`](./godot.md) | [Godot Platformer 2D demo: `2d/platformer/project.godot`](https://github.com/godotengine/godot-demo-projects/blob/a3b5c113112f77291d5f3d1360f33a882fdc52f7/2d/platformer/project.godot) | The project declares a name but no `config/version`. A version write left it byte-for-byte unchanged. Set that field deliberately before adopting manifest versioning, or retain a tag-based version source; dispat does not invent the field. |
| [`unreal`](./unreal.md) | [SocketIOClient Unreal plugin: `SocketIOClient.uplugin`](https://github.com/getnamo/SocketIOClient-Unreal/blob/f3e63dcf578095f897c977d991c3fbb9aa501b94/SocketIOClient.uplugin) | The plugin declares a literal `VersionName` and a separate integer `Version`. The local check changed `VersionName` while preserving the integer. Decide the build-counter policy separately, and retain Unreal’s native plugin packaging and target validation. |
| [`defold`](./defold.md) | [Monarch: `game.project`](https://github.com/britzl/monarch/blob/954097522e7c2d4a710464dd9e8b48fb309685ab/game.project) | The project’s literal version rewrote successfully. Its `dependencies#0` archive URL remained unchanged and was not returned as a dependency edge. Declare release dependencies explicitly; updating a library archive URL requires a targeted replacement or script. |
| [`o3de`](./o3de.md) | [O3DE Atom: `Gems/Atom/gem.json`](https://github.com/o3de/o3de/blob/80f47141642496748de7313c6c475a0b564ea060/Gems/Atom/gem.json) | The gem declares a version and an explicit gem-dependency list. The local check updated its version and changed `AtomShader` to `AtomShader==0.2.0`, preserving the other entries. This verifies manifest handling; CMake configuration, engine compatibility and artifact packaging remain native build checks. |

## Apply a result to your repository

First inspect the declared identity, literal versions, indirections and missing fields. Compare that with the
artifact’s actual metadata. Declare the dispat packages and any edges that manifests cannot supply. Keep the
existing build, test and publish commands, with one package-specific artifact inventory per publication record.

Run `dispat status` against a disposable checkout with representative commits. Exercise version reconciliation
there and inspect the diff. Then run the native build and install the exact resulting artifact in a clean consumer.
Only after those checks should the existing CI release job run dispat. A manifest check alone proves neither
build compatibility nor publication recovery. See [release integration](./release-integration.md).
