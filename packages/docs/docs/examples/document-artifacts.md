# Releasing skills, specifications and TeX documentation

A document needs a release when someone depends on a particular revision. That might be a skill with supporting
scripts, a protocol specification with a schema, or a TeX manual with its PDF and sources. Give it a package in the
release graph and keep its existing build and distribution commands.

## Identify the actual distribution

First decide what users should receive:

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

Test the files you plan to distribute. Check that relative references exist, that CLI help links reach the
intended immutable documentation snapshot, and that the document's compatibility declaration matches the installed
tool. Publish the complete file set before advancing a mutable discovery link such as `latest`.

## Keep document quality checks explicit

A successful TeX command does not tell you whether the PDF layout is ready to publish. Keep the project's bad-box
checks and acceptance rules in its build scripts, accounting for the chosen engine. Decide which warnings should
block publication and inspect the document itself.

For a manual, build in a clean directory, inspect the generated document and retain its source revision and file
inventory. For a schema, validate examples with the schema that will actually ship. For a skill, check the referenced
scripts and guides together. Then exercise an interrupted upload and a retry: tags, registry entries, attached files
and deployed pages are distinct observations. See [release integration](./release-integration.md).
