# Docs 1.8.2 verification

Verified on 5 September 2026 before the documentation patch release. The CLI and Go libraries are unchanged.

## Checks

- Documentation TypeScript and production build pass, including broken-link and anchor checks.
- All 17 documentation plugin tests and two title-timeline regression tests pass. The title check covers every frame of the README export. Extracted frames around the outgoing/incoming title boundary and final publication confirm that the rendered asset matches the check.
- Playwright checks all 19 slides at desktop, 390px, and 320px widths, across light and dark themes and reduced motion. Controls stay in place, transcript choices persist, and copyable transcripts remain selectable.
- Portrait layouts fit all 19 scenes within the mobile canvas. Geometry checks sample three timeline points and the paused still at 320px and 390px in both themes. The recorded-progress scene received a focused rerun after its final note was moved clear of the terminal. Package status labels and stage chips wrap within their cards. Increased row spacing and opaque edge-label backgrounds keep gray dependency labels clear of the connectors. Compact graphs are centered, and fanout labels are separated; the matrix also rejects overlapping visible edge labels. Terraform cards place their status on a separate line.
- Shared prose and code-block checks cover 320px, 390px, and 768px in both themes on the landing page, current CLI/API docs, and historical 1.7 docs. Wide commands scroll inside their code block.
- A fresh browser visits all 19 scenes with one font request, zero audio contexts, zero audio elements, and no font/audio/autoplay warnings. Silent autoplay advances without a user gesture; revisiting a scene does not request another font.
- Full 1× Playwright recordings cover every scene. Continuous checks reject horizontal terminal overflow and partially clipped rows.
- Delayed imports retain the current scene and caption. A visible Cancel control dismisses a pending selection, and a newer selection supersedes it. Late completions cannot replace the current choice. Failure/retry checks preserve manual pause state.
- Caption geometry checks reproduced a 47px desktop jump before the fix. All 19 scenes now retain the same description and transcript positions and overall height at 1440px, 390px, and 320px in both themes, with the transcript open or closed. Pending-load feedback does not move that text. The same browser run verifies the requested landing section order.
- The mobile burger menu opens a full-height, scrollable panel at 320px and 390px in both themes, including from scrolled landing, current docs, and 1.7 docs. Keyboard close, navigation, and reopening after browser back pass.
- Current 1.8 and historical 1.7 sidebars use the same category control. Linked and nested groups have matching caret dimensions, hover feedback, and Enter/Space behavior.
- Disposable CLI fixtures verify Compute previews and writes, Run selection and ordering without new tags, For iteration, and the conditional/replacement/local-link examples.
- A local release fixture verifies overlapping independent providers, npm workspace build readiness, published Go imports, Docker FROM readiness, built SDK assets consumed by the web image, a contained failure, a repair commit, and exact catch-up versions.
- The Terraform fixture verifies reconstruction and saved-plan stages, both applications waiting for infrastructure apply, overlapping independent application builds, exact versions and tags, and an unchanged rerun with no executed stages. Both current and 1.8 documentation describe the real repository configuration separately from this adapted fanout example.
- Page and version navigation clears inherited heading anchors. Explicit cross-page anchors, query parameters on version switches, and browser back/forward remain intact.
- A separate fixture verifies all seven release-control examples, including graduation, a held release that neither publishes nor tags, and resumed publication.
- Regenerated shared-source GIFs pass the export budget: `demo-release.gif` is 2,235,704 bytes and `demo-blast.gif` is 944,251 bytes.

## Reproduction and limits

[The demo README](./README.md) lists the commands for the four CLI fixtures and the Playwright checks. Recordings and screenshots are kept outside Git under `output/playwright/docs-1.8.2/`.

The stories illustrate specific repository configurations. Waiting for a published dependency is an explicit build policy; language alone does not choose it. Fixture publishers write locally and exercise release coordination without posting to external registries. An ambiguous external publish still requires destination reconciliation or an idempotent publisher, as the recovery and recorded-progress scenes explain.

The sidebar override retains Docusaurus 3.10.2 category behavior and unifies its two caret variants. The version-dropdown override removes inherited hashes while preserving upstream version selection. Review both overrides against upstream when upgrading Docusaurus.
