# Release-order fixture

These manifests illustrate the two published-artifact edges in the release demo. `example.invalid` keeps every Go
module path visibly local to the disposable fixture. In a real registry-backed run, the stage commands represented by
the fixture are:

```sh
docker build --build-arg CORE_VERSION="$DISPAT_UPDATED_CORE_NEW_VERSION" -t acme/api:"$DISPAT_NEW_VERSION" api
docker build -f web/Dockerfile --build-arg API_VERSION="$DISPAT_UPDATED_API_NEW_VERSION" -t acme/web:"$DISPAT_NEW_VERSION" .
```

The api Dockerfile updates `go.mod` to the published core version before it downloads and compiles the API binary. The
web Dockerfile then starts from the published api image and adds its static assets plus sdk's locally built browser
client. Its repository-root build context makes both `web/webassets` and `sdk/dist` available without a registry.

`verify-release.py` deliberately does not run Docker, publish to a registry, or use the network. It copies these files
to a temporary Git repository and uses local marker files to represent successful publishes. Its event assertions
prove that api starts building only after core's marker exists and web starts building only after api's marker exists.
