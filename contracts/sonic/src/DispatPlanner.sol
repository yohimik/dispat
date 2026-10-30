// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.28;

import {CCMETypes as Types} from "./CCMETypes.sol";
import {CCMEHistory as History} from "./CCMEHistory.sol";
import {CCMEGraph as Graph} from "./CCMEGraph.sol";
import {CCMEChannels as Channels} from "./CCMEChannels.sol";
import {CCMEVersion as Versioning} from "./CCMEVersion.sol";
import {CCMEDiagnostics as Diagnostics} from "./CCMEDiagnostics.sol";

/// @notice Computes a bounded CCME forward-release plan from normalized, caller-supplied history.
/// @dev This pure computation authenticates neither the Git snapshot nor permission to publish.
contract DispatPlanner {
    uint256 private constant EMPTY_MASK = 0;
    uint256 private constant PACKAGE_BIT = 1;
    uint256 private constant INDEX_OFFSET = 1;
    uint256 private constant UNSET_UNIT_POSITION = 0;
    uint256 private constant UNKNOWN_PACKAGE_INDEX = 0;
    uint16 private constant DISABLED_DEPTH = 0;
    uint16 private constant RELEASE_DIRECTIVE_CONFLICT_CODE = 153;
    uint16 private constant RELEASE_HOLD_CODE = 154;
    uint16 private constant EXACT_SCOPE_ERROR_CODE = 154;
    uint16 private constant REDUNDANT_RESUME_CODE = 158;
    uint16 private constant CATCH_UP_CODE = 193;
    uint16 private constant CHANNEL_ONLY_CODE = 202;
    uint16 private constant CHANNEL_ENTRY_PATCH_CODE = 204;
    uint16 private constant UNRESOLVABLE_BUMP_CODE = 208;
    uint16 private constant PACKAGE_DIAGNOSTIC_INDEX = type(uint16).max;
    bytes32 private constant STABLE_CHANNEL = bytes32(0);
    bytes32 private constant PREPARED_HISTORY_FIELD = "preparedHistory";
    bytes32 private constant INITIAL_CHANNEL_FIELD = "initialChannel";
    bool private constant PRESENT_FLAG = true;

    struct Controls {
        uint256 held;
        uint256[] pinUnits;
    }

    function compute(Types.Snapshot calldata snapshot) external pure returns (Types.Plan memory result) {
        History.State memory history = History.analyze(snapshot);
        return computePlan(snapshot, history);
    }

    /// @notice Computes from normalized history facts, for the multi-history adapter.
    /// @dev Supplied windows and ancestry are trusted numerical inputs, not authenticated ledger evidence.
    function computePrepared(Types.Snapshot calldata snapshot, History.State calldata prepared)
        external
        pure
        returns (Types.Plan memory)
    {
        uint256 count = snapshot.packages.length;
        uint256 units = snapshot.units.length;
        if (
            prepared.baselines.length != count || prepared.stableBaselines.length != count
                || prepared.baselineCommits.length != count || prepared.stableCommits.length != count
                || prepared.ancestors.length != snapshot.commits.length || prepared.scopes.length != units
                || prepared.fresh.length != units || prepared.train.length != units
                || prepared.cancelled.length != units || prepared.suppressedTargets.length != units
                || prepared.incorporated.length != count * count || units > Types.MAX_UNITS
                || snapshot.commits.length > Types.MAX_COMMITS || snapshot.tags.length > Types.MAX_TAGS
        ) {
            revert Types.InvalidInput(PREPARED_HISTORY_FIELD, UNKNOWN_PACKAGE_INDEX);
        }
        History.State memory history = prepared;
        return computePlan(snapshot, history);
    }

    function computePlan(Types.Snapshot calldata snapshot, History.State memory history)
        private
        pure
        returns (Types.Plan memory result)
    {
        Graph.State memory graph = Graph.analyze(snapshot);
        Diagnostics.Buffer memory diagnostics = Diagnostics.create(history.diagnostics);
        Controls memory controls = resolveControls(snapshot, history, diagnostics);
        uint256[] memory sources = resolveSourcePackages(snapshot, history, controls.held);
        bytes32[] memory channels = Channels.resolve(snapshot, history, graph, sources, diagnostics);
        for (uint256 i; i < channels.length; ++i) {
            if (controls.pinUnits[i] != UNSET_UNIT_POSITION) {
                channels[i] = snapshot.units[controls.pinUnits[i] - INDEX_OFFSET].pin.channel;
            }
        }
        result.packages = calculateDirectBumps(snapshot, history, channels, controls.held);
        propagateBumps(snapshot, history, graph, sources, result.packages, diagnostics);
        computeVersions(snapshot, history, controls, result.packages, diagnostics);
        result.publishOrder = selectOrder(graph.publishOrder, result.packages);
        reportCatchUp(result.packages, diagnostics);
        result.diagnostics = Diagnostics.finish(diagnostics);
    }

    function resolveControls(
        Types.Snapshot calldata snapshot,
        History.State memory history,
        Diagnostics.Buffer memory diagnostics
    ) private pure returns (Controls memory controls) {
        uint256 count = snapshot.packages.length;
        controls.pinUnits = new uint256[](count);
        Types.ReleaseKind[] memory winners = new Types.ReleaseKind[](count);
        uint256 olderHolds;
        for (uint256 i; i < snapshot.units.length; ++i) {
            Types.Unit calldata unit = snapshot.units[i];
            if (unit.releaseKind == Types.ReleaseKind.Unset) continue;
            uint256 scope = history.scopes[i];
            if (scope == EMPTY_MASK) continue;
            if (unit.releaseKind == Types.ReleaseKind.Exact && (scope & (scope - INDEX_OFFSET)) != EMPTY_MASK) {
                revert Types.CCMEError(EXACT_SCOPE_ERROR_CODE, UNKNOWN_PACKAGE_INDEX, i);
            }
            uint256 eligible = scope & history.fresh[i] & ~history.cancelled[i];
            for (uint256 target; target < count; ++target) {
                uint256 bit = PACKAGE_BIT << target;
                if ((eligible & bit) == EMPTY_MASK) continue;
                if (winners[target] != Types.ReleaseKind.Unset) {
                    Diagnostics.add(diagnostics, RELEASE_DIRECTIVE_CONFLICT_CODE, target, i);
                    if (unit.releaseKind == Types.ReleaseKind.Hold) olderHolds |= bit;
                    continue;
                }
                winners[target] = unit.releaseKind;
                if (unit.releaseKind == Types.ReleaseKind.Hold) controls.held |= bit;
                if (unit.releaseKind == Types.ReleaseKind.Exact) controls.pinUnits[target] = i + INDEX_OFFSET;
            }
        }
        for (uint256 i; i < count; ++i) {
            if (winners[i] == Types.ReleaseKind.Auto && (olderHolds & (PACKAGE_BIT << i)) == EMPTY_MASK) {
                Diagnostics.add(diagnostics, REDUNDANT_RESUME_CODE, i, PACKAGE_DIAGNOSTIC_INDEX);
            }
        }
    }

    function resolveSourcePackages(Types.Snapshot calldata snapshot, History.State memory history, uint256 held)
        private
        pure
        returns (uint256[] memory sources)
    {
        sources = new uint256[](snapshot.units.length);
        for (uint256 i; i < sources.length; ++i) {
            if (snapshot.units[i].isCancel) continue;
            sources[i] = history.scopes[i] & ~(history.fresh[i] & (history.cancelled[i] | held));
        }
    }

    function calculateDirectBumps(
        Types.Snapshot calldata snapshot,
        History.State memory history,
        bytes32[] memory channels,
        uint256 held
    ) private pure returns (Types.PackagePlan[] memory packages) {
        packages = new Types.PackagePlan[](snapshot.packages.length);
        for (uint256 i; i < packages.length; ++i) {
            Versioning.validate(snapshot.packages[i].initialVersion);
            if (snapshot.packages[i].initialVersion.channel != STABLE_CHANNEL) {
                revert Types.InvalidInput(INITIAL_CHANNEL_FIELD, i);
            }
            packages[i].baseline = history.baselines[i];
            packages[i].next = history.baselines[i];
            packages[i].channel = channels[i];
            packages[i].hasBaseline = (history.hasBaseline & (PACKAGE_BIT << i)) != EMPTY_MASK;
            packages[i].isHeld = (held & (PACKAGE_BIT << i)) != EMPTY_MASK;
        }
        for (uint256 i; i < snapshot.units.length; ++i) {
            Types.Unit calldata unit = snapshot.units[i];
            if (unit.isCancel || unit.bump == Types.Bump.None) continue;
            uint256 eligible = history.scopes[i] & ~history.cancelled[i];
            for (uint256 target; target < packages.length; ++target) {
                uint256 bit = PACKAGE_BIT << target;
                if ((eligible & bit) == EMPTY_MASK) continue;
                if ((history.train[i] & bit) != EMPTY_MASK) {
                    packages[target].trainBump = selectMaximumBump(packages[target].trainBump, unit.bump);
                }
                if ((history.fresh[i] & bit) != EMPTY_MASK) {
                    packages[target].directBump = selectMaximumBump(packages[target].directBump, unit.bump);
                }
            }
        }
    }

    function propagateBumps(
        Types.Snapshot calldata snapshot,
        History.State memory history,
        Graph.State memory graph,
        uint256[] memory sources,
        Types.PackagePlan[] memory packages,
        Diagnostics.Buffer memory diagnostics
    ) private pure {
        uint256 count = packages.length;
        uint256[] memory channelBits = new uint256[](count);
        for (uint256 i; i < count; ++i) {
            channelBits[i] = PACKAGE_BIT << i;
            for (uint256 j; j < i; ++j) {
                if (packages[j].channel == packages[i].channel) {
                    channelBits[i] = channelBits[j];
                    break;
                }
            }
        }
        for (uint256 i; i < snapshot.units.length; ++i) {
            Types.Unit calldata unit = snapshot.units[i];
            if (
                unit.bump == Types.Bump.None || unit.propagate == Types.Bump.None || unit.depth == DISABLED_DEPTH
                    || sources[i] == EMPTY_MASK
            ) continue;
            uint256 sourceChannels;
            bool hasStableSource;
            for (uint256 source; source < count; ++source) {
                if ((sources[i] & (PACKAGE_BIT << source)) == EMPTY_MASK) continue;
                sourceChannels |= channelBits[source];
                if (packages[source].channel == STABLE_CHANNEL) hasStableSource = PRESENT_FLAG;
            }
            for (uint256 target; target < count; ++target) {
                uint256 bit = PACKAGE_BIT << target;
                if ((unit.propagateScope & ~history.cancelled[i] & bit) == EMPTY_MASK) continue;
                uint256 reaching = Graph.findReachingSources(graph, count, sources[i], target, unit.depth);
                if (reaching == EMPTY_MASK) continue;
                uint256 owed;
                for (uint256 source; source < count; ++source) {
                    uint256 sourceBit = PACKAGE_BIT << source;
                    if (
                        (reaching & sourceBit) != EMPTY_MASK
                            && !History.isDelivered(history, count, source, target, unit.commitIndex)
                    ) owed |= sourceBit;
                }
                if ((history.suppressedTargets[i] & bit) != EMPTY_MASK) owed = EMPTY_MASK;
                if (!hasStableSource && (sourceChannels & channelBits[target]) == EMPTY_MASK) {
                    if (owed != EMPTY_MASK) Diagnostics.add(diagnostics, UNRESOLVABLE_BUMP_CODE, target, i);
                    continue;
                }
                bool isCarried = (history.train[i] & ~history.fresh[i] & bit) != EMPTY_MASK;
                if (isCarried) {
                    packages[target].trainBump = selectMaximumBump(packages[target].trainBump, unit.propagate);
                }
                if (owed == EMPTY_MASK) continue;
                packages[target].propagatedBump = selectMaximumBump(packages[target].propagatedBump, unit.propagate);
                packages[target].trainBump = selectMaximumBump(packages[target].trainBump, unit.propagate);
                packages[target].owedSources |= owed;
            }
        }
    }

    function computeVersions(
        Types.Snapshot calldata snapshot,
        History.State memory history,
        Controls memory controls,
        Types.PackagePlan[] memory packages,
        Diagnostics.Buffer memory diagnostics
    ) private pure {
        for (uint256 i; i < packages.length; ++i) {
            Types.PackagePlan memory entry = packages[i];
            Types.Bump effective = selectMaximumBump(entry.directBump, entry.propagatedBump);
            bool isChannelChanged = entry.channel != entry.baseline.channel;
            bool hasPin = controls.pinUnits[i] != UNSET_UNIT_POSITION;
            bool isAdmitted = effective != Types.Bump.None || isChannelChanged || hasPin;
            if (entry.isHeld) Diagnostics.add(diagnostics, RELEASE_HOLD_CODE, i, PACKAGE_DIAGNOSTIC_INDEX);
            if (!isAdmitted) continue;
            entry.isChannelOnly = effective == Types.Bump.None && isChannelChanged && !hasPin;
            if (entry.isChannelOnly) Diagnostics.add(diagnostics, CHANNEL_ONLY_CODE, i, PACKAGE_DIAGNOSTIC_INDEX);
            Types.Version memory pin;
            if (hasPin) pin = snapshot.units[controls.pinUnits[i] - INDEX_OFFSET].pin;
            (entry.next, entry.hasChannelEntryPatch) = Versioning.compute(
                Versioning.Input({
                    baseline: entry.baseline,
                    stableBaseline: history.stableBaselines[i],
                    initialVersion: snapshot.packages[i].initialVersion,
                    hasBaseline: entry.hasBaseline,
                    hasStableBaseline: (history.hasStableBaseline & (PACKAGE_BIT << i)) != EMPTY_MASK,
                    preserveMajorZero: snapshot.packages[i].preserveMajorZero,
                    bump: (entry.channel != STABLE_CHANNEL || entry.baseline.channel != STABLE_CHANNEL)
                        ? selectMaximumBump(entry.trainBump, effective)
                        : effective,
                    channel: entry.channel,
                    hasPin: hasPin,
                    pin: pin,
                    packageIndex: i
                })
            );
            if (entry.hasChannelEntryPatch) {
                Diagnostics.add(diagnostics, CHANNEL_ENTRY_PATCH_CODE, i, PACKAGE_DIAGNOSTIC_INDEX);
            }
            entry.isPlanned = !entry.isHeld;
        }
    }

    function reportCatchUp(Types.PackagePlan[] memory packages, Diagnostics.Buffer memory diagnostics) private pure {
        uint256 planned;
        for (uint256 i; i < packages.length; ++i) {
            if (packages[i].isPlanned) planned |= PACKAGE_BIT << i;
        }
        for (uint256 i; i < packages.length; ++i) {
            Types.PackagePlan memory entry = packages[i];
            if (
                entry.isPlanned && entry.directBump == Types.Bump.None && entry.owedSources != EMPTY_MASK
                    && (entry.owedSources & planned) == EMPTY_MASK
            ) {
                entry.isCatchUp = PRESENT_FLAG;
                Diagnostics.add(diagnostics, CATCH_UP_CODE, i, PACKAGE_DIAGNOSTIC_INDEX);
            }
        }
    }

    function selectOrder(uint16[] memory fullOrder, Types.PackagePlan[] memory packages)
        private
        pure
        returns (uint16[] memory result)
    {
        uint256 count;
        for (uint256 i; i < packages.length; ++i) {
            if (packages[i].isPlanned) ++count;
        }
        result = new uint16[](count);
        uint256 position;
        for (uint256 i; i < fullOrder.length; ++i) {
            if (packages[fullOrder[i]].isPlanned) result[position++] = fullOrder[i];
        }
    }

    function selectMaximumBump(Types.Bump left, Types.Bump right) private pure returns (Types.Bump) {
        return left > right ? left : right;
    }
}
