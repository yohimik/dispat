// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.28;

import {CCMETypes as Types} from "./CCMETypes.sol";
import {CCMEVersion as Versioning} from "./CCMEVersion.sol";
import {CCMEGraph as Graph} from "./CCMEGraph.sol";

/// @notice Bounded, single-repository ancestry and immutable-record projection.
/// @dev Inputs contain the entire reachable commit DAG, in canonical oldest-first
/// topological order. The last commit is HEAD. Unit order is newest commit first,
/// then last unit first. Ordering never consults a clock or commit timestamp.
library CCMEHistory {
    uint256 private constant MEMBER_BIT_MASK = 1;
    uint256 private constant EMPTY_SET_MASK = 0;
    uint256 private constant MASK_CAPACITY_BITS = 256;
    uint256 private constant MASK_TERMINATOR_OFFSET = 1;
    uint256 private constant PREVIOUS_INDEX_OFFSET = 1;
    uint256 private constant FIRST_INDEX_VALUE = 0;
    uint256 private constant EMPTY_COMMIT_COUNT = 0;
    uint256 private constant EMPTY_UNIT_COUNT = 0;
    uint256 private constant DISABLED_PROPAGATION_DEPTH = 0;
    uint256 private constant UNSPECIFIED_PACKAGE_INDEX = 0;
    int256 private constant SEMVER_EQUAL_ORDER_VALUE = 0;
    bytes32 private constant EMPTY_COMMIT_ID = bytes32(0);
    bytes32 private constant STABLE_CHANNEL_VALUE = bytes32(0);
    bool private constant CORRECTION_APPLIED_FLAG = true;
    uint16 private constant INVALID_CANCEL_DIRECTIVE_CODE = 171;
    uint16 private constant INVALID_RELEASE_DIRECTIVE_CODE = 173;
    uint16 private constant CONFLICTING_RELEASE_RECORD_CODE = 191;
    uint16 private constant INVALID_CORRECTION_TARGET_CODE = 210;
    uint16 private constant INVALID_CORRECTION_SELECTOR_CODE = 211;
    uint16 private constant CORRECTION_CONTROL_TARGET_CODE = 212;
    uint16 private constant CORRECTION_SCOPE_WIDENING_CODE = 213;
    uint16 private constant CANCELLED_PRERELEASE_WARNING_CODE = 171;
    uint16 private constant CORRECTION_NOOP_WARNING_CODE = 209;
    uint16 private constant CORRECTION_SUPERSEDED_WARNING_CODE = 210;
    uint16 private constant CORRECTION_VOID_WARNING_CODE = 215;
    bytes32 private constant PACKAGES_FIELD_ID = "packages";
    bytes32 private constant COMMITS_FIELD_ID = "commits";
    bytes32 private constant UNITS_FIELD_ID = "units";
    bytes32 private constant TAGS_FIELD_ID = "tags";
    bytes32 private constant EDGES_FIELD_ID = "edges";
    bytes32 private constant COMMIT_ID_FIELD_ID = "commit.id";
    bytes32 private constant COMMIT_PARENTS_FIELD_ID = "commit.parents";
    bytes32 private constant COMMIT_DUPLICATE_FIELD_ID = "commit.duplicate";
    bytes32 private constant COMMIT_UNREACHABLE_FIELD_ID = "commit.unreachable";
    bytes32 private constant TAG_PACKAGE_FIELD_ID = "tag.package";
    bytes32 private constant TAG_COMMIT_FIELD_ID = "tag.commit";
    bytes32 private constant UNIT_COMMIT_FIELD_ID = "unit.commit";
    bytes32 private constant UNIT_SCOPE_FIELD_ID = "unit.scope";
    bytes32 private constant UNIT_ORDER_FIELD_ID = "unit.order";
    bytes32 private constant CORRECTION_INHERITED_FIELD_ID = "correction.inherited";
    bytes32 private constant UNIT_CONTROL_FIELD_ID = "unit.control";
    bytes32 private constant CORRECTION_SCOPE_FIELD_ID = "correction.scope";

    struct State {
        Types.Version[] baselines;
        Types.Version[] stableBaselines;
        uint16[] baselineCommits;
        uint16[] stableCommits;
        uint256 hasBaseline;
        uint256 hasStableBaseline;
        uint256[] ancestors;
        uint256[] scopes;
        uint256[] fresh;
        uint256[] train;
        // Raw cancellation predicates: a target's owed contribution may still
        // be cancelled even when the target has released past its commit.
        uint256[] cancelled;
        // Wildcards remove a consumer's pending incoming contributions without
        // changing the source unit or its already-published train contribution.
        uint256[] suppressedTargets;
        // [consumer * packageCount + provider] holds commits delivered by that
        // provider before or at the consumer's highest-precedence baseline.
        uint256[] incorporated;
        Types.Diagnostic[] diagnostics;
    }

    function analyze(Types.Snapshot calldata input) internal pure returns (State memory history) {
        validateLimits(input);
        uint256 packageCount = input.packages.length;
        uint256 count = input.units.length;
        history.baselines = new Types.Version[](packageCount);
        history.stableBaselines = new Types.Version[](packageCount);
        history.baselineCommits = new uint16[](packageCount);
        history.stableCommits = new uint16[](packageCount);
        history.scopes = new uint256[](count);
        history.fresh = new uint256[](count);
        history.train = new uint256[](count);
        history.cancelled = new uint256[](count);
        history.suppressedTargets = new uint256[](count);
        history.incorporated = new uint256[](packageCount * packageCount);
        history.ancestors = computeAncestry(input);
        selectBaselines(input, history);
        normalizeUnits(input, history);
        computeDelivery(input, history);
        // Controls cannot be correction targets. Their immutable barrier facts
        // can therefore be indexed first without applying cancellation before
        // corrections. A later correction of already-cancelled work is W209
        // (SPEC edge case 119), while its own restatement still contributes.
        collectCancellations(input, history);
        applyCorrections(input, history);
    }

    function isDelivered(
        State memory history,
        uint256 packageCount,
        uint256 source,
        uint256 consumer,
        uint256 commitIndex
    ) internal pure returns (bool) {
        return history.incorporated[consumer * packageCount + source] & (MEMBER_BIT_MASK << commitIndex)
            != EMPTY_SET_MASK;
    }

    function validateLimits(Types.Snapshot calldata input) private pure {
        if (input.packages.length > Types.MAX_PACKAGES) {
            revert Types.InvalidInput(PACKAGES_FIELD_ID, input.packages.length);
        }
        if (input.commits.length > Types.MAX_COMMITS) {
            revert Types.InvalidInput(COMMITS_FIELD_ID, input.commits.length);
        }
        if (input.units.length > Types.MAX_UNITS) revert Types.InvalidInput(UNITS_FIELD_ID, input.units.length);
        if (input.tags.length > Types.MAX_TAGS) revert Types.InvalidInput(TAGS_FIELD_ID, input.tags.length);
        if (input.edges.length > Types.MAX_EDGES) revert Types.InvalidInput(EDGES_FIELD_ID, input.edges.length);
    }

    function createMask(uint256 count) private pure returns (uint256) {
        return count == MASK_CAPACITY_BITS ? type(uint256).max : (MEMBER_BIT_MASK << count) - MASK_TERMINATOR_OFFSET;
    }

    function computeAncestry(Types.Snapshot calldata input) private pure returns (uint256[] memory ancestors) {
        uint256 commitCount = input.commits.length;
        ancestors = new uint256[](commitCount);
        for (uint256 i; i < commitCount; ++i) {
            Types.Commit calldata commit = input.commits[i];
            if (commit.id == EMPTY_COMMIT_ID) revert Types.InvalidInput(COMMIT_ID_FIELD_ID, i);
            if (commit.parents & ~createMask(i) != EMPTY_SET_MASK) {
                revert Types.InvalidInput(COMMIT_PARENTS_FIELD_ID, i);
            }
            for (uint256 j; j < i; ++j) {
                if (input.commits[j].id == commit.id) revert Types.InvalidInput(COMMIT_DUPLICATE_FIELD_ID, i);
                if (commit.parents & (MEMBER_BIT_MASK << j) != EMPTY_SET_MASK) ancestors[i] |= ancestors[j];
            }
            ancestors[i] |= MEMBER_BIT_MASK << i;
        }
        if (
            commitCount != EMPTY_COMMIT_COUNT
                && ancestors[commitCount - PREVIOUS_INDEX_OFFSET] != createMask(commitCount)
        ) {
            revert Types.InvalidInput(COMMIT_UNREACHABLE_FIELD_ID, commitCount - PREVIOUS_INDEX_OFFSET);
        }
    }

    function selectBaselines(Types.Snapshot calldata input, State memory history) private pure {
        for (uint256 i; i < input.tags.length; ++i) {
            Types.Tag calldata tag = input.tags[i];
            uint256 packageIndex = tag.packageIndex;
            if (packageIndex >= input.packages.length) revert Types.InvalidInput(TAG_PACKAGE_FIELD_ID, i);
            if (tag.commitIndex >= input.commits.length) revert Types.InvalidInput(TAG_COMMIT_FIELD_ID, i);
            Versioning.validate(tag.version);
            // A version denotes exactly one immutable release position. Repeated
            // copies of that same record are harmless and do not create work.
            for (uint256 j; j < i; ++j) {
                Types.Tag calldata other = input.tags[j];
                if (
                    other.packageIndex == packageIndex
                        && Versioning.compare(other.version, tag.version) == SEMVER_EQUAL_ORDER_VALUE
                        && other.commitIndex != tag.commitIndex
                ) revert Types.CCMEError(CONFLICTING_RELEASE_RECORD_CODE, packageIndex, i);
            }
            uint256 bit = MEMBER_BIT_MASK << packageIndex;
            if (
                history.hasBaseline & bit == EMPTY_SET_MASK
                    || Versioning.compare(tag.version, history.baselines[packageIndex]) > SEMVER_EQUAL_ORDER_VALUE
            ) {
                history.hasBaseline |= bit;
                history.baselines[packageIndex] = tag.version;
                history.baselineCommits[packageIndex] = tag.commitIndex;
            }
            if (
                tag.version.channel == STABLE_CHANNEL_VALUE
                    && (history.hasStableBaseline & bit == EMPTY_SET_MASK
                        || Versioning.compare(tag.version, history.stableBaselines[packageIndex])
                            > SEMVER_EQUAL_ORDER_VALUE)
            ) {
                history.hasStableBaseline |= bit;
                history.stableBaselines[packageIndex] = tag.version;
                history.stableCommits[packageIndex] = tag.commitIndex;
            }
        }
    }

    function normalizeUnits(Types.Snapshot calldata input, State memory history) private pure {
        uint256 allPackages = createMask(input.packages.length);
        uint256 allUnits = createMask(input.units.length);
        for (uint256 i; i < input.units.length; ++i) {
            Types.Unit calldata unit = input.units[i];
            if (unit.commitIndex >= input.commits.length) revert Types.InvalidInput(UNIT_COMMIT_FIELD_ID, i);
            if ((unit.scope | unit.propagateScope | unit.channelScope) & ~allPackages != EMPTY_SET_MASK) {
                revert Types.InvalidInput(UNIT_SCOPE_FIELD_ID, i);
            }
            if (unit.correctionTargets & ~allUnits != EMPTY_SET_MASK) {
                revert Types.CCMEError(INVALID_CORRECTION_SELECTOR_CODE, UNSPECIFIED_PACKAGE_INDEX, i);
            }
            if (i != FIRST_INDEX_VALUE) {
                Types.Unit calldata previous = input.units[i - PREVIOUS_INDEX_OFFSET];
                if (
                    unit.commitIndex > previous.commitIndex
                        || (unit.commitIndex == previous.commitIndex && unit.unitIndex >= previous.unitIndex)
                ) {
                    revert Types.InvalidInput(UNIT_ORDER_FIELD_ID, i);
                }
            }
            bool isCorrection = unit.correctionTargets != EMPTY_SET_MASK || unit.correctionWildcard;
            if (unit.correctionScopeInherited && !isCorrection) {
                revert Types.InvalidInput(CORRECTION_INHERITED_FIELD_ID, i);
            }
            if (unit.isCancel && unit.isRelease) revert Types.InvalidInput(UNIT_CONTROL_FIELD_ID, i);
            if (
                unit.isCancel
                    && (unit.bump != Types.Bump.None
                        || unit.propagate != Types.Bump.None
                        || unit.depth != DISABLED_PROPAGATION_DEPTH
                        || unit.channelDepth != DISABLED_PROPAGATION_DEPTH
                        || unit.channel.mode != Types.ChannelMode.None
                        || unit.propagateChannel.mode != Types.ChannelMode.None
                        || unit.releaseKind != Types.ReleaseKind.Unset
                        || isCorrection)
            ) revert Types.CCMEError(INVALID_CANCEL_DIRECTIVE_CODE, UNSPECIFIED_PACKAGE_INDEX, i);
            if (unit.isRelease && (unit.bump != Types.Bump.None || isCorrection)) {
                revert Types.CCMEError(INVALID_RELEASE_DIRECTIVE_CODE, UNSPECIFIED_PACKAGE_INDEX, i);
            }
            history.scopes[i] = unit.scope;
            uint256 commitBit = MEMBER_BIT_MASK << unit.commitIndex;
            for (uint256 packageIndex; packageIndex < input.packages.length; ++packageIndex) {
                uint256 bit = MEMBER_BIT_MASK << packageIndex;
                if (
                    history.hasStableBaseline & bit == EMPTY_SET_MASK
                        || history.ancestors[history.stableCommits[packageIndex]] & commitBit == EMPTY_SET_MASK
                ) {
                    history.train[i] |= bit;
                }
                if (
                    history.train[i] & bit != EMPTY_SET_MASK
                        && (history.hasBaseline & bit == EMPTY_SET_MASK
                            || history.ancestors[history.baselineCommits[packageIndex]] & commitBit == EMPTY_SET_MASK)
                ) {
                    history.fresh[i] |= bit;
                }
            }
        }
    }

    function computeDelivery(Types.Snapshot calldata input, State memory history) private pure {
        uint256 packageCount = input.packages.length;
        for (uint256 consumer; consumer < packageCount; ++consumer) {
            if (history.hasBaseline & (MEMBER_BIT_MASK << consumer) == EMPTY_SET_MASK) continue;
            uint256 position = history.ancestors[history.baselineCommits[consumer]];
            for (uint256 tagIndex; tagIndex < input.tags.length; ++tagIndex) {
                Types.Tag calldata tag = input.tags[tagIndex];
                if (position & (MEMBER_BIT_MASK << tag.commitIndex) != EMPTY_SET_MASK) {
                    history.incorporated[
                        consumer * packageCount + tag.packageIndex
                    ] |= history.ancestors[tag.commitIndex];
                }
            }
        }
    }

    function applyCorrections(Types.Snapshot calldata input, State memory history) private pure {
        uint256 count = input.units.length;
        uint256[] memory noop = new uint256[](count);
        uint256[] memory superseded = new uint256[](count);
        uint256[] memory voided = new uint256[](count);
        Graph.State memory graph;
        for (uint256 i; i < count; ++i) {
            if (input.units[i].correctionWildcard) {
                graph = Graph.analyze(input);
                break;
            }
        }
        for (uint256 i; i < count; ++i) {
            Types.Unit calldata unit = input.units[i];
            if (unit.correctionTargets == EMPTY_SET_MASK && !unit.correctionWildcard) continue;
            validateCorrection(input, history, i);
            uint256 live = history.scopes[i];
            voided[i] = unit.scope & ~live;
            bool isWildcardChanged;
            for (uint256 j; j < count; ++j) {
                bool isExplicitTarget = unit.correctionTargets & (MEMBER_BIT_MASK << j) != EMPTY_SET_MASK;
                Types.Unit calldata target = input.units[j];
                bool isWildcardTarget = unit.correctionWildcard && !target.isCancel && !target.isRelease
                    && target.commitIndex != unit.commitIndex
                    && history.ancestors[unit.commitIndex] & (MEMBER_BIT_MASK << target.commitIndex) != EMPTY_SET_MASK;
                if (!isExplicitTarget && !isWildcardTarget) continue;
                uint256 reached = live & target.scope;
                uint256 pending = reached & history.fresh[j] & ~history.cancelled[j];
                uint256 dropped = pending & history.scopes[j];
                if (isExplicitTarget) {
                    noop[i] |= reached & ~pending;
                    superseded[i] |= pending & ~history.scopes[j];
                }
                if (isWildcardTarget && dropped != EMPTY_SET_MASK) isWildcardChanged = CORRECTION_APPLIED_FLAG;
                history.scopes[j] &= ~dropped;
                if (isWildcardTarget) {
                    uint256 sources = history.scopes[j] & ~(history.fresh[j] & history.cancelled[j]);
                    uint256 suppressed = findPendingPropagation(input, history, graph, j, live, sources);
                    history.suppressedTargets[j] |= suppressed;
                    if (suppressed != EMPTY_SET_MASK) isWildcardChanged = CORRECTION_APPLIED_FLAG;
                }
            }
            if (unit.correctionWildcard && !isWildcardChanged) noop[i] |= live;
        }
        collectCorrectionDiagnostics(input, history, noop, superseded, voided);
    }

    function findPendingPropagation(
        Types.Snapshot calldata input,
        State memory history,
        Graph.State memory graph,
        uint256 unitIndex,
        uint256 targets,
        uint256 sources
    ) internal pure returns (uint256 suppressed) {
        Types.Unit calldata unit = input.units[unitIndex];
        uint256 packageCount = input.packages.length;
        targets &= ~history.cancelled[unitIndex] & ~history.suppressedTargets[unitIndex];
        for (uint256 target; target < packageCount; ++target) {
            uint256 bit = MEMBER_BIT_MASK << target;
            if (targets & bit == EMPTY_SET_MASK) continue;
            if (
                unit.propagateChannel.mode != Types.ChannelMode.None
                    && unit.channelScope & history.fresh[unitIndex] & bit != EMPTY_SET_MASK
                    && Graph.findReachingSources(graph, packageCount, sources, target, unit.channelDepth)
                        != EMPTY_SET_MASK
            ) suppressed |= bit;
            if (
                unit.bump == Types.Bump.None || unit.propagate == Types.Bump.None
                    || unit.propagateScope & bit == EMPTY_SET_MASK
            ) continue;
            uint256 reaching = Graph.findReachingSources(graph, packageCount, sources, target, unit.depth);
            for (uint256 source; source < packageCount; ++source) {
                if (
                    reaching & (MEMBER_BIT_MASK << source) != EMPTY_SET_MASK
                        && !isDelivered(history, packageCount, source, target, unit.commitIndex)
                ) suppressed |= bit;
            }
        }
    }

    function validateCorrection(Types.Snapshot calldata input, State memory history, uint256 index) private pure {
        Types.Unit calldata unit = input.units[index];
        uint256 unionScopes;
        for (uint256 j; j < input.units.length; ++j) {
            if (unit.correctionTargets & (MEMBER_BIT_MASK << j) == EMPTY_SET_MASK) continue;
            Types.Unit calldata target = input.units[j];
            if (
                target.commitIndex == unit.commitIndex
                    || history.ancestors[unit.commitIndex] & (MEMBER_BIT_MASK << target.commitIndex) == EMPTY_SET_MASK
            ) {
                revert Types.CCMEError(INVALID_CORRECTION_TARGET_CODE, UNSPECIFIED_PACKAGE_INDEX, index);
            }
            if (target.isCancel || target.isRelease) {
                revert Types.CCMEError(CORRECTION_CONTROL_TARGET_CODE, UNSPECIFIED_PACKAGE_INDEX, index);
            }
            if (!unit.correctionScopeInherited && unit.scope & ~target.scope != EMPTY_SET_MASK) {
                revert Types.CCMEError(CORRECTION_SCOPE_WIDENING_CODE, UNSPECIFIED_PACKAGE_INDEX, index);
            }
            unionScopes |= target.scope;
        }
        if (unit.correctionScopeInherited) {
            uint256 inherited =
                unit.correctionTargets != EMPTY_SET_MASK ? unionScopes : createMask(input.packages.length);
            if (unit.scope != inherited) revert Types.InvalidInput(CORRECTION_SCOPE_FIELD_ID, index);
        }
    }

    function collectCorrectionDiagnostics(
        Types.Snapshot calldata input,
        State memory history,
        uint256[] memory noop,
        uint256[] memory superseded,
        uint256[] memory voided
    ) private pure {
        uint256[] memory cancelledPrerelease = collectPrereleaseCancellations(input, history);
        uint256 count;
        for (uint256 packageIndex; packageIndex < input.packages.length; ++packageIndex) {
            uint256 bit = MEMBER_BIT_MASK << packageIndex;
            for (uint256 i; i < input.units.length; ++i) {
                if (noop[i] & bit != EMPTY_SET_MASK) ++count;
                if (superseded[i] & bit != EMPTY_SET_MASK) ++count;
                if (voided[i] & bit != EMPTY_SET_MASK) ++count;
                if (cancelledPrerelease[i] & bit != EMPTY_SET_MASK) ++count;
            }
        }
        history.diagnostics = new Types.Diagnostic[](count);
        uint256 at;
        for (uint256 packageIndex; packageIndex < input.packages.length; ++packageIndex) {
            uint256 bit = MEMBER_BIT_MASK << packageIndex;
            // Reverse newest-first units to emit topological index ascending.
            for (uint256 reverse = input.units.length; reverse != EMPTY_UNIT_COUNT; --reverse) {
                uint256 i = reverse - PREVIOUS_INDEX_OFFSET;
                if (cancelledPrerelease[i] & bit != EMPTY_SET_MASK) {
                    history.diagnostics[at++] =
                        Types.Diagnostic(CANCELLED_PRERELEASE_WARNING_CODE, uint16(packageIndex), uint16(i));
                }
                if (noop[i] & bit != EMPTY_SET_MASK) {
                    history.diagnostics[at++] =
                        Types.Diagnostic(CORRECTION_NOOP_WARNING_CODE, uint16(packageIndex), uint16(i));
                }
                if (superseded[i] & bit != EMPTY_SET_MASK) {
                    history.diagnostics[at++] =
                        Types.Diagnostic(CORRECTION_SUPERSEDED_WARNING_CODE, uint16(packageIndex), uint16(i));
                }
                if (voided[i] & bit != EMPTY_SET_MASK) {
                    history.diagnostics[at++] =
                        Types.Diagnostic(CORRECTION_VOID_WARNING_CODE, uint16(packageIndex), uint16(i));
                }
            }
        }
    }

    function collectPrereleaseCancellations(Types.Snapshot calldata input, State memory history)
        private
        pure
        returns (uint256[] memory warnings)
    {
        warnings = new uint256[](input.units.length);
        for (uint256 i; i < input.units.length; ++i) {
            Types.Unit calldata barrier = input.units[i];
            if (!barrier.isCancel) continue;
            uint256 closure = history.ancestors[barrier.commitIndex];
            for (uint256 j; j < input.units.length; ++j) {
                if (
                    input.units[j].isCancel
                        || closure & (MEMBER_BIT_MASK << input.units[j].commitIndex) == EMPTY_SET_MASK
                ) continue;
                warnings[i] |= history.scopes[i] & history.scopes[j] & history.train[j] & ~history.fresh[j];
            }
        }
    }

    function collectCancellations(Types.Snapshot calldata input, State memory history) private pure {
        for (uint256 i; i < input.units.length; ++i) {
            Types.Unit calldata barrier = input.units[i];
            if (!barrier.isCancel) continue;
            uint256 closure = history.ancestors[barrier.commitIndex];
            for (uint256 j; j < input.units.length; ++j) {
                if (closure & (MEMBER_BIT_MASK << input.units[j].commitIndex) != EMPTY_SET_MASK) {
                    history.cancelled[j] |= history.scopes[i];
                }
            }
        }
    }
}
