(* SPDX-License-Identifier: GPL-3.0-or-later
   Copyright (c) 2026 yohimik *)

(* Optional exact checks for the ideal task-time claims in SPEC.md section 13.11.
   Run from any directory: wolframscript -file specs/ccme-spec/verify-math.wls
   Finite enumeration below is evidence for small cases, not a proof for all DAGs. *)

Print["Wolfram Language: ", $Version];

SetAttributes[requireTrue, HoldRest];
requireTrue[label_, expression_] := Module[{answer},
  answer = Check[TimeConstrained[expression, 180, $TimedOut], $Failed];
  If[TrueQ[answer],
    Print["PASS ", label],
    Print["FAIL ", label, ": ", InputForm[answer]];
    Exit[1]
  ]
];

requireTrue["weighted slot capacity: W <= s T implies T >= W/s",
  FullSimplify[W <= s T \[Implies] T >= W/s,
    Element[{W, s, T}, Reals] && W >= 0 && s > 0]];
requireTrue["independent work and span lower bounds",
  FullSimplify[(T >= W/s && T >= L) \[Implies] T >= Max[L, W/s],
    Element[{W, L, s, T}, Reals] && W >= 0 && L >= 0 && s > 0]];
requireTrue["Graham idle-capacity step",
  Reduce[Element[{s, T, W, L, idle}, Reals] && s >= 1 && W >= 0 &&
    L >= 0 && idle >= 0 && s T == W + idle && idle <= (s - 1) L &&
    T > W/s + (1 - 1/s) L, {s, T, W, L, idle}, Reals] === False];
requireTrue["Graham expression under one-pool assumptions",
  FullSimplify[W/s + (1 - 1/s) L <= (2 - 1/s) Max[L, W/s],
    Element[{W, L, s}, Reals] && W >= 0 && L >= 0 && s >= 1]];
requireTrue["uniform-speed scaling of work and span",
  FullSimplify[PiecewiseExpand[Max[L/v, W/(h c v)] - Max[L, W/(h c)]/v,
      Element[{W, L, h, c, v}, Reals] && W >= 0 && L >= 0 && h > 0 && c > 0 && v > 0] == 0,
    Element[{W, L, h, c, v}, Reals] && W >= 0 && L >= 0 && h > 0 && c > 0 && v > 0]];
requireTrue["a fixed chain ignores additional slots",
  FullSimplify[L/s + (1 - 1/s) L == L,
    Element[{L, s}, Reals] && L >= 0 && s >= 1]];

