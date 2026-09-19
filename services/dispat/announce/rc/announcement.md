One release graph can now span many repositories.

Two saga modes let you choose how those repositories work together:

- Orchestration uses a control repository to combine configuration and the source histories pinned by its checkout.
- Choreography lets repositories coordinate as peers, each keeping its own configuration and release records. No separate control repository is needed.

In either model, dispat calculates versions and runs packages in dependency order across repository boundaries. Before it plans a release, it acquires the remote release locks for every participating repository; if any lock cannot be claimed, the run fails closed without producing a plan.

Publication remains a recoverable saga. If a provider publishes successfully and a downstream consumer fails, the provider's durable release record stays in place. The next run reads that record, avoids republishing completed work, and plans the consumer catch-up. Independent packages can continue when their dependencies permit it.

Autistic stability for ADHD projects. Mathematical planning. Saga recovery.

Preview your exact release plan safely with `dispat status`, which runs no release stages and acquires no release lock. Read the guide: https://dispat.dev/
