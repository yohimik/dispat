// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.28;

import {CCMETypes as Types} from "./CCMETypes.sol";
import {DispatPlanner} from "./DispatPlanner.sol";
import {CCMEHistory as History} from "./CCMEHistory.sol";
import {DispatTaskGraph as TaskGraph} from "./DispatTaskGraph.sol";

/// @notice Coordinates bounded task graphs on a local EVM; commands and publication run outside the chain.
contract DispatExecution {
    uint256 private constant EMPTY_COUNT = 0;
    uint256 private constant INVALID_RUN_ID = 0;
    uint256 private constant INDEX_OFFSET = 1;
    uint256 private constant TASK_BIT = 1;
    uint256 private constant EMPTY_DEPENDENCIES = 0;
    uint256 private constant MAXIMUM_TASK_COUNT = 256;
    uint256 private constant MAXIMUM_NODE_COUNT = 64;
    uint256 private constant MAXIMUM_REPOSITORY_COUNT = 64;
    uint8 private constant MINIMUM_ATTEMPT_COUNT = 1;
    uint8 private constant MAXIMUM_ATTEMPT_COUNT = 64;
    address private constant EMPTY_ADDRESS = address(0);
    bytes32 private constant EMPTY_HASH = bytes32(0);
    bool private constant ENABLED_FLAG = true;

    enum Mode {
        Compilation,
        Interpretation,
        Hybrid
    }
    enum Status {
        Pending,
        Claimed,
        Proposed,
        Succeeded,
        Failed,
        Unknown
    }
    enum Outcome {
        Success,
        Failure,
        Unknown
    }

    struct Task {
        bytes32 descriptorHash;
        uint256 dependencies;
        uint16 packageIndex;
        TaskGraph.Kind kind;
        Status status;
        address executor;
        uint8 attempt;
        bytes32 resultHash;
        Outcome outcome;
    }

    struct Run {
        Mode mode;
        bytes32 inputHash;
        bytes32 planHash;
        bytes32 graphHash;
        uint16 taskCount;
        uint8 maxAttempts;
        bool isCancelled;
        bool isFinished;
    }

    struct Start {
        Mode mode;
        Types.Snapshot snapshot;
        TaskGraph.Descriptor[] tasks;
        address[] nodes;
        bytes32[] repositories;
        uint8 maxAttempts;
    }

    error Unauthorized();
    error InvalidRun();
    error InvalidTask();
    error WrongMode();
    error NotReady();
    error CapacityExceeded();
    error StaleAttempt();

    address public immutable orchestrator;
    DispatPlanner public immutable planner;
    uint256 public runCount;
    mapping(address => uint16) public capacity;
    mapping(address => uint16) public activeClaims;
    mapping(uint256 => Run) private runs;
    mapping(uint256 => mapping(address => bool)) public admittedNodes;
    mapping(uint256 => mapping(uint16 => Task)) private tasks;
    mapping(uint256 => mapping(uint16 => TaskGraph.Descriptor)) private compiled;
    mapping(bytes32 => uint256) public repositoryRun;
    mapping(uint256 => bytes32[]) private runRepositories;
    mapping(uint256 => mapping(uint16 => uint16)) private recordTasks;

    event RunStarted(uint256 indexed runId, Mode mode, bytes32 inputHash, bytes32 planHash, bytes32 graphHash);
    event TaskClaimed(uint256 indexed runId, uint16 indexed taskIndex, address indexed executor, uint8 attempt);
    event ResultProposed(
        uint256 indexed runId, uint16 indexed taskIndex, uint8 attempt, Outcome outcome, bytes32 resultHash
    );
    event ResultAccepted(uint256 indexed runId, uint16 indexed taskIndex, uint8 attempt, Status status);
    event RunCancelled(uint256 indexed runId);
    event RunFinished(uint256 indexed runId);
    event UnknownResolved(uint256 indexed runId, uint16 indexed taskIndex, bool isSuccessful, bytes32 evidenceHash);
    event RecordReconciled(uint256 indexed runId, uint16 indexed taskIndex, bytes32 evidenceHash);
    event UnknownDeclared(uint256 indexed runId, uint16 indexed taskIndex, uint8 attempt, bytes32 evidenceHash);

    modifier onlyOrchestrator() {
        if (msg.sender != orchestrator) revert Unauthorized();
        _;
    }

    constructor(DispatPlanner plannerContract) {
        if (address(plannerContract).code.length == EMPTY_COUNT) revert InvalidRun();
        orchestrator = msg.sender;
        planner = plannerContract;
    }

    function registerCapacity(uint16 commandCapacity) external {
        if (commandCapacity == EMPTY_COUNT || commandCapacity < activeClaims[msg.sender]) revert CapacityExceeded();
        capacity[msg.sender] = commandCapacity;
    }

    /// @notice Pins one semantic plan and one complete graph. Mode never changes the plan hash.
    function start(Start calldata request) external onlyOrchestrator returns (uint256 runId) {
        Types.Plan memory plan = planner.compute(request.snapshot);
        return startPlan(request, plan, keccak256(abi.encode(request.snapshot)));
    }

    /// @notice Uses the same modes with a federation adapter's normalized history projection.
    function startPrepared(Start calldata request, History.State calldata history)
        external
        onlyOrchestrator
        returns (uint256 runId)
    {
        Types.Plan memory plan = planner.computePrepared(request.snapshot, history);
        return startPlan(request, plan, keccak256(abi.encode(request.snapshot, history)));
    }

    function startPlan(Start calldata request, Types.Plan memory plan, bytes32 inputHash)
        private
        returns (uint256 runId)
    {
        if (
            request.tasks.length == EMPTY_COUNT || request.tasks.length > MAXIMUM_TASK_COUNT
                || request.nodes.length == EMPTY_COUNT || request.nodes.length > MAXIMUM_NODE_COUNT
                || request.repositories.length == EMPTY_COUNT || request.repositories.length > MAXIMUM_REPOSITORY_COUNT
                || request.maxAttempts < MINIMUM_ATTEMPT_COUNT || request.maxAttempts > MAXIMUM_ATTEMPT_COUNT
        ) revert InvalidRun();
        TaskGraph.validate(request.snapshot, plan, request.tasks);
        runId = ++runCount;
        Run storage run = runs[runId];
        run.mode = request.mode;
        run.inputHash = inputHash;
        run.planHash = keccak256(abi.encode(plan));
        run.graphHash = keccak256(abi.encode(request.tasks));
        run.taskCount = uint16(request.tasks.length);
        run.maxAttempts = request.maxAttempts;
        for (uint256 i; i < request.repositories.length; ++i) {
            bytes32 repository = request.repositories[i];
            if (
                repository == EMPTY_HASH || repositoryRun[repository] != INVALID_RUN_ID
                    || (i != EMPTY_COUNT && request.repositories[i - INDEX_OFFSET] >= repository)
            ) revert InvalidRun();
            repositoryRun[repository] = runId;
            runRepositories[runId].push(repository);
        }
        for (uint256 i; i < request.nodes.length; ++i) {
            address node = request.nodes[i];
            if (node == EMPTY_ADDRESS || capacity[node] == EMPTY_COUNT || admittedNodes[runId][node]) {
                revert InvalidRun();
            }
            admittedNodes[runId][node] = ENABLED_FLAG;
        }
        for (uint256 i; i < request.tasks.length; ++i) {
            TaskGraph.Descriptor calldata descriptor = request.tasks[i];
            if (
                descriptor.packageIndex >= plan.packages.length || !plan.packages[descriptor.packageIndex].isPlanned
                    || descriptor.commandHash == EMPTY_HASH || descriptor.dependencies >> i != EMPTY_COUNT
            ) revert InvalidTask();
            Task storage task = tasks[runId][uint16(i)];
            task.descriptorHash = keccak256(abi.encode(descriptor));
            task.dependencies = descriptor.dependencies;
            task.packageIndex = descriptor.packageIndex;
            task.kind = descriptor.kind;
            if (descriptor.kind == TaskGraph.Kind.Record) recordTasks[runId][descriptor.packageIndex] = uint16(i);
            if (
                request.mode == Mode.Compilation
                    || (request.mode == Mode.Hybrid && descriptor.kind == TaskGraph.Kind.Command)
            ) {
                compiled[runId][uint16(i)] = descriptor;
            }
        }
        emit RunStarted(runId, run.mode, run.inputHash, run.planHash, run.graphHash);
    }

    function claimCompiled(uint256 runId, uint16 taskIndex) external returns (uint8 attempt) {
        Run storage run = requireRun(runId);
        Task storage task = requireTask(runId, taskIndex);
        if (run.mode == Mode.Interpretation || (run.mode == Mode.Hybrid && task.kind != TaskGraph.Kind.Command)) {
            revert WrongMode();
        }
        return claim(runId, taskIndex, task);
    }

    /// @notice A node derives this descriptor locally; its commitment and prerequisites are checked on-chain.
    function claimInterpreted(uint256 runId, uint16 taskIndex, TaskGraph.Descriptor calldata descriptor)
        external
        returns (uint8 attempt)
    {
        Run storage run = requireRun(runId);
        Task storage task = requireTask(runId, taskIndex);
        if (run.mode == Mode.Compilation || (run.mode == Mode.Hybrid && task.kind == TaskGraph.Kind.Command)) {
            revert WrongMode();
        }
        if (keccak256(abi.encode(descriptor)) != task.descriptorHash) revert InvalidTask();
        return claim(runId, taskIndex, task);
    }

    function claim(uint256 runId, uint16 taskIndex, Task storage task) private returns (uint8) {
        Run storage run = runs[runId];
        if ((run.isCancelled && task.kind != TaskGraph.Kind.Record) || run.isFinished || task.status != Status.Pending) revert NotReady();
        if (task.kind != TaskGraph.Kind.Command) {
            if (msg.sender != orchestrator) revert Unauthorized();
        } else {
            if (!admittedNodes[runId][msg.sender]) revert Unauthorized();
            if (activeClaims[msg.sender] >= capacity[msg.sender]) revert CapacityExceeded();
        }
        for (uint16 i; i < taskIndex; ++i) {
            if (
                (task.dependencies & (TASK_BIT << i)) != EMPTY_DEPENDENCIES
                    && tasks[runId][i].status != Status.Succeeded
            ) revert NotReady();
        }
        task.status = Status.Claimed;
        task.executor = msg.sender;
        ++task.attempt;
        if (task.kind == TaskGraph.Kind.Command) ++activeClaims[msg.sender];
        emit TaskClaimed(runId, taskIndex, msg.sender, task.attempt);
        return task.attempt;
    }

    function proposeResult(uint256 runId, uint16 taskIndex, uint8 attempt, Outcome outcome, bytes32 resultHash)
        external
    {
        Task storage task = requireTask(runId, taskIndex);
        if (msg.sender != task.executor) revert Unauthorized();
        if (task.status != Status.Claimed || attempt != task.attempt) revert StaleAttempt();
        if (resultHash == EMPTY_HASH) revert InvalidTask();
        task.status = Status.Proposed;
        task.resultHash = resultHash;
        task.outcome = outcome;
        emit ResultProposed(runId, taskIndex, attempt, outcome, resultHash);
    }

    /// @notice Accepts verified output evidence; an unknown effect is terminal and cannot authorize a retry.
    function acceptResult(uint256 runId, uint16 taskIndex, uint8 attempt, bytes32 resultHash)
        external
        onlyOrchestrator
    {
        Task storage task = requireTask(runId, taskIndex);
        if (task.status != Status.Proposed || attempt != task.attempt || resultHash != task.resultHash) {
            revert StaleAttempt();
        }
        if (task.kind == TaskGraph.Kind.Command && task.outcome != Outcome.Unknown) --activeClaims[task.executor];
        if (task.outcome == Outcome.Success) task.status = Status.Succeeded;
        else if (task.outcome == Outcome.Unknown) task.status = Status.Unknown;
        else task.status = Status.Failed;
        emit ResultAccepted(runId, taskIndex, attempt, task.status);
    }

    function retryCommand(uint256 runId, uint16 taskIndex) external onlyOrchestrator {
        Run storage run = requireRun(runId);
        Task storage task = requireTask(runId, taskIndex);
        if (
            run.isCancelled || run.isFinished || task.kind != TaskGraph.Kind.Command || task.status != Status.Failed
                || task.attempt >= run.maxAttempts
        ) {
            revert NotReady();
        }
        task.status = Status.Pending;
        task.executor = EMPTY_ADDRESS;
        task.resultHash = EMPTY_HASH;
    }

    /// @notice Stops new claims. Existing executors must still settle; cancellation does not undo publication.
    function cancel(uint256 runId) external onlyOrchestrator {
        Run storage run = requireRun(runId);
        if (run.isFinished) revert InvalidRun();
        run.isCancelled = ENABLED_FLAG;
        emit RunCancelled(runId);
    }

    /// @notice Quarantines an unresolved active attempt; capacity and repository locks remain reserved.
    function markUnknown(uint256 runId, uint16 taskIndex, uint8 attempt, bytes32 evidenceHash)
        external
        onlyOrchestrator
    {
        Task storage task = requireTask(runId, taskIndex);
        if (task.status != Status.Claimed && task.status != Status.Proposed) revert NotReady();
        if (attempt != task.attempt) revert StaleAttempt();
        if (evidenceHash == EMPTY_HASH) revert InvalidTask();
        task.status = Status.Unknown;
        task.outcome = Outcome.Unknown;
        task.resultHash = evidenceHash;
        emit UnknownDeclared(runId, taskIndex, attempt, evidenceHash);
    }

    /// @notice Records an operator's reconciliation after the executor stopped and external output was inspected.
    function resolveUnknown(uint256 runId, uint16 taskIndex, bool isSuccessful, bytes32 evidenceHash)
        external
        onlyOrchestrator
    {
        Task storage task = requireTask(runId, taskIndex);
        if (task.status != Status.Unknown || evidenceHash == EMPTY_HASH) revert NotReady();
        if (task.kind == TaskGraph.Kind.Command) --activeClaims[task.executor];
        task.status = isSuccessful ? Status.Succeeded : Status.Failed;
        task.outcome = isSuccessful ? Outcome.Success : Outcome.Failure;
        task.resultHash = evidenceHash;
        emit UnknownResolved(runId, taskIndex, isSuccessful, evidenceHash);
    }

    /// @notice Accepts evidence that a failed durable record was repaired without publishing again.
    function reconcileRecord(uint256 runId, uint16 taskIndex, bytes32 evidenceHash) external onlyOrchestrator {
        Task storage task = requireTask(runId, taskIndex);
        if (task.kind != TaskGraph.Kind.Record || task.status != Status.Failed || evidenceHash == EMPTY_HASH) {
            revert NotReady();
        }
        for (uint16 i; i < taskIndex; ++i) {
            if (
                (task.dependencies & (TASK_BIT << i)) != EMPTY_DEPENDENCIES
                    && tasks[runId][i].status != Status.Succeeded
            ) revert NotReady();
        }
        task.status = Status.Succeeded;
        task.outcome = Outcome.Success;
        task.resultHash = evidenceHash;
        emit RecordReconciled(runId, taskIndex, evidenceHash);
    }

    function finish(uint256 runId) external onlyOrchestrator {
        Run storage run = requireRun(runId);
        if (run.isFinished) revert InvalidRun();
        for (uint16 i; i < run.taskCount; ++i) {
            Task storage task = tasks[runId][i];
            Status status = task.status;
            if (
                status == Status.Claimed || status == Status.Proposed || status == Status.Unknown
                    || (status == Status.Pending && !run.isCancelled)
            ) revert NotReady();
            if (
                task.kind == TaskGraph.Kind.Publish && status == Status.Succeeded
                    && tasks[runId][recordTasks[runId][task.packageIndex]].status != Status.Succeeded
            ) revert NotReady();
        }
        run.isFinished = ENABLED_FLAG;
        bytes32[] storage repositories = runRepositories[runId];
        for (uint256 i = repositories.length; i != EMPTY_COUNT; --i) {
            delete repositoryRun[repositories[i - INDEX_OFFSET]];
        }
        emit RunFinished(runId);
    }

    function getRun(uint256 runId) external view returns (Run memory) {
        return requireRun(runId);
    }

    function getTask(uint256 runId, uint16 taskIndex) external view returns (Task memory) {
        return requireTask(runId, taskIndex);
    }

    function getCompiledTask(uint256 runId, uint16 taskIndex) external view returns (TaskGraph.Descriptor memory) {
        Run storage run = requireRun(runId);
        Task storage task = requireTask(runId, taskIndex);
        if (run.mode == Mode.Interpretation || (run.mode == Mode.Hybrid && task.kind != TaskGraph.Kind.Command)) {
            revert WrongMode();
        }
        return compiled[runId][taskIndex];
    }

    function requireRun(uint256 runId) private view returns (Run storage run) {
        if (runId == INVALID_RUN_ID || runId > runCount) revert InvalidRun();
        return runs[runId];
    }

    function requireTask(uint256 runId, uint16 taskIndex) private view returns (Task storage task) {
        Run storage run = requireRun(runId);
        if (taskIndex >= run.taskCount) revert InvalidTask();
        return tasks[runId][taskIndex];
    }
}
