# Experiment measurements and formula reporting

This reporting profile accompanies [CCME §13.11 and §28.7](./SPEC.md). The keywords MUST, MUST NOT and SHOULD
apply to a report claiming to apply those formulas. This is a measurement contract, not a new executor,
performance guarantee or claim that existing measurements contain every required input.

## 1. Evidence before calculation

Each trial MUST identify the source, binary, toolchain, fixture/protocol revision, selected configuration,
workload, execution mode and actual topology. Retain the graph and raw timing/resource records, or immutable
references with digests to them. A graph captured before execution, an instrumented runtime graph and a
post-run reconstruction are different evidence sources and MUST be labelled. Configuration is a recorded
input, not a measured duration. Counts are calculated from the retained graph, not inferred from machine
count, artifact count or a tool name. Never include credentials in this evidence.

One trial has a unique run and repetition identity. Repetitions MUST NOT overwrite previous records. Record
failures, timeouts, blocked/unsupported trials and exclusions alongside successes. Aggregate only comparable
trials, state the statistic and sample count, and retain dispersion and the exclusion rule. A single sample
supports a recorded observation, not a claim about variability or universal superiority.

## 2. Additive report schema

An experiment cell MAY carry an `executionMeasurement` object. When present its `schema` is
`ccme-execution-measurement/v1`; its fields below have these semantics. This block is independent of an older
`timing` or `llvm` block. Absence means legacy evidence, not zero values. Unknown numeric values are `null`
with a reason; JSON nonfinite numbers, negative durations and inconsistent counts are invalid. Additional
fields may carry more evidence without changing these meanings.

| Field | Required content and meaning |
| --- | --- |
| `trial` | `id`, `repetition`, `status`, tool/mode/profile, pinned source/binary/protocol/configuration identities, and raw `evidence` entries (`id`, `path` or `uri`, `sha256`). Status is `completed`, `failed`, `timeout`, `blocked` or `unsupported`; it is not inferred from a duration. |
| `graph` | `id`, `scope`, `provenance`, `vertices`, `edges`, `omittedWork`. Scope is `native-task-graph`, `analysis-projection` or `fixture-workload`; do not interchange them. Vertices have unique `id`, timed `operation`, `isVirtual`, and `durationInputs` identifying the interval or explicit phase aggregation supplying `d_i`. Directed edges have `from`, `to` and prerequisite `kind`. Retain evidence for the graph and its derivation. |
| `graph.counts` | `vertices`, `edges`, `virtualVertices`, `timedVertices`, calculated from that graph. `timedVertices` counts unique nonvirtual vertices with complete accepted duration coverage, not attempts; untimed real vertices remain in `vertices`. Count each retained typed edge once; parallel edge kinds are disclosed. Package-graph or inner-tool counts, if available, belong in separately identified graphs. |
| `resources.nodes` | Executor identity, independently evidenced `hostId`/VM identity and topology, configured and admitted task slots, eligibility/placement, hardware/CPU quota, memory and relevant limits. Unknown hardware values remain null. Two process names do not establish two hosts. |
| `resources.pools` | Each resource's `id`, unit, configured capacity, evidenced admitted capacity/availability and applicable tasks/weights. Run stage, node, recording and owner limits remain separate. Record changes; do not collapse variable capacity into an invented constant. |
| `resources.taskSlotLimit` | `{value, evidence, reason}`: a positive integer upper bound on simultaneous modeled unit-slot tasks for the declared interval, or null. Its derivation must account for admission, compatibility and binding limits. Interchangeability is a separate premise needed for the one-pool upper bound. |
| `resources.usableOnePoolSlots` | `{value, evidence, reason}`: the fixed positive number of actually available interchangeable slots throughout the modeled schedule, or null. A configured ceiling or loose upper bound alone is insufficient for this field. |
| `resources.nested` | Per-task inner build parameters, such as Ninja job limit, link pool, target backend and host architecture. Inner jobs, vCPUs and outer task slots are different units. |
| `taskIntervals` | One record per measured physical attempt/phase: `taskId`, `attemptId`, `nodeId`, `status`, `operation`, `durationSeconds`, `clockDomain`, `clockKind`, optional `startSeconds`/`endSeconds`, and `evidence`. Clock kind is `monotonic`, `wall` or `bounded-mapping`. These coordinates must use the stated duration clock; optional wall timestamps are separately labelled metadata. State the exact operation endpoints. Count attempts separately from graph vertices. |
| `timeWindows` | Named `{id, boundary, seconds, clockDomain, clockKind, evidence}` records for release-command span, modeled-task span, coordinator interval, setup, teardown or other phases. A `step-sum` also specifies included step IDs/phase/kind and aggregation convention. Do not label a sum of disjoint commands as end-to-end elapsed. |
| `observedPeakOverlap` | `{value, evidence, reason}` for the maximum simultaneous measured intervals on a comparable timeline, or null. It is an observation, never a substitute for `taskSlotLimit`. |
| `analysis` | Records `{id, formula, inputs, value, unit, status, reasons, model}`. Inputs reference this block's fields/evidence or earlier calculations. Status is `computed`, `unavailable` or `inapplicable`; the latter two require null value and reasons. Model distinguishes `descriptive`, `observed-cap-diagnostic`, `fixed-duration-lower-bound` and `one-pool-upper-bound`. |

