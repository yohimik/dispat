# Cross-platform binaries on a GitHub release

Compile one CLI for four platforms and attach the results, with checksums, to the GitHub release the run creates.

You do not need a registry in this setup. The GitHub release *is* the distribution, so the build stage produces files
and tells dispat which ones to upload.

## The layout

```
cmd/acmectl/go.mod      the CLI
cmd/acmectl/build.sh    builds every target, then exports the asset list
dispat.json
```

## The configuration

```json title="dispat.json"
{
  "scripts": {"build": "./build.sh"},
  "packages": {
    "acmectl": {
      "path": "cmd/acmectl",
      "flow": {"build": "build"}
    }
  }
}
```

Leave out the publish script. You are not uploading to a third party, so the package's whole delivery is its tag and
its GitHub release. dispat writes both of these at the end of the run.

## The build script

```sh title="cmd/acmectl/build.sh"
#!/bin/sh
set -eu

rm -rf dist && mkdir -p dist
for target in linux/amd64 linux/arm64 darwin/arm64 windows/amd64; do
  os=${target%/*}; arch=${target#*/}
  ext=""; [ "$os" = "windows" ] && ext=".exe"
  name="acmectl_${DISPAT_NEW_VERSION}_${os}_${arch}${ext}"
  echo "building $name"
  GOOS="$os" GOARCH="$arch" go build -ldflags "-s -w -X main.version=$DISPAT_NEW_VERSION" -o "dist/$name" .
done

( cd dist && sha256sum * > SHA256SUMS )

echo "DISPAT_EXPORT_GITHUB=$(ls -d "$PWD"/dist/* | tr '\n' ' ')" >> "$DISPAT_OUTPUT"
```

The last line contains the whole integration. Append to the `$DISPAT_OUTPUT` file to set variables. Set
`DISPAT_EXPORT_GITHUB` to a whitespace-separated list of absolute paths.

This is the output name dispat treats as a directive. It uploads each path as an asset and names it after the file. Use
`$PWD` to get the package folder inside a stage, which makes `"$PWD"/dist/*` absolute without any bookkeeping.

The export acts as the opt-in. Do not export this variable if you want to skip creating a GitHub release. In a
monorepo, only the packages that produce artifacts create a release.

## A release

```console
$ git commit -m "feat(acmectl): add the --json flag"
$ dispat
12:56:24 INF release started root=.
12:56:24 INF ● changed baselineFromInitials=true bump=minor channel=stable dueToProviders=[] ownCommits=1 package=acmectl reason=direct space=acmectl version="1.4.0 -> 1.5.0"
12:56:24 INF release plan ready held=0 packages=1 releasing=1
12:56:24 INF build started package=acmectl stage=build version=1.5.0
12:56:25 INF building acmectl_1.5.0_linux_amd64 package=acmectl stage=build version=1.5.0
12:56:25 INF building acmectl_1.5.0_linux_arm64 package=acmectl stage=build version=1.5.0
12:56:25 INF building acmectl_1.5.0_darwin_arm64 package=acmectl stage=build version=1.5.0
12:56:25 INF building acmectl_1.5.0_windows_amd64.exe package=acmectl stage=build version=1.5.0
12:56:25 INF build succeeded package=acmectl stage=build version=1.5.0
12:56:25 INF published package=acmectl stage=publish tag=acmectl@1.5.0 version=1.5.0
12:56:25 INF summary channel=stable package=acmectl status=published tag=acmectl@1.5.0 took=0.5s version="1.4.0 -> 1.5.0"
12:56:25 INF done cancelled=0 failed=0 held=0 published=1 skipped=0 took=0.5s unchanged=0
```

The release named `acmectl@1.5.0` carries five assets. These are the four binaries and `SHA256SUMS`. The release body
contains the changelog sections.

Run without a token and dispat prints a warning, but everything else still happens:

```console
12:56:06 WRN github releases disabled error="no token found in $GITHUB_TOKEN" package=acmectl
```

Provide a `GITHUB_TOKEN` with `contents: write` in GitHub Actions. Elsewhere, set `github.owner` and `github.repo` and
pass a personal access token through `github.tokenEnv`.

## Naming the version inside the binary

Pass `-X main.version=$DISPAT_NEW_VERSION` to put the release version into the binary itself. This ensures
`acmectl --version` and the tag agree. Every stage receives the same variable.

A Rust build reads it in `build.rs`, and a Node build writes it into a generated file. You do not need a separate step
to bump anything.

## Building on more than one operating system

Cross-compiling covers most CLIs. Sometimes it does not work because of cgo or code signing, so the release job cannot
produce every asset by itself. Use one of these two approaches:

- **Build the artifacts in earlier CI jobs** on their own runners. Download them into the release job before dispat
  runs. The build script then only collects files and exports the list.
- **Attach the extra assets afterwards** with `gh release upload "$DISPAT_TAG" ...` in a
  [`postPublish` hook](../configuration/spaces.md). This runs once the release exists.

## Worth knowing

- **Assets are named after the files.** Put the version in the file name. Downloads stay unambiguous long after the
  release page scrolls away.
- **An invalid entry is skipped, not fatal.** Provide a relative path, a missing file, or a directory and dispat
  reports a warning. The release still goes out with its good assets.
- **`allPackages: true` inverts the opt-in**. Set this to create a release for every published package. The export then
  only adds assets, which helps when the release page is your changelog for everything.
- **The release body is the changelog entry.** It comes from the same commits. You do not write anything twice.

## See also

- Read [Release records](../configuration/records.md#github) for the GitHub recorder's options.
- Read [Script environment variables](../reference/environment.md#script-outputs) for outputs and the export directive.
- Read [dispat in CI](../reference/ci.md) for the job that runs this.
