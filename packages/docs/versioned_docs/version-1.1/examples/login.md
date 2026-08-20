# Registry login, once per space

How dispat authenticates to a registry. Authentication belongs to the space, not to any one package, so a `login`
script runs once per space and run: the space's first publish triggers it, every other publish of the space waits for
it, and if it fails, every publish of the space fails, since none of them could have succeeded. The login runs in the
space folder, so a space-local config file is always found at the same place.

The worked example logs into a Docker registry; the same slot serves npm, or any registry a shell command can log
into.

```json
{
  "scripts": {
    "img-build": "docker build -t registry.example.com/$DISPAT_PACKAGE:$DISPAT_NEW_VERSION .",
    "img-publish": "docker push registry.example.com/$DISPAT_PACKAGE:$DISPAT_NEW_VERSION",
    "docker-login": "echo \"$REGISTRY_TOKEN\" | docker login registry.example.com -u ci --password-stdin"
  },
  "spaces": {
    "images": {
      "path": "images",
      "isBuildWaitingPublish": true,
      "flow": {
        "build": "img-build",
        "publish": "img-publish",
        "login": "docker-login"
      }
    }
  }
}
```

For npm the same slot typically writes an `.npmrc`:

```sh
echo "//registry.npmjs.org/:_authToken=$NPM_TOKEN" >> ~/.npmrc
```

A login script can also pass values forward (a short-lived token, say) by appending
`DISPAT_OUTPUT_<NAME>=value` lines to `$DISPAT_OUTPUT`; the space's publish scripts then read
`$DISPAT_OUTPUT_<NAME>`. See [Script outputs](../reference/environment.md#script-outputs).
