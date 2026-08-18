# Cross-platform binaries on a GitHub release

Compiling one CLI for four platforms and attaching the results, with checksums, to the GitHub release the run creates.

There is no registry in this setup. The GitHub release *is* the distribution, so the build stage produces files and
tells dispat which of them to upload.

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

No publish script. Nothing is uploaded to a third party, so the package's whole delivery is its tag and its GitHub
release, both of which dispat writes at the end of the run.

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

The last line is the whole integration. `$DISPAT_OUTPUT` is a file the script appends to, and
`DISPAT_EXPORT_GITHUB` is the one output name dispat treats as a directive: a whitespace-separated list of absolute
paths, each uploaded as an asset and named after the file. `$PWD` inside a stage is the package folder, which is what
makes `"$PWD"/dist/*` absolute without any bookkeeping.

The export is also the opt-in. A package whose scripts never export it gets no GitHub release, so in a monorepo only
the packages that produce artifacts create one.

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

The release named `acmectl@1.5.0` then carries five assets, the four binaries and `SHA256SUMS`, with the changelog
sections as its body.

If the token is missing, the run says so plainly and everything else still happens:

```console
12:56:06 WRN github releases disabled error="no token found in $GITHUB_TOKEN" package=acmectl
```

In GitHub Actions, `GITHUB_TOKEN` with `contents: write` is enough; elsewhere, set `github.owner` and `github.repo`
and hand it a personal access token through `github.tokenEnv`.

## Naming the version inside the binary

`-X main.version=$DISPAT_NEW_VERSION` puts the release version into the binary itself, so `acmectl --version` and the
tag agree. Every stage receives the same variable, so a Rust build reads it in `build.rs`, and a Node build writes it
into a generated file. There is no separate step that has to remember to bump anything.

## Building on more than one operating system

Cross-compiling covers most CLIs. When it does not, because of cgo or code signing, the release job cannot produce
every asset by itself. Two workable shapes:

- **Build the artifacts in earlier CI jobs**, on their own runners, and download them into the release job before
  dispat runs. The build script then only collects files and exports the list.
- **Attach the extra assets afterwards** with `gh release upload "$DISPAT_TAG" ...` in a
  [`postPublish` hook](../configuration/spaces.md), which runs once the release exists.

## Worth knowing

- **Assets are named after the files.** Put the version in the file name and downloads stay unambiguous long after
  the release page has scrolled away.
- **An invalid entry is skipped, not fatal.** A relative path, a missing file or a directory is reported as a warning
  and the release with its good assets still goes out.
- **`allPackages: true` inverts the opt-in**, creating a release for every published package and letting the export
  only add assets. Useful when the release page is your changelog for everything.
- **The release body is the changelog entry.** It comes from the same commits, so there is nothing to write twice.

## See also

- [Release records](../configuration/records.md#github) for the GitHub recorder's options.
- [Script environment variables](../reference/environment.md#script-outputs) for outputs and the export directive.
- [dispat in CI](../reference/ci.md) for the job that runs this.
