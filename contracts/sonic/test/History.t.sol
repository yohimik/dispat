// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.28;

import {CCMETypes as Types} from "../src/CCMETypes.sol";
import {CCMEHistory as History} from "../src/CCMEHistory.sol";

contract HistoryHarness {
    function analyze(Types.Snapshot calldata input) external pure returns (History.State memory) {
        return History.analyze(input);
    }
}

contract HistoryTest {
    HistoryHarness private harness = new HistoryHarness();

    function createVersion(uint64 major, uint64 minor, uint64 patch) private pure returns (Types.Version memory) {
        return Types.Version(major, minor, patch, bytes32(0), 0);
    }

    function createSnapshot(uint256 packages, uint256 commits, uint256 units, uint256 tags)
        private
        pure
        returns (Types.Snapshot memory snapshot)
    {
        snapshot.packages = new Types.Package[](packages);
        for (uint256 packageIndex; packageIndex < packages; ++packageIndex) {
            snapshot.packages[packageIndex] = Types.Package(packageIndex == 0 ? "a" : "b", createVersion(0, 1, 0), true);
        }
        snapshot.commits = new Types.Commit[](commits);
        for (uint256 commitIndex; commitIndex < commits; ++commitIndex) {
            snapshot.commits[commitIndex] =
                Types.Commit(bytes32(commitIndex + 1), commitIndex == 0 ? 0 : uint256(1) << (commitIndex - 1));
        }
        snapshot.units = new Types.Unit[](units);
        snapshot.tags = new Types.Tag[](tags);
        snapshot.edges = new Types.Edge[](0);
    }

    function createUnit(uint16 commitIndex, uint256 scope) private pure returns (Types.Unit memory unit) {
        unit.commitIndex = commitIndex;
        unit.unitIndex = 1;
        unit.scope = scope;
        unit.bump = Types.Bump.Minor;
    }

    function _assertError(Types.Snapshot memory snapshot, bytes4 selector) private view {
        (bool success, bytes memory result) = address(harness).staticcall(abi.encodeCall(harness.analyze, (snapshot)));
        require(!success, "accepted invalid history");
        require(result.length >= 4, "missing structured error");
        bytes4 actual;
        assembly { actual := mload(add(result, 32)) }
        require(actual == selector, "wrong error selector");
    }

    function _assertCCMEError(Types.Snapshot memory snapshot, uint256 expectedCode) private view {
        (bool success, bytes memory result) = address(harness).staticcall(abi.encodeCall(harness.analyze, (snapshot)));
        require(!success && result.length == 100, "missing CCME error");
        bytes4 selector;
        uint256 code;
        assembly {
            selector := mload(add(result, 32))
            code := mload(add(result, 36))
        }
        require(selector == Types.CCMEError.selector && code == expectedCode, "wrong CCME diagnostic");
    }

    function testBaselineUsesSemverNotCommitRecency() public view {
        Types.Snapshot memory snapshot = createSnapshot(1, 3, 1, 2);
        snapshot.tags[0] = Types.Tag(0, 0, createVersion(1, 5, 0));
        snapshot.tags[1] = Types.Tag(0, 1, createVersion(1, 4, 0));
        snapshot.units[0] = createUnit(1, 1);
        History.State memory history = harness.analyze(snapshot);
        require(history.baselines[0].minor == 5 && history.baselineCommits[0] == 0, "wrong baseline");
        require(history.fresh[0] == 1, "recency discharged work");
    }

    function testTrainWindowRetainsPublishedPrereleaseWork() public view {
        Types.Snapshot memory snapshot = createSnapshot(1, 3, 2, 2);
        snapshot.tags[0] = Types.Tag(0, 0, createVersion(1, 0, 0));
        snapshot.tags[1] = Types.Tag(0, 1, Types.Version(1, 1, 0, bytes32("beta"), 0));
        snapshot.units[0] = createUnit(2, 1);
        snapshot.units[1] = createUnit(1, 1);
        History.State memory history = harness.analyze(snapshot);
        require(history.train[0] == 1 && history.train[1] == 1, "train lost published bump");
        require(history.fresh[0] == 1 && history.fresh[1] == 0, "fresh window confused with train");
    }

    function testConsumerAheadOfProviderStillOwed() public view {
        Types.Snapshot memory snapshot = createSnapshot(2, 4, 1, 4);
        snapshot.tags[0] = Types.Tag(0, 0, createVersion(1, 0, 0));
        snapshot.tags[1] = Types.Tag(1, 0, createVersion(1, 0, 0));
        snapshot.tags[2] = Types.Tag(1, 2, createVersion(1, 1, 0));
        snapshot.tags[3] = Types.Tag(0, 3, createVersion(1, 1, 0));
        snapshot.units[0] = createUnit(1, 1);
        History.State memory history = harness.analyze(snapshot);
        require(history.fresh[0] == 0, "fixture should be outside both fresh windows");
        require(!History.isDelivered(history, 2, 0, 1, 1), "consumer's own release falsely discharged provider");
        snapshot.tags[2].commitIndex = 3;
        history = harness.analyze(snapshot);
        require(History.isDelivered(history, 2, 0, 1, 1), "source then consumer did not discharge");
    }

    function testDeliveryIncludesOlderSourceTags() public view {
        Types.Snapshot memory snapshot = createSnapshot(2, 4, 1, 3);
        snapshot.tags[0] = Types.Tag(0, 1, createVersion(1, 1, 0));
        snapshot.tags[1] = Types.Tag(0, 3, createVersion(1, 2, 0));
        snapshot.tags[2] = Types.Tag(1, 2, createVersion(2, 0, 0));
        snapshot.units[0] = createUnit(1, 1);
        History.State memory history = harness.analyze(snapshot);
        require(History.isDelivered(history, 2, 0, 1, 1), "only newest provider tag was considered");
    }

    function testCancelFollowsAncestryAcrossMerge() public view {
        Types.Snapshot memory snapshot = createSnapshot(1, 4, 3, 0);
        snapshot.commits[2].parents = 1; // sibling of cancel commit 1
        snapshot.commits[3].parents = 6; // merge commits 1 and 2
        snapshot.units[0] = createUnit(2, 1);
        snapshot.units[1] = createUnit(1, 1);
        snapshot.units[1].bump = Types.Bump.None;
        snapshot.units[1].isCancel = true;
        snapshot.units[2] = createUnit(0, 1);
        History.State memory history = harness.analyze(snapshot);
        require(history.cancelled[0] == 0, "cancel crossed into unseen branch");
        require(history.cancelled[1] == 1 && history.cancelled[2] == 1, "cancel missed ancestor or self");
    }

    function testCancelIncludesOtherUnitsInItsOwnCommit() public view {
        Types.Snapshot memory snapshot = createSnapshot(1, 1, 2, 0);
        snapshot.units[0] = createUnit(0, 1);
        snapshot.units[0].unitIndex = 2;
        snapshot.units[1] = createUnit(0, 1);
        snapshot.units[1].bump = Types.Bump.None;
        snapshot.units[1].isCancel = true;
        History.State memory history = harness.analyze(snapshot);
        require(history.cancelled[0] == 1, "same-commit work escaped barrier");
    }

    function testDeletingCorrectionRestoresOriginal() public view {
        Types.Snapshot memory snapshot = createSnapshot(1, 3, 3, 0);
        snapshot.units[0] = createUnit(2, 1);
        snapshot.units[0].correctionTargets = 2;
        snapshot.units[1] = createUnit(1, 1);
        snapshot.units[1].correctionTargets = 4;
        snapshot.units[2] = createUnit(0, 1);
        History.State memory history = harness.analyze(snapshot);
        require(
            history.scopes[0] == 1 && history.scopes[1] == 0 && history.scopes[2] == 1,
            "void correction still dropped original"
        );
        require(history.diagnostics.length == 1 && history.diagnostics[0].code == 215, "missing void warning");
    }

    function testPartialCorrectionPreservesOtherPackages() public view {
        Types.Snapshot memory snapshot = createSnapshot(2, 2, 2, 0);
        snapshot.units[0] = createUnit(1, 1);
        snapshot.units[0].correctionTargets = 2;
        snapshot.units[1] = createUnit(0, 3);
        History.State memory history = harness.analyze(snapshot);
        require(history.scopes[1] == 2, "partial correction widened scope");
    }

    function testInheritedCorrectionUnionsDisjointTargets() public view {
        Types.Snapshot memory snapshot = createSnapshot(2, 3, 3, 0);
        snapshot.units[0] = createUnit(2, 3);
        snapshot.units[0].correctionTargets = 6;
        snapshot.units[0].correctionScopeInherited = true;
        snapshot.units[1] = createUnit(1, 2);
        snapshot.units[2] = createUnit(0, 1);
        History.State memory history = harness.analyze(snapshot);
        require(history.scopes[1] == 0 && history.scopes[2] == 0, "inherited union not applied");
    }

    function testCorrectionCannotRewriteReleasedTargetForOnePackage() public view {
        Types.Snapshot memory snapshot = createSnapshot(2, 2, 2, 1);
        snapshot.tags[0] = Types.Tag(0, 0, createVersion(1, 0, 0));
        snapshot.units[0] = createUnit(1, 3);
        snapshot.units[0].correctionTargets = 2;
        snapshot.units[1] = createUnit(0, 3);
        History.State memory history = harness.analyze(snapshot);
        require(history.scopes[1] == 1, "correction changed published history");
        require(
            history.diagnostics.length == 1 && history.diagnostics[0].code == 209
                && history.diagnostics[0].packageIndex == 0,
            "missing discharged-target warning"
        );
    }

    function testWildcardSkipsControlAndOwnCommit() public view {
        Types.Snapshot memory snapshot = createSnapshot(1, 2, 4, 0);
        snapshot.units[0] = createUnit(1, 1);
        snapshot.units[0].unitIndex = 2;
        snapshot.units[0].correctionWildcard = true;
        snapshot.units[1] = createUnit(1, 1);
        snapshot.units[2] = createUnit(0, 1);
        snapshot.units[2].unitIndex = 2;
        snapshot.units[2].bump = Types.Bump.None;
        snapshot.units[2].isRelease = true;
        snapshot.units[3] = createUnit(0, 1);
        History.State memory history = harness.analyze(snapshot);
        require(history.scopes[1] == 1 && history.scopes[2] == 1 && history.scopes[3] == 0, "wildcard reach incorrect");
    }

    function testCorrectionOfCancelledTargetIsNoop() public view {
        Types.Snapshot memory snapshot = createSnapshot(1, 3, 3, 0);
        snapshot.units[0] = createUnit(2, 1);
        snapshot.units[0].correctionTargets = 4;
        snapshot.units[1] = createUnit(1, 1);
        snapshot.units[1].bump = Types.Bump.None;
        snapshot.units[1].isCancel = true;
        snapshot.units[2] = createUnit(0, 1);
        History.State memory history = harness.analyze(snapshot);
        require(history.scopes[0] == 1 && history.scopes[2] == 1, "correction rewrote cancelled target");
        require(history.cancelled[0] == 0 && history.cancelled[2] == 1, "barrier masks changed");
        require(
            history.diagnostics.length == 1 && history.diagnostics[0].code == 209, "missing cancelled-target warning"
        );
    }

    function testRejectsSameVersionAtDifferentCommits() public view {
        Types.Snapshot memory snapshot = createSnapshot(1, 2, 0, 2);
        snapshot.tags[0] = Types.Tag(0, 0, createVersion(1, 0, 0));
        snapshot.tags[1] = Types.Tag(0, 1, createVersion(1, 0, 0));
        _assertCCMEError(snapshot, 191);
    }

    function testRejectsNonAncestorCorrection() public view {
        Types.Snapshot memory snapshot = createSnapshot(1, 4, 2, 0);
        snapshot.commits[2].parents = 1;
        snapshot.commits[3].parents = 6;
        snapshot.units[0] = createUnit(2, 1);
        snapshot.units[0].correctionTargets = 2;
        snapshot.units[1] = createUnit(1, 1);
        _assertCCMEError(snapshot, 210);
    }

    function testRejectsCorrectionOfReleaseControl() public view {
        Types.Snapshot memory snapshot = createSnapshot(1, 2, 2, 0);
        snapshot.units[0] = createUnit(1, 1);
        snapshot.units[0].correctionTargets = 2;
        snapshot.units[1] = createUnit(0, 1);
        snapshot.units[1].bump = Types.Bump.None;
        snapshot.units[1].isRelease = true;
        _assertCCMEError(snapshot, 212);
    }

    function testRejectsCorrectionScopeWidening() public view {
        Types.Snapshot memory snapshot = createSnapshot(2, 2, 2, 0);
        snapshot.units[0] = createUnit(1, 3);
        snapshot.units[0].correctionTargets = 2;
        snapshot.units[1] = createUnit(0, 1);
        _assertCCMEError(snapshot, 213);
    }

    function testRejectsForwardParentAndUnreachableHistory() public view {
        Types.Snapshot memory snapshot = createSnapshot(1, 2, 0, 0);
        snapshot.commits[0].parents = 2;
        _assertError(snapshot, Types.InvalidInput.selector);
        snapshot.commits[0].parents = 0;
        snapshot.commits[1].parents = 0;
        _assertError(snapshot, Types.InvalidInput.selector);
    }

    function testRejectsDuplicateCommitAndUnorderedUnits() public view {
        Types.Snapshot memory snapshot = createSnapshot(1, 2, 2, 0);
        snapshot.commits[1].id = snapshot.commits[0].id;
        _assertError(snapshot, Types.InvalidInput.selector);
        snapshot.commits[1].id = bytes32(uint256(2));
        snapshot.units[0] = createUnit(0, 1);
        snapshot.units[1] = createUnit(1, 1);
        _assertError(snapshot, Types.InvalidInput.selector);
    }

    function testRejectsOutOfBoundsMasks() public view {
        Types.Snapshot memory snapshot = createSnapshot(1, 1, 1, 0);
        snapshot.units[0] = createUnit(0, 2);
        _assertError(snapshot, Types.InvalidInput.selector);
        snapshot.units[0].scope = 1;
        snapshot.units[0].correctionTargets = 2;
        _assertCCMEError(snapshot, 211);
    }

    function testFull256CommitMaskDoesNotOverflow() public view {
        Types.Snapshot memory snapshot = createSnapshot(1, 256, 1, 0);
        snapshot.units[0] = createUnit(255, 1);
        History.State memory history = harness.analyze(snapshot);
        require(history.ancestors[255] == type(uint256).max, "256-bit mask overflow");
        require(history.fresh[0] == 1, "last commit bit lost");
    }
}
