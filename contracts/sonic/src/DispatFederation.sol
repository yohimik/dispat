// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.28;

import {CCMETypes as Types} from "./CCMETypes.sol";
import {CCMEHistory as History} from "./CCMEHistory.sol";
import {CCMEGraph as Graph} from "./CCMEGraph.sol";
import {CCMEVersion as Versioning} from "./CCMEVersion.sol";
import {DispatPlanner} from "./DispatPlanner.sol";
import {DispatHistoryProjector} from "./DispatHistoryProjector.sol";

/// @notice Selects and composes bounded repository histories for the pure planner.
/// @dev Boundary and observation rows are explicit trusted inputs, not proofs of
/// Git state. There are no synthetic cross-repository parents or timestamp ties.
contract DispatFederation {
    uint256 public constant MAX_REPOSITORY_COUNT = 16;
    uint256 public constant MAX_BOUNDARY_COUNT = 2048;
    uint256 public constant MAX_OBSERVATION_COUNT = 4096;
    uint16 public constant MISSING_COMMIT_INDEX = type(uint16).max;
    uint16 private constant EMPTY_POSITION_INDEX_OFFSET = 1;
    uint16 private constant EMPTY_REVISION_POSITION_INDEX = type(uint16).max - EMPTY_POSITION_INDEX_OFFSET;
    uint256 private constant MEMBER_BIT_MASK = 1;
    uint256 private constant EMPTY_SET_MASK = 0;
    uint256 private constant MASK_TERMINATOR_OFFSET = 1;
    uint256 private constant PREVIOUS_INDEX_OFFSET = 1;
    uint256 private constant FIRST_INDEX_VALUE = 0;
    uint256 private constant ENTRY_QUEUE_INDEX = 0;
    uint256 private constant ENTRY_QUEUE_COUNT = 1;
    uint256 private constant EMPTY_REPOSITORY_COUNT = 0;
    uint256 private constant EMPTY_DIAGNOSTIC_COUNT = 0;
    uint256 private constant EMPTY_CODE_LENGTH_BYTES = 0;
    uint256 private constant UNSPECIFIED_CONTEXT_INDEX = 0;
    uint256 private constant DISABLED_PROPAGATION_DEPTH = 0;
    int256 private constant SEMVER_EQUAL_ORDER_VALUE = 0;
    uint256 private constant IDENTITY_CAPACITY_BYTES = 32;
    uint256 private constant IDENTITY_FIRST_BYTE_SHIFT_BITS = 248;
    uint256 private constant BYTE_WIDTH_BITS = 8;
    uint8 private constant IDENTITY_PADDING_BYTE = 0;
    uint8 private constant ASCII_CASE_OFFSET = 32;
    uint8 private constant ASCII_UPPERCASE_FIRST_CODE = 65;
    uint8 private constant ASCII_UPPERCASE_LAST_CODE = 90;
    uint8 private constant ASCII_LOWERCASE_FIRST_CODE = 97;
    uint8 private constant ASCII_LOWERCASE_LAST_CODE = 122;
    uint8 private constant ASCII_DIGIT_FIRST_CODE = 48;
    uint8 private constant ASCII_DIGIT_LAST_CODE = 57;
    uint8 private constant ASCII_HYPHEN_CODE = 45;
    uint8 private constant ASCII_PERIOD_CODE = 46;
    uint8 private constant ASCII_UNDERSCORE_CODE = 95;
    bytes32 private constant EMPTY_REPOSITORY_ID = bytes32(0);
    bytes32 private constant RESERVED_CONTROL_REPOSITORY_ID = bytes32("control");
    bytes32 private constant STABLE_CHANNEL_VALUE = bytes32(0);
    bool private constant IDENTITY_TERMINATED_FLAG = true;
    bool private constant CHANNEL_WINNER_FOUND_FLAG = true;
    bool private constant RELEASE_WINNER_FOUND_FLAG = true;
    bool private constant PROPAGATED_CHANNEL_FLAG = true;
    bool private constant DIRECT_CHANNEL_FLAG = false;
    bool private constant DIRECT_CHANNEL_PRESENT_FLAG = true;
    bool private constant DIRECT_CHANNEL_ABSENT_FLAG = false;
    bool private constant CHANNEL_PROPOSAL_ABSENT_FLAG = false;
    uint16 private constant CANCELLED_PRERELEASE_WARNING_CODE = 171;
    uint16 private constant MISSING_PROVIDER_REPOSITORY_CODE = 332;
    uint16 private constant INVALID_REPOSITORY_BOUNDARY_CODE = 333;
    uint16 private constant INCOMPARABLE_DIRECTIVES_CODE = 334;
    uint16 private constant CYCLIC_PEER_TOPOLOGY_CODE = 338;
    uint16 private constant INVALID_REPOSITORY_IDENTITY_CODE = 339;
    bytes32 private constant PLANNER_FIELD_ID = "planner";
    bytes32 private constant REPOSITORIES_FIELD_ID = "repositories";
    bytes32 private constant FEDERATION_BOUNDS_FIELD_ID = "federation.bounds";
    bytes32 private constant SPECIFIED_REPOSITORIES_FIELD_ID = "specifiedRepositories";
    bytes32 private constant PEER_RECIPROCITY_FIELD_ID = "peer.reciprocity";
    bytes32 private constant INVENTORY_PACKAGES_FIELD_ID = "inventory.packages";
    bytes32 private constant INVENTORY_TARGETS_FIELD_ID = "inventory.targets";
    bytes32 private constant INVENTORY_HISTORY_FIELD_ID = "inventory.history";
    bytes32 private constant INVENTORY_EDGES_FIELD_ID = "inventory.edges";
    bytes32 private constant INVENTORY_TARGET_MASK_FIELD_ID = "inventory.targetMask";
    bytes32 private constant CROSS_EDGE_FIELD_ID = "crossEdge";

    enum Mode {
        Single,
        Specified,
        Discovered
    }

    struct Repository {
        bytes32 identity;
        uint256 peers;
        Types.Snapshot snapshot;
        // Masks use inventory order: repositories, then their local packages.
        uint256[] bumpTargets;
        uint256[] channelTargets;
    }

    struct CrossEdge {
        uint16 providerRepository;
        uint16 providerPackage;
        uint16 consumerRepository;
        uint16 consumerPackage;
        bool isPropagationEnabled;
        bool isPublishOrderEnabled;
    }

    struct Boundary {
        uint16 consumerRepository;
        uint16 consumerPackage;
        Types.Version releaseVersion;
        uint16 sourceRepository;
        // MISSING_COMMIT_INDEX explicitly means the release incorporated no source revision.
        uint16 sourceCommit;
    }

    struct Observation {
        uint16 observerRepository;
        uint16 observerCommit;
        uint16 sourceRepository;
        uint16 sourceCommit;
    }

    struct Input {
        Mode mode;
        uint16 entryRepository;
        uint256 specifiedRepositories;
        Repository[] repositories;
        CrossEdge[] crossEdges;
        Boundary[] boundaries;
        Observation[] observations;
    }

    struct Result {
        Types.Plan plan;
        uint256 selectedRepositories;
        uint16[] packageRepositories;
        uint16[] packageLocalIndices;
        uint16[] unitRepositories;
        uint16[] unitLocalIndices;
    }

    struct Composition {
        Types.Snapshot snapshot;
        History.State history;
        Graph.State graph;
        History.State[] nativeHistories;
        uint16[][] packageMap;
        uint16[][] unitMap;
        uint16[] commitOffsets;
        uint16[] inventoryOffsets;
        uint16[] packageRepositories;
        uint16[] packageLocalIndices;
        uint16[] unitRepositories;
        uint16[] unitLocalIndices;
        uint256 selected;
    }

    DispatPlanner public immutable planner;
    DispatHistoryProjector public immutable projector;

    constructor(DispatPlanner releasePlanner) {
        if (address(releasePlanner).code.length == EMPTY_CODE_LENGTH_BYTES) {
            revert Types.InvalidInput(PLANNER_FIELD_ID, UNSPECIFIED_CONTEXT_INDEX);
        }
        planner = releasePlanner;
        projector = new DispatHistoryProjector();
    }

    function compute(Input calldata input) external view returns (Result memory result) {
        Composition memory composition = prepareComposition(input);
        result.plan = planner.computePrepared(composition.snapshot, composition.history);
        result.selectedRepositories = composition.selected;
        result.packageRepositories = composition.packageRepositories;
        result.packageLocalIndices = composition.packageLocalIndices;
        result.unitRepositories = composition.unitRepositories;
        result.unitLocalIndices = composition.unitLocalIndices;
    }

    /// @notice Returns the selected normalized input for an execution-mode coordinator.
    function prepare(Input calldata input) external view returns (Types.Snapshot memory, History.State memory) {
        Composition memory composition = prepareComposition(input);
        return (composition.snapshot, composition.history);
    }

    function prepareComposition(Input calldata input) private view returns (Composition memory composition) {
        composition = composeRepositories(input, selectRepositories(input));
        composition.graph = projector.analyzeGraph(composition.snapshot);
        uint256[] memory sources = resolveSourcePackages(composition);
        if (input.mode != Mode.Single) {
            applyBoundaries(input, composition, sources);
            applyForeignCancellations(input, composition, sources);
            composition.history = projector.projectCorrections(
                composition.snapshot, composition.history, composition.graph, composition.unitRepositories, sources
            );
            validateChannelConflicts(composition, sources);
        }
    }

    function selectRepositories(Input calldata input) private pure returns (uint256 selected) {
        uint256 count = input.repositories.length;
        if (count == EMPTY_REPOSITORY_COUNT || count > MAX_REPOSITORY_COUNT || input.entryRepository >= count) {
            revert Types.InvalidInput(REPOSITORIES_FIELD_ID, count);
        }
        if (
            input.boundaries.length > MAX_BOUNDARY_COUNT || input.observations.length > MAX_OBSERVATION_COUNT
                || input.crossEdges.length > Types.MAX_EDGES
        ) revert Types.InvalidInput(FEDERATION_BOUNDS_FIELD_ID, UNSPECIFIED_CONTEXT_INDEX);
        bytes32[] memory identities = new bytes32[](count);
        for (uint256 i; i < count; ++i) {
            identities[i] = normalizeIdentity(input.repositories[i].identity);
            for (uint256 j; j < i; ++j) {
                if (identities[i] == identities[j]) revert Types.CCMEError(INVALID_REPOSITORY_IDENTITY_CODE, i, j);
            }
        }
        selected = MEMBER_BIT_MASK << input.entryRepository;
        if (input.mode == Mode.Specified) {
            if (
                input.specifiedRepositories & ~((MEMBER_BIT_MASK << count) - MASK_TERMINATOR_OFFSET) != EMPTY_SET_MASK
                    || input.specifiedRepositories & selected == EMPTY_SET_MASK
            ) revert Types.InvalidInput(SPECIFIED_REPOSITORIES_FIELD_ID, UNSPECIFIED_CONTEXT_INDEX);
            return input.specifiedRepositories;
        }
        if (input.specifiedRepositories != EMPTY_SET_MASK) {
            revert Types.InvalidInput(SPECIFIED_REPOSITORIES_FIELD_ID, UNSPECIFIED_CONTEXT_INDEX);
        }
        if (input.mode == Mode.Single) return selected;
        uint16[] memory parents = new uint16[](count);
        uint16[] memory queue = new uint16[](count);
        parents[input.entryRepository] = MISSING_COMMIT_INDEX;
        queue[ENTRY_QUEUE_INDEX] = input.entryRepository;
        uint256 queued = ENTRY_QUEUE_COUNT;
        for (uint256 position; position < queued; ++position) {
            uint256 current = queue[position];
            uint256 peers = input.repositories[current].peers;
            if (
                peers & ~((MEMBER_BIT_MASK << count) - MASK_TERMINATOR_OFFSET) != EMPTY_SET_MASK
                    || peers & (MEMBER_BIT_MASK << current) != EMPTY_SET_MASK
            ) {
                revert Types.CCMEError(INVALID_REPOSITORY_IDENTITY_CODE, current, UNSPECIFIED_CONTEXT_INDEX);
            }
            for (uint256 next; next < count; ++next) {
                uint256 bit = MEMBER_BIT_MASK << next;
                if (peers & bit == EMPTY_SET_MASK) continue;
                if (input.repositories[next].peers & (MEMBER_BIT_MASK << current) == EMPTY_SET_MASK) {
                    revert Types.InvalidInput(PEER_RECIPROCITY_FIELD_ID, current);
                }
                if (parents[current] == next) continue;
                if (selected & bit != EMPTY_SET_MASK) revert Types.CCMEError(CYCLIC_PEER_TOPOLOGY_CODE, current, next);
                selected |= bit;
                parents[next] = uint16(current);
                queue[queued++] = uint16(next);
            }
        }
    }

    function normalizeIdentity(bytes32 identity) private pure returns (bytes32 folded) {
        if (identity == EMPTY_REPOSITORY_ID) {
            revert Types.CCMEError(
                INVALID_REPOSITORY_IDENTITY_CODE, UNSPECIFIED_CONTEXT_INDEX, UNSPECIFIED_CONTEXT_INDEX
            );
        }
        bool isIdentityTerminated;
        for (uint256 i; i < IDENTITY_CAPACITY_BYTES; ++i) {
            uint8 character = uint8(identity[i]);
            if (character == IDENTITY_PADDING_BYTE) {
                isIdentityTerminated = IDENTITY_TERMINATED_FLAG;
                continue;
            }
            if (isIdentityTerminated) {
                revert Types.CCMEError(INVALID_REPOSITORY_IDENTITY_CODE, UNSPECIFIED_CONTEXT_INDEX, i);
            }
            if (character >= ASCII_UPPERCASE_FIRST_CODE && character <= ASCII_UPPERCASE_LAST_CODE) {
                character += ASCII_CASE_OFFSET;
            }
            if (
                !(character >= ASCII_LOWERCASE_FIRST_CODE && character <= ASCII_LOWERCASE_LAST_CODE)
                    && !(character >= ASCII_DIGIT_FIRST_CODE && character <= ASCII_DIGIT_LAST_CODE)
                    && character != ASCII_HYPHEN_CODE && character != ASCII_PERIOD_CODE
                    && character != ASCII_UNDERSCORE_CODE
            ) revert Types.CCMEError(INVALID_REPOSITORY_IDENTITY_CODE, UNSPECIFIED_CONTEXT_INDEX, i);
            folded |= bytes32(uint256(character) << (IDENTITY_FIRST_BYTE_SHIFT_BITS - BYTE_WIDTH_BITS * i));
        }
        if (folded == RESERVED_CONTROL_REPOSITORY_ID) {
            revert Types.CCMEError(
                INVALID_REPOSITORY_IDENTITY_CODE, UNSPECIFIED_CONTEXT_INDEX, UNSPECIFIED_CONTEXT_INDEX
            );
        }
    }

    function composeRepositories(Input calldata input, uint256 selected)
        private
        view
        returns (Composition memory composition)
    {
        uint256 repositoryCount = input.repositories.length;
        composition.selected = selected;
        composition.nativeHistories = new History.State[](repositoryCount);
        composition.packageMap = new uint16[][](repositoryCount);
        composition.unitMap = new uint16[][](repositoryCount);
        composition.commitOffsets = new uint16[](repositoryCount);
        composition.inventoryOffsets = new uint16[](repositoryCount);
        uint256 packageCount;
        uint256 commitCount;
        uint256 unitCount;
        uint256 tagCount;
        uint256 edgeCount;
        uint256 inventoryCount;
        uint256 diagnosticCount;
        for (uint256 i; i < repositoryCount; ++i) {
            Repository calldata repository = input.repositories[i];
            composition.inventoryOffsets[i] = uint16(inventoryCount);
            inventoryCount += repository.snapshot.packages.length;
            if (inventoryCount > Types.MAX_PACKAGES) {
                revert Types.InvalidInput(INVENTORY_PACKAGES_FIELD_ID, inventoryCount);
            }
            if (selected & (MEMBER_BIT_MASK << i) == EMPTY_SET_MASK) continue;
            if (
                input.mode != Mode.Single
                    && (repository.bumpTargets.length != repository.snapshot.units.length
                        || repository.channelTargets.length != repository.snapshot.units.length)
            ) {
                revert Types.InvalidInput(INVENTORY_TARGETS_FIELD_ID, i);
            }
            composition.nativeHistories[i] = projector.analyze(repository.snapshot);
            composition.packageMap[i] = new uint16[](repository.snapshot.packages.length);
            composition.unitMap[i] = new uint16[](repository.snapshot.units.length);
            composition.commitOffsets[i] = uint16(commitCount);
            packageCount += repository.snapshot.packages.length;
            commitCount += repository.snapshot.commits.length;
            unitCount += repository.snapshot.units.length;
            tagCount += repository.snapshot.tags.length;
            edgeCount += repository.snapshot.edges.length;
            diagnosticCount += composition.nativeHistories[i].diagnostics.length;
        }
        if (commitCount > Types.MAX_COMMITS || unitCount > Types.MAX_UNITS || tagCount > Types.MAX_TAGS) {
            revert Types.InvalidInput(INVENTORY_HISTORY_FIELD_ID, UNSPECIFIED_CONTEXT_INDEX);
        }
        if (input.mode != Mode.Single) {
            for (uint256 i; i < input.crossEdges.length; ++i) {
                CrossEdge calldata edge = input.crossEdges[i];
                validateCrossEdge(input, edge, i);
                if (selected & (MEMBER_BIT_MASK << edge.consumerRepository) == EMPTY_SET_MASK) continue;
                if (selected & (MEMBER_BIT_MASK << edge.providerRepository) == EMPTY_SET_MASK) {
                    revert Types.CCMEError(MISSING_PROVIDER_REPOSITORY_CODE, i, UNSPECIFIED_CONTEXT_INDEX);
                }
                ++edgeCount;
            }
        }
        if (edgeCount > Types.MAX_EDGES) revert Types.InvalidInput(INVENTORY_EDGES_FIELD_ID, edgeCount);
        allocateComposition(composition, packageCount, commitCount, unitCount, tagCount, edgeCount, diagnosticCount);
        composePackages(input, composition);
        normalizeUnits(input, composition, inventoryCount);
        composeRecordsAndEdges(input, composition);
    }

    function allocateComposition(
        Composition memory composition,
        uint256 packageCount,
        uint256 commitCount,
        uint256 unitCount,
        uint256 tagCount,
        uint256 edgeCount,
        uint256 diagnosticCount
    ) private pure {
        composition.snapshot.packages = new Types.Package[](packageCount);
        composition.snapshot.commits = new Types.Commit[](commitCount);
        composition.snapshot.units = new Types.Unit[](unitCount);
        composition.snapshot.tags = new Types.Tag[](tagCount);
        composition.snapshot.edges = new Types.Edge[](edgeCount);
        composition.packageRepositories = new uint16[](packageCount);
        composition.packageLocalIndices = new uint16[](packageCount);
        composition.unitRepositories = new uint16[](unitCount);
        composition.unitLocalIndices = new uint16[](unitCount);
        composition.history.baselines = new Types.Version[](packageCount);
        composition.history.stableBaselines = new Types.Version[](packageCount);
        composition.history.baselineCommits = new uint16[](packageCount);
        composition.history.stableCommits = new uint16[](packageCount);
        composition.history.ancestors = new uint256[](commitCount);
        composition.history.scopes = new uint256[](unitCount);
        composition.history.fresh = new uint256[](unitCount);
        composition.history.train = new uint256[](unitCount);
        composition.history.cancelled = new uint256[](unitCount);
        composition.history.suppressedTargets = new uint256[](unitCount);
        composition.history.incorporated = new uint256[](packageCount * packageCount);
        composition.history.diagnostics = new Types.Diagnostic[](diagnosticCount);
    }

    function composePackages(Input calldata input, Composition memory composition) private pure {
        uint256 count;
        for (uint256 repositoryIndex; repositoryIndex < input.repositories.length; ++repositoryIndex) {
            if (composition.selected & (MEMBER_BIT_MASK << repositoryIndex) == EMPTY_SET_MASK) continue;
            Types.Package[] calldata packages = input.repositories[repositoryIndex].snapshot.packages;
            for (uint256 localIndex; localIndex < packages.length; ++localIndex) {
                uint256 position = count++;
                while (
                    position != FIRST_INDEX_VALUE
                        && isNameLess(
                            bytes(packages[localIndex].name),
                            bytes(composition.snapshot.packages[position - PREVIOUS_INDEX_OFFSET].name)
                        )
                ) {
                    composition.snapshot.packages[position] =
                        composition.snapshot.packages[position - PREVIOUS_INDEX_OFFSET];
                    composition.packageRepositories[position] =
                        composition.packageRepositories[position - PREVIOUS_INDEX_OFFSET];
                    composition.packageLocalIndices[position] =
                        composition.packageLocalIndices[position - PREVIOUS_INDEX_OFFSET];
                    --position;
                }
                composition.snapshot.packages[position] = packages[localIndex];
                composition.packageRepositories[position] = uint16(repositoryIndex);
                composition.packageLocalIndices[position] = uint16(localIndex);
            }
        }
        for (uint256 i; i < count; ++i) {
            uint256 repositoryIndex = composition.packageRepositories[i];
            uint256 localIndex = composition.packageLocalIndices[i];
            composition.packageMap[repositoryIndex][localIndex] = uint16(i);
            History.State memory nativeHistory = composition.nativeHistories[repositoryIndex];
            composition.history.baselines[i] = nativeHistory.baselines[localIndex];
            composition.history.stableBaselines[i] = nativeHistory.stableBaselines[localIndex];
            composition.history.baselineCommits[i] =
                nativeHistory.baselineCommits[localIndex] + composition.commitOffsets[repositoryIndex];
            composition.history.stableCommits[i] =
                nativeHistory.stableCommits[localIndex] + composition.commitOffsets[repositoryIndex];
            if (nativeHistory.hasBaseline & (MEMBER_BIT_MASK << localIndex) != EMPTY_SET_MASK) {
                composition.history.hasBaseline |= MEMBER_BIT_MASK << i;
            }
            if (nativeHistory.hasStableBaseline & (MEMBER_BIT_MASK << localIndex) != EMPTY_SET_MASK) {
                composition.history.hasStableBaseline |= MEMBER_BIT_MASK << i;
            }
        }
    }

    function normalizeUnits(Input calldata input, Composition memory composition, uint256 inventoryCount) private pure {
        uint256 unitPosition;
        uint256 diagnosticPosition;
        uint256 inventoryMask = (MEMBER_BIT_MASK << inventoryCount) - MASK_TERMINATOR_OFFSET;
        for (uint256 reverse = input.repositories.length; reverse != EMPTY_REPOSITORY_COUNT; --reverse) {
            uint256 repositoryIndex = reverse - PREVIOUS_INDEX_OFFSET;
            if (composition.selected & (MEMBER_BIT_MASK << repositoryIndex) == EMPTY_SET_MASK) continue;
            Repository calldata repository = input.repositories[repositoryIndex];
            History.State memory nativeHistory = composition.nativeHistories[repositoryIndex];
            uint256 localCount = repository.snapshot.units.length;
            for (uint256 i; i < localCount; ++i) {
                composition.unitMap[repositoryIndex][i] = uint16(unitPosition + i);
            }
            for (uint256 i; i < localCount; ++i) {
                Types.Unit memory unit = repository.snapshot.units[i];
                unit.commitIndex += composition.commitOffsets[repositoryIndex];
                unit.scope = projectHistory(unit.scope, composition.packageMap[repositoryIndex]);
                (unit.propagateScope, unit.channelScope) =
                    projectUnitTargetMasks(input, composition, repositoryIndex, i, inventoryMask);
                unit.correctionTargets = projectHistory(unit.correctionTargets, composition.unitMap[repositoryIndex]);
                composition.snapshot.units[unitPosition] = unit;
                composition.unitRepositories[unitPosition] = uint16(repositoryIndex);
                composition.unitLocalIndices[unitPosition] = uint16(i);
                composition.history.scopes[unitPosition] =
                    projectHistory(nativeHistory.scopes[i], composition.packageMap[repositoryIndex]);
                composition.history.fresh[unitPosition] =
                    projectHistory(nativeHistory.fresh[i], composition.packageMap[repositoryIndex]);
                composition.history.train[unitPosition] =
                    projectHistory(nativeHistory.train[i], composition.packageMap[repositoryIndex]);
                composition.history.cancelled[unitPosition] =
                    projectHistory(nativeHistory.cancelled[i], composition.packageMap[repositoryIndex]);
                composition.history.suppressedTargets[unitPosition] =
                    projectHistory(nativeHistory.suppressedTargets[i], composition.packageMap[repositoryIndex]);
                ++unitPosition;
            }
            for (uint256 i; i < nativeHistory.diagnostics.length; ++i) {
                Types.Diagnostic memory diagnostic = nativeHistory.diagnostics[i];
                diagnostic.packageIndex = composition.packageMap[repositoryIndex][diagnostic.packageIndex];
                diagnostic.unitIndex = composition.unitMap[repositoryIndex][diagnostic.unitIndex];
                composition.history.diagnostics[diagnosticPosition++] = diagnostic;
            }
        }
    }

    function projectUnitTargetMasks(
        Input calldata input,
        Composition memory composition,
        uint256 repositoryIndex,
        uint256 unitIndex,
        uint256 inventoryMask
    ) private pure returns (uint256, uint256) {
        Repository calldata repository = input.repositories[repositoryIndex];
        if (input.mode == Mode.Single) {
            Types.Unit calldata unit = repository.snapshot.units[unitIndex];
            return (
                projectHistory(unit.propagateScope, composition.packageMap[repositoryIndex]),
                projectHistory(unit.channelScope, composition.packageMap[repositoryIndex])
            );
        }
        if (
            (repository.bumpTargets[unitIndex] | repository.channelTargets[unitIndex]) & ~inventoryMask
                != EMPTY_SET_MASK
        ) {
            revert Types.InvalidInput(INVENTORY_TARGET_MASK_FIELD_ID, unitIndex);
        }
        return (
            projectInventoryMask(input, composition, repository.bumpTargets[unitIndex]),
            projectInventoryMask(input, composition, repository.channelTargets[unitIndex])
        );
    }

    function composeRecordsAndEdges(Input calldata input, Composition memory composition) private pure {
        uint256 tagPosition;
        uint256 edgePosition;
        uint256 packageCount = composition.snapshot.packages.length;
        for (uint256 repositoryIndex; repositoryIndex < input.repositories.length; ++repositoryIndex) {
            if (composition.selected & (MEMBER_BIT_MASK << repositoryIndex) == EMPTY_SET_MASK) continue;
            Types.Snapshot calldata nativeSnapshot = input.repositories[repositoryIndex].snapshot;
            History.State memory nativeHistory = composition.nativeHistories[repositoryIndex];
            uint256 commitOffset = composition.commitOffsets[repositoryIndex];
            for (uint256 i; i < nativeSnapshot.commits.length; ++i) {
                composition.snapshot.commits[commitOffset + i] =
                    Types.Commit(nativeSnapshot.commits[i].id, nativeSnapshot.commits[i].parents << commitOffset);
                composition.history.ancestors[commitOffset + i] = nativeHistory.ancestors[i] << commitOffset;
            }
            for (uint256 i; i < nativeSnapshot.tags.length; ++i) {
                Types.Tag memory tag = nativeSnapshot.tags[i];
                tag.packageIndex = composition.packageMap[repositoryIndex][tag.packageIndex];
                tag.commitIndex += uint16(commitOffset);
                composition.snapshot.tags[tagPosition++] = tag;
            }
            for (uint256 i; i < nativeSnapshot.edges.length; ++i) {
                Types.Edge memory edge = nativeSnapshot.edges[i];
                edge.provider = composition.packageMap[repositoryIndex][edge.provider];
                edge.consumer = composition.packageMap[repositoryIndex][edge.consumer];
                composition.snapshot.edges[edgePosition++] = edge;
            }
            uint256 localCount = nativeSnapshot.packages.length;
            for (uint256 consumer; consumer < localCount; ++consumer) {
                for (uint256 provider; provider < localCount; ++provider) {
                    composition.history
                    .incorporated[
                        composition.packageMap[repositoryIndex][consumer] * packageCount
                            + composition.packageMap[repositoryIndex][provider]
                    ] =
                        nativeHistory.incorporated[consumer * localCount + provider] << commitOffset;
                }
            }
        }
        if (input.mode == Mode.Single) return;
        for (uint256 i; i < input.crossEdges.length; ++i) {
            CrossEdge calldata edge = input.crossEdges[i];
            if (composition.selected & (MEMBER_BIT_MASK << edge.consumerRepository) == EMPTY_SET_MASK) continue;
            composition.snapshot.edges[edgePosition++] = Types.Edge(
                composition.packageMap[edge.providerRepository][edge.providerPackage],
                composition.packageMap[edge.consumerRepository][edge.consumerPackage],
                edge.isPropagationEnabled,
                edge.isPublishOrderEnabled
            );
        }
    }

    function applyBoundaries(Input calldata input, Composition memory composition, uint256[] memory sources)
        private
        pure
    {
        uint256 repositoryCount = input.repositories.length;
        uint256 packageCount = composition.snapshot.packages.length;
        uint16[] memory baselinePositions = new uint16[](repositoryCount * packageCount);
        uint16[] memory stablePositions = new uint16[](repositoryCount * packageCount);
        for (uint256 i; i < baselinePositions.length; ++i) {
            baselinePositions[i] = MISSING_COMMIT_INDEX;
            stablePositions[i] = MISSING_COMMIT_INDEX;
        }
        for (uint256 i; i < input.boundaries.length; ++i) {
            Boundary calldata boundary = input.boundaries[i];
            Versioning.validate(boundary.releaseVersion);
            validateBoundaryReference(
                input,
                boundary.consumerRepository,
                boundary.consumerPackage,
                boundary.sourceRepository,
                boundary.sourceCommit,
                i
            );
            if (
                composition.selected & (MEMBER_BIT_MASK << boundary.consumerRepository) == EMPTY_SET_MASK
                    || composition.selected & (MEMBER_BIT_MASK << boundary.sourceRepository) == EMPTY_SET_MASK
            ) continue;
            uint256 target = composition.packageMap[boundary.consumerRepository][boundary.consumerPackage];
            uint256 index = target * repositoryCount + boundary.sourceRepository;
            uint16 position =
                boundary.sourceCommit == MISSING_COMMIT_INDEX ? EMPTY_REVISION_POSITION_INDEX : boundary.sourceCommit;
            bool isBaselineVersion = composition.history.hasBaseline & (MEMBER_BIT_MASK << target) != EMPTY_SET_MASK
                && Versioning.compare(boundary.releaseVersion, composition.history.baselines[target])
                    == SEMVER_EQUAL_ORDER_VALUE;
            bool isStableVersion = composition.history.hasStableBaseline & (MEMBER_BIT_MASK << target) != EMPTY_SET_MASK
                && Versioning.compare(boundary.releaseVersion, composition.history.stableBaselines[target])
                    == SEMVER_EQUAL_ORDER_VALUE;
            if (!isBaselineVersion && !isStableVersion) {
                revert Types.CCMEError(INVALID_REPOSITORY_BOUNDARY_CODE, target, i);
            }
            if (isBaselineVersion) {
                if (baselinePositions[index] != MISSING_COMMIT_INDEX) {
                    revert Types.CCMEError(INVALID_REPOSITORY_BOUNDARY_CODE, target, i);
                }
                baselinePositions[index] = position;
            }
            if (isStableVersion) {
                if (stablePositions[index] != MISSING_COMMIT_INDEX) {
                    revert Types.CCMEError(INVALID_REPOSITORY_BOUNDARY_CODE, target, i);
                }
                stablePositions[index] = position;
            }
        }
        uint256[] memory relevant = new uint256[](repositoryCount);
        for (uint256 i; i < composition.snapshot.units.length; ++i) {
            relevant[
                composition.unitRepositories[i]
            ] |= findTargets(composition, sources[i], composition.snapshot.units[i]);
        }
        for (uint256 target; target < packageCount; ++target) {
            uint256 bit = MEMBER_BIT_MASK << target;
            for (uint256 repositoryIndex; repositoryIndex < repositoryCount; ++repositoryIndex) {
                if (
                    composition.selected & (MEMBER_BIT_MASK << repositoryIndex) == EMPTY_SET_MASK
                        || composition.packageRepositories[target] == repositoryIndex
                        || relevant[repositoryIndex] & bit == EMPTY_SET_MASK
                ) continue;
                uint256 index = target * repositoryCount + repositoryIndex;
                if (composition.history.hasBaseline & bit == EMPTY_SET_MASK) {
                    baselinePositions[index] = EMPTY_REVISION_POSITION_INDEX;
                }
                if (composition.history.hasStableBaseline & bit == EMPTY_SET_MASK) {
                    stablePositions[index] = EMPTY_REVISION_POSITION_INDEX;
                }
                if (baselinePositions[index] == MISSING_COMMIT_INDEX || stablePositions[index] == MISSING_COMMIT_INDEX)
                {
                    revert Types.CCMEError(INVALID_REPOSITORY_BOUNDARY_CODE, target, repositoryIndex);
                }
                applyForeignWindow(
                    input, composition, target, repositoryIndex, baselinePositions[index], stablePositions[index]
                );
            }
        }
    }

    function applyForeignWindow(
        Input calldata input,
        Composition memory composition,
        uint256 target,
        uint256 repositoryIndex,
        uint16 baselinePosition,
        uint16 stablePosition
    ) private pure {
        History.State memory nativeHistory = composition.nativeHistories[repositoryIndex];
        uint256 baselineAncestors = baselinePosition == EMPTY_REVISION_POSITION_INDEX
            ? EMPTY_SET_MASK
            : nativeHistory.ancestors[baselinePosition];
        uint256 stableAncestors =
            stablePosition == EMPTY_REVISION_POSITION_INDEX ? EMPTY_SET_MASK : nativeHistory.ancestors[stablePosition];
        uint256 bit = MEMBER_BIT_MASK << target;
        Types.Snapshot calldata nativeSnapshot = input.repositories[repositoryIndex].snapshot;
        for (uint256 i; i < nativeSnapshot.units.length; ++i) {
            uint256 commitBit = MEMBER_BIT_MASK << nativeSnapshot.units[i].commitIndex;
            uint256 globalUnit = composition.unitMap[repositoryIndex][i];
            if (stableAncestors & commitBit == EMPTY_SET_MASK) {
                composition.history.train[globalUnit] |= bit;
                if (baselineAncestors & commitBit == EMPTY_SET_MASK) composition.history.fresh[globalUnit] |= bit;
            }
        }
        for (uint256 i; i < nativeSnapshot.tags.length; ++i) {
            Types.Tag calldata tag = nativeSnapshot.tags[i];
            if (baselineAncestors & (MEMBER_BIT_MASK << tag.commitIndex) == EMPTY_SET_MASK) continue;
            uint256 provider = composition.packageMap[repositoryIndex][tag.packageIndex];
            composition.history
            .incorporated[
                target * composition.snapshot.packages.length + provider
            ] |= nativeHistory.ancestors[tag.commitIndex] << composition.commitOffsets[repositoryIndex];
        }
    }

    function applyForeignCancellations(Input calldata input, Composition memory composition, uint256[] memory sources)
        private
        pure
    {
        uint256 repositoryCount = input.repositories.length;
        uint256[] memory warningMasks = new uint256[](composition.snapshot.units.length);
        uint16[] memory observed = new uint16[](composition.snapshot.commits.length * repositoryCount);
        for (uint256 i; i < observed.length; ++i) {
            observed[i] = MISSING_COMMIT_INDEX;
        }
        for (uint256 i; i < input.observations.length; ++i) {
            Observation calldata observation = input.observations[i];
            if (
                observation.observerRepository >= repositoryCount || observation.sourceRepository >= repositoryCount
                    || observation.observerRepository == observation.sourceRepository
                    || observation.observerCommit
                        >= input.repositories[observation.observerRepository].snapshot.commits.length
                    || (observation.sourceCommit != MISSING_COMMIT_INDEX
                        && observation.sourceCommit
                            >= input.repositories[observation.sourceRepository].snapshot.commits.length)
            ) {
                revert Types.CCMEError(INVALID_REPOSITORY_BOUNDARY_CODE, i, UNSPECIFIED_CONTEXT_INDEX);
            }
            if (
                composition.selected & (MEMBER_BIT_MASK << observation.observerRepository) == EMPTY_SET_MASK
                    || composition.selected & (MEMBER_BIT_MASK << observation.sourceRepository) == EMPTY_SET_MASK
            ) continue;
            uint256 index = (composition.commitOffsets[observation.observerRepository] + observation.observerCommit)
                * repositoryCount + observation.sourceRepository;
            if (observed[index] != MISSING_COMMIT_INDEX) {
                revert Types.CCMEError(INVALID_REPOSITORY_BOUNDARY_CODE, i, UNSPECIFIED_CONTEXT_INDEX);
            }
            observed[index] = observation.sourceCommit == MISSING_COMMIT_INDEX
                ? EMPTY_REVISION_POSITION_INDEX
                : observation.sourceCommit;
        }
        for (uint256 i; i < composition.snapshot.units.length; ++i) {
            Types.Unit memory barrier = composition.snapshot.units[i];
            if (!barrier.isCancel) continue;
            for (uint256 j; j < composition.snapshot.units.length; ++j) {
                uint256 sourceRepository = composition.unitRepositories[j];
                if (sourceRepository == composition.unitRepositories[i]) continue;
                uint256 targets = barrier.scope & findTargets(composition, sources[j], composition.snapshot.units[j]);
                if (targets == EMPTY_SET_MASK) continue;
                uint16 position = observed[uint256(barrier.commitIndex) * repositoryCount + sourceRepository];
                if (position == MISSING_COMMIT_INDEX) revert Types.CCMEError(INVALID_REPOSITORY_BOUNDARY_CODE, i, j);
                if (position == EMPTY_REVISION_POSITION_INDEX) continue;
                uint256 localCommit =
                    composition.snapshot.units[j].commitIndex - composition.commitOffsets[sourceRepository];
                if (
                    composition.nativeHistories[sourceRepository].ancestors[position] & (MEMBER_BIT_MASK << localCommit)
                        != EMPTY_SET_MASK
                ) {
                    composition.history.cancelled[j] |= targets;
                    warningMasks[i] |= targets & composition.history.train[j] & ~composition.history.fresh[j];
                }
            }
        }
        appendForeignCancellationWarnings(composition, warningMasks);
    }

    function appendForeignCancellationWarnings(Composition memory composition, uint256[] memory warningMasks)
        private
        pure
    {
        Types.Diagnostic[] memory previous = composition.history.diagnostics;
        for (uint256 i; i < previous.length; ++i) {
            if (previous[i].code == CANCELLED_PRERELEASE_WARNING_CODE) {
                warningMasks[previous[i].unitIndex] &= ~(MEMBER_BIT_MASK << previous[i].packageIndex);
            }
        }
        uint256 additionalCount;
        for (uint256 i; i < warningMasks.length; ++i) {
            for (uint256 j; j < composition.snapshot.packages.length; ++j) {
                if (warningMasks[i] & (MEMBER_BIT_MASK << j) != EMPTY_SET_MASK) ++additionalCount;
            }
        }
        if (additionalCount == EMPTY_DIAGNOSTIC_COUNT) return;
        Types.Diagnostic[] memory diagnostics = new Types.Diagnostic[](previous.length + additionalCount);
        for (uint256 i; i < previous.length; ++i) {
            diagnostics[i] = previous[i];
        }
        uint256 position = previous.length;
        for (uint256 i; i < warningMasks.length; ++i) {
            for (uint256 j; j < composition.snapshot.packages.length; ++j) {
                if (warningMasks[i] & (MEMBER_BIT_MASK << j) != EMPTY_SET_MASK) {
                    diagnostics[position++] = Types.Diagnostic(CANCELLED_PRERELEASE_WARNING_CODE, uint16(j), uint16(i));
                }
            }
        }
        composition.history.diagnostics = diagnostics;
    }

    function validateChannelConflicts(Composition memory composition, uint256[] memory sources) private pure {
        uint256 count = composition.snapshot.packages.length;
        for (uint256 target; target < count; ++target) {
            if (isDirectChannelPresent(composition, target)) continue;
            uint256 seenRepositories;
            bytes32 winner;
            bool isWinnerFound;
            for (uint256 i; i < composition.snapshot.units.length; ++i) {
                Types.Unit memory unit = composition.snapshot.units[i];
                uint256 repositoryBit = MEMBER_BIT_MASK << composition.unitRepositories[i];
                uint256 targetBit = MEMBER_BIT_MASK << target;
                if (
                    seenRepositories & repositoryBit != EMPTY_SET_MASK
                        || unit.channelDepth == DISABLED_PROPAGATION_DEPTH
                        || unit.channelScope & composition.history.fresh[i] & ~composition.history.cancelled[i]
                                & ~composition.history.suppressedTargets[i] & targetBit == EMPTY_SET_MASK
                        || Graph.findReachingSources(composition.graph, count, sources[i], target, unit.channelDepth)
                            == EMPTY_SET_MASK
                ) continue;
                (bool isProposalActive, bytes32 value) = resolveChannelProposal(
                    unit.propagateChannel,
                    composition.history.baselines[target].channel,
                    resolveOrigin(composition, unit, sources[i]),
                    PROPAGATED_CHANNEL_FLAG
                );
                if (!isProposalActive) continue;
                seenRepositories |= repositoryBit;
                if (isWinnerFound && value != winner) revert Types.CCMEError(INCOMPARABLE_DIRECTIVES_CODE, target, i);
                winner = value;
                isWinnerFound = CHANNEL_WINNER_FOUND_FLAG;
            }
        }
    }

    function isDirectChannelPresent(Composition memory composition, uint256 target) private pure returns (bool) {
        uint256 bit = MEMBER_BIT_MASK << target;
        bool isReleaseWinnerFound;
        for (uint256 i; i < composition.snapshot.units.length; ++i) {
            Types.Unit memory unit = composition.snapshot.units[i];
            if (
                composition.history.scopes[i] & composition.history.fresh[i] & ~composition.history.cancelled[i] & bit
                    == EMPTY_SET_MASK
            ) continue;
            if (unit.releaseKind != Types.ReleaseKind.Unset && !isReleaseWinnerFound) {
                if (unit.releaseKind == Types.ReleaseKind.Exact) return DIRECT_CHANNEL_PRESENT_FLAG;
                isReleaseWinnerFound = RELEASE_WINNER_FOUND_FLAG;
            }
            (bool isProposalActive,) = resolveChannelProposal(
                unit.channel, composition.history.baselines[target].channel, STABLE_CHANNEL_VALUE, DIRECT_CHANNEL_FLAG
            );
            if (isProposalActive) return DIRECT_CHANNEL_PRESENT_FLAG;
        }
        return DIRECT_CHANNEL_ABSENT_FLAG;
    }

    function resolveOrigin(Composition memory composition, Types.Unit memory unit, uint256 sources)
        private
        pure
        returns (bytes32)
    {
        for (uint256 i; i < composition.snapshot.packages.length; ++i) {
            if (sources & (MEMBER_BIT_MASK << i) == EMPTY_SET_MASK) continue;
            bytes32 baseline = composition.history.baselines[i].channel;
            (bool isProposalActive, bytes32 value) =
                resolveChannelProposal(unit.channel, baseline, STABLE_CHANNEL_VALUE, DIRECT_CHANNEL_FLAG);
            return isProposalActive ? value : baseline;
        }
        return STABLE_CHANNEL_VALUE;
    }

    function resolveChannelProposal(
        Types.ChannelDirective memory directive,
        bytes32 baseline,
        bytes32 origin,
        bool isPropagated
    ) private pure returns (bool, bytes32) {
        if (directive.mode == Types.ChannelMode.None) {
            return (CHANNEL_PROPOSAL_ABSENT_FLAG, STABLE_CHANNEL_VALUE);
        }
        if (
            directive.mode == Types.ChannelMode.Transition
                && (directive.from == directive.to || directive.from != baseline)
        ) return (CHANNEL_PROPOSAL_ABSENT_FLAG, STABLE_CHANNEL_VALUE);
        if (directive.mode == Types.ChannelMode.AnyPrerelease && baseline == STABLE_CHANNEL_VALUE) {
            return (CHANNEL_PROPOSAL_ABSENT_FLAG, STABLE_CHANNEL_VALUE);
        }
        bytes32 target = directive.mode == Types.ChannelMode.Inherit ? origin : directive.to;
        if (
            isPropagated && target == STABLE_CHANNEL_VALUE && baseline != STABLE_CHANNEL_VALUE
                && directive.mode != Types.ChannelMode.Transition && directive.mode != Types.ChannelMode.AnyPrerelease
        ) return (CHANNEL_PROPOSAL_ABSENT_FLAG, STABLE_CHANNEL_VALUE);
        return (target != baseline, target);
    }

    function resolveSourcePackages(Composition memory composition) private pure returns (uint256[] memory sources) {
        uint256 controls;
        uint256 held;
        for (uint256 i; i < composition.snapshot.units.length; ++i) {
            Types.Unit memory unit = composition.snapshot.units[i];
            if (unit.releaseKind == Types.ReleaseKind.Unset) continue;
            uint256 eligible = composition.history.scopes[i] & composition.history.fresh[i]
                & ~composition.history.cancelled[i] & ~controls;
            controls |= eligible;
            if (unit.releaseKind == Types.ReleaseKind.Hold) held |= eligible;
        }
        sources = new uint256[](composition.snapshot.units.length);
        for (uint256 i; i < sources.length; ++i) {
            if (composition.snapshot.units[i].isCancel) continue;
            sources[i] = composition.history.scopes[i]
                & ~(composition.history.fresh[i] & (composition.history.cancelled[i] | held));
        }
    }

    function findTargets(Composition memory composition, uint256 sources, Types.Unit memory unit)
        private
        pure
        returns (uint256 targets)
    {
        uint256 count = composition.snapshot.packages.length;
        for (uint256 target; target < count; ++target) {
            uint256 bit = MEMBER_BIT_MASK << target;
            bool isBumpReached = unit.bump != Types.Bump.None && unit.propagate != Types.Bump.None
                && unit.propagateScope & bit != EMPTY_SET_MASK
                && Graph.findReachingSources(composition.graph, count, sources, target, unit.depth) != EMPTY_SET_MASK;
            bool isChannelReached = unit.propagateChannel.mode != Types.ChannelMode.None
                && unit.channelScope & bit != EMPTY_SET_MASK
                && Graph.findReachingSources(composition.graph, count, sources, target, unit.channelDepth)
                    != EMPTY_SET_MASK;
            if (isBumpReached || isChannelReached) targets |= bit;
        }
    }

    function validateCrossEdge(Input calldata input, CrossEdge calldata edge, uint256 index) private pure {
        if (
            edge.providerRepository >= input.repositories.length || edge.consumerRepository >= input.repositories.length
                || edge.providerPackage >= input.repositories[edge.providerRepository].snapshot.packages.length
                || edge.consumerPackage >= input.repositories[edge.consumerRepository].snapshot.packages.length
                || edge.providerRepository == edge.consumerRepository
                || (!edge.isPropagationEnabled && !edge.isPublishOrderEnabled)
        ) {
            revert Types.InvalidInput(CROSS_EDGE_FIELD_ID, index);
        }
    }

    function validateBoundaryReference(
        Input calldata input,
        uint256 consumerRepository,
        uint256 consumerPackage,
        uint256 sourceRepository,
        uint16 sourceCommit,
        uint256 index
    ) private pure {
        if (
            consumerRepository >= input.repositories.length || sourceRepository >= input.repositories.length
                || consumerRepository == sourceRepository
                || consumerPackage >= input.repositories[consumerRepository].snapshot.packages.length
                || (sourceCommit != MISSING_COMMIT_INDEX
                    && sourceCommit >= input.repositories[sourceRepository].snapshot.commits.length)
        ) {
            revert Types.CCMEError(INVALID_REPOSITORY_BOUNDARY_CODE, index, UNSPECIFIED_CONTEXT_INDEX);
        }
    }

    function projectInventoryMask(Input calldata input, Composition memory composition, uint256 mask)
        private
        pure
        returns (uint256 result)
    {
        for (uint256 repositoryIndex; repositoryIndex < input.repositories.length; ++repositoryIndex) {
            if (composition.selected & (MEMBER_BIT_MASK << repositoryIndex) == EMPTY_SET_MASK) continue;
            uint256 localMask = mask >> composition.inventoryOffsets[repositoryIndex];
            result |= projectHistory(localMask, composition.packageMap[repositoryIndex]);
        }
    }

    function projectHistory(uint256 mask, uint16[] memory mappingIndices) private pure returns (uint256 result) {
        for (uint256 i; i < mappingIndices.length; ++i) {
            if (mask & (MEMBER_BIT_MASK << i) != EMPTY_SET_MASK) result |= MEMBER_BIT_MASK << mappingIndices[i];
        }
    }

    function isNameLess(bytes memory left, bytes memory right) private pure returns (bool) {
        uint256 count = left.length < right.length ? left.length : right.length;
        for (uint256 i; i < count; ++i) {
            if (left[i] != right[i]) return uint8(left[i]) < uint8(right[i]);
        }
        return left.length < right.length;
    }
}
