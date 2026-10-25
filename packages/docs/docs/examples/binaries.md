# Cross-platform binaries on a GitHub release

This example compiles a Go CLI for four platforms and attaches the binaries and checksums to a GitHub release. The
build script produces the files and exports their paths for dispat to upload. No separate package registry is needed.

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

There is no publish script here. The GitHub recorder handles the upload from the exported asset list when dispat
writes the release records.

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

The last line appends `DISPAT_EXPORT_GITHUB` to `$DISPAT_OUTPUT`. Its value is a whitespace-separated list of absolute
file paths. dispat uploads each file using its filename as the asset name. `$PWD` is the package folder during the
build, so `"$PWD"/dist/*` supplies absolute paths.

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

Without a token, dispat skips the GitHub release and prints this warning. The other release steps still run:

```console
12:56:06 WRN github releases disabled error="no token found in $GITHUB_TOKEN" package=acmectl
```

Provide a `GITHUB_TOKEN` with `contents: write` in GitHub Actions. Elsewhere, set `github.owner` and `github.repo` and
pass a personal access token through `github.tokenEnv`.

## Naming the version inside the binary

Pass `-X main.version=$DISPAT_NEW_VERSION` to put the release version into the binary itself. This ensures
`acmectl --version` and the tag agree. Every stage receives the same variable.

For other languages, read the variable in a build script or write it into a generated source file. A Rust project can
use `build.rs`; a Node project can generate a version module.

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

## Bound producer queues before release

If the binary contains a producer/worker queue, include a backpressure check in its build tests. Block a worker,
submit more work than the queue should accept, and fail the test unless enqueue waits for capacity. A performance
change can otherwise make the producer outpace the workers.

Run the small queue probe in `build`, then exercise the produced library or binary in a representative consumer before
exporting it. A successful GitHub upload proves that the asset exists. It does not prove that a source fix reached the
asset or that long-running memory remains bounded.

## See also

- Read [Release records](../configuration/records.md#github) for the GitHub recorder's options.
- Read [Script environment variables](../reference/environment.md#script-outputs) for outputs and the export directive.
- Read [dispat in CI](../reference/ci.md) for the job that runs this.
