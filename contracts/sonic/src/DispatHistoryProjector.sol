// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.28;

import {CCMETypes as Types} from "./CCMETypes.sol";
import {CCMEHistory as History} from "./CCMEHistory.sol";
import {CCMEGraph as Graph} from "./CCMEGraph.sol";

/// @notice Validates one repository without manufacturing ancestry between repositories.
contract DispatHistoryProjector {
    uint256 private constant EMPTY_SET_MASK = 0;
    uint256 private constant MEMBER_BIT_MASK = 1;
    uint16 private constant CORRECTION_NOOP_WARNING_CODE = 209;
    bytes32 private constant FOREIGN_WILDCARD_FIELD_ID = "correction.foreignWildcard";

    function analyze(Types.Snapshot calldata snapshot) external pure returns (History.State memory) {
        Graph.analyze(snapshot);
        return History.analyze(snapshot);
    }

    function analyzeGraph(Types.Snapshot calldata snapshot) external pure returns (Graph.State memory) {
        return Graph.analyze(snapshot);
    }

    /// @dev Source-local ancestry cannot prove the causal reach of a wildcard
    /// against a foreign pending contribution. Such inputs need a richer adapter.
    function projectCorrections(
        Types.Snapshot calldata snapshot,
        History.State calldata history,
        Graph.State calldata graph,
        uint16[] calldata unitRepositories,
        uint256[] calldata sources
    ) external pure returns (History.State memory projectedHistory) {
        projectedHistory = history;
        Graph.State memory projectedGraph = graph;
        uint256[] memory changedTargets = new uint256[](snapshot.units.length);
        for (uint256 i; i < snapshot.units.length; ++i) {
            if (!snapshot.units[i].correctionWildcard || history.scopes[i] == EMPTY_SET_MASK) continue;
            for (uint256 j; j < snapshot.units.length; ++j) {
                uint256 suppressed = History.findPendingPropagation(
                    snapshot, projectedHistory, projectedGraph, j, history.scopes[i], sources[j]
                );
                if (suppressed == EMPTY_SET_MASK) continue;
                if (unitRepositories[i] != unitRepositories[j]) {
                    revert Types.InvalidInput(FOREIGN_WILDCARD_FIELD_ID, i);
                }
                if (
                    snapshot.units[j].isCancel || snapshot.units[j].isRelease
                        || snapshot.units[i].commitIndex == snapshot.units[j].commitIndex
                        || history.ancestors[snapshot.units[i].commitIndex]
                                & (MEMBER_BIT_MASK << snapshot.units[j].commitIndex) == EMPTY_SET_MASK
                ) continue;
                projectedHistory.suppressedTargets[j] |= suppressed;
                changedTargets[i] |= suppressed;
            }
        }
        uint256 retainedCount;
        for (uint256 i; i < history.diagnostics.length; ++i) {
            Types.Diagnostic calldata diagnostic = history.diagnostics[i];
            if (
                diagnostic.code == CORRECTION_NOOP_WARNING_CODE
                    && changedTargets[diagnostic.unitIndex] & (MEMBER_BIT_MASK << diagnostic.packageIndex)
                        != EMPTY_SET_MASK
            ) continue;
            projectedHistory.diagnostics[retainedCount++] = diagnostic;
        }
        Types.Diagnostic[] memory diagnostics = new Types.Diagnostic[](retainedCount);
        for (uint256 i; i < retainedCount; ++i) {
            diagnostics[i] = projectedHistory.diagnostics[i];
        }
        projectedHistory.diagnostics = diagnostics;
    }
}