Arrays may instead be represented by immutable evidence references with counts and digests when too large
for an inline report. The reader MUST be able to recover the exact vertex/edge/task records used to calculate
the displayed values. Counts alone cannot reconstruct a graph or its critical path. Serialized reports,
Go/TypeScript readers and rendered views MUST preserve the same fields and null/status semantics.

An input reference is `{source: "measurement", pointer: "/path/to/value"}` within this block, or
`{source: "evidence", evidenceId: "retained-id", pointer: "/path/to/value"}` within the identified JSON
evidence. Pointers start at the selected document root, use slash-separated object keys or zero-based array
indices, and escape `~` as `~0` and `/` as `~1`. `analysis.inputs` maps formula symbols to these references;
`durationInputs` lists them. An aggregation also identifies its rule and constituent phases, preserves the
declared operation boundary and does not count nested/overlapping durations twice. The upper-bound model
additionally requires that the resulting vertex represents a nonpreemptive task, not disconnected fragments.

## 3. Durations, clocks and coverage

For a failure-free, completely observed modeled DAG, each nonvirtual vertex has exactly one successful
measured duration `d_i >= 0` for its stated operation. A declared virtual relay may have duration zero; an
untimed real task MUST NOT be replaced by a zero-duration relay. The report checks graph acyclicity, endpoint
identities, required task coverage and the applicable precedence/resource constraints. Extra attempts and
failed or skipped required work are recorded, not silently removed to obtain a faster successful graph.

Each local elapsed duration uses one monotonic clock. Cross-host work/time arithmetic additionally requires
documented compatible elapsed-time scales and measurement uncertainty; monotonicity alone does not calibrate
clock rates. On that basis durations can be summed as work-seconds without comparing their clock origins.
A global task span or peak overlap needs comparable start
and finish coordinates. Unaligned host wall clocks or serialized timestamps stripped of their monotonic
context do not establish that comparability. With no mapping, those quantities remain null even when local
durations are known. A bounded clock mapping may produce interval estimates with explicit uncertainty; it
MUST NOT be presented as exact cross-host latency. Historical wall-clock measurements keep their provenance
and limitations; a tolerance for rounding does not repair an unbounded clock error.

For comparable task coordinates, `T_task = max_i(end_i) - min_i(start_i)`. This is the span of the named
modeled operations, not necessarily the whole assigned commands or their lease lifetimes. An empty graph
has no measured task interval, so `T_task` is null even though its model has `W = L = 0`. `T_release` is
the span from the first selected release command's start to the last selected release command's end.
`T_coordinator` is one coordinator-clock interval with explicitly named endpoints. It may include startup,
coordination, transport and verification. A reported step sum is `sum_j(end_j - start_j)` over its declared
steps; gaps and unselected setup are excluded. Do not sum overlapping phase durations and call the sum a
wall interval. New derived values use unrounded inputs and round once for display. Legacy sums of rounded
step values may remain, identified as such; do not silently change their aggregation convention.

An enclosing coordinator interval can contain every modeled operation without cross-node timestamp
subtraction; the dispatch/result protocol and local endpoints must establish that containment. Its excess
over a projected task span is not automatically pure scheduler overhead: omitted work, startup, validation
and transfer may also contribute. Failure elapsed time may be shown as a diagnostic but is not a completed
trial duration. A timeout threshold is a configured limit, not a measured completion time.

