# Registry login, once per space

Define a `login` script to authenticate dispat to a registry. Authentication belongs to the space rather than any
single package, so this script runs exactly once per space. The first package publish triggers it, and all other
publishes in that space wait for it to finish.

If the login fails, every publish in the space fails. dispat runs the script in the space folder, which keeps
space-local config files in a predictable place.

Use this script for any registry a shell command can access. The example below logs into a Docker registry.

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

For npm, you typically use this slot to write an `.npmrc` file:

```sh
echo "//registry.npmjs.org/:_authToken=$NPM_TOKEN" >> ~/.npmrc
```

Pass values forward to publish scripts by appending `DISPAT_OUTPUT_<NAME>=value` lines to `$DISPAT_OUTPUT`. You can use
this to share a short-lived token, and the publish scripts in that space then read `$DISPAT_OUTPUT_<NAME>`. See
[Script outputs](../reference/environment.md#script-outputs).
