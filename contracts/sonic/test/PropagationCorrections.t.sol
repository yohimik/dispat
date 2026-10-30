// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.28;

import {CCMETypes as Types} from "../src/CCMETypes.sol";
import {DispatPlanner} from "../src/DispatPlanner.sol";

contract PropagationCorrectionsTest {
    DispatPlanner private planner = new DispatPlanner();

    function testConsumerWildcardDiscardsPropagationWithoutDroppingProvider() public view {
        Types.Snapshot memory snapshot = createPair(3, 2, 2);
        snapshot.units[0] = createWildcard(2, 2);
        snapshot.units[1] = createProviderWork(1);
        Types.Plan memory plan = planner.compute(snapshot);
        require(plan.packages[0].isPlanned && plan.packages[0].next.minor == 1, "provider direct work erased");
        require(
            !plan.packages[1].isPlanned && plan.packages[1].owedSources == 0, "consumer wildcard retained propagation"
        );
        require(!hasDiagnostic(plan, 209), "effective consumer wildcard reported no work");
    }

    function testConsumerWildcardDiscardsChannelAxisWithoutBump() public view {
        Types.Snapshot memory snapshot = createPair(3, 2, 2);
        snapshot.units[0] = createWildcard(2, 2);
        snapshot.units[1] = createUnit(1, 1, Types.Bump.None);
        snapshot.units[1].propagateChannel = Types.ChannelDirective(Types.ChannelMode.Set, 0, "beta");
        snapshot.units[1].channelDepth = 1;
        snapshot.units[1].channelScope = 2;
        Types.Plan memory plan = planner.compute(snapshot);
        require(plan.publishOrder.length == 0, "wildcard retained channel-only release");
        require(plan.packages[1].channel == 0, "discarded channel directive changed consumer");
        require(!hasDiagnostic(plan, 209), "effective channel correction reported no work");
    }

    function testWildcardRemovingDirectSourceAlsoSuppressesIncomingContribution() public view {
        Types.Snapshot memory snapshot = createPair(3, 2, 2);
        snapshot.units[0] = createWildcard(2, 2);
        snapshot.units[1] = createProviderWork(1);
        snapshot.units[1].scope = 3;
        Types.Plan memory plan = planner.compute(snapshot);
        require(plan.packages[0].isPlanned && plan.packages[0].next.minor == 1, "other source direct work erased");
        require(!plan.packages[1].isPlanned, "removed source reacquired incoming propagation");
        require(
            plan.packages[1].directBump == Types.Bump.None && plan.packages[1].owedSources == 0,
            "corrected source still has work"
        );
        require(!hasDiagnostic(plan, 209), "direct correction incorrectly reported no work");
    }

    function testConsumerWildcardDiscardsDebtEvenAfterConsumerPassedOrigin() public view {
        Types.Snapshot memory snapshot = createPair(5, 2, 4);
        snapshot.tags[2] = Types.Tag(1, 2, Types.Version(1, 0, 1, 0, 0));
        snapshot.tags[3] = Types.Tag(0, 3, Types.Version(1, 1, 0, 0, 0));
        snapshot.units[0] = createWildcard(4, 2);
        snapshot.units[1] = createProviderWork(1);
        Types.Plan memory plan = planner.compute(snapshot);
        require(plan.publishOrder.length == 0, "consumer-ahead debt survived wildcard");
        require(!plan.packages[1].isCatchUp && plan.packages[1].owedSources == 0, "discarded debt retained provenance");
        require(plan.packages[1].baseline.patch == 1, "consumer baseline changed");
        require(!hasDiagnostic(plan, 209), "owed debt mistaken for delivered work");
    }

    function testVoidingConsumerWildcardRestoresPropagation() public view {
        Types.Snapshot memory snapshot = createPair(4, 3, 2);
        snapshot.units[0] = createUnit(3, 2, Types.Bump.None);
        snapshot.units[0].correctionTargets = 2;
        snapshot.units[1] = createWildcard(2, 2);
        snapshot.units[2] = createProviderWork(1);
        Types.Plan memory plan = planner.compute(snapshot);
        require(
            plan.packages[0].isPlanned && plan.packages[1].isPlanned, "voided wildcard still suppressed propagation"
        );
        require(
            plan.packages[1].next.patch == 1 && plan.packages[1].owedSources == 1,
            "restored propagation changed identity"
        );
        require(hasDiagnostic(plan, 215), "voided wildcard was not reported");
    }

    function testSameCommitWildcardPreservesSiblingPropagation() public view {
        Types.Snapshot memory snapshot = createPair(2, 2, 2);
        snapshot.units[0] = createWildcard(1, 2);
        snapshot.units[0].unitIndex = 2;
        snapshot.units[1] = createProviderWork(1);
        Types.Plan memory plan = planner.compute(snapshot);
        require(plan.packages[0].isPlanned && plan.packages[1].isPlanned, "wildcard reached same-commit sibling");
        require(plan.packages[1].next.patch == 1, "same-commit propagation changed");
        require(hasDiagnostic(plan, 209), "same-commit wildcard should be inert");
    }

    function testPublishedProviderWildcardCannotEraseConsumerDebt() public view {
        Types.Snapshot memory snapshot = createPair(4, 2, 3);
        snapshot.tags[2] = Types.Tag(0, 2, Types.Version(1, 1, 0, 0, 0));
        snapshot.units[0] = createWildcard(3, 1);
        snapshot.units[1] = createProviderWork(1);
        Types.Plan memory plan = planner.compute(snapshot);
        require(!plan.packages[0].isPlanned && plan.packages[0].baseline.minor == 1, "published provider changed");
        require(plan.packages[1].isPlanned && plan.packages[1].isCatchUp, "provider wildcard retracted published work");
        require(plan.packages[1].owedSources == 1 && plan.packages[1].next.patch == 1, "published source debt changed");
    }

    function testConsumerWildcardDoesNotPruneOtherTargetsWithinOriginalDepth() public view {
        Types.Snapshot memory snapshot = createPair(3, 2, 3);
        snapshot.packages = new Types.Package[](3);
        snapshot.packages[0] = Types.Package("a", Types.Version(0, 1, 0, 0, 0), true);
        snapshot.packages[1] = Types.Package("b", Types.Version(0, 1, 0, 0, 0), true);
        snapshot.packages[2] = Types.Package("c", Types.Version(0, 1, 0, 0, 0), true);
        snapshot.tags[2] = Types.Tag(2, 0, Types.Version(1, 0, 0, 0, 0));
        snapshot.edges = new Types.Edge[](2);
        snapshot.edges[0] = Types.Edge(0, 1, true, true);
        snapshot.edges[1] = Types.Edge(1, 2, true, true);
        snapshot.units[0] = createWildcard(2, 2);
        snapshot.units[1] = createProviderWork(1);
        snapshot.units[1].depth = 2;
        snapshot.units[1].propagateScope = 6;
        Types.Plan memory plan = planner.compute(snapshot);
        require(!plan.packages[1].isPlanned, "corrected consumer still planned");
        require(plan.packages[2].isPlanned && plan.packages[2].next.patch == 1, "correction pruned traversal");
        require(plan.packages[2].owedSources == 1, "correction rebased source attribution");
    }

    function testWildcardPreservesDeliveredTrainTargetWhenFreshFixArrives() public view {
        Types.Snapshot memory snapshot = createPair(5, 3, 4);
        snapshot.tags[2] = Types.Tag(0, 2, Types.Version(1, 1, 0, "rc", 0));
        snapshot.tags[3] = Types.Tag(1, 2, Types.Version(1, 1, 0, "rc", 0));
        snapshot.units[0] = createUnit(4, 2, Types.Bump.Patch);
        snapshot.units[1] = createWildcard(3, 2);
        snapshot.units[2] = createTrainWork(1);
        Types.Plan memory plan = planner.compute(snapshot);
        assertCarriedTrain(plan);
    }

    function testWildcardClearsDebtButPreservesCarriedTrainTarget() public view {
        Types.Snapshot memory snapshot = createPair(6, 3, 4);
        snapshot.tags[2] = Types.Tag(1, 2, Types.Version(1, 1, 0, "rc", 0));
        snapshot.tags[3] = Types.Tag(0, 3, Types.Version(1, 1, 0, "rc", 0));
        snapshot.units[0] = createUnit(5, 2, Types.Bump.Patch);
        snapshot.units[1] = createWildcard(4, 2);
        snapshot.units[2] = createTrainWork(1);
        Types.Plan memory plan = planner.compute(snapshot);
        assertCarriedTrain(plan);

        snapshot.units = new Types.Unit[](2);
        snapshot.units[0] = createWildcard(4, 2);
        snapshot.units[1] = createTrainWork(1);
        plan = planner.compute(snapshot);
        require(plan.publishOrder.length == 0, "carried history admitted another release without fresh work");
        require(plan.packages[1].owedSources == 0, "wildcard did not clear consumer-ahead debt");
    }

    function assertCarriedTrain(Types.Plan memory plan) private pure {
        Types.PackagePlan memory consumer = plan.packages[1];
        require(consumer.isPlanned && consumer.directBump == Types.Bump.Patch, "fresh consumer fix lost");
        require(consumer.trainBump == Types.Bump.Minor, "wildcard erased carried train target");
        require(consumer.owedSources == 0, "suppressed or delivered source still owed");
        require(
            consumer.next.major == 1 && consumer.next.minor == 1 && consumer.next.patch == 0
                && consumer.next.channel == bytes32("rc") && consumer.next.counter == 1,
            "carried train did not increment its counter"
        );
    }

    function createTrainWork(uint16 commitIndex) private pure returns (Types.Unit memory unit) {
        unit = createProviderWork(commitIndex);
        unit.propagate = Types.Bump.Minor;
        unit.channel = Types.ChannelDirective(Types.ChannelMode.Set, 0, "rc");
        unit.propagateChannel = Types.ChannelDirective(Types.ChannelMode.Inherit, 0, 0);
        unit.channelDepth = 1;
        unit.channelScope = 2;
    }

    function createPair(uint256 commitCount, uint256 unitCount, uint256 tagCount)
        private
        pure
        returns (Types.Snapshot memory snapshot)
    {
        snapshot.packages = new Types.Package[](2);
        snapshot.packages[0] = Types.Package("a", Types.Version(0, 1, 0, 0, 0), true);
        snapshot.packages[1] = Types.Package("b", Types.Version(0, 1, 0, 0, 0), true);
        snapshot.commits = new Types.Commit[](commitCount);
        for (uint256 i; i < commitCount; ++i) {
            snapshot.commits[i] = Types.Commit(bytes32(i + 1), i == 0 ? 0 : uint256(1) << (i - 1));
        }
        snapshot.units = new Types.Unit[](unitCount);
        snapshot.tags = new Types.Tag[](tagCount);
        snapshot.tags[0] = Types.Tag(0, 0, Types.Version(1, 0, 0, 0, 0));
        snapshot.tags[1] = Types.Tag(1, 0, Types.Version(1, 0, 0, 0, 0));
        snapshot.edges = new Types.Edge[](1);
        snapshot.edges[0] = Types.Edge(0, 1, true, true);
    }

    function createProviderWork(uint16 commitIndex) private pure returns (Types.Unit memory unit) {
        unit = createUnit(commitIndex, 1, Types.Bump.Minor);
        unit.propagate = Types.Bump.Patch;
        unit.depth = 1;
        unit.propagateScope = 2;
    }

    function createWildcard(uint16 commitIndex, uint256 scope) private pure returns (Types.Unit memory unit) {
        unit = createUnit(commitIndex, scope, Types.Bump.None);
        unit.correctionWildcard = true;
    }

    function createUnit(uint16 commitIndex, uint256 scope, Types.Bump bump)
        private
        pure
        returns (Types.Unit memory unit)
    {
        unit.commitIndex = commitIndex;
        unit.unitIndex = 1;
        unit.scope = scope;
        unit.bump = bump;
    }

    function hasDiagnostic(Types.Plan memory plan, uint16 code) private pure returns (bool) {
        for (uint256 i; i < plan.diagnostics.length; ++i) {
            if (plan.diagnostics[i].code == code) return true;
        }
        return false;
    }
}
