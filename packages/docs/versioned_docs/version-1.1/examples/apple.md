# An iOS app and a CocoaPods library

An app archived and uploaded to App Store Connect, a pod published to trunk, and the four Apple file formats that
carry a version between them.

## The layout

```
pods/core/AcmeCore.podspec              the library, published to CocoaPods trunk
apps/ios/Podfile                        the app's dependencies, including AcmeCore
apps/ios/App/Info.plist                 CFBundleShortVersionString and CFBundleVersion
apps/ios/Acme.xcodeproj/project.pbxproj MARKETING_VERSION and CURRENT_PROJECT_VERSION
dispat.json
```

## The configuration

```json title="dispat.json"
{
  "scripts": {
    "stamp": "dispat writer App/Info.plist Acme.xcodeproj/project.pbxproj --set-version \"$DISPAT_NEW_VERSION\" --set-build \"$GITHUB_RUN_NUMBER\"",
    "archive": "xcodebuild archive -scheme App -archivePath build/App.xcarchive",
    "upload": "xcrun altool --upload-app -f build/App.ipa -u \"$APPLE_ID\" -p \"$APP_PASSWORD\"",
    "lint": "pod lib lint",
    "trunk": "pod trunk push AcmeCore.podspec"
  },
  "spaces": {
    "pods": {
      "path": "pods",
      "flow": {"build": "lint", "publish": "trunk"},
      "autoVersion": {"enabled": true, "range": "~> {version}"}
    },
    "apps": {
      "path": "apps",
      "flow": {"beforeBuild": "stamp", "build": "archive", "publish": "upload"},
      "autoVersion": {"enabled": true, "manifests": "all", "range": "~> {version}"}
    }
  },
  "packages": {
    "core": {"manifestNames": ["AcmeCore"]}
  }
}
```

**`range: "~> {version}"`** because CocoaPods writes optimistic requirements the Ruby way, not the npm way.

**`manifestNames`** connects the pod name `AcmeCore` to the folder called `core`, so a `pod 'AcmeCore'` line anywhere
is recognised as this package.

**The `stamp` script** exists because of where Xcode keeps versions. `autoVersion` writes a package's own version into
the manifests directly in its folder, and an iOS project keeps `Info.plist` and `project.pbxproj` one level deeper, in
`App/` and `Acme.xcodeproj/`. One `dispat writer` call in `beforeBuild` writes both, along with the build number.

## A release

```console
$ git commit -m "feat(core)^: background refresh"
$ dispat
12:49:43 INF release started root=.
12:49:43 INF ● changed baselineFromInitials=true bump=minor channel=stable dueToProviders=[] ownCommits=1 package=core reason=direct space=pods version="1.2.0 -> 1.3.0"
12:49:43 INF ● changed baselineFromInitials=true bump=patch channel=stable dependsOn=["core"] dueToProviders=["core"] ownCommits=0 package=ios reason="propagated from core" space=apps version="0.4.1 -> 0.4.2"
12:49:43 INF release plan ready held=0 packages=2 releasing=2
12:49:43 INF manifest reconciled manifest=AcmeCore.podspec package=core ranges=0 stage=version version=1.3.0 versionWritten=true
12:49:43 INF version succeeded package=core stage=version version=1.3.0
12:49:43 INF build started package=core stage=build version=1.3.0
12:49:43 INF build succeeded package=core stage=build version=1.3.0
12:49:43 INF publish started package=core stage=publish version=1.3.0
12:49:43 INF manifest reconciled manifest=Podfile package=ios ranges=1 stage=version version=0.4.2 versionWritten=false
12:49:43 INF version succeeded package=ios stage=version version=0.4.2
12:49:43 INF App/Info.plist package=ios stage=build version=0.4.2
12:49:43 INF   version written package=ios stage=build version=0.4.2
12:49:43 INF   build number written package=ios stage=build version=0.4.2
12:49:43 INF Acme.xcodeproj/project.pbxproj package=ios stage=build version=0.4.2
12:49:43 INF   version written package=ios stage=build version=0.4.2
12:49:43 INF   build number written package=ios stage=build version=0.4.2
12:49:43 INF 2 manifest(s): 0 applied, 0 skipped, 0 missing package=ios stage=build version=0.4.2
12:49:43 INF build started package=ios stage=build version=0.4.2
12:49:43 INF published package=core stage=publish tag=core@1.3.0 version=1.3.0
12:49:43 INF build succeeded package=ios stage=build version=0.4.2
12:49:43 INF publish started package=ios stage=publish version=0.4.2
12:49:43 INF published package=ios stage=publish tag=ios@0.4.2 version=0.4.2
12:49:43 INF done cancelled=0 failed=0 held=0 published=2 skipped=0 took=0.6s unchanged=0
```