(* Vertices are numbered in one topological order; every edge points upward. *)
span[edges_, durations_] := Module[{n = Length[durations], predecessors, finish},
  predecessors = Table[First /@ Select[edges, Last[#] == vertex &], {vertex, n}];
  finish = ConstantArray[0, n];
  Do[
    finish[[vertex]] = durations[[vertex]] +
      If[predecessors[[vertex]] === {}, 0, Max[finish[[predecessors[[vertex]]]]]],
    {vertex, n}
  ];
  Max[finish]
];

(* Nonpreemptive, work-conserving event simulation with a fixed ready priority. *)
listTime[edges_, durations_, slots_, priority_] := Module[
  {n = Length[durations], predecessors, done, running = {}, clock = 0,
   active, ready, started, next, completed},
  predecessors = Table[First /@ Select[edges, Last[#] == vertex &], {vertex, n}];
  done = ConstantArray[False, n];
  While[!And @@ done,
    active = If[running === {}, {}, running[[All, 1]]];
    ready = Select[priority, Function[vertex,
      !done[[vertex]] && !MemberQ[active, vertex] &&
        AllTrue[predecessors[[vertex]], done[[#]] &]]];
    started = Take[ready, Min[slots - Length[running], Length[ready]]];
    running = Join[running, ({#, clock + durations[[#]]} & /@ started)];
    If[running === {}, Break[]];
    next = Min[running[[All, 2]]];
    completed = Select[running, Last[#] == next &];
    Do[done[[job[[1]]]] = True, {job, completed}];
    running = Select[running, Last[#] > next &];
    clock = next;
  ];
  If[And @@ done, clock, $Failed]
];

finiteCount = 0;
graphCount = 0;
finiteResult = Check[TimeConstrained[Catch[
  Do[
    allEdges = Subsets[Range[n], {2}];
    priorities = DeleteDuplicates[{Range[n], Reverse[Range[n]], RotateLeft[Range[n], 1]}];
    Do[
      graphCount++;
      Do[
        work = Total[durations];
        longest = span[edges, durations];
        Do[
          Do[
            observed = listTime[edges, durations, slots, priority];
            finiteCount++;
            If[!TrueQ[observed >= Max[longest, work/slots] &&
                observed <= work/slots + (1 - 1/slots) longest],
              Throw[{n, edges, durations, slots, priority, observed,
                longest, work}, "finiteCounterexample"]];
            If[!TrueQ[listTime[edges, durations, n, priority] == longest],
              Throw[{"ASAP", n, edges, durations, priority, longest},
                "finiteCounterexample"]],
            {priority, priorities}],
          {slots, 1, Min[n, 3]}],
        {durations, Tuples[{1, 2}, n]}],
      {edges, Subsets[allEdges]}],
    {n, 1, 4}];
  True,
  "finiteCounterexample"
], 180, $TimedOut], $Failed];
If[finiteResult =!= True,
  Print["FAIL bounded DAG list bounds and enough-slot ASAP: ", InputForm[finiteResult]];
  Exit[1]];
Print["PASS bounded DAG list bounds and enough-slot ASAP"];
Print["Finite DAG checks: ", graphCount,
  " topologically labelled DAGs, ", finiteCount,
  " duration/slot/priority schedules; 1 <= n <= 4, durations in {1,2}, slots <= 3."];
requireTrue["all-zero chain and independent jobs finish at time zero",
  span[{{1, 2}, {2, 3}}, {0, 0, 0}] == 0 &&
    listTime[{{1, 2}, {2, 3}}, {0, 0, 0}, 1, {1, 2, 3}] == 0 &&
    listTime[{}, {0, 0, 0}, 3, {1, 2, 3}] == 0];

requireTrue["sharp list-priority family for s = 2..12",
  And @@ Table[
    Module[{durations = Join[ConstantArray[1, slots (slots - 1)], {slots}],
      count = slots (slots - 1) + 1},
      Total[durations] == slots^2 && span[{}, durations] == slots &&
        listTime[{}, durations, slots, Range[count]] == 2 slots - 1 &&
        listTime[{}, durations, slots, Prepend[Range[count - 1], count]] == slots
    ],
    {slots, 2, 12}]];
requireTrue["finite packing gap: three unit jobs on two slots",
  Max[span[{}, {1, 1, 1}], 3/2] == 3/2 &&
    listTime[{}, {1, 1, 1}, 2, {1, 2, 3}] == 2];
requireTrue["independent equal-job integer packing, n <= 20",
  And @@ Flatten[Table[
    listTime[{}, ConstantArray[d, n], slots, Range[n]]/v ==
      (d/v) Ceiling[n/slots],
    {n, 1, 20}, {slots, 1, 8}, {d, 1, 4}, {v, 1, 3}]]];

(* Width uses transitive reachability: counting comparable tasks as an
   antichain would give a false sufficient saturation threshold. *)
horizontalZeroCount = 0;
horizontalOneCount = 1;
horizontalZeroWork = 0;
horizontalZeroDuration = 0;
horizontalZeroRate = 0;
horizontalOneSpeedup = 1;
horizontalPairSize = 2;
horizontalMinimumVertexCount = 1;
horizontalMaximumVertexCount = 4;
horizontalReverseChainVertexCount = 3;
horizontalShortDuration = 1;
horizontalLongDuration = 2;
horizontalPackingLongestDuration = 3;
horizontalPackingSlotCount = 3;
horizontalFiniteLimitSeconds = 180;
horizontalWidthCounterexampleTag = "horizontalWidthCounterexample";
horizontalWidthLabel = "bounded width slots attain weighted DAG span";
horizontalReverseWidthLabel = "reverse-labeled reachability preserves poset width";
horizontalCrossoverLabel = "work/span crossover is only a lower-bound crossover";
horizontalPackingWitnessLabel = "work/span crossover can precede ideal saturation";
horizontalTailLabel = "serial setup model rises by a after task-time saturation";
horizontalImprovementLabel = "exact improvement requires saved compute to exceed added setup";
horizontalSharedBoundLabel = "shared mandatory bottleneck contributes Q/R lower bound";
horizontalTransferLabel = "mandatory edge delays augment the path lower bound";
horizontalWidthReportPrefix = "Horizontal width checks: ";
horizontalWidthReportSeparator = " DAGs, ";
horizontalWidthReportSuffix = " schedules.";

posetWidth[edges_, vertexCount_] := Module[{closure, antichains},
  closure = TransitiveClosureGraph[
    Graph[Range[vertexCount], DirectedEdge @@@ edges]];
  antichains = Select[Subsets[Range[vertexCount]],
    Function[vertices, AllTrue[Subsets[vertices, {horizontalPairSize}],
      Function[pair, !EdgeQ[closure, DirectedEdge @@ pair] &&
        !EdgeQ[closure, DirectedEdge @@ Reverse[pair]]]]]];
  Max[Length /@ antichains]
];
requireTrue[horizontalReverseWidthLabel,
  posetWidth[{{horizontalReverseChainVertexCount, horizontalPairSize},
    {horizontalPairSize, horizontalOneCount}},
    horizontalReverseChainVertexCount] == horizontalOneCount];

horizontalWidthGraphCount = horizontalZeroCount;
horizontalWidthScheduleCount = horizontalZeroCount;
horizontalWidthResult = Check[TimeConstrained[Catch[
  Do[
    horizontalCandidateEdges = Subsets[Range[horizontalVertexCount],
      {horizontalPairSize}];
    horizontalPriorities = DeleteDuplicates[{
      Range[horizontalVertexCount], Reverse[Range[horizontalVertexCount]],
      RotateLeft[Range[horizontalVertexCount], horizontalOneCount]}];
    Do[
      horizontalWidthGraphCount++;
      horizontalWidth = posetWidth[horizontalEdges, horizontalVertexCount];
      Do[
        horizontalExpectedSpan = span[horizontalEdges, horizontalDurations];
        Do[
          horizontalObservedTime = listTime[horizontalEdges,
            horizontalDurations, horizontalWidth, horizontalPriority];
          horizontalWidthScheduleCount++;
          If[horizontalObservedTime =!= horizontalExpectedSpan,
            Throw[{horizontalVertexCount, horizontalEdges,
              horizontalDurations, horizontalWidth, horizontalPriority,
              horizontalObservedTime, horizontalExpectedSpan},
              horizontalWidthCounterexampleTag]],
          {horizontalPriority, horizontalPriorities}],
        {horizontalDurations, Tuples[
          {horizontalShortDuration, horizontalLongDuration},
          horizontalVertexCount]}],
      {horizontalEdges, Subsets[horizontalCandidateEdges]}],
    {horizontalVertexCount, horizontalMinimumVertexCount,
      horizontalMaximumVertexCount}];
  True,
  horizontalWidthCounterexampleTag
], horizontalFiniteLimitSeconds, $TimedOut], $Failed];
requireTrue[horizontalWidthLabel, horizontalWidthResult === True];
Print[horizontalWidthReportPrefix, horizontalWidthGraphCount,
  horizontalWidthReportSeparator, horizontalWidthScheduleCount,
  horizontalWidthReportSuffix];

requireTrue[horizontalCrossoverLabel,
  FullSimplify[
    (W/(h c v) <= L/v) \[Equivalent] (h >= W/(c L)),
    Element[{W, L, h, c, v}, Reals] && W >= horizontalZeroWork &&
      L > horizontalZeroDuration && h > horizontalZeroCount &&
      c > horizontalZeroCount && v >= horizontalOneSpeedup]];
horizontalPackingDurations = {horizontalPackingLongestDuration,
  horizontalLongDuration, horizontalLongDuration, horizontalLongDuration};
horizontalPackingAllSlots = Length[horizontalPackingDurations];
requireTrue[horizontalPackingWitnessLabel,
  Module[{packingWork = Total[horizontalPackingDurations],
    packingSpan = Max[horizontalPackingDurations]},
    packingWork/horizontalPackingSlotCount == packingSpan &&
      listTime[{}, horizontalPackingDurations, horizontalPackingSlotCount,
        Range[horizontalPackingAllSlots]] > packingSpan &&
      listTime[{}, horizontalPackingDurations, horizontalPackingAllSlots,
        Range[horizontalPackingAllSlots]] == packingSpan
  ]];

horizontalRunTime[hostCount_, taskCount_, slotsPerHost_, taskDuration_,
    speedup_, setupPerAdditionalHost_] :=
  (taskDuration/speedup) Ceiling[taskCount/(hostCount slotsPerHost)] +
    setupPerAdditionalHost (hostCount - horizontalOneCount);
horizontalModelAssumptions = Element[{h, n, c}, Integers] &&
  h >= horizontalOneCount && n >= horizontalOneCount &&
  c >= horizontalOneCount && Element[{d, v, a}, Reals] &&
  d > horizontalZeroDuration && v >= horizontalOneSpeedup &&
  a > horizontalZeroDuration;
requireTrue[horizontalTailLabel,
  FullSimplify[
    horizontalRunTime[h, n, c, d, v, a] ==
      d/v + a (h - horizontalOneCount) &&
    horizontalRunTime[h + horizontalOneCount, n, c, d, v, a] -
      horizontalRunTime[h, n, c, d, v, a] == a,
    horizontalModelAssumptions && h >= Ceiling[n/c]]];
requireTrue[horizontalImprovementLabel,
  FullSimplify[
    (horizontalRunTime[h + horizontalOneCount, n, c, d, v, a] <
      horizontalRunTime[h, n, c, d, v, a]) \[Equivalent]
      ((d/v) (Ceiling[n/(h c)] -
        Ceiling[n/((h + horizontalOneCount) c)]) > a),
    horizontalModelAssumptions]];

(* Mandatory serial work and delayed predecessor edges may overlap in elapsed
   time; their maximum is a lower bound, while their sum could double count. *)
requireTrue[horizontalSharedBoundLabel,
  FullSimplify[
    (T >= Q/R && T >= L) \[Implies] T >= Max[Q/R, L],
    Element[{T, Q, R, L}, Reals] && Q >= horizontalZeroWork &&
      R > horizontalZeroRate && L >= horizontalZeroDuration]];
requireTrue[horizontalTransferLabel,
  Reduce[
    Element[{taskStartA, taskStartB, taskStartC, taskDurationA,
      taskDurationB, taskDurationC, transferDelayAB, transferDelayBC,
      completionTime}, Reals] &&
    taskDurationA >= horizontalZeroDuration &&
    taskDurationB >= horizontalZeroDuration &&
    taskDurationC >= horizontalZeroDuration &&
    transferDelayAB >= horizontalZeroDuration &&
    transferDelayBC >= horizontalZeroDuration &&
    taskStartA >= horizontalZeroDuration &&
    taskStartB >= taskStartA + taskDurationA + transferDelayAB &&
    taskStartC >= taskStartB + taskDurationB + transferDelayBC &&
    completionTime >= taskStartC + taskDurationC &&
    completionTime < taskDurationA + transferDelayAB + taskDurationB +
      transferDelayBC + taskDurationC,
    {taskStartA, taskStartB, taskStartC, taskDurationA, taskDurationB,
      taskDurationC, transferDelayAB, transferDelayBC, completionTime},
    Reals] === False];
(* Two independent jobs start together at time zero: the duration-1 job
   occupies the sole task slot, and the duration-100 record occupies the
   separate recording unit without taking a task slot. *)
requireTrue["separate record capacity invalidates aggregate W/s",
  Module[{slotWork = 1, recordWork = 100, slotCapacity = 1,
    recordCapacity = 1, slotStart = 0, recordStart = 0,
    witnessSpan, scheduleMakespan, separateBound, falseAggregateBound},
    witnessSpan = Max[slotWork, recordWork];
    scheduleMakespan = Max[slotStart + slotWork, recordStart + recordWork];
    separateBound = Max[witnessSpan, slotWork/slotCapacity,
      recordWork/recordCapacity];
    falseAggregateBound = (slotWork + recordWork)/slotCapacity;
    scheduleMakespan == 100 && scheduleMakespan == separateBound &&
      scheduleMakespan < falseAggregateBound
  ]];

oldVisits[p_] := p (3 p - 1)/2;
newVisits[p_] := 2 p - 1;
requireTrue["chain-window visit formulas",
  FullSimplify[Sum[3 i - 2, {i, 1, p}] == oldVisits[p] &&
    p + (p - 1) == newVisits[p], Element[p, Integers] && p >= 1]];
(* Observed AuthorScans/op on the 2026-10-25 chain-window benchmark,
   before and after the verified rank/author-prefix implementation. *)
measuredBefore = <|64 -> 6112, 128 -> 24512, 256 -> 98176|>;
measuredAfter = <|64 -> 127, 128 -> 255, 256 -> 511|>;
requireTrue["chain-window formulas match measured AuthorScans/op",
  And @@ Table[oldVisits[p] == measuredBefore[p] &&
    newVisits[p] == measuredAfter[p], {p, Keys[measuredBefore]}]];
Print["Chain-window AuthorScans/op: ",
  Table[{p, measuredBefore[p], measuredAfter[p]}, {p, Keys[measuredBefore]}]];

(* Finite illustration of the node-player game, not an equilibrium theorem.
   Each independent unit task is eligible only on its own node. The
   orchestrator has already dispatched both at time zero and cannot reroute
   or duplicate them, so the two node start delays are the only actions. *)
requireTrue["bounded node game: ASAP and an inefficient weak equilibrium",
  Module[{actions = Tuples[Range[0, 2], 2], payoff, isNash},
    payoff[choice_] := -Max[choice + {1, 1}];
    isNash[choice_] := And @@ Flatten[Table[
      payoff[choice] >= payoff[ReplacePart[choice, node -> delay]],
      {node, 1, 2}, {delay, 0, 2}]];
    isNash[{0, 0}] && payoff[{0, 0}] == Max[payoff /@ actions] &&
      isNash[{1, 1}] && payoff[{1, 1}] < payoff[{0, 0}]
  ]];
Print["All optional CCME mathematical checks passed."];
