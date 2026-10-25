# Registry login, once per space

Use `flow.login` when several packages share registry credentials. The first publish in a space runs the login
script, and the other publishes in that space wait for it to finish. It runs once per space per release.

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

If login produces a value that publishers need, append `DISPAT_OUTPUT_<NAME>=value` to `$DISPAT_OUTPUT`. For example,
a login command can obtain a short-lived token and the space's publish scripts can read it as `$DISPAT_OUTPUT_<NAME>`.
See
[Script outputs](../reference/environment.md#script-outputs).
