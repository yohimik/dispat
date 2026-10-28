# Changelog

## specs/ccme-spec/v3.1.0-rc.9 (2026-10-28)

### Fixes

- cite compilation trade-off foundations ([22b07b2](https://github.com/yohimik/dispat/commit/22b07b2c5883d756a1be8c7417cc82ec8630fa6f)) (by yohimik, Codex)
  Connect reuse cost accounting to amortized analysis, partial evaluation and ski-rental models. State the conditions under which compiled runs approach execution-dominated cost, with all recurring overhead charged.

  Validate the references and algebra independently, run CCME checks and release fixtures, and verify the symbolic identities and exact rational examples.

- prove compilation reuse break-even ([357d096](https://github.com/yohimik/dispat/commit/357d096f7ca665f2593b52c537c2cbe06a091e4e)) (by yohimik, Codex)
  Account for setup and per-use discovery, validation and execution costs.
  State conditional reuse thresholds and measure complete same-task costs
  without claiming a universally fastest mode or optimal compiled graph.

- state two-phase task completion bound ([15b9dbe](https://github.com/yohimik/dispat/commit/15b9dbe48925f1026456dd9b3922487428bdc135)) (by yohimik, Codex)
  Define discovery join time from actual acquisition completion times and
  charge post-join execution and dispatch wait. Preserve positive-latency,
  overlap, shared-resource and worst-case accounting premises.

- account for discovery and compiled-plan acquisition ([bc06423](https://github.com/yohimik/dispat/commit/bc06423e66225a1ad8d9c9eef4cbc0ce51567a39)) (by yohimik, Codex)
  Prove the sufficient-information requirement and scope positive discovery
  latency and representation-access bounds. Distinguish semantic discovery,
  compiled acquisition, incremental loading and retained-state reuse.

- require chains for minimal topology ([9863f25](https://github.com/yohimik/dispat/commit/9863f25bc9357f90f224611248f5962072a43c6d)) (by yohimik, Codex)
  Join existing path components at free endpoints and reject branches while preserving links. Recompose between joins, require owner roster declarations before creating links, and report incomplete repairs. Keep star as the explicit entry-hub shape and require selection for newly discovered interactive suggestions.

  Update the specification, CLI help, topology documentation and regression coverage.

- document classical minimum-edge induction proof ([6724d7e](https://github.com/yohimik/dispat/commit/6724d7e72982adedbfe4a98e9c864c00aba2f865)) (by yohimik, Codex)
  Prove the n-1 lower bound for connected undirected graphs by strong induction
  and characterize equality by trees. Relate the established result to fleet links.

- clarify distributed clock limits and event histories ([c663b41](https://github.com/yohimik/dispat/commit/c663b41cb111008e1e07ef46acb760e9f9687c15)) (by yohimik, Codex)
  State the finite-transcript limitation for exact physical clock offsets under
  unknown delays, and prove deterministic replay from a fenced observer history.
  Separate acceptance order, source causality, timing uncertainty and effect evidence.

- define same-task device and compiler optimality ([92d369f](https://github.com/yohimik/dispat/commit/92d369f05021a974473a96724c45fc0e15605a31)) (by yohimik, Codex)
  Formalize joint designs under a resource budget, prove finite minimum-time attainment and Pareto selection, and state the evidence required for same-task experiments and broader optimality claims.

- clarify word-operation scaling and useful capacity ([7fe4b40](https://github.com/yohimik/dispat/commit/7fe4b40a54706094534ecac73a23d69130eec3aa)) (by yohimik, Codex)
  Apply work and span bounds to fixed word-operation DAGs. Distinguish sequential dependencies, word width and operation slots, and define ideal saturation separately from physical resource costs.

- clarify minimum critical path across execution DAGs ([f1bc701](https://github.com/yohimik/dispat/commit/f1bc701ee3d27ade6d16f7c6aaf27151959fd788)) (by yohimik, Codex)
  State the minimum-over-DAGs, maximum-over-paths formulation while retaining every task, hard prerequisite and slot constraint. Keep release intent fixed and preserve normal plan validation.

- prove optimal scheduling for undirected conflicts ([6123812](https://github.com/yohimik/dispat/commit/6123812d7c0828931148f16a5b1bb87e6dd002be)) (by yohimik, Codex)
  Distinguish bidirectional relations from hard prerequisites. Prove an attained minimum makespan by finite order and slot enumeration, with a canonical tie rule, explicit input assumptions and separately charged planning costs.

- extend theorem to cyclic release intent ([8f5a6e3](https://github.com/yohimik/dispat/commit/8f5a6e3689913ee4d5d93c95690652b6dcc2faad)) (by yohimik, Codex)
  Prove finite cyclic intent determinacy and least repository closure, then state the enabledness, input and capacity premises for executing every task once. Preserve hard-cycle refusal and publication recovery guarantees.

- define timestamped interpretation pickups and bounded attempts ([5552fc8](https://github.com/yohimik/dispat/commit/5552fc8bdd38265f74e797605976809bcb9cc41b)) (by yohimik, Codex)

### Authors

- yohimik
- Codex


## specs/ccme-spec/v3.1.0-rc.8 (2026-10-27)

### Features

- preserve worker pickups through failure and recovery ([4a2d83a](https://github.com/yohimik/dispat/commit/4a2d83a9e2df5bb5d9d1a1308ffb3c23792075f8)) (by yohimik, Codex)
  Give every physical pickup a durable unique identity and immutable pending,
  output or failed branch. Admit a replacement after failure or signed contact
  loss only under the signed retry-safety policy. Keep unknown physical work
  charged until its exact acknowledgement settles, and preserve the first
  verified eligible success across replay and late returns.

  Retain unresolved child processes and publication requests without inventing
  completion. Record known earlier publications while their successor remains
  uncertain. Correct worker restart cleanup and pinned HTTPS transport for the
  release TinyGo compiler, with integration and recovery regressions.

  Integrate the reviewed CCME theorem, stopping, capacity and asynchronous
  cancellation corrections with their executable specification fixtures. Keep
  the existing release holds until the complete RC.9 gates pass.

### Fixes

- scope graph complexity and verification bounds ([adab238](https://github.com/yohimik/dispat/commit/adab238af798b0b0d30e8b7a6ec32f6edf40f354)) (by yohimik, Codex)
  Integrate the reviewed constructive computation, unknown-graph scheduling
  and non-strict solve/check cost arguments without changing the pinned-run
  or external-effect retry contracts.

- clarify mode equivalence and execution settlement ([ce64251](https://github.com/yohimik/dispat/commit/ce6425153b95a4d731ff96dc208789a2814c2bb9)) (by yohimik, Codex)
  Integrate the reviewed weighted-duration and interpretation/hybrid proofs.
  Clarify output admission, missing placement and the signed repeat-safe
  exception without treating lost contact as proof that an attempt stopped.

- align pickup authority and failover conformance rules ([c6c9f57](https://github.com/yohimik/dispat/commit/c6c9f57cc4636b522c5bb477d6abacf9176eacd5)) (by yohimik, Codex)
  Reserve canonical task, ledger and native release refs for the first node,
  while workers create only their own immutable pending and outcome refs.
  Require confirmation of the exact remote pending object before commands or
  hooks start. Distinguish default retry refusal from explicitly repeat-safe
  failover with spare capacity and the old attempt still charged.

  Update the conformance vectors and worker documentation to agree with the
  implemented pickup protocol. Preserve the reviewed mathematical statements.

### Authors

- yohimik
- Codex


## specs/ccme-spec/v3.1.0-rc.7 (2026-10-26)

### Features

- define all distribution modes before implementation ([ee55295](https://github.com/yohimik/dispat/commit/ee5529516cdc03ec54732c90e9bc134882148836)) (by yohimik, Codex)
  Specify compilation, interpretation and mixed execution over the same pinned
  semantic plan and task graph. Reserve Git pushes and release effects for the
  first node while defining independent interpretation, exact claim admission,
  recovery and cross-partition outcome gates.

  State conditional scheduling, scaling and equilibrium bounds without claiming
  a unique algorithm or a universal planner time or memory minimum. Separate the
  published specification from the engine modes still awaiting implementation.

- initialize worker task branches before execution ([def8e7d](https://github.com/yohimik/dispat/commit/def8e7d41914e266d474b04c43de31e07be5d0a4)) (by yohimik, Codex)
  Reserve every eligible task attempt and bounded worker notice slots before
  starting package frames. Activate ready work with atomic leased updates,
  carry exact input commits with assignments, and keep publication on the
  orchestrator. Workers verify and execute assignments without replanning.

  Cover cancellation, uncertain pushes, carrier integrity and large fan-in.
  Document the node-player execution game and prove its scoped equilibrium
  and span optimum without claiming a unique algorithm or equilibrium.

- execute configured stages with explicit dependency ordering ([1bf38e3](https://github.com/yohimik/dispat/commit/1bf38e3ab10c7f3f8ba6204033fbde8f4749066b)) (by yohimik, Codex)
  Compile configured stage placement and selected provider outcomes before
  dispatch, rejecting task cycles before effects. Carry verified predecessor
  outputs between local and worker stages from pinned source inputs, including
  idempotent preparation and providers that do not release.

  Preserve durable publication receipts and suppress stages after a failed
  required record. Document configuration inheritance, concurrency premises
  and the boundary between declared dependencies and arbitrary script reads.

### Fixes

- admit completed native records across repository owners ([ace697e](https://github.com/yohimik/dispat/commit/ace697ea9f2c249264501bbf1b08e321b5d72d27)) (by yohimik, Codex)
  Preserve parallel publication when a relevant peer completes a nested native
  commit or link settlement. Validate its exact completed effects before
  advancing the snapshot, and wait for unfinished transitions without locks.

- keep publication on the orchestrator ([280d782](https://github.com/yohimik/dispat/commit/280d7823200c69be7a96a9b7c1b3ae02801f58ec)) (by yohimik, Codex)
  Run publication and release recording on the orchestrator while workers
  execute build, test and configured computation stages. Refuse worker
  publication placement and signed publication permits, and remove the
  worker publication executor and authorization handshake.

  Keep local input and ownership validation, document the single entry
  point and mailbox credentials, and replace obsolete worker-publication
  tests with local publication and refusal coverage. Repair remaining
  recovery and stage fixture assumptions without weakening their checks.

- preserve recovery guarantees across release retries ([5bb70d5](https://github.com/yohimik/dispat/commit/5bb70d5b8260f893dc10d09a1f208c36be2560e8)) (by yohimik, Codex)
  Complete receipt-backed releases when an immutable remote tag already names the prepared commit, and retry branch races from authoritative state. Report pinned-source capture errors and exercise recovery faults after the new dependency and output-preparation boundaries.

- name the optional release stage test ([6a648af](https://github.com/yohimik/dispat/commit/6a648af70e9b8035d4dbc78d8e1991b76b536ecd)) (by yohimik, Codex)
  Use the verb test in flow configuration, hooks, task identities and stage constraints. Refuse the former prerelease spellings with migration guidance while preserving scripts named tests. Align custom-stage docs, the agent guide and CCME, and keep skipped-consumer fixtures ordered before provider settlement.

- recover published packages with durable receipts ([80ecde8](https://github.com/yohimik/dispat/commit/80ecde80937e618469fdd47b7e975a850fb9cf3e)) (by yohimik, Codex)
  Persist per-package publication evidence before dependent publication and
  optional recorders. Verify repeated artifact identity, preserve exported
  source targets, and reject conflicting or unverifiable records.

  Finalize release commits with atomic branch and immutable-tag pushes plus
  leased receipt deletion. Bind local retries to prepared targets, reconcile
  lost replies and remote advances, and keep optional aliases independent.

  Cover rejected writes, interrupted finalization, same-checkout retries,
  tag tampering, stale receipts and adoption mismatches with regressions.

- close ancestry and recovery proof gaps ([1460e93](https://github.com/yohimik/dispat/commit/1460e93f219255a4e475947501b3306e72f80a3e)) (by yohimik, Codex)
  Correct the pending-union ancestry relation and charge induced adjacency,
  zero-marker traversal and queries about targets outside the union.

  Limit replay guarantees to fixed publication identities and conditional
  forward-plan convergence. Account for extra catch-up releases and exclude
  post-record stages without independent durable replay contracts.

  Show that the list-scheduling factor is sharp for arbitrary priorities
  and that the work/span lower bound need not be attained with finite slots.
  Keep the exact global minimum scoped to execution of a fixed task DAG.

- scope the global execution minimum to a compiled task DAG ([006df5a](https://github.com/yohimik/dispat/commit/006df5ae7fd9e3b3836238e9517097d120632e73)) (by yohimik, Codex)
  State that the exact minimum compares precedence-feasible executions of
  one fixed task graph with fixed durations and enough compatible capacity
  for immediate dispatch. Keep finite-slot approximation bounds and the
  separate planning, bookkeeping and recovery cost accounts explicit.

- compare planner and execution bounds across methods ([ee5dde1](https://github.com/yohimik/dispat/commit/ee5dde1be47e403d5f789a7b608ba17d5cb07a51)) (by yohimik, Codex)
  Prove growing task-time gaps for sequential execution, level barriers and
  duration-oblivious static placement under the stated identical-slot model.
  Compare event counters with full dependency rescans and heap ordering with
  least-ready scans, keeping actual scheduler sorting costs separate.

  Derive the expected list-scheduling bound from its pointwise guarantee for
  any workload distribution with finite expected optimum. State where other
  schedulers match the bound and where an unimplemented chain-rank planner
  fast path could improve window and author processing.

- distinguish planner and execution optimality proofs ([0e49579](https://github.com/yohimik/dispat/commit/0e4957971754ff8bc523588b2ef48f5f5f5cb7ea)) (by yohimik, Codex)
  Prove that immediate dispatch is the only universally exact online start
  policy under unlimited compatible capacity and independently unknown task
  durations. Separate that result from asymptotic optimality with an online
  schedule that deliberately delays work while staying within twice the span.

  State the full planner's input, output and comparison-model lower bounds,
  the task compiler's explicit graph costs, and a restricted readiness problem
  whose auxiliary memory has a matching bit lower bound. Record the linear
  history family where materialized windows and repeated author scans use
  more memory and work than needed.

  Compose semantic planning, task dependencies and durable replay for
  correctness without inferring a global time or auxiliary-memory optimum.

- prove compiled-plan bounds and model configured stages ([20e694f](https://github.com/yohimik/dispat/commit/20e694f26002eea76b9aa11c89573c8fcb6172ca)) (by yohimik, Claude Opus 5.5, Codex)
  Define configured stage graphs, required precedence, publication and record
  edges, and their acyclicity and order-reduction proofs. Keep one semantic
  plan for fixed inputs with no fixed upper bound on tasks, edges or workers.

  Prove the work-span list-scheduling bound with a schedule-dependent chain
  and the disjoint-stage-budget bound. Prove the unique earliest-start vector
  under sufficient compatible capacity and the vertical and horizontal
  scaling laws for chains and independent tasks. Account separately for node,
  stage, record-unit and owner capacity, and derive the scaling knee.
  Separate graph input, auxiliary scheduler state, placement and priority
  costs, and durable recovery. Give counterexamples to executor uniqueness
  and explain which asymptotic minima the stated models actually establish.

  Prove necessary saga-style forward recovery from irreversible effects and
  crash-history indistinguishability. State the crash-readiness contract,
  outcome-settlement requirements, and limits on conditional progress.

  Align build-readiness formulas, configured-stage conformance and the design
  history with those premises. Preserve the specification version markers.

### Authors

- yohimik
- Codex
- Claude Opus 5.5


## specs/ccme-spec/v3.1.0-rc.6 (2026-10-25)

### Fixes

- state the dispat algorithm as the theorem's executor ([477c928](https://github.com/yohimik/dispat/commit/477c928212d10867f969b57ca637c6b5a3d21bc2)) (by yohimik, Claude Fable 5.1)
  Section 13.11 now states the dispat algorithm beside the dispat theorem:
  a distributed, saga-style executor of a compiled plan whose tasks,
  dependencies and unrecoverable set are known before the first task
  starts, with only durations learned by running. Its six steps are fixing
  the inputs under the locks, compiling the plan and its task graph,
  work-conserving execution with a deterministic order among ready tasks,
  durable records as a saga, resumption by recomputation with adoption,
  and joining a moved branch, each with its cost from the table.

  It is minimal in time in two senses: no execution beats the theorem's
  bound, and no algorithm that learns durations by running tasks can
  guarantee a better factor than the one step 3 attains, shown by the
  adversary that starts the one long task last. It is minimal in memory
  in the ledger's sense: what it retains is the graph, its counters, one
  lease per in-flight task, an output set while a consumer needs it, and
  one record per completed unrecoverable task, each of which the theorem
  shows necessary. Tasks discovered during execution and destinations
  whose irreversibility is not declared are outside the premise.

- state the dispat theorem over compiled plans with unrecoverable tasks ([c7b14d3](https://github.com/yohimik/dispat/commit/c7b14d320d53580a00d7ba04612e52b180f9e961)) (by yohimik, Claude Fable 5.1)
  The theorem's object is now the compiled plan: a finite set of tasks
  with known dependencies and durations, an acyclic precedence relation,
  and a subset of tasks with potential unrecovery, whose effect no later
  action restores. A release plan of section 13 compiles to exactly one
  such plan, with its publications, records and withdrawals in that
  subset; a command sweep and a configured stage compile to one as well.
  The saga clause distinguishes the two kinds of task: a record is
  mandatory for an unrecoverable task, because losing it is the double
  publication, and economical for a recoverable one, whose loss costs its
  own duration again and no more.

- state the dispat theorem on the one fastest release plan ([94591bd](https://github.com/yohimik/dispat/commit/94591bd98b06cf84fd42a17d094f9e57964bc103)) (by yohimik, Claude Fable 5.1)
  Section 13.11 said in passing what any execution of a plan can achieve.
  Those statements are now one named theorem, the dispat theorem. For a
  fixed history, configuration and inventory there is one release plan,
  and no execution of it finishes before the longer of its longest
  precedence chain and its total work divided by the usable slots; a
  placement that never idles a usable slot while a task is ready stays
  within Graham's factor of that bound when slots are interchangeable and
  transfers cost nothing.

  Only a saga reaches the bound across a failure, proved by contradiction:
  an execution that keeps the bound records each completed task durably
  the instant it completes, blocks the consumers of a failure and resumes
  by recomputing from the records. Each other way of executing a release
  ends in a named anomaly (the double publication, the orphan record, the
  unrecorded publication, the dangling record, the stale plan, destination
  lag), and each repeats paid work, never converges or needs a further
  release to repair. The publication-to-record window is the one no record
  closes, and 19.4's reconciliation is the saga's answer to it. A branch
  that moves mid-release is covered the same way: the only execution that
  keeps the bound joins, recording on the planned commit and merging what
  arrived on top; a rebase, an overwrite, a refused push, a replan or a
  rewritten release commit each end in one of those anomalies. These are
  the rules 19.1, 19.3, 19.4 and 17.2 already state. The build readiness
  and distributed placement passages cite the theorem instead of restating
  it, and 19.2a points at it. The theorem is informative and explicitly
  not a conformance obligation.

### Authors

- yohimik
- Claude Fable 5.1


## specs/ccme-spec/v3.1.0-rc.5 (2026-09-25)

### Fixes

- add the prerelease-train form of vector 80d ([2132a8a](https://github.com/yohimik/dispat/commit/2132a8a490013314dda553be92d2bb67e8dc79cd)) (by yohimik, Claude Opus 5.5)
  Vector 80d covered only a consumer that proceeds past a failed provider on its
  stable line. Vector 80e states the same run sequence for a consumer that
  proceeds onto a prerelease train: the commit stays in its train window but
  leaves its fresh window, the train published the commit and not the
  provider's version, so the provider still owes the consumer, E201 and the
  later catch-up apply unchanged, and the bump keeps counting toward the train.
  It names the failure an implementation reading a train-carried contribution
  as delivered produces.

- record the repository as dispat's default mailbox ([61321d4](https://github.com/yohimik/dispat/commit/61321d4e4bcbf0090995764d7c85d9ac575896fd)) (by yohimik, Claude Opus 5.5)
  Section 28.4 leaves it to an implementation to state whether its mailbox
  already holds the repository's objects. The design history now records that
  the dispat engine uses the repository's own remote by default: a worker link
  with no endpoint reaches the push URL of the remote the release is locked on,
  checked against the endpoint rules of section 28.2, and an endpoint remains
  an override. With the repository as the mailbox, the host's branch and tag
  rules are what keep transport credentials away from release refs. The entry
  also records the run identity in the lock, ownership verified before the
  first probe, bounded lock reads, and the lock read before every publication.
  The example of section 28.2 now shows the project's own repository and a
  link that states no endpoint; nothing normative changes.

- describe per-repository serialisation without file locks ([54d64cb](https://github.com/yohimik/dispat/commit/54d64cb3a331d6c75b3550c32f01b4395cbb2743)) (by yohimik, Claude Opus 5.5)
  The polyrepository profile no longer describes per-worktree mutation locks.
  An implementation serialises its own native Git transactions per repository
  within one process and claims no exclusion against other processes; the
  fleet lock coordinates participating runs, and the fixed-input checks still
  detect relevant changes at their validation points without making
  publication atomic with arbitrary writers. An operation that serialises more
  than one repository, like one that holds more than one fleet lock, takes
  them in one stable total order and releases them in reverse, and hooks,
  including those that bracket a fleet-link settlement, run outside those
  transactions.

- restore delivery by ancestry and withdraw the provider receipt ([1f941a6](https://github.com/yohimik/dispat/commit/1f941a6585848417a6963edcf8ca67ade4cb9751)) (by yohimik, Claude Opus 5.5)
  Delivery is read from tags and ancestry again (§13.4a): a source has delivered
  a commit to a target when a release of the source carries the commit and the
  target's baseline reaches that release. The provider receipt of 3.1.0-rc.4,
  which had every consumer release tag name the provider tags it observed, is
  withdrawn, and a tag that carries one is an ordinary release tag whose message
  is not read. The owed windows of §13.3 keep a consumer's debt visible after its
  provider released in a run the consumer sat out, and E201 (§19.3) keeps the one
  state ancestry cannot order, two releases on one commit, from arising unseen.
  The §13.3 admission table, vector 90 and the catch-up text of §13.7a return to
  that rule; vector 90a is withdrawn because vector 80d covers its case. The
  design history records the withdrawal and the implementation of both halves.

- keep a linked peer's version groups repository-local ([77d7fa8](https://github.com/yohimik/dispat/commit/77d7fa8d3f606451c123d2c2b39091aecc273ca7)) (by yohimik, Claude Opus 5.5)
  §27.11 made the active peers of a linked fleet share one case-insensitive
  version-group namespace, joining same-named groups across repositories
  and failing composition when their policies differed. That contradicts
  §27.3, under which every space and version group of an imported
  configuration is local to its repository, and §27.11's own rule that each
  peer is such a root. The linked peer topology applies the
  repository-local spaces and version groups of §27.3 unchanged, and an
  unqualified group selector still selects the matching group of every
  peer.

### Authors

- yohimik
- Claude Opus 5.5


## specs/ccme-spec/v3.1.0-rc.4 (2026-09-23)

### Fixes

- harden fleet release recovery and shared version policies ([dbf10c7](https://github.com/yohimik/dispat/commit/dbf10c73478d0bb9f298d4b1ec19cd6c8b177005)) (by yohimik)

### Authors

- yohimik


## specs/ccme-spec/v3.1.0-rc.3 (2026-09-23)

### Features

- let a command sweep run on workers without a release lock ([d124d42](https://github.com/yohimik/dispat/commit/d124d429a08e1c6272c07114d25487d027fc9e7b)) (by yohimik, Claude Fable 5.1)
  Section 28.2 lets the invocation state execution links under the same
  validation and refusals as the configuration; section 28.3 lets read-only
  planning with links fix the digest and probe, never assign; section 28.4
  lists the run kind; the new section 28.10 defines command sweeps: no
  record, no lock, a generation drawn from a fresh run identity, the task
  frame, the sweep output roots with their merge rules, and vectors 31 to 34.

- record distributed execution as implemented ([b83e259](https://github.com/yohimik/dispat/commit/b83e2595f189e8f12d19973b5cfa1b257533af58)) (by yohimik, Claude Fable 5.1)
  Section 28 stops being an unimplemented profile: dispat implements it, and
  the rules its implementation showed to be missing are stated (local
  execution captured like a worker's, unclaimed work outside capacity, an
  unreadable tip not counted as seen, outermost-first materialization, the
  phase a cancelled attempt stopped in, revocation limited to unadmitted
  branches, the summary shape of a prepared provider). No speedup is claimed.

- name what a consumer's build waits for in its provider ([8212d9e](https://github.com/yohimik/dispat/commit/8212d9e95ee42e7c524f6d7c3d43ebc31c3d3295)) (by yohimik, Claude Fable 5.1)
  §19.2a gives every edge between two packages that build a declared build
  readiness relation, none, build or publish, ordered by what the consumer's
  build waits for. Publication order and blocking are untouched under all
  three, the relation is execution policy and never plan input, build order is
  taken over the whole graph through packages that do not build, and under §28
  a none edge carries no output into the consumer's build task. §13.11 states
  the run durations the three values lead to and that no duration is promised:
  under budgets a weaker relation can lengthen a greedy schedule. Vectors 80b
  and 80c pin one edge under the three relations and the order through a
  package that does not build.

### Fixes

- record the first end-to-end use of command sweeps on a machine the pipeline created ([0ced8c2](https://github.com/yohimik/dispat/commit/0ced8c28752606bcbcd4cc2886781ea96037a8ea)) (by yohimik, Claude Fable 5.1)

- record the reference engine's command sweeps and their two departures ([89d3bb3](https://github.com/yohimik/dispat/commit/89d3bb37f6b054575fe9aff8ddadc0ca872eb9c6)) (by yohimik, Claude Fable 5.1)
  The design history states what the engine does for a sweep on workers, from
  the generation drawn from the run identity to the merge of sweep outputs, and
  keeps two departures: a task placed on the orchestrator is not captured, so
  its conflict with a delegated task goes undetected, and a node predating the
  run kind is not refused at preflight. The README's section 28 bullet gains
  the sweep clause.

- record what the reaching-rule conformance row found in the reference engine ([a58bde3](https://github.com/yohimik/dispat/commit/a58bde384e5dae3e76641b6aaaa3fe097fd32825)) (by yohimik, Claude Fable 5.1)
  The row written for the reaching set found the engine attributing a unit's
  whole source set to every dependent its walk reached; the design history
  says so, that the engine was corrected in the same candidate, and that the
  row stays as its fence.

- answer the report review of the delivery rule, the windows and the cost table ([a5ea5fe](https://github.com/yohimik/dispat/commit/a5ea5fef019745c78a09894c977990f9cb8f16e3)) (by yohimik, Claude Fable 5.1)
  Owed windows are stated per reachable pair with the formula the prose now
  matches; section 9.2 and section 13.7b agree on one reaching and owed set;
  a package whose every cause is owed by a failed provider is not attempted;
  an applicable exact Release-As is a cause of its own; owing is admission in
  every respect but delivery; the stable branch takes a graduation's version
  from section 11.5; the cost table's bound, anomaly order, window count and
  cause test are stated as they are; vectors 80d, 82b1 and 80c and the
  rendering, the fragment and the measurement entry's date and commit are
  corrected.

- record the LLVM measurement of the execution profile ([e383de0](https://github.com/yohimik/dispat/commit/e383de0e0426e003d681b818ec0b78b10d2b6550)) (by yohimik, Claude Fable 5.1)
  The 2026-09-22 entry replaces its "discarded" clause with the matrix pair
  measured today in the shape §28.7 asks for: fixture, nodes, both wall times
  with the per-package times, bytes, warm sources and cold outputs, the product
  comparison, what the pair shows and its caveats, the discarded attempts with
  their causes, and the transport defect they exposed. No speedup of the
  profile or of the engine is claimed beyond what the pairs measured.

- record which parts of the delivery rule the engine implements ([a7bce48](https://github.com/yohimik/dispat/commit/a7bce48e68c47a3292c588f0a6564ffd5839a0d6)) (by yohimik, Claude Fable 5.1)
  The delivery entry states that dispat implements the delivery admission and
  the reconciliation of a proceeding consumer, that owed windows and E201 are
  not yet implemented, and that a consumer sitting out the run in which its
  provider releases the owed commit is therefore still stranded there, as its
  release notes list.

- keep an owed contribution visible and refuse a release that would strand it ([51d96af](https://github.com/yohimik/dispat/commit/51d96afeb9695495b5c92c118e2f35789644e49e)) (by yohimik, Claude Fable 5.1)
  Section 13.3 gains owed windows, so a provider publishing a commit in a run
  its consumer sat out leaves that commit visible to the next plan; section
  19.3 gains E201, which refuses releasing a provider at the baseline commit
  of a consumer it still owes unless that consumer publishes after it in the
  same run, because two tags on one commit have no order and no later plan can
  compute the debt. Section 13.4a states the limitation and the fleet position.

- record the first same-hardware measurement of the execution profile ([b6928a6](https://github.com/yohimik/dispat/commit/b6928a6f79f44d18738075b75f6afcc32571bfaf)) (by yohimik, Claude Fable 5.1)
  The 2026-09-22 entry replaces "no comparison has been made" with the kernel
  farm pairs measured today (one node alone against the same node plus one
  slower worker, same recipe), states their caveats, records the two discarded
  LLVM attempts with their causes, and claims no speedup of the profile.

- state what remains pending now that distributed execution is implemented ([b439e2c](https://github.com/yohimik/dispat/commit/b439e2cdedc0acfd336859663d58f62328220379)) (by yohimik, Claude Fable 5.1)
  The status line named a protocol implementation as pending after both the
  polyrepository and the distributed execution profiles were implemented; it
  now names the adapter and rollback protocols. The 2026-09-21 entry points
  at the record of the implementation, the category departure is narrowed
  to the sites that still lack it, and the placement rules and the two field
  observations of the implementation are recorded.

- let delivery discharge a propagated contribution ([87a1be3](https://github.com/yohimik/dispat/commit/87a1be342a4bfe49d20f181aa233d5765dd76f91)) (by yohimik, Claude Fable 5.1)
  A contribution propagated to a consumer is admitted until a release of the
  provider that carries it is an ancestor of the consumer's baseline, not
  merely while the consumer has not released past the commit: a consumer that
  proceeded on its own past a failed or held provider is otherwise never
  planned again and stays on the old version with no diagnostic.

- allow either realisation of the whole-graph build order ([7918d1b](https://github.com/yohimik/dispat/commit/7918d1b88c1299c5d0d51fc1811fdf18195af28c)) (by yohimik, Claude Fable 5.1)
  §19.2a named a pass-through node per package that does not build as the way
  to keep the build order at O(P + E). A memoised index of the nearest building
  packages each package reaches without a none edge costs the same and leaves a
  scheduler's task set free of nodes that run nothing, so the sentence and the
  §13.11 row now name both and rule out only a search per pair.

- say what is a release record and what centre joining reduces to ([d1e0fc1](https://github.com/yohimik/dispat/commit/d1e0fc110bcd0a82f3e05a7c551b88dedb909c6b)) (by yohimik, Claude Fable 5.1)
  The comparison under the lock reads only what parses as a release tag of a
  workspace package, refuses an unreadable inventory as incomplete history, and
  is skipped for a repository that owns no package and under a setting that
  forgoes reads of the store, which the engine reports. Centre joining is stated
  in the form an engine can observe: the entry's group plus unlinked identities,
  with the composed member nearest a centre as the fallback, and vector 30 now
  describes that shape.

- plan from the store's release records as read under the lock ([8f0f7be](https://github.com/yohimik/dispat/commit/8f0f7be5621c623148b47df0f62b75d5b8f6b52e)) (by yohimik, Claude Fable 5.1)
  A run that can write plans from the authoritative store's release records,
  not from whatever its checkout happened to fetch. After it holds every lock
  and before it fixes its planning input, the engine compares each
  participating store's release records with the ones it is about to plan from:
  a stored record on a commit reachable from the planned head that the input
  lacks is E196, the same package and version at another commit is E191, and
  neither is repaired by planning the package as unreleased or by a silent
  refresh. A release tag is created and never replaced; an identical existing
  tag is the retry of an uncertain write. The Git mapping of the VCS protocol
  says how the built-in driver restores the lock's snapshot condition.

  For distributed execution the text adds what a Git-only transport and remote
  publishers need: recording a publication authorized before a lock was lost
  still proceeds, the run's coordination refs are the evidence an operator
  settles before removing a retained lock, an assignment may carry a deadline
  the worker enforces and an authorization a validity bound, the cost rows and
  the limits of what placement can buy join the complexity section, the minimal
  topology joins groups at their centres to keep the longest route short, and
  the plan function is written in words where its letters collided with the
  package and incidence symbols.

- name coordination branches dispat-worker-<id>-<workinfo> ([859a6bb](https://github.com/yohimik/dispat/commit/859a6bb592fb30dea8b2092d5b526b7f96bd45b3)) (by yohimik, Claude Opus 5)
  The distributed execution profile addresses a coordination branch to the
  node that must find it: the name carries the execution link's node name
  and a diagnostic date and kind beside the random suffix. It is an
  untrusted routing hint that lets a node list only the branches addressed
  to it, while the authenticated manifest stays the authority on node, run,
  task and attempt.

  The section's remaining corrections state what a Git-only transport
  requires. Both parties may advance an attempt's branch, each update under
  the expected previous object ID and attempt ownership. A consumer may
  fetch a ref that advertises a checkpoint and resolve the exact object ID
  locally. Authorization to publish is an explicit single-use step, issued
  after the package's beforePublish hook has completed on the executing
  node, so the revalidation of 27.2 still brackets the publish command when
  that hook runs on a worker. An attempt that was never authorized to
  perform an external effect is fenced by revoking its coordination ref,
  and lock release need not await its acknowledgement. A command
  environment travels as secret references and resolves on the executing
  node. The ownership generation is defined for the Git release-lock
  convention. The six diagnostic categories carry stable identifiers. Every
  reconciliation of a shared manifest or lockfile is an explicit task under
  the orchestrator, and each build binds the prepared state it consumed.
  Hooks that bracket a delegated stage run with it; run-level, recording
  and failure hooks run under the orchestrator. Vectors that assume remote
  publishers, execution-only edges, rollback or cross-run reuse state that
  condition, and an inapplicable vector is neither satisfied nor violated.

### Authors

- yohimik
- Claude Fable 5.1


## specs/ccme-spec/v3.1.0-rc.2 (2026-09-21)

### Features

- define distributed execution and verified output reuse ([80b4d8c](https://github.com/yohimik/dispat/commit/80b4d8c101ce4437ce762284e85f7f2a5125997a)) (by yohimik)
  Specify orchestrator and worker authority, complete locking before planning, task transport, output admission, publication fencing and native recovery. Integrate the optional profile across all three history modes and clarify existing planning bounds and conformance cases. Distributed execution remains an unimplemented specification contract.

- define shared-version groups and their sharing axes ([da64d0f](https://github.com/yohimik/dispat/commit/da64d0f05422d3ba647a22e1f1b3a32b3fff4a10)) (by yohimik, Claude Opus 5)
  The specification used "version group" and "shared-version group" in §13.1,
  §13.11 and §27 without ever defining one, and said nothing about a group
  sitting on a prerelease train, which is where every question a group raises
  becomes hard: whose counter is it, whose channel is it, and what does a retry
  owe.

  §13.9a answers them, normative for an engine that offers groups and
  explicitly optional otherwise. It defines membership, the group baseline and
  line, the three sharing axes and the one combination that has no meaning, the
  engagement table, the member target floor that lets a rider compute a version
  its own window does not explain, the rule that a channel proposal is a
  directive rather than a resting position, assignment under each channel axis,
  and alignment. It states the guarantees honestly: §13.7c holds per member
  under an independent counter, and under a shared one a retry advances the
  counter, so G3 there covers the core.

  §2 gains the two terms, §11.4, §11.5, §13.1 and §13.9 point at the floor,
  §14 lists the key, §15.8 collects the edge cases and Appendix B.12 carries
  the vectors, matching the engine's own tests one to one.

### Fixes

- state where a withheld floor is made up for ([b2b5b14](https://github.com/yohimik/dispat/commit/b2b5b14fceb8f553c6099106c0d294da8a30e3d7)) (by yohimik)
  §13.9a's alignment rule said a releasing laggard under an independent counter
  is raised by the floor, which is not the whole answer: the member the floor
  declines to lift is brought to the prefix by alignment. §15.8 and B.12 gain
  the case.

- correct the hand-edited-tag vector of B.12 ([bdf7e47](https://github.com/yohimik/dispat/commit/bdf7e47e239601e1d0ed4aff06640bdebabb7d1d)) (by yohimik)
  Vector 143 put the hand-edited tag on the member that was also the group's
  own baseline, which is not the case it meant to describe: the guard then
  fires against the group's computation and, being repository-scoped, aborts
  the run before any member is assigned. The vector now states a member below
  the group's baseline and above its line, which is what produces the E185 it
  claims, and names the other outcome rather than leaving it to be discovered.

- describe three history modes without a publishing repository ([c9b34f1](https://github.com/yohimik/dispat/commit/c9b34f1335dc0a78efdb1f8f4ecbdd090120aea4)) (by yohimik, Claude Fable 5.1)
  §27 now opens with the three modes an engine runs in: a single history,
  specified histories, and histories discovered through a topology. The
  control repository is only the name of the repository holding the entry
  configuration of the second mode. It is not where releases come from:
  packages publish from their own paths, a checkpoint is optional evidence,
  a source may release on its own in the first mode, and the profile does not
  fix where the engine is invoked. Vector 28 of §27.10 pins the standalone
  source release.

  Wording that implied otherwise is gone: the checkpoint "after each source
  release", the "control run", the "central hub topology", and the last three
  "saga" phrasings of §27.11.

  No release plan changes.

- admit settlement heads and tighten fleet cost bounds ([a7b35ab](https://github.com/yohimik/dispat/commit/a7b35abcbbabca01fef68fbc22418f9099460f64)) (by yohimik, Claude Fable 5.1)
  A settlement moves heads before the revalidation point of §27.2, the
  consumer's own included, and §27.2 admitted only a native record step, so a
  literal reading refused every settled consumer with E330. The exact
  revision a settlement wrote is now an admitted transition, which is what the
  engine already does; vector 28 of §27.12 and the docs page say so.

  The cost model is corrected where it was loose or silent. The publication
  input closure is one condensation and one bitset union per edge, not one
  traversal per repository. A fresh window is the meet of two single-boundary
  windows, so reachability classes count distinct boundary commits and never
  (stable, fresh) pairs, and a window is keyed by the repository it ranges over
  and not by the owner of the package reading it (vector 29). The linked peer
  topology gains the rows it never had: link evidence and settlement, with a
  star bounding every settlement at two commits.

  No release plan changes.

### Authors

- yohimik
- Claude Opus 5


## specs/ccme-spec/v3.1.0-rc.1 (2026-09-20)

### Fixes

- verify the heading the specification has ([2068769](https://github.com/yohimik/dispat/commit/206876905db716a7e48415c28d640560e61b1300)) (by yohimik, Claude Fable 5.1)
  verify.sh required "### 27.11 Choreographed saga", which the specification
  renamed to "Linked peer topology", so the release failed at the beforeVersion
  hook after other packages had already published. test.sh only ever verified a
  hand-written stand-in for SPEC.md and could not notice; it now runs the
  verifier against the real document first.

- describe linked release ownership without saga selectors ([35664e1](https://github.com/yohimik/dispat/commit/35664e1685e703c347233421f9faefdba8b1622a)) (by yohimik)

- clarify fleet locking and partial release recovery ([2f591b0](https://github.com/yohimik/dispat/commit/2f591b0139339d13f20c4718056d14be6e0dea98)) (by yohimik)

### Authors

- yohimik
- Claude Fable 5.1


## specs/ccme-spec/v3.1.0-rc.0 (2026-09-15)

### Features

- define polyrepository Git planning ([7e38ae1](https://github.com/yohimik/dispat/commit/7e38ae131734159acb7011a835219f765c46a983)) (by yohimik)
  Add the optional profile for repository identities, fixed histories, consumer
  boundaries, source records, fleet locks and bounded planning indexes.

### Authors

- yohimik


## specs/ccme-spec/v3.0.2 (2026-09-09)

### Fixes

- clarify commit validation ([4c22611](https://github.com/yohimik/dispat/commit/4c22611dc02c6a88e39fae48f9b3184cdb3ae2fe)) (by yohimik)

### Authors

- yohimik


## specs/ccme-spec/v3.0.1 (2026-09-06)

No changes: a version set by Release-As.


## specs/ccme-spec/v3.0.0 (2026-09-06)

No changes: a version set by Release-As.


## specs/ccme-spec/v2.0.0 (2026-09-05)

### Breaking Changes

- separate the specification ([1c96acd](https://github.com/yohimik/dispat/commit/1c96acda11e7e5efefe32a7dcb410a841c76fc02)) (by yohimik)

### Fixes

- use native version replacement ([417fdd5](https://github.com/yohimik/dispat/commit/417fdd5467b279216fcdb8008c6de2a5def797bc)) (by yohimik)

- stamp before building ([6b29eb6](https://github.com/yohimik/dispat/commit/6b29eb6dde09adb4dcaa822f403ec7a912581706)) (by yohimik)

### Authors

- yohimik
