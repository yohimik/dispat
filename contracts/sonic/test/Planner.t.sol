// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.28;

import {CCMETypes as Types} from "../src/CCMETypes.sol";
import {DispatPlanner} from "../src/DispatPlanner.sol";
import {CCMEGraph} from "../src/CCMEGraph.sol";

contract PlannerTest {
    DispatPlanner private planner;

    function setUp() public {
        planner = new DispatPlanner();
    }

    function testProviderSuccessLeavesConsumerCatchUpThenConverges() public view {
        Types.Snapshot memory snapshot = createPair(4, 4, 1);
        snapshot.units[0] = createWork(1, 2, Types.Bump.Minor, 1);
        snapshot.tags[2] = createTag(1, 2, createVersion(1, 1, 0));
        snapshot.tags[3] = snapshot.tags[2];

        Types.Plan memory retry = compute(snapshot);
        require(!retry.packages[1].isPlanned, "provider already released");
        require(retry.packages[0].isPlanned && retry.packages[0].isCatchUp, "consumer catch-up missing");
        require(retry.packages[0].owedSources == 2, "consumer provenance");
        assertVersion(retry.packages[0].next, 1, 0, 1, 0, 0);
        require(retry.publishOrder.length == 1 && retry.publishOrder[0] == 0, "catch-up order");

        snapshot.tags[3] = createTag(0, 3, createVersion(1, 0, 1));
        Types.Plan memory complete = compute(snapshot);
        require(complete.publishOrder.length == 0, "recorded delivery must converge");
    }

    function testConsumerAheadOfProviderIsStillOwed() public view {
        Types.Snapshot memory snapshot = createPair(4, 4, 1);
        snapshot.units[0] = createWork(1, 2, Types.Bump.Minor, 1);
        snapshot.tags[2] = createTag(0, 1, createVersion(1, 0, 1));
        snapshot.tags[3] = createTag(1, 2, createVersion(1, 1, 0));
        Types.Plan memory result = compute(snapshot);
        require(result.packages[0].isPlanned, "consumer position is not delivery");
        assertVersion(result.packages[0].next, 1, 0, 2, 0, 0);
    }

    function testOriginalDepthDoesNotCascadeOnRetry() public view {
        Types.Snapshot memory snapshot = createChain(4, 5, 1);
        snapshot.units[0] = createWork(1, 2, Types.Bump.Minor, 1);
        snapshot.tags[3] = createTag(1, 2, createVersion(1, 1, 0));
        snapshot.tags[4] = createTag(2, 3, createVersion(1, 0, 1));
        Types.Plan memory result = compute(snapshot);
        require(result.publishOrder.length == 0, "intermediate delivery cannot extend depth");
        require(!result.packages[0].isPlanned, "app outside original depth");
    }

    function testDiamondUsesShortestPathAndMaxNotAddition() public view {
        Types.Snapshot memory snapshot = createSnapshot(4, 2, 4, 1, 5);
        snapshot.packages[0].name = "app";
        snapshot.packages[1].name = "core";
        snapshot.packages[2].name = "left";
        snapshot.packages[3].name = "right";
        snapshot.edges[0] = createEdge(1, 2);
        snapshot.edges[1] = createEdge(1, 3);
        snapshot.edges[2] = createEdge(2, 0);
        snapshot.edges[3] = createEdge(3, 0);
        snapshot.edges[4] = createEdge(1, 0);
        snapshot.units[0] = createWork(1, 2, Types.Bump.Minor, 1);
        Types.Plan memory result = compute(snapshot);
        assertVersion(result.packages[0].next, 1, 0, 1, 0, 0);
        require(result.packages[0].owedSources == 2, "diamond duplicates source");
        require(result.publishOrder.length == 4, "direct edge admits app at depth one");
    }

    function testHeldUnreleasedSourceCannotPropagate() public view {
        Types.Snapshot memory snapshot = createPair(3, 2, 2);
        snapshot.units[0] = createControl(2, 2, Types.ReleaseKind.Hold);
        snapshot.units[1] = createWork(1, 2, Types.Bump.Minor, 1);
        Types.Plan memory result = compute(snapshot);
        require(result.packages[1].isHeld && !result.packages[1].isPlanned, "hold not applied");
        assertVersion(result.packages[1].next, 1, 1, 0, 0, 0);
        require(!result.packages[0].isPlanned, "held source leaked propagation");
    }

    function testHoldAfterSourcePublicationPreservesConsumerDebt() public view {
        Types.Snapshot memory snapshot = createPair(4, 3, 2);
        snapshot.tags[2] = createTag(1, 2, createVersion(1, 1, 0));
        snapshot.units[0] = createControl(3, 2, Types.ReleaseKind.Hold);
        snapshot.units[1] = createWork(1, 2, Types.Bump.Minor, 1);
        Types.Plan memory result = compute(snapshot);
        require(result.packages[1].isHeld, "provider hold");
        require(result.packages[0].isCatchUp && result.packages[0].isPlanned, "published source remains owed");
        assertVersion(result.packages[0].next, 1, 0, 1, 0, 0);
    }

    function testConsumerHoldSurvivesBumpsUntilAuto() public view {
        Types.Snapshot memory snapshot = createPair(4, 3, 3);
        snapshot.tags[2] = createTag(1, 2, createVersion(1, 1, 0));
        snapshot.units[0] = createControl(3, 1, Types.ReleaseKind.Hold);
        snapshot.units[1] = createWork(2, 1, Types.Bump.Patch, 0);
        snapshot.units[2] = createWork(1, 2, Types.Bump.Minor, 1);
        Types.Plan memory held = compute(snapshot);
        require(held.packages[0].isHeld && !held.packages[0].isPlanned, "consumer hold");
        assertVersion(held.packages[0].next, 1, 0, 1, 0, 0);
        snapshot.units[0].releaseKind = Types.ReleaseKind.Auto;
        Types.Plan memory resumed = compute(snapshot);
        require(!resumed.packages[0].isHeld && resumed.packages[0].isPlanned, "auto must resume debt");
        assertVersion(resumed.packages[0].next, 1, 0, 1, 0, 0);
    }

    function testConsumerCancellationSuppressesCatchUp() public view {
        Types.Snapshot memory snapshot = createPair(4, 3, 2);
        snapshot.tags[2] = createTag(1, 2, createVersion(1, 1, 0));
        snapshot.units[0] = createCancel(3, 1);
        snapshot.units[1] = createWork(1, 2, Types.Bump.Minor, 1);
        Types.Plan memory result = compute(snapshot);
        require(result.publishOrder.length == 0, "consumer cancellation must discard debt");
    }

    function testPublishedProviderCancellationCannotSuppressCatchUp() public view {
        Types.Snapshot memory snapshot = createPair(4, 3, 2);
        snapshot.tags[2] = createTag(1, 2, createVersion(1, 1, 0));
        snapshot.units[0] = createCancel(3, 2);
        snapshot.units[1] = createWork(1, 2, Types.Bump.Minor, 1);
        Types.Plan memory result = compute(snapshot);
        require(result.packages[0].isPlanned && result.packages[0].isCatchUp, "source cancel retracts publication");
    }

    function testCancellationKeepsIncomparableBranchWork() public view {
        Types.Snapshot memory snapshot = createPair(5, 2, 3);
        snapshot.commits[3].parents = 2;
        snapshot.commits[4].parents = 4 | 8;
        snapshot.units[0] = createWork(3, 2, Types.Bump.Patch, 0);
        snapshot.units[1] = createCancel(2, 2);
        snapshot.units[2] = createWork(1, 2, Types.Bump.Minor, 0);
        Types.Plan memory result = compute(snapshot);
        assertVersion(result.packages[1].next, 1, 0, 1, 0, 0);
        require(result.packages[1].directBump == Types.Bump.Patch, "cancel crossed ancestry");
    }

    function testSameCommitCancellationDiscardsSiblingWork() public view {
        Types.Snapshot memory snapshot = createPair(2, 2, 2);
        snapshot.units[0] = createCancel(1, 2);
        snapshot.units[0].unitIndex = 2;
        snapshot.units[1] = createWork(1, 2, Types.Bump.Minor, 1);
        Types.Plan memory result = compute(snapshot);
        require(result.publishOrder.length == 0, "cancel must include its own commit");
    }

    function testPrereleaseSourceCannotBumpStableConsumer() public view {
        Types.Snapshot memory snapshot = createPair(2, 2, 1);
        snapshot.units[0] = createWork(1, 2, Types.Bump.Minor, 1);
        snapshot.units[0].channel = createChannel("beta");
        Types.Plan memory result = compute(snapshot);
        require(!result.packages[0].isPlanned, "stable consumer cannot resolve beta");
        assertVersion(result.packages[1].next, 1, 1, 0, "beta", 0);
        require(isDiagnosticPresent(result, 208, 0), "suppressed caret diagnostic");
    }

    function testChannelPhaseMovesConsumerBeforeBumpAdmission() public view {
        Types.Snapshot memory snapshot = createPair(2, 2, 1);
        snapshot.units[0] = createWork(1, 2, Types.Bump.Minor, 1);
        snapshot.units[0].channel = createChannel("beta");
        snapshot.units[0].propagateChannel.mode = Types.ChannelMode.Inherit;
        snapshot.units[0].channelDepth = 1;
        Types.Plan memory result = compute(snapshot);
        assertVersion(result.packages[0].next, 1, 0, 1, "beta", 0);
        assertVersion(result.packages[1].next, 1, 1, 0, "beta", 0);
        require(result.packages[0].isPlanned, "consumer on resolved beta must accept bump");
    }

    function testChannelOnlyEntryAppliesOnePatch() public view {
        Types.Snapshot memory snapshot = createPair(2, 2, 1);
        snapshot.units[0] = createWork(1, 2, Types.Bump.Minor, 0);
        snapshot.units[0].channel = createChannel("beta");
        snapshot.units[0].propagateChannel.mode = Types.ChannelMode.Inherit;
        snapshot.units[0].channelDepth = 1;
        Types.Plan memory result = compute(snapshot);
        require(result.packages[0].isChannelOnly, "channel-only cause");
        require(result.packages[0].hasChannelEntryPatch, "channel entry patch");
        assertVersion(result.packages[0].next, 1, 0, 1, "beta", 0);
    }

    function testHistoricalTrainCannotAdmitAnotherRelease() public view {
        Types.Snapshot memory snapshot = createPair(3, 3, 1);
        snapshot.units[0] = createWork(1, 2, Types.Bump.Minor, 0);
        snapshot.tags[2] = createTag(1, 2, createPrerelease(1, 1, 0, "beta", 0));
        Types.Plan memory result = compute(snapshot);
        require(!result.packages[1].isPlanned, "historical train is not fresh admission");
        assertVersion(result.packages[1].next, 1, 1, 0, "beta", 0);
        require(result.publishOrder.length == 0, "quiescent train");
    }

    function testFreshPatchKeepsAggregateTrainMinorTarget() public view {
        Types.Snapshot memory snapshot = createPair(4, 3, 2);
        snapshot.tags[2] = createTag(1, 2, createPrerelease(1, 1, 0, "beta", 0));
        snapshot.units[0] = createWork(3, 2, Types.Bump.Patch, 0);
        snapshot.units[1] = createWork(1, 2, Types.Bump.Minor, 0);
        Types.Plan memory result = compute(snapshot);
        assertVersion(result.packages[1].next, 1, 1, 0, "beta", 1);
    }

    function testPublishOrderKeepsPathsThroughInactivePackages() public view {
        Types.Snapshot memory snapshot = createChain(2, 3, 2);
        snapshot.units[0] = createWork(1, 1, Types.Bump.Patch, 0);
        snapshot.units[0].unitIndex = 2;
        snapshot.units[1] = createWork(1, 2, Types.Bump.Minor, 0);
        Types.Plan memory result = compute(snapshot);
        require(!result.packages[2].isPlanned, "intermediate is unchanged");
        require(result.publishOrder.length == 2, "planned package count");
        require(result.publishOrder[0] == 1 && result.publishOrder[1] == 0, "full-graph topo constraints lost");
    }

    function testPropagationScopeFiltersTargetsWithoutPruningWalk() public view {
        Types.Snapshot memory snapshot = createChain(2, 3, 1);
        snapshot.units[0] = createWork(1, 2, Types.Bump.Minor, 2);
        snapshot.units[0].propagateScope = 1;
        Types.Plan memory result = compute(snapshot);
        require(!result.packages[2].isPlanned, "middle excluded by scope");
        require(result.packages[0].isPlanned, "scope exclusion must not prune path");
    }

    function testOlderDirectChannelBeatsNewerPropagatedChannel() public view {
        Types.Snapshot memory snapshot = createPair(3, 2, 2);
        snapshot.units[0] = createControl(2, 2, Types.ReleaseKind.Unset);
        snapshot.units[0].propagateChannel = createChannel("rc");
        snapshot.units[0].channelDepth = 1;
        snapshot.units[1] = createControl(1, 1, Types.ReleaseKind.Unset);
        snapshot.units[1].channel = createChannel("beta");
        Types.Plan memory result = compute(snapshot);
        assertVersion(result.packages[0].next, 1, 0, 1, "beta", 0);
    }

    function testNewerNoOpAndUnmatchedDirectivesAreNotCompetitors() public view {
        Types.Snapshot memory snapshot = createPair(4, 2, 3);
        snapshot.units[0] = createControl(3, 1, Types.ReleaseKind.Unset);
        snapshot.units[0].channel = Types.ChannelDirective(Types.ChannelMode.Transition, "rc", 0);
        snapshot.units[1] = createControl(2, 1, Types.ReleaseKind.Unset);
        snapshot.units[1].channel = createChannel(0);
        snapshot.units[2] = createControl(1, 1, Types.ReleaseKind.Unset);
        snapshot.units[2].channel = createChannel("beta");
        Types.Plan memory result = compute(snapshot);
        assertVersion(result.packages[0].next, 1, 0, 1, "beta", 0);
        require(!isDiagnosticPresent(result, 186, 0), "inert direct directives are not conflicts");
    }

    function testPropagationGraduatesOnlyByExplicitTransition() public view {
        Types.Snapshot memory snapshot = createPair(3, 3, 1);
        snapshot.tags[2] = createTag(0, 1, createPrerelease(1, 0, 1, "beta", 0));
        snapshot.units[0] = createControl(2, 2, Types.ReleaseKind.Unset);
        snapshot.units[0].propagateChannel = createChannel(0);
        snapshot.units[0].channelDepth = 1;
        Types.Plan memory implicit = compute(snapshot);
        require(!implicit.packages[0].isPlanned, "bare stable must not graduate");
        require(isDiagnosticPresent(implicit, 200, 0), "implicit graduation warning");

        snapshot.units[0].propagateChannel = Types.ChannelDirective(Types.ChannelMode.Transition, "beta", 0);
        Types.Plan memory explicit = compute(snapshot);
        require(explicit.packages[0].isPlanned, "explicit transition graduates");
        assertVersion(explicit.packages[0].next, 1, 0, 1, 0, 0);
    }

    function testCancellationClearsTrainWorkWithoutDeletingPrerelease() public view {
        Types.Snapshot memory snapshot = createPair(4, 3, 2);
        snapshot.tags[2] = createTag(1, 2, createPrerelease(1, 1, 0, "beta", 0));
        snapshot.units[0] = createCancel(3, 2);
        snapshot.units[1] = createWork(1, 2, Types.Bump.Minor, 0);
        Types.Plan memory result = compute(snapshot);
        require(!result.packages[1].isPlanned, "cancel does not graduate or re-release");
        require(result.packages[1].trainBump == Types.Bump.None, "published train bump must be cancellable");
        assertVersion(result.packages[1].next, 1, 1, 0, "beta", 0);
        require(isDiagnosticPresent(result, 171, 1), "cancelled prerelease work warning");
    }

    function testDeliveredPropagationStillShapesAdmittedTrainTarget() public view {
        Types.Snapshot memory snapshot = createPair(4, 4, 2);
        snapshot.tags[2] = createTag(1, 2, createPrerelease(1, 1, 0, "beta", 0));
        snapshot.tags[3] = createTag(0, 2, createPrerelease(1, 1, 0, "beta", 0));
        snapshot.units[0] = createWork(3, 1, Types.Bump.Patch, 0);
        snapshot.units[1] = createWork(1, 2, Types.Bump.Minor, 1);
        snapshot.units[1].propagate = Types.Bump.Minor;
        snapshot.units[1].channel = createChannel("beta");
        snapshot.units[1].propagateChannel.mode = Types.ChannelMode.Inherit;
        snapshot.units[1].channelDepth = 1;
        Types.Plan memory result = compute(snapshot);
        require(result.packages[0].directBump == Types.Bump.Patch, "fresh patch admission");
        require(result.packages[0].propagatedBump == Types.Bump.None, "old debt already delivered");
        require(result.packages[0].trainBump == Types.Bump.Minor, "train retains historical propagation");
        assertVersion(result.packages[0].next, 1, 1, 0, "beta", 1);
    }

    function testMultiSourceProvenanceNamesOnlyReachableOwedSources() public view {
        Types.Snapshot memory snapshot = createSnapshot(3, 2, 3, 1, 1);
        snapshot.packages[0].name = "api";
        snapshot.packages[1].name = "cli";
        snapshot.packages[2].name = "core";
        snapshot.edges[0] = createEdge(2, 1);
        snapshot.units[0] = createWork(1, 1 | 4, Types.Bump.Minor, 1);
        Types.Plan memory result = compute(snapshot);
        require(result.packages[1].owedSources == 4, "unreachable source cannot enter provenance");
        assertVersion(result.packages[1].next, 1, 0, 1, 0, 0);
    }

    function testMultiSourceResolvabilityUsesWholeSourceSet() public view {
        Types.Snapshot memory snapshot = createSnapshot(3, 3, 4, 2, 1);
        snapshot.packages[0].name = "api";
        snapshot.packages[1].name = "cli";
        snapshot.packages[2].name = "core";
        snapshot.edges[0] = createEdge(2, 1);
        snapshot.tags[3] = createTag(2, 1, createPrerelease(1, 1, 0, "beta", 0));
        snapshot.units[0] = createWork(2, 1 | 4, Types.Bump.Patch, 1);
        snapshot.units[1] = createWork(1, 4, Types.Bump.Minor, 0);
        Types.Plan memory result = compute(snapshot);
        require(result.packages[1].isPlanned, "stable source anywhere in set admits bump");
        require(result.packages[1].owedSources == 4, "only reachable source is owed");
    }

    function testCycleAcrossPropagationAndPublishKindsIsRejected() public view {
        Types.Snapshot memory snapshot = createPair(1, 2, 0);
        snapshot.edges = new Types.Edge[](2);
        snapshot.edges[0] = Types.Edge(1, 0, true, false);
        snapshot.edges[1] = Types.Edge(0, 1, false, true);
        expectError(snapshot, CCMEGraph.DependencyCycle.selector);
    }

    function testInvalidEdgeIndexIsRejected() public view {
        Types.Snapshot memory snapshot = createPair(1, 2, 0);
        snapshot.edges[0].consumer = 2;
        expectError(snapshot, Types.InvalidInput.selector);
    }

    function testUnknownPackageMaskIsRejected() public view {
        Types.Snapshot memory snapshot = createPair(2, 2, 1);
        snapshot.units[0] = createWork(1, 4, Types.Bump.Patch, 0);
        snapshot.units[0].propagateScope = 3;
        snapshot.units[0].channelScope = 3;
        expectError(snapshot, Types.InvalidInput.selector);
    }

    function expectError(Types.Snapshot memory snapshot, bytes4 selector) private view {
        try planner.compute(snapshot) returns (Types.Plan memory) {
            revert("expected error");
        } catch (bytes memory reason) {
            require(reason.length >= 4 && bytes4(reason) == selector, "unexpected error");
        }
    }

    function compute(Types.Snapshot memory snapshot) private view returns (Types.Plan memory) {
        uint256 allPackages = (uint256(1) << snapshot.packages.length) - 1;
        for (uint256 i; i < snapshot.units.length; ++i) {
            if (snapshot.units[i].propagateScope == type(uint64).max) snapshot.units[i].propagateScope = allPackages;
            if (snapshot.units[i].channelScope == type(uint64).max) snapshot.units[i].channelScope = allPackages;
        }
        return planner.compute(snapshot);
    }

    function createPair(uint256 commitCount, uint256 tagCount, uint256 unitCount)
        private
        pure
        returns (Types.Snapshot memory snapshot)
    {
        snapshot = createSnapshot(2, commitCount, tagCount, unitCount, 1);
        snapshot.packages[0].name = "cli";
        snapshot.packages[1].name = "core";
        snapshot.edges[0] = createEdge(1, 0);
    }

    function createChain(uint256 commitCount, uint256 tagCount, uint256 unitCount)
        private
        pure
        returns (Types.Snapshot memory snapshot)
    {
        snapshot = createSnapshot(3, commitCount, tagCount, unitCount, 2);
        snapshot.packages[0].name = "app";
        snapshot.packages[1].name = "base";
        snapshot.packages[2].name = "middle";
        snapshot.edges[0] = createEdge(1, 2);
        snapshot.edges[1] = createEdge(2, 0);
    }

    function createSnapshot(
        uint256 packageCount,
        uint256 commitCount,
        uint256 tagCount,
        uint256 unitCount,
        uint256 edgeCount
    ) private pure returns (Types.Snapshot memory snapshot) {
        snapshot.packages = new Types.Package[](packageCount);
        snapshot.commits = new Types.Commit[](commitCount);
        snapshot.tags = new Types.Tag[](tagCount);
        snapshot.units = new Types.Unit[](unitCount);
        snapshot.edges = new Types.Edge[](edgeCount);
        for (uint256 i; i < packageCount; ++i) {
            snapshot.packages[i].initialVersion = createVersion(0, 1, 0);
            snapshot.packages[i].preserveMajorZero = true;
            snapshot.tags[i] = createTag(uint16(i), 0, createVersion(1, 0, 0));
        }
        for (uint256 i; i < commitCount; ++i) {
            snapshot.commits[i].id = bytes32(i + 1);
            if (i != 0) snapshot.commits[i].parents = uint256(1) << (i - 1);
        }
    }

    function createWork(uint16 commitIndex, uint256 scope, Types.Bump bump, uint16 depth)
        private
        pure
        returns (Types.Unit memory unit)
    {
        unit.commitIndex = commitIndex;
        unit.unitIndex = 1;
        unit.scope = scope;
        unit.bump = bump;
        unit.propagate = Types.Bump.Patch;
        unit.depth = depth;
        unit.propagateScope = type(uint64).max;
        unit.channelScope = type(uint64).max;
    }

    function createControl(uint16 commitIndex, uint256 scope, Types.ReleaseKind kind)
        private
        pure
        returns (Types.Unit memory unit)
    {
        unit = createWork(commitIndex, scope, Types.Bump.None, 0);
        unit.releaseKind = kind;
        unit.isRelease = true;
    }

    function createCancel(uint16 commitIndex, uint256 scope) private pure returns (Types.Unit memory unit) {
        unit = createWork(commitIndex, scope, Types.Bump.None, 0);
        unit.isCancel = true;
        unit.propagate = Types.Bump.None;
        unit.propagateScope = 0;
        unit.channelScope = 0;
    }

    function createEdge(uint16 provider, uint16 consumer) private pure returns (Types.Edge memory) {
        return Types.Edge(provider, consumer, true, true);
    }

    function createTag(uint16 packageIndex, uint16 commitIndex, Types.Version memory value)
        private
        pure
        returns (Types.Tag memory)
    {
        return Types.Tag(packageIndex, commitIndex, value);
    }

    function createVersion(uint64 major, uint64 minor, uint64 patch) private pure returns (Types.Version memory) {
        return Types.Version(major, minor, patch, 0, 0);
    }

    function createPrerelease(uint64 major, uint64 minor, uint64 patch, bytes32 channel, uint64 counter)
        private
        pure
        returns (Types.Version memory)
    {
        return Types.Version(major, minor, patch, channel, counter);
    }

    function createChannel(bytes32 channel) private pure returns (Types.ChannelDirective memory) {
        return Types.ChannelDirective(Types.ChannelMode.Set, 0, channel);
    }

    function assertVersion(
        Types.Version memory actual,
        uint64 major,
        uint64 minor,
        uint64 patch,
        bytes32 channel,
        uint64 counter
    ) private pure {
        require(actual.major == major && actual.minor == minor && actual.patch == patch, "version core");
        require(actual.channel == channel && actual.counter == counter, "version channel/counter");
    }

    function isDiagnosticPresent(Types.Plan memory result, uint16 code, uint16 packageIndex)
        private
        pure
        returns (bool)
    {
        for (uint256 i; i < result.diagnostics.length; ++i) {
            if (result.diagnostics[i].code == code && result.diagnostics[i].packageIndex == packageIndex) {
                return true;
            }
        }
        return false;
    }
}
