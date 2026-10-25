# O3DE projects and gems

Use `project.json` and `gem.json` to read project or gem versions and explicit gem dependencies. This guide shows
manifest edits and a two-gem release graph. Keep the release-train version separate from engine compatibility
constraints when they describe different things.

## Version and dependency boundaries

The writer can update a gem's existing version and a literal dependency constraint:

```sh
dispat writer Gems/Atom/gem.json --set-version 1.0.0 --set 'AtomShader===0.2.0' --strict
dispat scanner . --strict --log-format json
```

The first `=` separates the dependency name from its range; the remaining `==` is the range operator. These manifest
operations do not configure CMake, compile the engine, validate a gem against an engine build, or create an installer.
Keep those checks in the native build and publish commands.

## Declare the release units

For two separately released gems in one repository, keep the graph and native version policy explicit:

```json
{
  "spaces": {
    "gems": {
      "path": "Gems",
      "autoVersion": {"enabled": true},
      "packages": {
        "AtomShader": {},
        "Atom": {"dependencies": [{"provider": "AtomShader", "keep": true}]}
      }
    }
  }
}
```

This fragment illustrates the graph. Add your actual starting versions and CMake, build and packaging commands
before releasing. A `gem.json` directly in the package folder is in the default manifest scope. If you include nested
manifests, check that unrelated gems and demo projects will not receive the same version.

Use `dispat compute` to inspect native gem dependencies and `dispat status` to inspect the release plan. A dependency
constraint says which gem version is required; it does not prove the resulting binaries are ABI-compatible or that
the installer includes the right payload. Test those with the native toolchain and record the artifact inventory.

## Preserve the right version source

A release-train tag, gem version and engine-compatibility constraint can have different values. Decide which artifact
the release record identifies before choosing a field to update. If that version is outside the supported manifest
fields, use a version script and leave compatibility constraints under their existing policy.

See [game development](./game.md) for installer identity and recovery findings, and
[From one package to many](./one-to-many.md) for adding tools, plugins and sites around an existing deliverable.