The pod publishes first, the app's `Podfile` is reconciled to `~> 1.3.0`, and both project files end up carrying
`0.4.2` with `CFBundleVersion` and `CURRENT_PROJECT_VERSION` set to the run number:

```
<key>CFBundleShortVersionString</key>
<string>0.4.2</string>
<key>CFBundleVersion</key>
<string>87</string>
```

## What dispat reads and writes

```console
$ dispat scanner
apps/ios/Acme.xcodeproj/project.pbxproj  xcode  com.acme.app@0.4.1  build 41
apps/ios/App/Info.plist  plist  com.acme.app@0.4.1  build 41
apps/ios/Podfile  cocoapods
  dependencies  AcmeCore   ~> 1.2.0
  dependencies  Alamofire  ~> 5.9
pods/core/AcmeCore.podspec  cocoapods  AcmeCore@1.2.0
  dependencies  Alamofire  ~> 5.9
4 manifest(s), 3 dependency declaration(s)
```

| File | Identity | Version | Build counter | Dependencies |
|------|----------|---------|---------------|--------------|
| `Info.plist` | `CFBundleIdentifier` | `CFBundleShortVersionString` | `CFBundleVersion` | none |
| `project.pbxproj` | `PRODUCT_BUNDLE_IDENTIFIER` | `MARKETING_VERSION` | `CURRENT_PROJECT_VERSION` | none |
| `Podfile` | none | none | none | every `pod` line |
| `*.podspec` | `name` | `version` | none | every `s.dependency` |

Writes to `project.pbxproj` reach every build configuration, so Debug and Release do not drift apart. A value that
defers to something else is skipped rather than flattened: `$(MARKETING_VERSION)` in a plist points at the build
setting, and a podspec saying `s.version = Acme::VERSION` points at a Ruby constant. Point an
[`autoVersion.replace`](../configuration/autoversion.md) rule at the file that really holds the number in those cases.

A pod inside a test target is reported as a dev dependency. Pods pinned by `:git` or `:path` name a place rather than
a version and are never rewritten.

## Worth knowing

- **`CFBundleVersion` must increase for every upload**, even when the marketing version does not change. That is the
  whole reason it is separate, and why the stamp script uses the CI run number rather than anything derived from the
  release.
- **A version write never moves a build counter**, and `--set-build` never moves a version. Two numbers, two writes,
  no surprises.
- **`pod trunk push` needs a session**, which belongs in [`flow.login`](./login.md) so it happens once per space.
- **App Store Connect is asynchronous.** The upload succeeding is not the release being live; the tag records what you
  shipped, and review is downstream of it.
- **Swift Package Manager needs no manifest support here.** `Package.swift` resolves versions from git tags, so the
  tag dispat writes is the whole story, exactly as with [Go](./go.md).

## See also

- [A Flutter app and its packages](./flutter.md) if the app is built from Dart.
- [An Android app](./android.md) for the same shape on the other platform.
- [Manifest tools](../editing/manifests.md#writing-the-build-counter) for `--set-build` on its own.
