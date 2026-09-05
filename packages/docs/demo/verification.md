# Docs 1.8.2 verification

Verified on 5 September 2026 before the documentation patch release. The CLI and Go libraries are unchanged.

## Checks

- Documentation TypeScript and production build pass, including broken-link and anchor checks.
- All 17 documentation plugin tests pass.
- Playwright checks all 18 slides at desktop, 390px, and 320px widths, across light and dark themes and reduced motion. Controls stay in place, transcript choices persist, and copyable transcripts remain selectable.
- Full 1× Playwright recordings cover every scene. Continuous checks reject horizontal terminal overflow and partially clipped rows.
- Delayed imports retain the current scene and caption. Rapid selection keeps the latest request; an aborted import retains the current scene and can be retried without losing pause state.
- Current 1.8 and historical 1.7 sidebars use the same category control. Linked and nested groups have matching caret dimensions, hover feedback, and Enter/Space behavior.
- Disposable CLI fixtures verify Compute previews and writes, Run selection and ordering without new tags, For iteration, and the conditional/replacement/local-link examples.
- A local release fixture verifies overlapping independent providers, npm workspace build readiness, Go publish readiness, a contained failure, a repair commit, and exact catch-up versions.
- A separate fixture verifies all seven release-control examples, including graduation, a held release that neither publishes nor tags, and resumed publication.
- Regenerated shared-source GIFs pass the export budget: `demo-release.gif` is 2,061,207 bytes and `demo-blast.gif` is 944,251 bytes.

## Reproduction and limits

[The demo README](./README.md) lists the commands for the three CLI fixtures and three Playwright checks. Recordings and screenshots are kept outside Git under `output/playwright/docs-1.8.2/`.

The stories illustrate specific repository configurations. Waiting for a published dependency is an explicit build policy; language alone does not choose it. Fixture publishers write locally and exercise release coordination without posting to external registries. An ambiguous external publish still requires destination reconciliation or an idempotent publisher, as the recovery and recorded-progress scenes explain.

The sidebar override retains Docusaurus 3.10.2 category behavior and unifies its two caret variants. Review the override against upstream when upgrading Docusaurus.
