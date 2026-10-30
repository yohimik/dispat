// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.28;

import {CCMETypes as Types} from "./CCMETypes.sol";
import {CCMEHistory as History} from "./CCMEHistory.sol";
import {CCMEVersion as Versioning} from "./CCMEVersion.sol";
import {CCMEChannels as Channels} from "./CCMEChannels.sol";
import {DispatPlanner} from "./DispatPlanner.sol";

/// @notice Append-only local-chain representation of one normalized repository's history and release records.
/// @dev The owner imports facts; authorization does not prove their correspondence to an external Git repository.
contract DispatLedger {
    uint8 private constant WORKSPACE_RECORD_KIND = 0;
    uint8 private constant COMMIT_RECORD_KIND = 1;
    uint8 private constant RELEASE_RECORD_KIND = 2;
    uint256 private constant EMPTY_CONTRACT_CODE_BYTES = 0;
    uint256 private constant EMPTY_HISTORY_COUNT = 0;
    uint256 private constant ABSENT_RECORD_POSITION = 0;
    uint256 private constant STORED_POSITION_OFFSET_COUNT = 1;
    uint256 private constant UNIT_ORDINAL_OFFSET_COUNT = 1;
    uint256 private constant ARRAY_START_INDEX = 0;
    uint256 private constant ARRAY_PREVIOUS_OFFSET_COUNT = 1;
    uint256 private constant MASK_SEED_VALUE = 1;
    uint256 private constant EMPTY_MASK_VALUE = 0;
    uint256 private constant MASK_WIDTH_BITS = 256;
    uint256 private constant INVALID_FIELD_DEFAULT_INDEX = 0;
    uint256 private constant CORRECTION_DEFAULT_PACKAGE_INDEX = 0;
    bytes32 private constant EMPTY_COMMIT_ID_VALUE = bytes32(0);
    bool private constant DIRECT_CHANNEL_DIRECTIVE_FLAG = false;
    bool private constant PROPAGATED_CHANNEL_DIRECTIVE_FLAG = true;
    uint16 private constant CORRECTION_SELECTOR_ERROR_CODE = 211;
    uint16 private constant RELEASE_IDENTITY_CONFLICT_ERROR_CODE = 191;
    uint16 private constant CORRECTION_ANCESTRY_ERROR_CODE = 210;
    bytes32 private constant PLANNER_FIELD_NAME = "planner";
    bytes32 private constant COMMITS_FIELD_NAME = "commits";
    bytes32 private constant COMMIT_ID_FIELD_NAME = "commit.id";
    bytes32 private constant COMMIT_PARENTS_FIELD_NAME = "commit.parents";
    bytes32 private constant UNITS_FIELD_NAME = "units";
    bytes32 private constant UNIT_POSITION_FIELD_NAME = "unit.position";
    bytes32 private constant TAG_PACKAGE_FIELD_NAME = "tag.package";
    bytes32 private constant TAG_COMMIT_FIELD_NAME = "tag.commit";
    bytes32 private constant TAGS_FIELD_NAME = "tags";
    bytes32 private constant HEAD_FIELD_NAME = "head";
    bytes32 private constant COMMIT_FIELD_NAME = "commit";
    bytes32 private constant UNIT_FIELD_NAME = "unit";
    bytes32 private constant TAG_FIELD_NAME = "tag";

    error Unauthorized();

    struct Projection {
        uint16[] commits;
        uint16[] units;
        uint256 commitCount;
        uint256 unitCount;
        uint256 tagCount;
    }

    address public immutable owner;
    DispatPlanner public immutable planner;
    bytes32 public ledgerHash;
    uint256 public sequence;

    Types.Package[] private packages;
    Types.Edge[] private edges;
    Types.Commit[] private commits;
    Types.Unit[] private units;
    Types.Tag[] private tags;
    uint256[] private ancestors;
    mapping(bytes32 => uint256) private commitPositions;
    mapping(bytes32 => uint256) private releasePositions;

    event LedgerRecord(
        uint256 indexed sequence,
        bytes32 indexed recordHash,
        bytes32 indexed previousHash,
        uint8 kind,
        bytes32 payloadHash
    );
    event CommitAppended(
        uint16 indexed commitIndex, bytes32 indexed commitId, uint256 parents, uint16 firstUnit, uint16 unitCount
    );
    event ReleaseRecorded(
        uint16 indexed tagIndex, uint16 indexed packageIndex, uint16 indexed commitIndex, bytes32 versionHash
    );

    modifier onlyOwner() {
        if (msg.sender != owner) revert Unauthorized();
        _;
    }

    constructor(
        DispatPlanner plannerContract,
        Types.Package[] memory workspacePackages,
        Types.Edge[] memory workspaceEdges
    ) {
        if (address(plannerContract).code.length == EMPTY_CONTRACT_CODE_BYTES) {
            revert Types.InvalidInput(PLANNER_FIELD_NAME, INVALID_FIELD_DEFAULT_INDEX);
        }
        owner = msg.sender;
        planner = plannerContract;
        Types.Snapshot memory workspace;
        workspace.packages = workspacePackages;
        workspace.edges = workspaceEdges;
        plannerContract.compute(workspace);
        for (uint256 i; i < workspacePackages.length; ++i) {
            packages.push(workspacePackages[i]);
        }
        for (uint256 i; i < workspaceEdges.length; ++i) {
            edges.push(workspaceEdges[i]);
        }
        appendRecord(
            WORKSPACE_RECORD_KIND, keccak256(abi.encode(address(plannerContract), workspacePackages, workspaceEdges))
        );
    }

    /// @notice Appends a commit and its complete, ascending, one-based unit list atomically.
    /// @dev Parent bits and correction target bits use permanent append-order commit and unit indices.
    function appendCommit(bytes32 commitId, uint256 parentCommits, Types.Unit[] calldata commitUnits)
        external
        onlyOwner
        returns (uint16 commitIndex)
    {
        uint256 position = commits.length;
        if (position >= Types.MAX_COMMITS) revert Types.InvalidInput(COMMITS_FIELD_NAME, position);
        if (commitId == EMPTY_COMMIT_ID_VALUE || commitPositions[commitId] != ABSENT_RECORD_POSITION) {
            revert Types.InvalidInput(COMMIT_ID_FIELD_NAME, position);
        }
        if ((parentCommits & ~_createMask(position)) != EMPTY_MASK_VALUE) {
            revert Types.InvalidInput(COMMIT_PARENTS_FIELD_NAME, position);
        }
        uint256 firstUnit = units.length;
        if (firstUnit + commitUnits.length > Types.MAX_UNITS) {
            revert Types.InvalidInput(UNITS_FIELD_NAME, firstUnit + commitUnits.length);
        }
        uint256 reachable = MASK_SEED_VALUE << position;
        for (uint256 i; i < position; ++i) {
            if ((parentCommits & (MASK_SEED_VALUE << i)) != EMPTY_MASK_VALUE) reachable |= ancestors[i];
        }
        commits.push(Types.Commit(commitId, parentCommits));
        ancestors.push(reachable);
        commitPositions[commitId] = position + STORED_POSITION_OFFSET_COUNT;
        for (uint256 i; i < commitUnits.length; ++i) {
            Types.Unit calldata unit = commitUnits[i];
            if (unit.commitIndex != position || unit.unitIndex != i + UNIT_ORDINAL_OFFSET_COUNT) {
                revert Types.InvalidInput(UNIT_POSITION_FIELD_NAME, i);
            }
            if ((unit.correctionTargets & ~_createMask(firstUnit)) != EMPTY_MASK_VALUE) {
                revert Types.CCMEError(CORRECTION_SELECTOR_ERROR_CODE, CORRECTION_DEFAULT_PACKAGE_INDEX, firstUnit + i);
            }
            validateCorrectionReach(unit.correctionTargets, reachable, firstUnit + i);
            units.push(unit);
        }
        // Validate syntax and history without requiring a publishable plan; later commits can repair a blocked plan.
        this.validateHistory(_createSnapshot(reachable));
        commitIndex = uint16(position);
        emit CommitAppended(commitIndex, commitId, parentCommits, uint16(firstUnit), uint16(commitUnits.length));
        appendRecord(COMMIT_RECORD_KIND, keccak256(abi.encode(commitId, parentCommits, commitUnits)));
    }

    /// @notice Records a publication once. An identical receipt is idempotent; a version cannot move to another commit.
    function appendRelease(uint16 packageIndex, uint16 commitIndex, Types.Version calldata version)
        external
        onlyOwner
        returns (uint16 tagIndex)
    {
        if (packageIndex >= packages.length) revert Types.InvalidInput(TAG_PACKAGE_FIELD_NAME, packageIndex);
        if (commitIndex >= commits.length) revert Types.InvalidInput(TAG_COMMIT_FIELD_NAME, commitIndex);
        Versioning.validate(version);
        bytes32 versionHash = keccak256(abi.encode(packageIndex, version));
        uint256 existing = releasePositions[versionHash];
        if (existing != ABSENT_RECORD_POSITION) {
            if (tags[existing - STORED_POSITION_OFFSET_COUNT].commitIndex != commitIndex) {
                revert Types.CCMEError(
                    RELEASE_IDENTITY_CONFLICT_ERROR_CODE, packageIndex, existing - STORED_POSITION_OFFSET_COUNT
                );
            }
            return uint16(existing - STORED_POSITION_OFFSET_COUNT);
        }
        if (tags.length >= Types.MAX_TAGS) revert Types.InvalidInput(TAGS_FIELD_NAME, tags.length);
        tagIndex = uint16(tags.length);
        tags.push(Types.Tag(packageIndex, commitIndex, version));
        releasePositions[versionHash] = uint256(tagIndex) + STORED_POSITION_OFFSET_COUNT;
        emit ReleaseRecorded(tagIndex, packageIndex, commitIndex, versionHash);
        appendRecord(RELEASE_RECORD_KIND, keccak256(abi.encode(packageIndex, commitIndex, version)));
    }

    /// @notice Returns the complete reachable history of the last appended commit, in planner order.
    function projectSnapshot() public view returns (Types.Snapshot memory) {
        if (commits.length == EMPTY_HISTORY_COUNT) return _createSnapshot(EMPTY_MASK_VALUE);
        return _createSnapshot(ancestors[commits.length - ARRAY_PREVIOUS_OFFSET_COUNT]);
    }

    function projectSnapshotAt(uint16 commitIndex) external view returns (Types.Snapshot memory) {
        if (commitIndex >= commits.length) revert Types.InvalidInput(HEAD_FIELD_NAME, commitIndex);
        return _createSnapshot(ancestors[commitIndex]);
    }

    function compute() external view returns (Types.Plan memory) {
        return planner.compute(projectSnapshot());
    }

    function getCounts() external view returns (uint256 commitCount, uint256 unitCount, uint256 tagCount) {
        return (commits.length, units.length, tags.length);
    }

    function getCommit(uint16 commitIndex) external view returns (Types.Commit memory) {
        if (commitIndex >= commits.length) revert Types.InvalidInput(COMMIT_FIELD_NAME, commitIndex);
        return commits[commitIndex];
    }

    function getUnit(uint16 unitIndex) external view returns (Types.Unit memory) {
        if (unitIndex >= units.length) revert Types.InvalidInput(UNIT_FIELD_NAME, unitIndex);
        return units[unitIndex];
    }

    function getTag(uint16 tagIndex) external view returns (Types.Tag memory) {
        if (tagIndex >= tags.length) revert Types.InvalidInput(TAG_FIELD_NAME, tagIndex);
        return tags[tagIndex];
    }

    /// @dev Exposed so append validation can reuse calldata-based normalization without duplicating it.
    function validateHistory(Types.Snapshot calldata history) external pure {
        History.analyze(history);
        for (uint256 i; i < history.units.length; ++i) {
            Channels.validate(history.units[i].channel, DIRECT_CHANNEL_DIRECTIVE_FLAG);
            Channels.validate(history.units[i].propagateChannel, PROPAGATED_CHANNEL_DIRECTIVE_FLAG);
            if (history.units[i].releaseKind == Types.ReleaseKind.Exact) Versioning.validate(history.units[i].pin);
        }
    }

    function validateCorrectionReach(uint256 targets, uint256 reachable, uint256 unitIndex) private view {
        for (uint256 i; i < units.length; ++i) {
            if ((targets & (MASK_SEED_VALUE << i)) == EMPTY_MASK_VALUE) continue;
            if ((reachable & (MASK_SEED_VALUE << units[i].commitIndex)) == EMPTY_MASK_VALUE) {
                revert Types.CCMEError(CORRECTION_ANCESTRY_ERROR_CODE, CORRECTION_DEFAULT_PACKAGE_INDEX, unitIndex);
            }
        }
    }

    function _createSnapshot(uint256 reachable) private view returns (Types.Snapshot memory history) {
        Projection memory projection = projectHistory(reachable);
        history.packages = packages;
        history.edges = edges;
        history.commits = new Types.Commit[](projection.commitCount);
        history.units = new Types.Unit[](projection.unitCount);
        history.tags = new Types.Tag[](projection.tagCount);
        for (uint256 i; i < commits.length; ++i) {
            if ((reachable & (MASK_SEED_VALUE << i)) == EMPTY_MASK_VALUE) continue;
            history.commits[projection.commits[i]] =
                Types.Commit(commits[i].id, remapMask(commits[i].parents, projection.commits));
        }
        for (uint256 i; i < units.length; ++i) {
            Types.Unit memory unit = units[i];
            if ((reachable & (MASK_SEED_VALUE << unit.commitIndex)) == EMPTY_MASK_VALUE) continue;
            unit.commitIndex = projection.commits[unit.commitIndex];
            unit.correctionTargets = remapMask(unit.correctionTargets, projection.units);
            history.units[projection.units[i]] = unit;
        }
        uint256 nextTag;
        for (uint256 i; i < tags.length; ++i) {
            Types.Tag memory tag = tags[i];
            if ((reachable & (MASK_SEED_VALUE << tag.commitIndex)) == EMPTY_MASK_VALUE) continue;
            tag.commitIndex = projection.commits[tag.commitIndex];
            history.tags[nextTag++] = tag;
        }
    }

    function projectHistory(uint256 reachable) private view returns (Projection memory projection) {
        projection.commits = new uint16[](commits.length);
        projection.units = new uint16[](units.length);
        for (uint256 i; i < commits.length; ++i) {
            if ((reachable & (MASK_SEED_VALUE << i)) != EMPTY_MASK_VALUE) {
                projection.commits[i] = uint16(projection.commitCount++);
            }
        }
        for (uint256 i = units.length; i != ARRAY_START_INDEX; --i) {
            if (
                (reachable & (MASK_SEED_VALUE << units[i - ARRAY_PREVIOUS_OFFSET_COUNT].commitIndex))
                    != EMPTY_MASK_VALUE
            ) {
                projection.units[i - ARRAY_PREVIOUS_OFFSET_COUNT] = uint16(projection.unitCount++);
            }
        }
        for (uint256 i; i < tags.length; ++i) {
            if ((reachable & (MASK_SEED_VALUE << tags[i].commitIndex)) != EMPTY_MASK_VALUE) ++projection.tagCount;
        }
    }

    function remapMask(uint256 original, uint16[] memory positions) private pure returns (uint256 mapped) {
        for (uint256 i; i < positions.length; ++i) {
            if ((original & (MASK_SEED_VALUE << i)) != EMPTY_MASK_VALUE) mapped |= MASK_SEED_VALUE << positions[i];
        }
    }

    function appendRecord(uint8 kind, bytes32 payloadHash) private {
        bytes32 previous = ledgerHash;
        ++sequence;
        ledgerHash = keccak256(abi.encode(block.chainid, address(this), sequence, previous, kind, payloadHash));
        emit LedgerRecord(sequence, ledgerHash, previous, kind, payloadHash);
    }

    function _createMask(uint256 length) private pure returns (uint256) {
        return length == MASK_WIDTH_BITS ? type(uint256).max : (MASK_SEED_VALUE << length) - MASK_SEED_VALUE;
    }
}
