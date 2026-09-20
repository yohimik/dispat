One release graph can now span many repositories.

Linked fleets are enabled by repository identity; an optional roster names the other peers. Every peer keeps its own configuration and release records, and no separate control repository is needed.

Preview the links with `dispat compute --topology minimal` to preserve existing links and add the fewest needed, or `dispat compute --topology star` to link every peer directly to the entry repository. Neither shape changes repository-local ownership or deletes links. Existing centrally configured fleets keep their established behavior.

In either model, dispat calculates versions and runs packages in dependency order across repository boundaries. Before it plans a release, it acquires the remote release locks for every participating repository; if any lock cannot be claimed, the run fails closed without producing a plan.

Publication remains a recoverable saga. If a provider publishes successfully and a downstream consumer fails, the provider's durable release record stays in place. The next run reads that record, avoids republishing completed work, and plans the consumer catch-up. Independent packages can continue when their dependencies permit it.

Autistic stability for ADHD projects. Mathematical planning. Saga recovery.

Preview your exact release plan safely with `dispat status`, which runs no release stages and acquires no release lock.
