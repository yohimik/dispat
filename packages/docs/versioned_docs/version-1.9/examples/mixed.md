# npm and Docker in one graph

This is the polyglot monorepo release dispat exists for. You have a TypeScript library, a service that uses it, and a
Docker image that ships the service. Each space brings its own scripts and its own ordering rule, and the dependency
edges connect them into one graph.

```json
{
  "scripts": {
    "npm-build": "npm ci && npm run build",
    "npm-publish": "npm publish --access public",
    "img-build": "docker build -t registry.example.com/$DISPAT_PACKAGE:$DISPAT_NEW_VERSION .",
    "img-publish": "docker push registry.example.com/$DISPAT_PACKAGE:$DISPAT_NEW_VERSION"
  },
  "spaces": {
    "libs": {
      "path": "packages",
      "flow": {
        "build": "npm-build",
        "publish": "npm-publish"
      }
    },
    "images": {
      "path": "images",
      "isBuildWaitingPublish": true,
      "flow": {
        "build": "img-build",
        "publish": "img-publish"
      }
    }
  },
  "dependencies": {
    "service": ["sdk"],
    "service-image": ["service"]
  }
}
```

Look at how `sdk` and `service` live under `packages/` for npm, while `service-image` lives under `images/` for Docker.
Write a `feat(sdk)^^: ...` commit to release all three.

The `service` package builds as soon as `sdk` has *built* because the npm space has no waiting flag. The
`service-image` package waits until `service` is *published* so the image build can pull the published package. You
declare your intent once per space, and the scheduler works out the rest per run.
