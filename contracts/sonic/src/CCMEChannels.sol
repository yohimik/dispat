// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.28;

import {CCMETypes as Types} from "./CCMETypes.sol";
import {CCMEHistory as History} from "./CCMEHistory.sol";
import {CCMEGraph as Graph} from "./CCMEGraph.sol";
import {CCMEVersion as Versioning} from "./CCMEVersion.sol";
import {CCMEDiagnostics as Diagnostics} from "./CCMEDiagnostics.sol";

library CCMEChannels {
    uint256 private constant EMPTY_MASK = 0;
    uint256 private constant PACKAGE_BIT = 1;
    uint16 private constant DISABLED_DEPTH = 0;
    uint16 private constant NO_WARNING_CODE = 0;
    uint16 private constant PROPAGATED_CHANNEL_CONFLICT_CODE = 160;
    uint16 private constant DIRECT_CHANNEL_CONFLICT_CODE = 186;
    uint16 private constant UNCHANGED_CHANNEL_CODE = 199;
    uint16 private constant IMPLICIT_GRADUATION_CODE = 200;
    uint16 private constant IDENTITY_TRANSITION_CODE = 207;
    bytes32 private constant UNSET_CHANNEL = bytes32(0);
    bytes32 private constant DIRECTIVE_FIELD = "channelDirective";
    bytes32 private constant FROM_FIELD = "channelFrom";
    bool private constant PRESENT_FLAG = true;
    bool private constant ABSENT_FLAG = false;

    function resolve(
        Types.Snapshot calldata snapshot,
        History.State memory history,
        Graph.State memory graph,
        uint256[] memory sources,
        Diagnostics.Buffer memory diagnostics
    ) internal pure returns (bytes32[] memory channels) {
        uint256 count = snapshot.packages.length;
        channels = new bytes32[](count);
        bool[] memory hasPropagated = new bool[](count);
        bool[] memory hasDirect = new bool[](count);
        for (uint256 i; i < count; ++i) {
            channels[i] = history.baselines[i].channel;
        }
        for (uint256 i; i < snapshot.units.length; ++i) {
            Types.Unit calldata unit = snapshot.units[i];
            validate(unit.channel, ABSENT_FLAG);
            validate(unit.propagateChannel, PRESENT_FLAG);
            if (
                unit.isCancel || sources[i] == EMPTY_MASK || unit.channelDepth == DISABLED_DEPTH
                    || unit.propagateChannel.mode == Types.ChannelMode.None
            ) continue;
            bytes32 origin;
            bool isOriginComputed;
            for (uint256 target; target < count; ++target) {
                uint256 bit = PACKAGE_BIT << target;
                if (
                    (unit.channelScope & history.fresh[i] & ~history.cancelled[i] & ~history.suppressedTargets[i] & bit)
                        == EMPTY_MASK
                ) continue;
                if (Graph.findReachingSources(graph, count, sources[i], target, unit.channelDepth) == EMPTY_MASK) {
                    continue;
                }
                if (unit.propagateChannel.mode == Types.ChannelMode.Inherit && !isOriginComputed) {
                    origin = resolveOriginChannel(unit, history, sources[i], count, diagnostics, i);
                    isOriginComputed = PRESENT_FLAG;
                }
                (bool isProposed, bytes32 value, uint16 warning) =
                    resolveProposal(unit.propagateChannel, history.baselines[target].channel, origin, PRESENT_FLAG);
                if (warning != NO_WARNING_CODE) Diagnostics.add(diagnostics, warning, target, i);
                if (!isProposed) continue;
                if (hasPropagated[target]) {
                    if (channels[target] != value) {
                        Diagnostics.add(diagnostics, PROPAGATED_CHANNEL_CONFLICT_CODE, target, i);
                    }
                    continue;
                }
                hasPropagated[target] = PRESENT_FLAG;
                channels[target] = value;
            }
        }
        for (uint256 i; i < snapshot.units.length; ++i) {
            Types.Unit calldata unit = snapshot.units[i];
            if (unit.isCancel || unit.channel.mode == Types.ChannelMode.None) continue;
            uint256 eligible = history.scopes[i] & history.fresh[i] & ~history.cancelled[i];
            for (uint256 target; target < count; ++target) {
                if ((eligible & (PACKAGE_BIT << target)) == EMPTY_MASK) continue;
                (bool isProposed, bytes32 value, uint16 warning) =
                    resolveProposal(unit.channel, history.baselines[target].channel, UNSET_CHANNEL, ABSENT_FLAG);
                if (warning != NO_WARNING_CODE) Diagnostics.add(diagnostics, warning, target, i);
                if (!isProposed) continue;
                if (hasDirect[target]) {
                    Diagnostics.add(diagnostics, DIRECT_CHANNEL_CONFLICT_CODE, target, i);
                    continue;
                }
                hasDirect[target] = PRESENT_FLAG;
                channels[target] = value;
            }
        }
    }

    function validate(Types.ChannelDirective calldata directive, bool isPropagated) internal pure {
        if (directive.mode == Types.ChannelMode.None || directive.mode == Types.ChannelMode.Inherit) {
            if (
                directive.from != UNSET_CHANNEL || directive.to != UNSET_CHANNEL
                    || (directive.mode == Types.ChannelMode.Inherit && !isPropagated)
            ) {
                revert Types.InvalidInput(DIRECTIVE_FIELD, uint256(directive.mode));
            }
            return;
        }
        Versioning.validateChannel(directive.to);
        if (directive.mode == Types.ChannelMode.Transition) {
            Versioning.validateChannel(directive.from);
            return;
        }
        if (directive.from != UNSET_CHANNEL) revert Types.InvalidInput(FROM_FIELD, uint256(directive.mode));
    }

    function resolveProposal(
        Types.ChannelDirective calldata directive,
        bytes32 baseline,
        bytes32 origin,
        bool isPropagated
    ) private pure returns (bool, bytes32, uint16) {
        if (directive.mode == Types.ChannelMode.None) {
            return (ABSENT_FLAG, UNSET_CHANNEL, NO_WARNING_CODE);
        }
        if (directive.mode == Types.ChannelMode.Transition) {
            if (directive.from == directive.to) return (ABSENT_FLAG, UNSET_CHANNEL, IDENTITY_TRANSITION_CODE);
            if (directive.from != baseline) return (ABSENT_FLAG, UNSET_CHANNEL, NO_WARNING_CODE);
        }
        if (directive.mode == Types.ChannelMode.AnyPrerelease && baseline == UNSET_CHANNEL) {
            return (ABSENT_FLAG, UNSET_CHANNEL, NO_WARNING_CODE);
        }
        bytes32 target = directive.mode == Types.ChannelMode.Inherit ? origin : directive.to;
        bool isTransition =
            directive.mode == Types.ChannelMode.Transition || directive.mode == Types.ChannelMode.AnyPrerelease;
        if (isPropagated && !isTransition && target == UNSET_CHANNEL && baseline != UNSET_CHANNEL) {
            return (ABSENT_FLAG, UNSET_CHANNEL, IMPLICIT_GRADUATION_CODE);
        }
        if (target == baseline) return (ABSENT_FLAG, UNSET_CHANNEL, UNCHANGED_CHANNEL_CODE);
        return (PRESENT_FLAG, target, NO_WARNING_CODE);
    }

    function resolveOriginChannel(
        Types.Unit calldata unit,
        History.State memory history,
        uint256 sources,
        uint256 count,
        Diagnostics.Buffer memory diagnostics,
        uint256 unitIndex
    ) private pure returns (bytes32 result) {
        bool isFound;
        for (uint256 i; i < count; ++i) {
            if ((sources & (PACKAGE_BIT << i)) == EMPTY_MASK) continue;
            bytes32 baseline = history.baselines[i].channel;
            (bool isProposed, bytes32 proposed,) = resolveProposal(unit.channel, baseline, UNSET_CHANNEL, ABSENT_FLAG);
            bytes32 value = isProposed ? proposed : baseline;
            if (!isFound) {
                result = value;
                isFound = PRESENT_FLAG;
                continue;
            }
            if (result != value) Diagnostics.add(diagnostics, PROPAGATED_CHANNEL_CONFLICT_CODE, i, unitIndex);
        }
    }
}
