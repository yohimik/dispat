// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.28;

import {CCMETypes as Types} from "../src/CCMETypes.sol";
import {DispatPlanner} from "../src/DispatPlanner.sol";

contract PlannerAuditTest {
    DispatPlanner private planner = new DispatPlanner();

    function createSnapshot(uint256 commitCount, uint256 unitCount, uint256 tagCount)
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

    function testPrereleasePinControlsReportedChannelAndResolvability() public view {
        Types.Snapshot memory snapshot = createSnapshot(2, 1, 2);
        snapshot.units[0] = createUnit(1, 1, Types.Bump.Minor);
        snapshot.units[0].releaseKind = Types.ReleaseKind.Exact;
        snapshot.units[0].pin = Types.Version(1, 1, 0, bytes32("beta"), 0);
        snapshot.units[0].propagate = Types.Bump.Patch;
        snapshot.units[0].depth = 1;
        snapshot.units[0].propagateScope = 2;
        Types.Plan memory plan = planner.compute(snapshot);
        require(plan.packages[0].channel == bytes32("beta"), "pin channel disagrees with release");
        require(plan.packages[0].next.channel == bytes32("beta"), "pin version lost");
        require(!plan.packages[1].isPlanned, "stable consumer was bumped by prerelease pin");
    }

    function testUnresolvableHistoricalPropagationDoesNotRaiseTrain() public view {
        Types.Snapshot memory snapshot = createSnapshot(4, 3, 4);
        snapshot.tags[2] = Types.Tag(0, 1, Types.Version(1, 1, 0, bytes32("beta"), 0));
        snapshot.tags[3] = Types.Tag(1, 2, Types.Version(1, 0, 1, bytes32("rc"), 0));
        snapshot.units[0] = createUnit(3, 2, Types.Bump.Patch);
        snapshot.units[1] = createUnit(2, 2, Types.Bump.Patch);
        snapshot.units[2] = createUnit(1, 1, Types.Bump.Minor);
        snapshot.units[2].channel = Types.ChannelDirective(Types.ChannelMode.Set, 0, bytes32("beta"));
        snapshot.units[2].propagate = Types.Bump.Major;
        snapshot.units[2].depth = 1;
        snapshot.units[2].propagateScope = 2;
        Types.Plan memory plan = planner.compute(snapshot);
        require(plan.packages[1].isPlanned, "fresh fix lost");
        require(
            plan.packages[1].next.major == 1 && plan.packages[1].next.minor == 0 && plan.packages[1].next.patch == 1
                && plan.packages[1].next.counter == 1,
            "unresolvable historical bump contaminated train"
        );
    }

    function testDeletedExactDirectiveDoesNotInvalidatePlan() public view {
        Types.Snapshot memory snapshot = createSnapshot(3, 2, 2);
        snapshot.units[0] = createUnit(2, 1, Types.Bump.None);
        snapshot.units[0].correctionTargets = 2;
        snapshot.units[1] = createUnit(1, 1, Types.Bump.Minor);
        snapshot.units[1].releaseKind = Types.ReleaseKind.Exact;
        snapshot.units[1].pin = Types.Version(1, 1, 0, 0, 0);
        Types.Plan memory plan = planner.compute(snapshot);
        require(!plan.packages[0].isPlanned, "deleted exact directive planned release");
        require(plan.publishOrder.length == 0, "deleted unit left publication work");
    }

    function testEmptyExactScopeDoesNotInvalidatePlan() public view {
        Types.Snapshot memory snapshot = createSnapshot(2, 1, 2);
        snapshot.units[0] = createUnit(1, 0, Types.Bump.Minor);
        snapshot.units[0].releaseKind = Types.ReleaseKind.Exact;
        snapshot.units[0].pin = Types.Version(1, 1, 0, 0, 0);
        Types.Plan memory plan = planner.compute(snapshot);
        require(plan.publishOrder.length == 0, "empty exact scope left publication work");
    }

    function testCorrectedCorrectionRestoresExactDirective() public view {
        Types.Snapshot memory snapshot = createSnapshot(4, 3, 2);
        snapshot.units[0] = createUnit(3, 1, Types.Bump.None);
        snapshot.units[0].correctionTargets = 2;
        snapshot.units[1] = createUnit(2, 1, Types.Bump.None);
        snapshot.units[1].correctionTargets = 4;
        snapshot.units[2] = createUnit(1, 1, Types.Bump.Minor);
        snapshot.units[2].releaseKind = Types.ReleaseKind.Exact;
        snapshot.units[2].pin = Types.Version(1, 1, 0, 0, 0);
        Types.Plan memory plan = planner.compute(snapshot);
        require(plan.packages[0].isPlanned, "correcting correction did not restore exact directive");
        require(plan.packages[0].next.minor == 1, "restored exact version lost");
    }

    function testCancelledPrereleaseTrainRejectsRegressingRestatement() public view {
        Types.Snapshot memory snapshot = createSnapshot(4, 3, 3);
        snapshot.tags[2] = Types.Tag(0, 1, Types.Version(1, 1, 0, bytes32("rc"), 0));
        snapshot.units[0] = createUnit(3, 1, Types.Bump.Patch);
        snapshot.units[1] = createUnit(2, 1, Types.Bump.None);
        snapshot.units[1].isCancel = true;
        snapshot.units[2] = createUnit(1, 1, Types.Bump.Minor);
        snapshot.units[2].channel = Types.ChannelDirective(Types.ChannelMode.Set, 0, bytes32("rc"));
        try planner.compute(snapshot) returns (Types.Plan memory) {
            revert("cancelled prerelease train silently restored");
        } catch (bytes memory reason) {
            require(
                keccak256(reason) == keccak256(abi.encodeWithSelector(Types.CCMEError.selector, 195, 0, 0)),
                "regressing restatement did not report E195"
            );
        }
    }
}
