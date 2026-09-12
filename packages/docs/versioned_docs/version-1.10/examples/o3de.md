# O3DE projects and gems

O3DE’s `project.json` and `gem.json` can supply a project or gem version and explicit gem dependency constraints.
Keep engine compatibility and the release-train identity separate when they serve different purposes.

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

This is a graph fragment, not a claim about O3DE’s own release configuration. Supply real baselines and the existing
CMake/build/package commands before releasing. A root `gem.json` is in the default manifest scope. For nested
manifests, choose the scope deliberately so that unrelated gems and demo projects do not inherit the same version.

Use `dispat compute` to inspect native gem dependencies and `dispat status` to inspect the release plan. A dependency
constraint says which gem version is required; it does not prove the resulting binaries are ABI-compatible or that
the installer includes the right payload. Test those with the native toolchain and record the artifact inventory.

## Preserve the right version source

A release-train tag, gem version and engine-compatibility constraint need not be equal. Do not rewrite compatibility
fields merely to match the train’s semantic version. Choose which artifact gets the release record, and use a
version script when the published identity lives outside the supported manifest fields.

See [game development](./game.md) for installer identity and recovery findings, and
[From one package to many](./one-to-many.md) for adding tools, plugins and sites around an existing deliverable.
