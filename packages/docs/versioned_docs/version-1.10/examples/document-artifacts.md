# Releasing skills, specifications and TeX documentation

Treat documents as deliverables when another tool or person relies on a particular revision. A skill includes its
referenced guides and scripts; a protocol specification includes its schema and compatibility policy; a TeX manual
includes the compiled PDF and any files required to rebuild it. These are release packages in the graph, not extra
native manifest ecosystems.

## Identify the actual distribution

The September 10, 2026 review found several different contracts:

| Project | Checked distribution | Useful integration rule |
|---|---|---|
| [Playwright CLI 0.1.19](https://github.com/microsoft/playwright-cli/releases/tag/v0.1.19) | The npm archive contains the root skill and reference guides. The [reproduced checker gap](https://github.com/microsoft/playwright-cli/issues/463) concerns reference-only drift. | Inventory and compare the whole instruction tree, preserving intentional local edits and line-ending normalization. |
| [Spec Kit 1.0.5](https://github.com/github/spec-kit/releases/tag/v1.0.5) | [PyPI](https://pypi.org/pypi/specify-cli/1.0.5/json) has a wheel and sdist; the wheel includes `specify_cli/core_pack/templates/`. The GitHub release has no uploaded assets. | Match documentation and recovery instructions to today's publisher. [The stale release-guide report](https://github.com/github/spec-kit/issues/4501) concerns obsolete ZIP instructions, not missing packages. |
| [MCP specification 2026-07-28](https://github.com/modelcontextprotocol/modelcontextprotocol/releases/tag/2026-07-28) | A date-named specification revision is released independently of SDK versions. | Preserve the protocol's external revision identifier; do not force all language SDKs or documents to share one SemVer. |
| [l3build 2026-09-09](https://github.com/latex3/l3build/releases/tag/2026-09-09) | [CTAN metadata](https://ctan.org/pkg/l3build) reports the same release date. | Keep CTAN packaging, TeX engines and acceptance rules in the native scripts; GitHub release presence is a separate check. |

A repository containing skills need not promise versioned releases. The reviewed [Anthropic skills repository](https://github.com/anthropics/skills)
had no GitHub releases; that alone does not establish a failed publication or justify changing its distribution policy.

## Choose the compatibility boundary

Declare a documentation package with a real folder and its own tag format. A guide tied to the CLI's major/minor
can use a `fixedMajorMinor` version group and receive independent patches. A standalone specification can version
independently. dispat's [agent-guide configuration](https://github.com/yohimik/dispat/blob/v1.10.0/specs/agent-guide/dispat.yaml)
shows version-marker replacement without a package-manager manifest, and a native verification script before recording.

Do not add an npm manifest solely to coordinate a PDF or skill folder. Keep the actual compiler and distribution
commands in the package's scripts. dispat plans SemVer versions; a project's date-based protocol or CTAN revision
needs an explicit mapping in those scripts and metadata, rather than pretending a date is an interchangeable SemVer.

Test the contract from the released artifact. Check that relative references exist, that CLI help links reach the
intended immutable documentation snapshot, and that the document's compatibility declaration matches the installed
tool. Publish the complete file set before advancing a mutable discovery link such as `latest`.

## Keep document quality checks explicit

A zero TeX exit status does not establish that the layout meets a project's publication requirements. In
[l3build's resolved discussion](https://github.com/latex3/l3build/issues/470), maintainers discussed opt-in bad-box
checking and accepted an implementation. Preserve the project's chosen setting and its engine limitations; do not
report every warning as a publishing failure or claim that release orchestration validates PDF layout.

For a manual, build in a clean directory, inspect the generated document and retain its source revision and file
inventory. For a schema, validate examples with the schema that will actually ship. For a skill, check the referenced
scripts and guides together. Then exercise an interrupted upload and a retry: tags, registry entries, attached files
and deployed pages are distinct observations. See [release integration](./release-integration.md).
