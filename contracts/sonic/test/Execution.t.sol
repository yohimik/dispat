// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.28;

import {CCMETypes as Types} from "../src/CCMETypes.sol";
import {DispatPlanner} from "../src/DispatPlanner.sol";
import {DispatExecution as Execution} from "../src/DispatExecution.sol";
import {DispatTaskGraph as TaskGraph} from "../src/DispatTaskGraph.sol";
import {CCMEHistory as History} from "../src/CCMEHistory.sol";
import {DispatFederation as Federation} from "../src/DispatFederation.sol";

contract ExecutionActor {
    function callExecution(Execution execution, bytes calldata callData) external returns (bool, bytes memory) {
        return address(execution).call(callData);
    }
}

contract ExecutionTest {
    Execution private execution;
    ExecutionActor private firstNode;
    ExecutionActor private secondNode;

    function setUp() public {
        execution = new Execution(new DispatPlanner());
        firstNode = new ExecutionActor();
        secondNode = new ExecutionActor();
        _actorSuccess(firstNode, abi.encodeCall(execution.registerCapacity, (uint16(1))));
        _actorSuccess(secondNode, abi.encodeCall(execution.registerCapacity, (uint16(1))));
    }

    function testAllModesProduceIdenticalPlanAndCompleteSameGraph() public {
        bytes32 inputHash;
        bytes32 planHash;
        bytes32 graphHash;
        for (uint8 i; i < 3; ++i) {
            Execution.Mode mode = Execution.Mode(i);
            Execution.Start memory request = _createStart(mode, "repository");
            uint256 runId = execution.start(request);
            Execution.Run memory run = execution.getRun(runId);
            if (i == 0) {
                inputHash = run.inputHash;
                planHash = run.planHash;
                graphHash = run.graphHash;
            }
            require(
                run.inputHash == inputHash && run.planHash == planHash && run.graphHash == graphHash,
                "mode changed semantics"
            );
            _completeCommand(runId, mode, request.tasks[0], Execution.Outcome.Success);
            _completeEffect(runId, 1, mode, request.tasks[1], Execution.Outcome.Success);
            _completeEffect(runId, 2, mode, request.tasks[2], Execution.Outcome.Success);
            execution.finish(runId);
            require(
                execution.getRun(runId).isFinished && execution.repositoryRun("repository") == 0,
                "run did not release lock"
            );
            for (uint16 taskIndex; taskIndex < 3; ++taskIndex) {
                require(
                    execution.getTask(runId, taskIndex).status == Execution.Status.Succeeded, "mode changed outcome"
                );
            }
        }
    }

    function testAllNineHistoryAndExecutionModeCombinations() public {
        Federation federation = new Federation(execution.planner());
        for (uint8 historyMode; historyMode < 3; ++historyMode) {
            Federation.Input memory federationInput = _createFederationInput(Federation.Mode(historyMode));
            (Types.Snapshot memory snapshot, History.State memory history) = federation.prepare(federationInput);
            Federation.Result memory expected = federation.compute(federationInput);
            uint256 packageCount = historyMode == 0 ? 1 : 2;
            require(
                snapshot.packages.length == packageCount && expected.plan.publishOrder.length == packageCount,
                "history selection incorrect"
            );
            bytes32 planHash = keccak256(abi.encode(expected.plan));
            bytes32 inputHash = keccak256(abi.encode(snapshot, history));
            for (uint8 executionMode; executionMode < 3; ++executionMode) {
                Execution.Start memory request = _createStart(Execution.Mode(executionMode), "alpha");
                request.snapshot = snapshot;
                request.tasks = _createIndependentLifecycles(packageCount);
                request.repositories = new bytes32[](packageCount);
                request.repositories[0] = "alpha";
                if (packageCount == 2) request.repositories[1] = "beta";
                uint256 runId = execution.startPrepared(request, history);
                Execution.Run memory run = execution.getRun(runId);
                require(run.planHash == planHash && run.inputHash == inputHash, "execution mode changed federated plan");
                require(run.graphHash == keccak256(abi.encode(request.tasks)), "execution mode changed lifecycle graph");
                _completeLifecycle(runId, request.mode, request.tasks);
                execution.finish(runId);
                require(
                    execution.repositoryRun("alpha") == 0 && execution.repositoryRun("beta") == 0,
                    "combined run retained locks"
                );
            }
        }
        require(execution.runCount() == 9, "mode matrix incomplete");
    }

    function testModeEndpointsAndDescriptorCommitmentAreEnforced() public {
        Execution.Start memory request = _createStart(Execution.Mode.Interpretation, "interpreted");
        uint256 runId = execution.start(request);
        _actorReverts(
            firstNode, abi.encodeCall(execution.claimCompiled, (runId, uint16(0))), Execution.WrongMode.selector
        );
        _assertReverts(abi.encodeCall(execution.getCompiledTask, (runId, uint16(0))), Execution.WrongMode.selector);
        TaskGraph.Descriptor memory forged = request.tasks[0];
        forged.commandHash = "forged-command";
        _actorReverts(
            firstNode,
            abi.encodeCall(execution.claimInterpreted, (runId, uint16(0), forged)),
            Execution.InvalidTask.selector
        );
        require(execution.activeClaims(address(firstNode)) == 0, "forged descriptor acquired capacity");

        request = _createStart(Execution.Mode.Hybrid, "hybrid");
        runId = execution.start(request);
        _actorReverts(
            firstNode,
            abi.encodeCall(execution.claimInterpreted, (runId, uint16(0), request.tasks[0])),
            Execution.WrongMode.selector
        );
        _assertReverts(abi.encodeCall(execution.claimCompiled, (runId, uint16(1))), Execution.WrongMode.selector);
        TaskGraph.Descriptor memory stored = execution.getCompiledTask(runId, 0);
        require(keccak256(abi.encode(stored)) == keccak256(abi.encode(request.tasks[0])), "compiled descriptor changed");
    }

    function testProposalsDoNotSatisfyDependenciesUntilAccepted() public {
        Execution.Start memory request = _createStart(Execution.Mode.Compilation, "repository");
        uint256 runId = execution.start(request);
        uint8 attempt = _claimCommand(runId, request.mode, request.tasks[0]);
        _assertReverts(abi.encodeCall(execution.claimCompiled, (runId, uint16(1))), Execution.NotReady.selector);
        _actorSuccess(
            firstNode,
            abi.encodeCall(
                execution.proposeResult, (runId, uint16(0), attempt, Execution.Outcome.Success, bytes32("build-result"))
            )
        );
        _assertReverts(abi.encodeCall(execution.claimCompiled, (runId, uint16(1))), Execution.NotReady.selector);
        require(execution.activeClaims(address(firstNode)) == 1, "unaccepted output freed capacity");
        execution.acceptResult(runId, 0, attempt, "build-result");
        require(execution.activeClaims(address(firstNode)) == 0, "accepted output retained capacity");
        require(execution.claimCompiled(runId, 1) == 1, "publish not ready after acceptance");
    }

    function testEffectsAndAcceptanceRemainOrchestratorOwned() public {
        Execution.Start memory request = _createStart(Execution.Mode.Compilation, "repository");
        _actorReverts(firstNode, abi.encodeCall(execution.start, (request)), Execution.Unauthorized.selector);
        uint256 runId = execution.start(request);
        _actorReverts(
            firstNode, abi.encodeCall(execution.claimCompiled, (runId, uint16(1))), Execution.Unauthorized.selector
        );
        _actorReverts(
            firstNode,
            abi.encodeCall(execution.acceptResult, (runId, uint16(0), uint8(1), bytes32("result"))),
            Execution.Unauthorized.selector
        );
        _actorReverts(firstNode, abi.encodeCall(execution.cancel, (runId)), Execution.Unauthorized.selector);
        _actorReverts(firstNode, abi.encodeCall(execution.finish, (runId)), Execution.Unauthorized.selector);
        ExecutionActor outsider = new ExecutionActor();
        _actorSuccess(outsider, abi.encodeCall(execution.registerCapacity, (uint16(2))));
        _actorReverts(
            outsider, abi.encodeCall(execution.claimCompiled, (runId, uint16(0))), Execution.Unauthorized.selector
        );
    }

    function testCapacityIsSharedAcrossRunsAndCannotShrinkBelowClaims() public {
        Execution.Start memory firstRequest = _createStart(Execution.Mode.Compilation, "first");
        Execution.Start memory secondRequest = _createStart(Execution.Mode.Compilation, "second");
        uint256 firstRun = execution.start(firstRequest);
        uint256 secondRun = execution.start(secondRequest);
        _claimCommand(firstRun, firstRequest.mode, firstRequest.tasks[0]);
        _actorReverts(
            firstNode,
            abi.encodeCall(execution.claimCompiled, (secondRun, uint16(0))),
            Execution.CapacityExceeded.selector
        );
        _actorReverts(
            firstNode, abi.encodeCall(execution.registerCapacity, (uint16(0))), Execution.CapacityExceeded.selector
        );
        _actorSuccess(secondNode, abi.encodeCall(execution.claimCompiled, (secondRun, uint16(0))));
        require(
            execution.activeClaims(address(firstNode)) == 1 && execution.activeClaims(address(secondNode)) == 1,
            "capacity attribution"
        );
        _actorSuccess(firstNode, abi.encodeCall(execution.registerCapacity, (uint16(2))));
        Execution.Start memory thirdRequest = _createStart(Execution.Mode.Compilation, "third");
        uint256 thirdRun = execution.start(thirdRequest);
        _claimCommand(thirdRun, thirdRequest.mode, thirdRequest.tasks[0]);
        _actorReverts(
            firstNode, abi.encodeCall(execution.registerCapacity, (uint16(1))), Execution.CapacityExceeded.selector
        );
    }

    function testStaleAttemptWrongExecutorAndResultReplayAreRejected() public {
        Execution.Start memory request = _createStart(Execution.Mode.Compilation, "repository");
        uint256 runId = execution.start(request);
        uint8 attempt = _claimCommand(runId, request.mode, request.tasks[0]);
        _actorReverts(
            secondNode,
            abi.encodeCall(
                execution.proposeResult, (runId, uint16(0), attempt, Execution.Outcome.Success, bytes32("result"))
            ),
            Execution.Unauthorized.selector
        );
        _actorReverts(
            firstNode,
            abi.encodeCall(
                execution.proposeResult, (runId, uint16(0), uint8(0), Execution.Outcome.Success, bytes32("result"))
            ),
            Execution.StaleAttempt.selector
        );
        _actorReverts(
            firstNode,
            abi.encodeCall(execution.proposeResult, (runId, uint16(0), attempt, Execution.Outcome.Success, bytes32(0))),
            Execution.InvalidTask.selector
        );
        _actorSuccess(
            firstNode,
            abi.encodeCall(
                execution.proposeResult, (runId, uint16(0), attempt, Execution.Outcome.Success, bytes32("result"))
            )
        );
        _actorReverts(
            firstNode,
            abi.encodeCall(
                execution.proposeResult, (runId, uint16(0), attempt, Execution.Outcome.Success, bytes32("result"))
            ),
            Execution.StaleAttempt.selector
        );
        _assertReverts(
            abi.encodeCall(execution.acceptResult, (runId, uint16(0), attempt, bytes32("different"))),
            Execution.StaleAttempt.selector
        );
        execution.acceptResult(runId, 0, attempt, "result");
        _assertReverts(
            abi.encodeCall(execution.acceptResult, (runId, uint16(0), attempt, bytes32("result"))),
            Execution.StaleAttempt.selector
        );
    }

    function testOnlyFailedCommandsRetryAndAttemptsAreBounded() public {
        Execution.Start memory request = _createStart(Execution.Mode.Compilation, "repository");
        request.maxAttempts = 2;
        uint256 runId = execution.start(request);
        _completeCommand(runId, request.mode, request.tasks[0], Execution.Outcome.Failure);
        _assertReverts(abi.encodeCall(execution.claimCompiled, (runId, uint16(1))), Execution.NotReady.selector);
        execution.retryCommand(runId, 0);
        uint8 attempt = _claimCommand(runId, request.mode, request.tasks[0]);
        require(attempt == 2, "retry reused attempt identity");
        _actorReverts(
            firstNode,
            abi.encodeCall(
                execution.proposeResult, (runId, uint16(0), uint8(1), Execution.Outcome.Success, bytes32("stale"))
            ),
            Execution.StaleAttempt.selector
        );
        _actorSuccess(
            firstNode,
            abi.encodeCall(
                execution.proposeResult, (runId, uint16(0), attempt, Execution.Outcome.Failure, bytes32("failed-again"))
            )
        );
        execution.acceptResult(runId, 0, attempt, "failed-again");
        _assertReverts(abi.encodeCall(execution.retryCommand, (runId, uint16(0))), Execution.NotReady.selector);
        execution.cancel(runId);
        execution.finish(runId);
        _assertReverts(abi.encodeCall(execution.retryCommand, (runId, uint16(0))), Execution.NotReady.selector);
    }

    function testPublicationFailureCannotBeRetried() public {
        Execution.Start memory request = _createStart(Execution.Mode.Compilation, "repository");
        uint256 runId = execution.start(request);
        _completeCommand(runId, request.mode, request.tasks[0], Execution.Outcome.Success);
        _completeEffect(runId, 1, request.mode, request.tasks[1], Execution.Outcome.Failure);
        _assertReverts(abi.encodeCall(execution.retryCommand, (runId, uint16(1))), Execution.NotReady.selector);
        _assertReverts(abi.encodeCall(execution.claimCompiled, (runId, uint16(2))), Execution.NotReady.selector);
    }

    function testUnknownCommandQuarantinesCapacityAndLocksUntilReconciled() public {
        Execution.Start memory request = _createStart(Execution.Mode.Compilation, "repository");
        uint256 runId = execution.start(request);
        _completeCommand(runId, request.mode, request.tasks[0], Execution.Outcome.Unknown);
        require(execution.activeClaims(address(firstNode)) == 1, "uncertain process capacity freed");
        _assertReverts(abi.encodeCall(execution.retryCommand, (runId, uint16(0))), Execution.NotReady.selector);
        execution.cancel(runId);
        _assertReverts(abi.encodeCall(execution.finish, (runId)), Execution.NotReady.selector);
        _assertReverts(abi.encodeCall(execution.start, (request)), Execution.InvalidRun.selector);
        _actorReverts(
            firstNode,
            abi.encodeCall(execution.resolveUnknown, (runId, uint16(0), false, bytes32("inspected"))),
            Execution.Unauthorized.selector
        );
        execution.resolveUnknown(runId, 0, false, "inspected");
        require(execution.activeClaims(address(firstNode)) == 0, "reconciled process capacity retained");
        execution.finish(runId);
        require(execution.repositoryRun("repository") == 0, "reconciled run lock retained");
        _assertReverts(
            abi.encodeCall(execution.resolveUnknown, (runId, uint16(0), false, bytes32("again"))),
            Execution.NotReady.selector
        );
    }

    function testLostWorkerCanBeQuarantinedWithoutItsResponse() public {
        Execution.Start memory request = _createStart(Execution.Mode.Compilation, "repository");
        uint256 runId = execution.start(request);
        _assertReverts(
            abi.encodeCall(execution.markUnknown, (runId, uint16(0), uint8(0), bytes32("lost"))),
            Execution.NotReady.selector
        );
        uint8 attempt = _claimCommand(runId, request.mode, request.tasks[0]);
        _actorReverts(
            firstNode,
            abi.encodeCall(execution.markUnknown, (runId, uint16(0), attempt, bytes32("lost"))),
            Execution.Unauthorized.selector
        );
        _assertReverts(
            abi.encodeCall(execution.markUnknown, (runId, uint16(0), uint8(0), bytes32("lost"))),
            Execution.StaleAttempt.selector
        );
        execution.markUnknown(runId, 0, attempt, "connection-lost");
        require(
            execution.getTask(runId, 0).status == Execution.Status.Unknown
                && execution.activeClaims(address(firstNode)) == 1,
            "lost worker not quarantined"
        );
        _actorReverts(
            firstNode,
            abi.encodeCall(
                execution.proposeResult, (runId, uint16(0), attempt, Execution.Outcome.Success, bytes32("late"))
            ),
            Execution.StaleAttempt.selector
        );
        execution.resolveUnknown(runId, 0, true, "verified-output");
        require(execution.activeClaims(address(firstNode)) == 0, "reconciliation retained capacity");
        require(execution.claimCompiled(runId, 1) == 1, "verified output failed to unblock publish");
    }

    function testCancellationStopsClaimsButLetsCurrentCommandSettle() public {
        Execution.Start memory request = _createStart(Execution.Mode.Compilation, "repository");
        uint256 runId = execution.start(request);
        uint8 attempt = _claimCommand(runId, request.mode, request.tasks[0]);
        execution.cancel(runId);
        _assertReverts(abi.encodeCall(execution.finish, (runId)), Execution.NotReady.selector);
        _actorSuccess(
            firstNode,
            abi.encodeCall(
                execution.proposeResult, (runId, uint16(0), attempt, Execution.Outcome.Success, bytes32("settled"))
            )
        );
        execution.acceptResult(runId, 0, attempt, "settled");
        _assertReverts(abi.encodeCall(execution.claimCompiled, (runId, uint16(1))), Execution.NotReady.selector);
        execution.finish(runId);
        require(execution.getRun(runId).isCancelled && execution.getRun(runId).isFinished, "cancellation not finalized");
    }

    function testCancelledPublicationStillRequiresDurableRecord() public {
        Execution.Start memory request = _createStart(Execution.Mode.Compilation, "repository");
        uint256 runId = execution.start(request);
        _completeCommand(runId, request.mode, request.tasks[0], Execution.Outcome.Success);
        uint8 attempt = execution.claimCompiled(runId, 1);
        execution.cancel(runId);
        execution.proposeResult(runId, 1, attempt, Execution.Outcome.Success, "published");
        execution.acceptResult(runId, 1, attempt, "published");
        _assertReverts(abi.encodeCall(execution.finish, (runId)), Execution.NotReady.selector);
        require(execution.repositoryRun("repository") == runId, "unrecorded publication lost lock");
        _completeEffect(runId, 2, request.mode, request.tasks[2], Execution.Outcome.Success);
        execution.finish(runId);
        require(execution.repositoryRun("repository") == 0, "recorded cancellation retained lock");
    }

    function testUnknownPublicationCannotRetryAndMustBeRecordedAfterReconciliation() public {
        Execution.Start memory request = _createStart(Execution.Mode.Hybrid, "repository");
        uint256 runId = execution.start(request);
        _completeCommand(runId, request.mode, request.tasks[0], Execution.Outcome.Success);
        _completeEffect(runId, 1, request.mode, request.tasks[1], Execution.Outcome.Unknown);
        _assertReverts(abi.encodeCall(execution.retryCommand, (runId, uint16(1))), Execution.NotReady.selector);
        execution.cancel(runId);
        _assertReverts(abi.encodeCall(execution.finish, (runId)), Execution.NotReady.selector);
        execution.resolveUnknown(runId, 1, true, "registry-identity-verified");
        _assertReverts(abi.encodeCall(execution.finish, (runId)), Execution.NotReady.selector);
        _completeEffect(runId, 2, request.mode, request.tasks[2], Execution.Outcome.Success);
        execution.finish(runId);
    }

    function testPublicationRecordRepairDoesNotRepublish() public {
        Execution.Start memory request = _createStart(Execution.Mode.Compilation, "repository");
        uint256 runId = execution.start(request);
        _completeCommand(runId, request.mode, request.tasks[0], Execution.Outcome.Success);
        _completeEffect(runId, 1, request.mode, request.tasks[1], Execution.Outcome.Success);
        _completeEffect(runId, 2, request.mode, request.tasks[2], Execution.Outcome.Failure);
        execution.cancel(runId);
        _assertReverts(abi.encodeCall(execution.finish, (runId)), Execution.NotReady.selector);
        _actorReverts(
            firstNode,
            abi.encodeCall(execution.reconcileRecord, (runId, uint16(2), bytes32("receipt"))),
            Execution.Unauthorized.selector
        );
        _assertReverts(
            abi.encodeCall(execution.reconcileRecord, (runId, uint16(2), bytes32(0))), Execution.NotReady.selector
        );
        execution.reconcileRecord(runId, 2, "immutable-receipt-verified");
        require(execution.getTask(runId, 1).attempt == 1, "publication replayed during record repair");
        require(execution.getTask(runId, 2).status == Execution.Status.Succeeded, "receipt repair not recorded");
        execution.finish(runId);
        _assertReverts(
            abi.encodeCall(execution.reconcileRecord, (runId, uint16(2), bytes32("again"))), Execution.NotReady.selector
        );
    }

    function testPreparedInputHashIncludesNormalizedHistory() public {
        Execution.Start memory request = _createStart(Execution.Mode.Hybrid, "first");
        History.State memory history = this.analyzeHistory(request.snapshot);
        uint256 firstRun = execution.startPrepared(request, history);
        require(
            execution.getRun(firstRun).inputHash == keccak256(abi.encode(request.snapshot, history)),
            "history missing from input commitment"
        );
        request.repositories[0] = "second";
        history.diagnostics = new Types.Diagnostic[](1);
        history.diagnostics[0] = Types.Diagnostic(192, 0, type(uint16).max);
        uint256 secondRun = execution.startPrepared(request, history);
        require(
            execution.getRun(firstRun).inputHash != execution.getRun(secondRun).inputHash,
            "different prepared input reused commitment"
        );
    }

    function testFuzzBadAcknowledgementCannotFreeCapacity(uint8 wrongAttempt, bytes32 wrongResult) public {
        if (wrongAttempt == 1 && wrongResult == bytes32("proposed-output")) return;
        Execution.Start memory request = _createStart(Execution.Mode.Compilation, "repository");
        uint256 runId = execution.start(request);
        uint8 attempt = _claimCommand(runId, request.mode, request.tasks[0]);
        _actorSuccess(
            firstNode,
            abi.encodeCall(
                execution.proposeResult,
                (runId, uint16(0), attempt, Execution.Outcome.Success, bytes32("proposed-output"))
            )
        );
        _assertReverts(
            abi.encodeCall(execution.acceptResult, (runId, uint16(0), wrongAttempt, wrongResult)),
            Execution.StaleAttempt.selector
        );
        require(execution.activeClaims(address(firstNode)) == 1, "bad acknowledgement freed capacity");
        require(execution.getTask(runId, 0).status == Execution.Status.Proposed, "bad acknowledgement changed task");
        execution.acceptResult(runId, 0, attempt, "proposed-output");
        require(execution.activeClaims(address(firstNode)) == 0, "valid acknowledgement retained capacity");
    }

    function testFuzzRetriesPreservePlanAndGraphCommitments(uint8 modeSeed, uint8 limitSeed) public {
        Execution.Mode mode = Execution.Mode(modeSeed % 3);
        Execution.Start memory request = _createStart(mode, "repository");
        request.maxAttempts = 1 + limitSeed % 4;
        uint256 runId = execution.start(request);
        Execution.Run memory original = execution.getRun(runId);
        for (uint8 expectedAttempt = 1; expectedAttempt <= request.maxAttempts; ++expectedAttempt) {
            _completeCommand(runId, mode, request.tasks[0], Execution.Outcome.Failure);
            require(execution.getTask(runId, 0).attempt == expectedAttempt, "retry identity drift");
            require(execution.activeClaims(address(firstNode)) == 0, "failed attempt retained capacity");
            if (expectedAttempt < request.maxAttempts) execution.retryCommand(runId, 0);
        }
        _assertReverts(abi.encodeCall(execution.retryCommand, (runId, uint16(0))), Execution.NotReady.selector);
        Execution.Run memory finalRun = execution.getRun(runId);
        require(
            finalRun.inputHash == original.inputHash && finalRun.planHash == original.planHash
                && finalRun.graphHash == original.graphHash,
            "retry mutated semantic commitments"
        );
    }

    function analyzeHistory(Types.Snapshot calldata snapshot) external pure returns (History.State memory) {
        return History.analyze(snapshot);
    }

    function testRepositoryLocksAreAtomicSortedAndExclusive() public {
        Execution.Start memory request = _createStart(Execution.Mode.Compilation, "repository");
        uint256 runId = execution.start(request);
        _assertReverts(abi.encodeCall(execution.start, (request)), Execution.InvalidRun.selector);
        require(
            execution.runCount() == 1 && execution.repositoryRun("repository") == runId, "failed start mutated locks"
        );
        Execution.Start memory invalid = _createStart(Execution.Mode.Compilation, "unused");
        invalid.repositories = new bytes32[](2);
        invalid.repositories[0] = "z";
        invalid.repositories[1] = "a";
        _assertReverts(abi.encodeCall(execution.start, (invalid)), Execution.InvalidRun.selector);
        require(execution.repositoryRun("z") == 0 && execution.runCount() == 1, "partial lock acquisition leaked");
        execution.cancel(runId);
        execution.finish(runId);
        require(execution.start(request) == 2, "finished repository remained locked");
    }

    function testMalformedTaskTopologyIsRejected() public {
        Execution.Start memory request = _createStart(Execution.Mode.Compilation, "repository");
        request.tasks[0].dependencies = 1;
        _assertGraphReject(request, 0);
        request = _createStart(Execution.Mode.Compilation, "repository");
        request.tasks[1].dependencies = 4;
        _assertGraphReject(request, 1);
        request = _createStart(Execution.Mode.Compilation, "repository");
        request.tasks[0].packageIndex = 1;
        _assertGraphReject(request, 0);
        request = _createStart(Execution.Mode.Compilation, "repository");
        request.tasks[1].commandHash = 0;
        _assertGraphReject(request, 1);
        require(execution.runCount() == 0 && execution.repositoryRun("repository") == 0, "bad graph acquired run state");
    }

    function testTaskGraphRequiresAllLifecycleTasksAndPrerequisites() public {
        Execution.Start memory request = _createStart(Execution.Mode.Compilation, "repository");
        request.tasks[1].dependencies = 0;
        _assertGraphReject(request, 1);
        request = _createStart(Execution.Mode.Compilation, "repository");
        request.tasks[2].dependencies = 1;
        _assertGraphReject(request, 1);
        request = _createStart(Execution.Mode.Compilation, "repository");
        request.tasks[2].kind = TaskGraph.Kind.Publish;
        _assertGraphReject(request, 2);
        request = _createStart(Execution.Mode.Compilation, "repository");
        request.tasks = new TaskGraph.Descriptor[](1);
        request.tasks[0] = TaskGraph.Descriptor(0, TaskGraph.Kind.Command, "build-only", 0);
        _assertGraphReject(request, 0);
    }

    function testProviderBuildAndRecordMustPrecedeConsumer() public {
        Execution.Start memory request = _createStart(Execution.Mode.Hybrid, "repository");
        request.snapshot.packages = new Types.Package[](2);
        request.snapshot.packages[0] = Types.Package("consumer", Types.Version(0, 1, 0, 0, 0), true);
        request.snapshot.packages[1] = Types.Package("provider", Types.Version(0, 1, 0, 0, 0), true);
        request.snapshot.units[0].scope = 3;
        request.snapshot.edges = new Types.Edge[](1);
        request.snapshot.edges[0] = Types.Edge(1, 0, true, true);
        request.tasks = new TaskGraph.Descriptor[](6);
        request.tasks[0] = TaskGraph.Descriptor(1, TaskGraph.Kind.Command, "provider-build", 0);
        request.tasks[1] = TaskGraph.Descriptor(1, TaskGraph.Kind.Publish, "provider-publish", 1);
        request.tasks[2] = TaskGraph.Descriptor(1, TaskGraph.Kind.Record, "provider-record", 2);
        request.tasks[3] = TaskGraph.Descriptor(0, TaskGraph.Kind.Command, "consumer-build", 1);
        request.tasks[4] = TaskGraph.Descriptor(0, TaskGraph.Kind.Publish, "consumer-publish", 12);
        request.tasks[5] = TaskGraph.Descriptor(0, TaskGraph.Kind.Record, "consumer-record", 16);
        request.tasks[4].dependencies = 8;
        _assertGraphReject(request, 4);
        request.tasks[4].dependencies = 12;
        request.tasks[3].dependencies = 0;
        _assertGraphReject(request, 3);
        request.tasks[3].dependencies = 1;
        uint256 runId = execution.start(request);
        _actorReverts(
            secondNode, abi.encodeCall(execution.claimCompiled, (runId, uint16(3))), Execution.NotReady.selector
        );
        _completeCommand(runId, request.mode, request.tasks[0], Execution.Outcome.Success);
        _actorSuccess(secondNode, abi.encodeCall(execution.claimCompiled, (runId, uint16(3))));
        _assertReverts(
            abi.encodeCall(execution.claimInterpreted, (runId, uint16(4), request.tasks[4])),
            Execution.NotReady.selector
        );
    }

    function _assertGraphReject(Execution.Start memory request, uint256 index) private {
        (bool isSuccessful, bytes memory reason) = address(execution).call(abi.encodeCall(execution.start, (request)));
        require(
            !isSuccessful
                && keccak256(reason) == keccak256(abi.encodeWithSelector(TaskGraph.InvalidTaskGraph.selector, index)),
            "wrong task graph rejection"
        );
    }

    function _completeLifecycle(uint256 runId, Execution.Mode mode, TaskGraph.Descriptor[] memory descriptors) private {
        for (uint16 taskIndex; taskIndex < descriptors.length; ++taskIndex) {
            TaskGraph.Descriptor memory descriptor = descriptors[taskIndex];
            if (descriptor.kind != TaskGraph.Kind.Command) {
                _completeEffect(runId, taskIndex, mode, descriptor, Execution.Outcome.Success);
                continue;
            }
            bytes memory claimData = mode == Execution.Mode.Interpretation
                ? abi.encodeCall(execution.claimInterpreted, (runId, taskIndex, descriptor))
                : abi.encodeCall(execution.claimCompiled, (runId, taskIndex));
            uint8 attempt = abi.decode(_actorSuccess(firstNode, claimData), (uint8));
            _actorSuccess(
                firstNode,
                abi.encodeCall(
                    execution.proposeResult,
                    (runId, taskIndex, attempt, Execution.Outcome.Success, bytes32("combined-output"))
                )
            );
            execution.acceptResult(runId, taskIndex, attempt, "combined-output");
        }
    }

    function _createIndependentLifecycles(uint256 packageCount)
        private
        pure
        returns (TaskGraph.Descriptor[] memory descriptors)
    {
        descriptors = new TaskGraph.Descriptor[](packageCount * 3);
        for (uint16 packageIndex; packageIndex < packageCount; ++packageIndex) {
            uint256 firstTask = uint256(packageIndex) * 3;
            descriptors[firstTask] = TaskGraph.Descriptor(packageIndex, TaskGraph.Kind.Command, "build", 0);
            descriptors[firstTask + 1] =
                TaskGraph.Descriptor(packageIndex, TaskGraph.Kind.Publish, "publish", uint256(1) << firstTask);
            descriptors[firstTask + 2] =
                TaskGraph.Descriptor(packageIndex, TaskGraph.Kind.Record, "record", uint256(1) << (firstTask + 1));
        }
    }

    function _createFederationInput(Federation.Mode mode)
        private
        view
        returns (Federation.Input memory federationInput)
    {
        federationInput.mode = mode;
        federationInput.specifiedRepositories = mode == Federation.Mode.Specified ? 3 : 0;
        federationInput.repositories = new Federation.Repository[](2);
        for (uint256 i; i < 2; ++i) {
            Execution.Start memory local = _createStart(Execution.Mode.Compilation, "unused");
            local.snapshot.packages[0].name = i == 0 ? "a" : "b";
            federationInput.repositories[i].identity = i == 0 ? bytes32("alpha") : bytes32("beta");
            federationInput.repositories[i].peers = i == 0 ? 2 : 1;
            federationInput.repositories[i].snapshot = local.snapshot;
            federationInput.repositories[i].bumpTargets = new uint256[](1);
            federationInput.repositories[i].channelTargets = new uint256[](1);
        }
    }

    function _completeCommand(
        uint256 runId,
        Execution.Mode mode,
        TaskGraph.Descriptor memory descriptor,
        Execution.Outcome outcome
    ) private {
        uint8 attempt = _claimCommand(runId, mode, descriptor);
        _actorSuccess(
            firstNode,
            abi.encodeCall(execution.proposeResult, (runId, uint16(0), attempt, outcome, bytes32("command-result")))
        );
        execution.acceptResult(runId, 0, attempt, "command-result");
    }

    function _claimCommand(uint256 runId, Execution.Mode mode, TaskGraph.Descriptor memory descriptor)
        private
        returns (uint8)
    {
        if (mode == Execution.Mode.Interpretation) {
            return abi.decode(
                _actorSuccess(firstNode, abi.encodeCall(execution.claimInterpreted, (runId, uint16(0), descriptor))),
                (uint8)
            );
        }
        return
            abi.decode(_actorSuccess(firstNode, abi.encodeCall(execution.claimCompiled, (runId, uint16(0)))), (uint8));
    }

    function _completeEffect(
        uint256 runId,
        uint16 taskIndex,
        Execution.Mode mode,
        TaskGraph.Descriptor memory descriptor,
        Execution.Outcome outcome
    ) private {
        uint8 attempt = mode == Execution.Mode.Compilation
            ? execution.claimCompiled(runId, taskIndex)
            : execution.claimInterpreted(runId, taskIndex, descriptor);
        execution.proposeResult(runId, taskIndex, attempt, outcome, "effect-result");
        execution.acceptResult(runId, taskIndex, attempt, "effect-result");
    }

    function _actorSuccess(ExecutionActor actor, bytes memory callData) private returns (bytes memory result) {
        (bool isSuccessful, bytes memory output) = actor.callExecution(execution, callData);
        require(isSuccessful, "actor call failed");
        return output;
    }

    function _actorReverts(ExecutionActor actor, bytes memory callData, bytes4 selector) private {
        (bool isSuccessful, bytes memory reason) = actor.callExecution(execution, callData);
        require(
            !isSuccessful && keccak256(reason) == keccak256(abi.encodeWithSelector(selector)), "wrong actor rejection"
        );
    }

    function _assertReverts(bytes memory callData, bytes4 selector) private {
        (bool isSuccessful, bytes memory reason) = address(execution).call(callData);
        require(
            !isSuccessful && keccak256(reason) == keccak256(abi.encodeWithSelector(selector)),
            "wrong execution rejection"
        );
    }

    function _createStart(Execution.Mode mode, bytes32 repository)
        private
        view
        returns (Execution.Start memory request)
    {
        request.mode = mode;
        request.snapshot.packages = new Types.Package[](1);
        request.snapshot.packages[0] = Types.Package("core", Types.Version(0, 1, 0, 0, 0), true);
        request.snapshot.commits = new Types.Commit[](1);
        request.snapshot.commits[0] = Types.Commit("root", 0);
        request.snapshot.units = new Types.Unit[](1);
        request.snapshot.units[0].unitIndex = 1;
        request.snapshot.units[0].scope = 1;
        request.snapshot.units[0].bump = Types.Bump.Minor;
        request.tasks = new TaskGraph.Descriptor[](3);
        request.tasks[0] = TaskGraph.Descriptor(0, TaskGraph.Kind.Command, "build", 0);
        request.tasks[1] = TaskGraph.Descriptor(0, TaskGraph.Kind.Publish, "publish", 1);
        request.tasks[2] = TaskGraph.Descriptor(0, TaskGraph.Kind.Record, "record", 2);
        request.nodes = new address[](2);
        request.nodes[0] = address(firstNode);
        request.nodes[1] = address(secondNode);
        request.repositories = new bytes32[](1);
        request.repositories[0] = repository;
        request.maxAttempts = 3;
    }
}
