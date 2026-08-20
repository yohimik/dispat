# npm and Docker in one graph

The polyglot monorepo release dispat exists for: a TypeScript library, a service that uses it, and a Docker image that
ships the service. Each space brings its own scripts and its own ordering rule, and the dependency edges connect them
into one graph.

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

Here `sdk` and `service` live under `packages/` (npm), `service-image` under `images/` (Docker). A
`feat(sdk)^^: ...` commit releases all three: `service` builds as soon as `sdk` has *built* (npm space, no waiting
flag), while `service-image` waits until `service` is *published*, because the image's build pulls the published
package. You declare intent once per space; the scheduler works out the rest per run.
