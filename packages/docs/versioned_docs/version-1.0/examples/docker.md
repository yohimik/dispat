# A Docker image chain

Docker monorepo versioning with dispat: images that depend on images, released in an order where a consumer's build
waits for its base image to be published, and `FROM` lines that follow the released versions.

Docker is the case that breaks "build everything, then publish everything", because an image that starts `FROM` your
base image can only be *built* after the base image is *pushed* to the registry. The per-space `isBuildWaitingPublish`
flag states exactly that.

```json
{
  "scripts": {
    "build": "docker build -t registry.example.com/$DISPAT_PACKAGE:$DISPAT_NEW_VERSION .",
    "publish": "docker push registry.example.com/$DISPAT_PACKAGE:$DISPAT_NEW_VERSION"
  },
  "spaces": {
    "images": {
      "path": "images",
      "isBuildWaitingPublish": true,
      "flow": {
        "build": "build",
        "publish": "publish"
      }
    }
  },
  "dependencies": {
    "app": ["base"]
  }
}
```

A change to the base image, with a caret so it reaches the images built on top of it:

```console
$ git commit -m "feat(base)^: harden the base image"
$ dispat
12:04:05 INF ● changed bump=minor channel=stable dueToProviders=[] ownCommits=1 package=base reason=direct space=images version="0.0.0 -> 0.1.0"
12:04:05 INF ● changed bump=patch channel=stable dependsOn=["base"] dueToProviders=["base"] ownCommits=0 package=app reason="propagated from base" space=images version="0.0.0 -> 0.0.1"
12:04:05 INF release plan ready held=0 packages=2 releasing=2
12:04:05 INF build started package=base stage=build version=0.1.0
12:04:05 INF docker build -t registry.example.com/base:0.1.0 . package=base stage=build version=0.1.0
12:04:05 INF build succeeded package=base stage=build version=0.1.0
12:04:05 INF publish started package=base stage=publish version=0.1.0
12:04:05 INF docker push registry.example.com/base:0.1.0 package=base stage=publish version=0.1.0
12:04:05 INF published package=base stage=publish tag=base@0.1.0 version=0.1.0
12:04:05 INF version succeeded package=app stage=version version=0.0.1
12:04:05 INF build started package=app stage=build version=0.0.1
12:04:05 INF docker build -t registry.example.com/app:0.0.1 . package=app stage=build version=0.0.1
12:04:05 INF build succeeded package=app stage=build version=0.0.1
12:04:05 INF publish started package=app stage=publish version=0.0.1
12:04:05 INF docker push registry.example.com/app:0.0.1 package=app stage=publish version=0.0.1
12:04:05 INF published package=app stage=publish tag=app@0.0.1 version=0.0.1
12:04:05 INF summary channel=stable package=base status=published tag=base@0.1.0 took=1.2s version="0.0.0 -> 0.1.0"
12:04:05 INF summary channel=stable package=app status=published tag=app@0.0.1 took=1.2s version="0.0.0 -> 0.0.1"
12:04:05 INF done cancelled=0 failed=0 held=0 published=2 skipped=0 took=1.2s unchanged=0
```

Read the order: `app`'s build starts only after `docker push` of `base` finished, because the flag told the scheduler
that this space's consumers need their providers *published*, not merely built. Without the flag,
`app`'s build would have run in parallel with `base`'s push (that is the right setting for npm, where a consumer builds
against the local workspace).

That leaves the base image's version in `app`'s Dockerfile. There are two ways to keep it current, and the first is
usually the one you want.

**Let dispat write the tag.** dispat reads Dockerfiles, so the `FROM` line is a declared dependency like any other and
an `autoVersion` block reconciles it at the version stage:

```json
{
  "spaces": {
    "images": {
      "path": "images",
      "isBuildWaitingPublish": true,
      "autoVersion": {},
      "flow": { "build": "build", "publish": "publish" }
    }
  }
}
```

```dockerfile
FROM registry.example.com/base:0.1.0
```

After the run above, that line reads `registry.example.com/base:0.2.0` and every other byte of the file is untouched.
The base's package must answer to the repository name for the two to connect (`"packages": {"base": {"manifestNames":
["registry.example.com/base"]}}`), because an image is called `registry.example.com/base` while the folder is called
`base`. With that in place `dispat compute` will even propose the `app -> base` edge for you, straight off the `FROM`
line, so the `dependencies` block above need not be written by hand. The details are in
[manifests](../editing/manifests.md#docker).

**Or pass it as a build argument.** When you would rather the Dockerfile stay version-free:

```dockerfile
ARG BASE_VERSION
FROM registry.example.com/base:${BASE_VERSION}
```

dispat never rewrites an interpolated reference, so this one is left alone by design. Pass it in the build script with
`--build-arg BASE_VERSION=$DISPAT_UPDATED_BASE_NEW_VERSION`, which is set whenever base moved in this run, however it
moved. Use `$DISPAT_WORKSPACE_BASE_VERSION` when you want the version base carries whether or not it is releasing; see
[the script environment](../reference/environment.md).

**A worked example, in this repository.** dispat's own
[container images](https://github.com/yohimik/dispat/tree/main/docker) are four packages set up this way, and they go
one step further: each one's `docker-compose.yml` *is* its manifest, so the version lives there and the whole of the
build and publish stages is `docker compose build` and `docker compose push`. The same file shows both halves of this
section: a rewritten literal (`image:`) beside an interpolated reference dispat leaves alone (the channel tag).
