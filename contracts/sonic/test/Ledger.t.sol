// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.28;

import {CCMETypes as Types} from "../src/CCMETypes.sol";
import {DispatPlanner} from "../src/DispatPlanner.sol";
import {DispatLedger} from "../src/DispatLedger.sol";

contract LedgerCaller {
    function appendCommit(DispatLedger ledger) external returns (bool, bytes memory) {
        Types.Unit[] memory commitUnits = new Types.Unit[](0);
        return address(ledger).call(abi.encodeCall(ledger.appendCommit, (bytes32("foreign"), uint256(0), commitUnits)));
    }

    function appendRelease(DispatLedger ledger) external returns (bool, bytes memory) {
        Types.Version memory version = Types.Version(1, 0, 0, bytes32(0), 0);
        return address(ledger).call(abi.encodeCall(ledger.appendRelease, (uint16(0), uint16(0), version)));
    }
}

contract LedgerTest {
    DispatPlanner private planner;
    DispatLedger private ledger;

    function setUp() public {
        planner = new DispatPlanner();
        ledger = new DispatLedger(planner, _createPackages(), new Types.Edge[](0));
    }

    function testStoredHistoryPlansAndDischargesRelease() public {
        _appendEmpty("root", 0);
        ledger.appendRelease(0, 0, _createVersion(1, 0, 0));
        _appendUnit("feature", 1, 1, Types.Bump.Minor, 0);
        Types.Plan memory plan = ledger.compute();
        require(plan.publishOrder.length == 1 && plan.packages[0].isPlanned, "feature not planned");
        require(plan.packages[0].next.minor == 1 && plan.packages[0].next.patch == 0, "wrong feature version");
        ledger.appendRelease(0, 1, plan.packages[0].next);
        plan = ledger.compute();
        require(plan.publishOrder.length == 0 && !plan.packages[0].isPlanned, "release not discharged");
        (uint256 commitCount, uint256 unitCount, uint256 tagCount) = ledger.getCounts();
        require(commitCount == 2 && unitCount == 1 && tagCount == 2, "stored history counts");
    }

    function testOwnerIsRequiredForBothAppendPaths() public {
        LedgerCaller caller = new LedgerCaller();
        bytes32 previousHash = ledger.ledgerHash();
        (bool isSuccessful, bytes memory reason) = caller.appendCommit(ledger);
        require(
            !isSuccessful && keccak256(reason) == keccak256(abi.encodeWithSelector(DispatLedger.Unauthorized.selector)),
            "commit authorization"
        );
        (isSuccessful, reason) = caller.appendRelease(ledger);
        require(
            !isSuccessful && keccak256(reason) == keccak256(abi.encodeWithSelector(DispatLedger.Unauthorized.selector)),
            "release authorization"
        );
        require(ledger.sequence() == 1 && ledger.ledgerHash() == previousHash, "rejected caller changed chain");
    }

    function testReleaseIdentityIsImmutableAndDuplicateIsIdempotent() public {
        _appendEmpty("root", 0);
        Types.Version memory version = _createVersion(1, 0, 0);
        uint16 firstTag = ledger.appendRelease(0, 0, version);
        bytes32 recordedHash = ledger.ledgerHash();
        uint256 recordedSequence = ledger.sequence();
        require(ledger.appendRelease(0, 0, version) == firstTag, "duplicate receipt index");
        require(
            ledger.ledgerHash() == recordedHash && ledger.sequence() == recordedSequence,
            "duplicate receipt mutated chain"
        );
        _appendEmpty("next", 1);
        (bool isSuccessful, bytes memory reason) =
            address(ledger).call(abi.encodeCall(ledger.appendRelease, (uint16(0), uint16(1), version)));
        require(!isSuccessful, "release identity moved");
        require(
            keccak256(reason)
                == keccak256(abi.encodeWithSelector(Types.CCMEError.selector, uint16(191), uint256(0), uint256(0))),
            "immutable version error"
        );
        Types.Tag memory tag = ledger.getTag(firstTag);
        require(tag.commitIndex == 0, "original receipt overwritten");
    }

    function testBranchProjectionOmitsUnreachableUnitsAndTags() public {
        _appendEmpty("root", 0);
        ledger.appendRelease(0, 0, _createVersion(1, 0, 0));
        _appendUnit("left", 1, 1, Types.Bump.Minor, 0);
        ledger.appendRelease(0, 1, _createVersion(1, 1, 0));
        _appendUnit("right", 1, 2, Types.Bump.Patch, 0);

        Types.Snapshot memory current = ledger.projectSnapshot();
        require(
            current.commits.length == 2 && current.commits[1].id == bytes32("right"), "branch commits not projected"
        );
        require(current.units.length == 1 && current.units[0].commitIndex == 1, "branch units not remapped");
        require(current.tags.length == 1 && current.tags[0].commitIndex == 0, "unreachable release imported");
        Types.Plan memory plan = ledger.compute();
        require(plan.packages[0].next.minor == 0 && plan.packages[0].next.patch == 1, "other branch affected plan");

        Types.Snapshot memory left = ledger.projectSnapshotAt(1);
        require(
            left.commits.length == 2 && left.commits[1].id == bytes32("left") && left.tags.length == 2,
            "historical snapshot"
        );
        _appendEmpty("merge", 6);
        current = ledger.projectSnapshot();
        require(current.commits.length == 4 && current.units.length == 2, "merge did not join branches");
        require(current.commits[3].parents == 6, "merge parents changed");
    }

    function testCorrectionTargetsUsePermanentUnitIdsAcrossBranches() public {
        _appendEmpty("root", 0);
        ledger.appendRelease(0, 0, _createVersion(1, 0, 0));
        _appendUnit("feature", 1, 1, Types.Bump.Minor, 0);
        _appendUnit("other-branch", 1, 2, Types.Bump.Major, 0);
        _appendUnit("correction", 2, 3, Types.Bump.Patch, 1);

        Types.Unit memory stored = ledger.getUnit(2);
        require(stored.correctionTargets == 1 && stored.commitIndex == 3, "permanent unit identity changed");
        Types.Snapshot memory current = ledger.projectSnapshot();
        require(current.units.length == 2 && current.units[0].correctionTargets == 2, "correction target not projected");
        require(
            current.units[0].commitIndex == 2 && current.units[1].commitIndex == 1, "correction commits not projected"
        );
        Types.Plan memory plan = ledger.compute();
        require(
            plan.packages[0].next.minor == 0 && plan.packages[0].next.patch == 1, "correction did not replace feature"
        );
    }

    function testRejectCorrectionAcrossUnmergedBranchAtomically() public {
        _appendEmpty("root", 0);
        _appendUnit("left", 1, 1, Types.Bump.Minor, 0);
        Types.Unit[] memory commitUnits = _createUnits(2, Types.Bump.Patch, 1);
        bytes32 previousHash = ledger.ledgerHash();
        (bool isSuccessful, bytes memory reason) =
            address(ledger).call(abi.encodeCall(ledger.appendCommit, (bytes32("right"), uint256(1), commitUnits)));
        require(!isSuccessful, "cross-branch correction accepted");
        require(
            keccak256(reason)
                == keccak256(abi.encodeWithSelector(Types.CCMEError.selector, uint16(210), uint256(0), uint256(1))),
            "non-ancestor correction error"
        );
        (uint256 commitCount, uint256 unitCount,) = ledger.getCounts();
        require(commitCount == 2 && unitCount == 1 && ledger.ledgerHash() == previousHash, "failed append leaked state");
    }

    function testRejectDuplicateCommitFutureParentAndWrongUnitPosition() public {
        _appendEmpty("root", 0);
        Types.Unit[] memory noUnits = new Types.Unit[](0);
        (bool isSuccessful,) =
            address(ledger).call(abi.encodeCall(ledger.appendCommit, (bytes32("root"), uint256(1), noUnits)));
        require(!isSuccessful, "duplicate commit accepted");
        (isSuccessful,) =
            address(ledger).call(abi.encodeCall(ledger.appendCommit, (bytes32("future"), uint256(2), noUnits)));
        require(!isSuccessful, "future parent accepted");
        Types.Unit[] memory wrongPosition = _createUnits(0, Types.Bump.Patch, 0);
        (isSuccessful,) =
            address(ledger).call(abi.encodeCall(ledger.appendCommit, (bytes32("wrong"), uint256(1), wrongPosition)));
        require(!isSuccessful, "wrong unit position accepted");
        (uint256 commitCount, uint256 unitCount,) = ledger.getCounts();
        require(commitCount == 1 && unitCount == 0, "invalid append mutated history");
    }

    function testValidationRevertDoesNotPoisonHistory() public {
        _appendEmpty("root", 0);
        Types.Unit[] memory commitUnits = _createUnits(1, Types.Bump.Minor, 0);
        commitUnits[0].scope = 2;
        bytes32 previousHash = ledger.ledgerHash();
        (bool isSuccessful,) = address(ledger)
            .call(abi.encodeCall(ledger.appendCommit, (bytes32("invalid-scope"), uint256(1), commitUnits)));
        require(!isSuccessful && ledger.ledgerHash() == previousHash, "invalid scope persisted");
        commitUnits[0].scope = 1;
        require(ledger.appendCommit("invalid-scope", 1, commitUnits) == 1, "reverted commit identity retained");
    }

    function testHashChainCommitsToDomainPredecessorAndPayload() public {
        bytes32 previousHash = ledger.ledgerHash();
        Types.Unit[] memory noUnits = new Types.Unit[](0);
        bytes32 payloadHash = keccak256(abi.encode(bytes32("root"), uint256(0), noUnits));
        _appendEmpty("root", 0);
        bytes32 expected =
            keccak256(abi.encode(block.chainid, address(ledger), uint256(2), previousHash, uint8(1), payloadHash));
        require(ledger.sequence() == 2 && ledger.ledgerHash() == expected, "commit hash chain");
        DispatLedger second = new DispatLedger(planner, _createPackages(), new Types.Edge[](0));
        second.appendCommit("root", 0, noUnits);
        require(second.ledgerHash() != ledger.ledgerHash(), "ledger domain missing");
        Types.Version memory version = _createVersion(1, 0, 0);
        previousHash = ledger.ledgerHash();
        ledger.appendRelease(0, 0, version);
        payloadHash = keccak256(abi.encode(uint16(0), uint16(0), version));
        expected =
            keccak256(abi.encode(block.chainid, address(ledger), uint256(3), previousHash, uint8(2), payloadHash));
        require(ledger.ledgerHash() == expected, "release hash chain");
    }

    function testMultipleCommitUnitsReverseWithoutChangingUnitNumbers() public {
        Types.Unit[] memory commitUnits = new Types.Unit[](2);
        commitUnits[0].unitIndex = 1;
        commitUnits[0].scope = 1;
        commitUnits[0].bump = Types.Bump.Minor;
        commitUnits[1].unitIndex = 2;
        commitUnits[1].scope = 1;
        commitUnits[1].isRelease = true;
        commitUnits[1].releaseKind = Types.ReleaseKind.Hold;
        ledger.appendCommit("held", 0, commitUnits);
        Types.Snapshot memory current = ledger.projectSnapshot();
        require(current.units[0].unitIndex == 2 && current.units[1].unitIndex == 1, "unit precedence lost");
        Types.Plan memory plan = ledger.compute();
        require(plan.packages[0].isHeld && !plan.packages[0].isPlanned, "hold precedence lost");
    }

    function testFuzzAppendOnlyDAGPreservesEarlierRecords(uint64 seed, uint8 lengthSeed) public {
        uint256 length = 1 + uint256(lengthSeed) % 6;
        uint256[] memory reachableCommits = new uint256[](length);
        bytes32[] memory commitIds = new bytes32[](length);
        uint256[] memory parents = new uint256[](length);
        for (uint256 i; i < length; ++i) {
            parents[i] = (uint256(seed) >> (i * 8)) & ((uint256(1) << i) - 1);
            commitIds[i] = keccak256(abi.encode("immutable-history", seed, i));
            reachableCommits[i] = uint256(1) << i;
            for (uint256 j; j < i; ++j) {
                if ((parents[i] & (uint256(1) << j)) != 0) reachableCommits[i] |= reachableCommits[j];
            }
            bytes32 previousHash = ledger.ledgerHash();
            ledger.appendCommit(commitIds[i], parents[i], _createUnits(uint16(i), Types.Bump.Patch, 0));
            require(ledger.ledgerHash() != previousHash, "append did not advance hash");
            ledger.appendRelease(0, uint16(i), _createVersion(1, 0, uint64(i)));
            require(ledger.sequence() == 3 + i * 2, "append sequence drift");
            _assertStoredPrefix(commitIds, parents, i + 1);
            Types.Snapshot memory current = ledger.projectSnapshot();
            uint256 reachableCount;
            for (uint256 j; j <= i; ++j) {
                if ((reachableCommits[i] & (uint256(1) << j)) != 0) ++reachableCount;
            }
            require(current.commits.length == reachableCount, "DAG projection lost commits");
            require(
                current.units.length == reachableCount && current.tags.length == reachableCount,
                "DAG projection lost records"
            );
            require(ledger.compute().publishOrder.length == 0, "current release did not discharge reachable work");
        }
    }

    function _assertStoredPrefix(bytes32[] memory commitIds, uint256[] memory parents, uint256 length) private view {
        for (uint256 i; i < length; ++i) {
            Types.Commit memory commit = ledger.getCommit(uint16(i));
            require(commit.id == commitIds[i] && commit.parents == parents[i], "old commit changed");
            Types.Unit memory unit = ledger.getUnit(uint16(i));
            require(unit.commitIndex == i && unit.unitIndex == 1 && unit.bump == Types.Bump.Patch, "old unit changed");
            Types.Tag memory tag = ledger.getTag(uint16(i));
            require(tag.commitIndex == i && tag.version.major == 1 && tag.version.patch == i, "old release changed");
        }
    }

    function _appendEmpty(bytes32 commitId, uint256 parents) private {
        ledger.appendCommit(commitId, parents, new Types.Unit[](0));
    }

    function _appendUnit(
        bytes32 commitId,
        uint256 parents,
        uint16 commitIndex,
        Types.Bump bump,
        uint256 correctionTargets
    ) private {
        ledger.appendCommit(commitId, parents, _createUnits(commitIndex, bump, correctionTargets));
    }

    function _createUnits(uint16 commitIndex, Types.Bump bump, uint256 correctionTargets)
        private
        pure
        returns (Types.Unit[] memory commitUnits)
    {
        commitUnits = new Types.Unit[](1);
        commitUnits[0].commitIndex = commitIndex;
        commitUnits[0].unitIndex = 1;
        commitUnits[0].scope = 1;
        commitUnits[0].bump = bump;
        commitUnits[0].correctionTargets = correctionTargets;
    }

    function _createPackages() private pure returns (Types.Package[] memory workspacePackages) {
        workspacePackages = new Types.Package[](1);
        workspacePackages[0] = Types.Package("core", _createVersion(0, 1, 0), true);
    }

    function _createVersion(uint64 major, uint64 minor, uint64 patch) private pure returns (Types.Version memory) {
        return Types.Version(major, minor, patch, bytes32(0), 0);
    }
}
