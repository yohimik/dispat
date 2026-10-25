# npm and Docker in one graph

This example releases a TypeScript SDK, a service that uses it, and a Docker image that installs the service. npm
packages and images use different commands, so they have separate spaces. Dependency edges connect their releases.

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
  "packages": {
    "service": {"isBuildWaitingPublish": true}
  },
  "dependencies": {
    "service": ["sdk"],
    "service-image": ["service"]
  }
}
```

Put `sdk` and `service` under `packages/`, and `service-image` under `images/`.
Write a `feat(sdk)^^: ...` commit to release all three.

`service` can build once `sdk` has built. The image must wait longer: it installs the service from a registry, so
`isBuildWaitingPublish` is set on the `service` provider. `service-image` builds only after that publish succeeds.
The flag on the image space applies the same rule to any consumers of its images. See
[build boundaries](./release-integration.md#choose-the-build-boundary) when deciding where to put the flag.
