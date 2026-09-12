# Releasing skills, specifications and TeX documentation

Treat documents as deliverables when another tool or person relies on a particular revision. A skill includes its
referenced guides and scripts; a protocol specification includes its schema and compatibility policy; a TeX manual
includes the compiled PDF and any files required to rebuild it. These are release packages in the graph, not extra
native manifest ecosystems.

## Identify the actual distribution

Different document types have different distribution contracts:

| Deliverable | Useful integration rule |
|---|---|
| A skill with referenced guides and scripts | Inventory and compare the whole instruction tree, preserving intentional edits and line-ending normalization. |
| Templates distributed inside a package | Match documentation and recovery instructions to the package publisher; an absent release-page ZIP is not necessarily missing output. |
| A date-named protocol revision | Preserve the protocol's external revision identifier; do not force all SDKs or documents to share one SemVer. |
| A TeX package and compiled manual | Keep packaging, engines and acceptance rules in native scripts; a GitHub release is a separate destination. |

A repository containing documents or skills need not promise versioned releases. Declare a package only when the
project's distribution policy requires one.

## Choose the compatibility boundary

Declare a documentation package with a real folder and its own tag format. A guide tied to the CLI's major/minor
can use a `fixedMajorMinor` version group and receive independent patches. A standalone specification can version
independently. A version-marker replacement can update a deliverable without a package-manager manifest; put its
native validation before publication.

Do not add an npm manifest solely to coordinate a PDF or skill folder. Keep the actual compiler and distribution
commands in the package's scripts. dispat plans SemVer versions; a project's date-based protocol or CTAN revision
needs an explicit mapping in those scripts and metadata, rather than pretending a date is an interchangeable SemVer.

Test the contract from the released artifact. Check that relative references exist, that CLI help links reach the
intended immutable documentation snapshot, and that the document's compatibility declaration matches the installed
tool. Publish the complete file set before advancing a mutable discovery link such as `latest`.

## Keep document quality checks explicit

A zero TeX exit status does not establish that the layout meets a project's publication requirements. Keep bad-box
checking and acceptance rules explicit. Preserve the project's chosen setting and its engine limitations; do not
report every warning as a publishing failure or claim that release orchestration validates PDF layout.

For a manual, build in a clean directory, inspect the generated document and retain its source revision and file
inventory. For a schema, validate examples with the schema that will actually ship. For a skill, check the referenced
scripts and guides together. Then exercise an interrupted upload and a retry: tags, registry entries, attached files
and deployed pages are distinct observations. See [release integration](./release-integration.md).
