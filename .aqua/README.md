# CI tools

Dispat installs the latest Aqua CLI. Aqua installs the Crier and custom TinyGo versions recorded in `aqua.yaml`, using the asset digests in `aqua-checksums.json`.

From the repository root:

```sh
sh scripts/install-tools.sh all "$HOME/.local/bin"
```

Use `crier` or `tinygo` instead of `all` to install one tool. Crier is copied as a standalone executable. The destination's `tinygo` symlink points to Aqua's complete toolchain tree, including `lib` and `src`. Keep that cache available while using the compiler.

## Update a tool

Run these commands from the repository root, with Aqua on `PATH`:

```sh
export AQUA_POLICY_CONFIG="$PWD/.aqua/aqua-policy.yaml"
aqua -c .aqua/aqua.yaml update crier
aqua -c .aqua/aqua.yaml update-checksum
```

For the custom TinyGo fork, choose a published prerelease explicitly. Replace the example version with the intended release:

```sh
aqua -c .aqua/aqua.yaml update tinygo@v0.43.0-net.1
aqua -c .aqua/aqua.yaml update-checksum
```

Check that the release has all four supported assets before updating. A draft or a Git tag alone is not an installable release. Review and commit the manifest and checksums together, then rerun the relevant build gates. The registry deliberately points to `yohimik/tinygo`, not the upstream compiler.

The checked-in policy allows the local registry for these commands. Set `AQUA_GITHUB_TOKEN` if GitHub authentication is needed; the installer also accepts `GITHUB_TOKEN`.
