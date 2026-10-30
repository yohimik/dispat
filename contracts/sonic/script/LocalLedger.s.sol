// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.28;

import {CCMETypes as Types} from "../src/CCMETypes.sol";
import {DispatPlanner} from "../src/DispatPlanner.sol";
import {DispatLedger} from "../src/DispatLedger.sol";

contract LocalLedgerDemo {
    uint256 private constant PACKAGE_COUNT = 1;
    uint16 private constant PACKAGE_INDEX = 0;
    uint256 private constant EDGE_COUNT = 0;
    uint256 private constant GENESIS_UNIT_COUNT = 0;
    uint256 private constant FEATURE_UNIT_COUNT = 1;
    uint256 private constant FEATURE_UNIT_ARRAY_INDEX = 0;
    uint16 private constant GENESIS_COMMIT_INDEX = 0;
    uint16 private constant FEATURE_COMMIT_INDEX = 1;
    uint16 private constant FEATURE_UNIT_INDEX = 1;
    uint256 private constant GENESIS_PARENT_MASK = 0;
    uint256 private constant FEATURE_PARENT_MASK = 1;
    uint256 private constant PACKAGE_SCOPE_MASK = 1;
    uint64 private constant INITIAL_MAJOR_VERSION = 0;
    uint64 private constant INITIAL_MINOR_VERSION = 1;
    uint64 private constant STABLE_MAJOR_VERSION = 1;
    uint64 private constant GENESIS_MINOR_VERSION = 0;
    uint64 private constant FEATURE_MINOR_VERSION = 1;
    uint64 private constant PATCH_VERSION = 0;
    bytes32 private constant STABLE_CHANNEL_NAME = bytes32(0);
    uint64 private constant STABLE_COUNTER_VALUE = 0;
    bool private constant PRESERVE_MAJOR_ZERO_FLAG = true;
    uint256 private constant EXPECTED_RELEASE_COUNT = 1;
    uint256 private constant EXPECTED_REMAINING_RELEASE_COUNT = 0;
    string private constant PACKAGE_NAME = "sol";
    string private constant GENESIS_COMMIT_MESSAGE = "demo: genesis";
    string private constant FEATURE_COMMIT_MESSAGE = "feat(sol): demonstrate local release history";
    string private constant RELEASE_COUNT_ERROR_MESSAGE = "expected one sol release";
    string private constant FEATURE_VERSION_ERROR_MESSAGE = "feature must plan stable 1.1.0";
    string private constant REMAINING_RELEASE_ERROR_MESSAGE = "recorded release must discharge its work";
    string private constant RECEIPT_IDEMPOTENCE_ERROR_MESSAGE = "identical receipt must preserve history";

    event ContractsCreated(address indexed planner, address indexed ledger);
    event ReleasePlanned(string packageName, uint64 major, uint64 minor, uint64 patch);
    event LedgerCompleted(bytes32 indexed ledgerHash, uint256 recordCount, uint256 remainingReleases);

    function run()
        external
        returns (
            address plannerAddress,
            address ledgerAddress,
            Types.Version memory releasedVersion,
            bytes32 ledgerHash,
            uint256 remainingReleases
        )
    {
        DispatPlanner plannerContract = new DispatPlanner();
        Types.Package[] memory workspacePackages = new Types.Package[](PACKAGE_COUNT);
        workspacePackages[PACKAGE_INDEX] = Types.Package({
            name: PACKAGE_NAME,
            initialVersion: Types.Version({
                major: INITIAL_MAJOR_VERSION,
                minor: INITIAL_MINOR_VERSION,
                patch: PATCH_VERSION,
                channel: STABLE_CHANNEL_NAME,
                counter: STABLE_COUNTER_VALUE
            }),
            preserveMajorZero: PRESERVE_MAJOR_ZERO_FLAG
        });
        DispatLedger ledgerContract = new DispatLedger(plannerContract, workspacePackages, new Types.Edge[](EDGE_COUNT));
        emit ContractsCreated(address(plannerContract), address(ledgerContract));

        ledgerContract.appendCommit(
            keccak256(bytes(GENESIS_COMMIT_MESSAGE)), GENESIS_PARENT_MASK, new Types.Unit[](GENESIS_UNIT_COUNT)
        );
        ledgerContract.appendRelease(
            PACKAGE_INDEX,
            GENESIS_COMMIT_INDEX,
            Types.Version({
                major: STABLE_MAJOR_VERSION,
                minor: GENESIS_MINOR_VERSION,
                patch: PATCH_VERSION,
                channel: STABLE_CHANNEL_NAME,
                counter: STABLE_COUNTER_VALUE
            })
        );

        Types.Unit[] memory featureUnits = new Types.Unit[](FEATURE_UNIT_COUNT);
        featureUnits[FEATURE_UNIT_ARRAY_INDEX].commitIndex = FEATURE_COMMIT_INDEX;
        featureUnits[FEATURE_UNIT_ARRAY_INDEX].unitIndex = FEATURE_UNIT_INDEX;
        featureUnits[FEATURE_UNIT_ARRAY_INDEX].scope = PACKAGE_SCOPE_MASK;
        featureUnits[FEATURE_UNIT_ARRAY_INDEX].bump = Types.Bump.Minor;
        ledgerContract.appendCommit(keccak256(bytes(FEATURE_COMMIT_MESSAGE)), FEATURE_PARENT_MASK, featureUnits);

        Types.Plan memory releasePlan = ledgerContract.compute();
        require(
            releasePlan.publishOrder.length == EXPECTED_RELEASE_COUNT
                && releasePlan.publishOrder[PACKAGE_INDEX] == PACKAGE_INDEX,
            RELEASE_COUNT_ERROR_MESSAGE
        );
        releasedVersion = releasePlan.packages[PACKAGE_INDEX].next;
        require(
            releasedVersion.major == STABLE_MAJOR_VERSION && releasedVersion.minor == FEATURE_MINOR_VERSION
                && releasedVersion.patch == PATCH_VERSION && releasedVersion.channel == STABLE_CHANNEL_NAME
                && releasedVersion.counter == STABLE_COUNTER_VALUE,
            FEATURE_VERSION_ERROR_MESSAGE
        );
        emit ReleasePlanned(PACKAGE_NAME, releasedVersion.major, releasedVersion.minor, releasedVersion.patch);

        ledgerContract.appendRelease(PACKAGE_INDEX, FEATURE_COMMIT_INDEX, releasedVersion);
        Types.Plan memory remainingPlan = ledgerContract.compute();
        remainingReleases = remainingPlan.publishOrder.length;
        require(remainingReleases == EXPECTED_REMAINING_RELEASE_COUNT, REMAINING_RELEASE_ERROR_MESSAGE);
        ledgerHash = ledgerContract.ledgerHash();
        uint256 recordCount = ledgerContract.sequence();
        ledgerContract.appendRelease(PACKAGE_INDEX, FEATURE_COMMIT_INDEX, releasedVersion);
        require(
            ledgerContract.ledgerHash() == ledgerHash && ledgerContract.sequence() == recordCount,
            RECEIPT_IDEMPOTENCE_ERROR_MESSAGE
        );
        emit LedgerCompleted(ledgerHash, recordCount, remainingReleases);
        return (address(plannerContract), address(ledgerContract), releasedVersion, ledgerHash, remainingReleases);
    }
}