## 4. Formula records

The following are the exact formula semantics; all work and time quantities use seconds. They are evaluated
per trial on one identified graph and realized duration vector. Placement, contention or caching can change
those durations on another schedule or host; these are not predictions of such a different experiment.

| Analysis `id` | Formula and applicability |
| --- | --- |
| `work` | `W = sum_i d_i`. Requires complete durations of the declared modeled vertices. This is work-seconds, not wall time. |
| `critical-path` | `F_i = d_i + max_{j -> i} F_j`, with empty maximum zero; `L = max_i F_i`. Evaluate topologically and retain one attaining path. For an empty graph, `W = L = 0`. |
| `slot-lower-bound` | `B_slot = max(L, W/s)`, where `s = resources.taskSlotLimit.value`. Requires an evidenced unit-slot concurrency upper bound and valid modeled precedence. Unknown `s` means this result is unavailable; `L` remains a separate span lower bound when its premises hold. |
| `resource-lower-bound` | `B_resource = max(L, max_r W_r/c_r)`. Each fixed positive capacity `c_r` bounds a declared resource and `W_r` is its occupied resource-unit seconds. If task `i` holds weight `w_ir` throughout its modeled duration, `W_r = sum_i w_ir*d_i`; otherwise retain measured/declared holding intervals. Include recording/owner reservations explicitly and identify a window containing all counted intervals. Overlapping resources contribute a maximum, not a sum. |
| `task-ratio` | `R_task = T_task/B`, identifying the selected valid lower-bound record `B`. Requires a comparable task span and `B > 0`. |
| `enclosing-ratio` | `R_enclosing = T_enclosing/B`, identifying the release or coordinator interval and its containment evidence. Label the numerator; this is not the task-only ratio or a scheduler-efficiency percentage. |
| `one-pool-upper-bound` | `U = W/s + (1 - 1/s)*L`, with `s = resources.usableOnePoolSlots.value`, not merely a concurrency ceiling. Only under all §13.11 clause 2 premises: one fixed available interchangeable unit-slot pool, fixed nonpreemptive tasks, immediate ready zero-time closure, no extra binding resource/placement restriction, zero transfer/scheduling overhead and work conservation. It bounds modeled task time, not an enclosing interval with unmodeled work. |
| `observed-cap-diagnostic` | `B_obs = max(L, W/s_obs)` and the explicitly named numerator divided by `B_obs`, where `s_obs` is observed peak overlap. It describes that graph/run at its observed overlap. It is not a lower bound for schedules allowed more capacity or a cross-tool speed ranking. |

An unavailable or violated premise MUST be listed, not assumed from a mode name, two executors, a successful
exit or a small ratio. In particular, distributed placement, weighted/overlapping budgets, owner lanes,
transport and failures do not inherit the one-pool upper bound. Preserve known measurements on a failed or
retried trial, but mark successful failure-free schedule ratios/guarantees inapplicable; attempt-work totals
may be shown separately. Division by a zero bound is undefined and MUST produce null, not zero or infinity.
For a resource bound, every counted holding interval must lie inside the interval being bounded; a bound
including reservations outside `T_task` cannot be compared with that narrower task span. Variable resource
availability may supply a looser constant bound only when the chosen capacity is an evidenced upper limit
throughout that interval. Observed peak use or minimum observed free capacity is not that evidence.
Do not display `T/B` as speedup. Cross-tool or before/after speedup is `T_reference/T_candidate` only with
matching declared timing boundaries and a disclosed comparable workload, result, resource and cache policy.

## 5. Standard release timing mapping

The existing standard timing fields retain their historical meanings:
`work = W`, `criticalPath = L`, `parallelism = s_obs`, `bound = B_obs`,
`makespanSeconds = T_release`, and `ratio = T_release/B_obs`. These are not silently renamed to use configured
capacity. Render them as observed-overlap diagnostics alongside the new inputs/formula block.

The standard analysis graph is a projection with a build and publication vertex per package and declared
readiness relation `build`, `publish` or `none`; absent kinds are explicit zero-duration relays under that
projection's construction only when those operations are excluded or nonexistent in its declared scope.
A required modeled operation with missing timing remains incomplete. Retain the actual graph used for the critical-path recurrence, including relays
and edges through them. `taskTimes.length`, the package count and `consumerBuilds.edges` are not its full
vertex/edge counts. The last counts provider/consumer build pairs only. Builds and registry upload intervals
do not include every sign/version/record/coordination operation, so this projection is not the native engine
graph. A projected lower bound may bound the enclosing release while leaving substantial work unmodeled.

Capture manifest dependencies, selected release plan and readiness configuration before the measured
release, or label a later reconstruction honestly. Do not infer missing pre-run inputs from rewritten
post-release manifests. Preserve each tool's actual graph and policy; do not force different release sets
to appear equal. A refused initial attempt and a completed release are different outcomes. Recovery can
converge while failing a specific behavior assertion; assertion counts from different check sets are not
comparable correctness percentages.

## 6. LLVM and other nested builds

The declared LLVM fixture workload has two independent requests, one enabling X86 and one enabling AArch64.
If the retained fixture confirms that shape, its outer workload graph has `|V| = 2`, `|E| = 0`. These are
enabled compiler backends, not the architectures of the machines executing the build. The native executor
graph and the internal Ninja compiler DAG have separate identities and counts; without exports they are
unknown. Never describe the two-request graph as all LLVM compilation operations.

An analysis projection may time each request's CMake configuration plus Ninja build and output-directory
creation, omitting artifact copy/check/return phases. With those exact local durations `d_X` and `d_A`,
`W = d_X + d_A` and `L = max(d_X, d_A)`. If an evidenced outer task-slot cap is two,
`B_slot = max(L, W/2) = L`, because `(d_X + d_A)/2 <= max(d_X, d_A)`.
This identity does not prove measured wall time equals `L`. Distinct eligible placement or other constraints
can make the one-pool upper-bound premises inapplicable even though the lower bound remains useful.

Ninja `-j2` and a per-task link pool of one are nested parameters. Two outer executors each using two inner
jobs do not supply four outer task slots or prove four available CPUs. Record configured task capacity,
actual admitted compatible capacity, CPU quota, memory and host identities separately. Cloud VM provisioning
is distinct from the tool's scheduler: a manually sharded CLI invocation MUST be labelled as custom
orchestration, not evidence of a tool-native distributed mode.

Retain each task manifest and its timing before disposable workspace cleanup. In the existing LLVM runner,
the task's recorded `seconds` covers CMake configuration, Ninja and output-directory creation, ending before artifact copy and
peer-side executable checks. The existing `llvm.seconds` covers peer startup through coordinator artifact
verification, excluding fixture/source/tool/client preparation and deferred peer shutdown. Neither is an
unqualified full-experiment time. Record new phase endpoints explicitly; a changed timer boundary requires
a new labelled measurement, not reinterpretation of an older value. The timeout's start boundary may differ
from the completion timer's boundary and must be recorded too.

Preserve full/smoke target lists, disabled LLVM subprojects/tests, source revision, toolchain/options, cache
state, actual node/host placement and returned-artifact verification. Smoke measurements cannot stand in for
full builds. Blocked/unsupported cells retain reasons and null completion calculations. For separate hosts,
local durations may supply `W` and `L` while global task span/overlap stays unavailable; a coordinator-clock
enclosing ratio is permitted only with the stated containment and lower-bound evidence.

## 7. Display and validation

Show, in separately labelled tables or panels: (1) captured workload/graph sizes and resource inputs,
(2) measured task/phase intervals and outcomes, and (3) formula expressions, their selected inputs,
applicability/reasons and derived values. Make raw records accessible. Show uncertainty, excluded phases and
unknown values beside the affected result. Preserve each graph's scope and each time window's boundary in
compact views; a table labelled only "time", "nodes" or "ratio" is insufficient.

Before rendering, check unique IDs and valid references, DAG/count consistency, complete duration coverage,
finite values, clock compatibility, resource-capacity units, numerator containment, and recomputation of all
displayed formula values with a stated rounding tolerance. Recorded violations remain visible and disable
the affected model claim. A passed parser or arithmetic test validates the report calculation, not all
executor behavior or a performance result that has not been measured.
