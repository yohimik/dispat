// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.28;

import {CCMETypes as Types} from "./CCMETypes.sol";

library CCMEGraph {
    uint256 private constant EMPTY_MASK = 0;
    uint256 private constant PACKAGE_BIT = 1;
    uint256 private constant INDEX_OFFSET = 1;
    uint256 private constant EMPTY_COUNT = 0;
    uint256 private constant FIRST_INDEX = 0;
    uint256 private constant MAXIMUM_PACKAGE_NAME_BYTES = 214;
    uint16 private constant FIRST_DISTANCE = 1;
    uint16 private constant NO_DISTANCE = 0;
    bool private constant FOUND_FLAG = true;
    bytes32 private constant PACKAGES_FIELD = "packages";
    bytes32 private constant EDGES_FIELD = "edges";
    bytes32 private constant PACKAGE_NAME_FIELD = "packageName";
    bytes32 private constant PACKAGE_ORDER_FIELD = "packageOrder";
    bytes32 private constant EDGE_FIELD = "edge";

    error DependencyCycle(uint256 remainingPackages);

    struct State {
        uint16[] publishOrder;
        uint16[] distances;
    }

    function analyze(Types.Snapshot calldata snapshot) internal pure returns (State memory state) {
        uint256 count = snapshot.packages.length;
        if (count == EMPTY_COUNT || count > Types.MAX_PACKAGES) revert Types.InvalidInput(PACKAGES_FIELD, count);
        if (snapshot.edges.length > Types.MAX_EDGES) revert Types.InvalidInput(EDGES_FIELD, snapshot.edges.length);
        uint256[] memory unionParents = new uint256[](count);
        uint256[] memory publishParents = new uint256[](count);
        uint256[] memory dependents = new uint256[](count);
        for (uint256 i; i < count; ++i) {
            bytes memory name = bytes(snapshot.packages[i].name);
            if (name.length == EMPTY_COUNT || name.length > MAXIMUM_PACKAGE_NAME_BYTES) {
                revert Types.InvalidInput(PACKAGE_NAME_FIELD, i);
            }
            if (i != FIRST_INDEX && !isLess(bytes(snapshot.packages[i - INDEX_OFFSET].name), name)) {
                revert Types.InvalidInput(PACKAGE_ORDER_FIELD, i);
            }
        }
        for (uint256 i; i < snapshot.edges.length; ++i) {
            Types.Edge calldata edge = snapshot.edges[i];
            if (edge.provider >= count || edge.consumer >= count || (!edge.propagates && !edge.ordersPublish)) {
                revert Types.InvalidInput(EDGE_FIELD, i);
            }
            uint256 bit = PACKAGE_BIT << edge.provider;
            unionParents[edge.consumer] |= bit;
            if (edge.ordersPublish) publishParents[edge.consumer] |= bit;
            if (edge.propagates) dependents[edge.provider] |= PACKAGE_BIT << edge.consumer;
        }
        order(unionParents);
        state.publishOrder = order(publishParents);
        state.distances = new uint16[](count * count);
        for (uint256 i; i < count; ++i) {
            uint256 seen = PACKAGE_BIT << i;
            uint256 frontier = seen;
            for (uint16 depth = FIRST_DISTANCE; frontier != EMPTY_MASK && depth < count; ++depth) {
                uint256 next;
                for (uint256 j; j < count; ++j) {
                    if ((frontier & (PACKAGE_BIT << j)) != EMPTY_MASK) next |= dependents[j];
                }
                next &= ~seen;
                for (uint256 j; j < count; ++j) {
                    if ((next & (PACKAGE_BIT << j)) != EMPTY_MASK) state.distances[i * count + j] = depth;
                }
                seen |= next;
                frontier = next;
            }
        }
    }

    function order(uint256[] memory parents) private pure returns (uint16[] memory result) {
        uint256 count = parents.length;
        result = new uint16[](count);
        uint256 remaining = (PACKAGE_BIT << count) - INDEX_OFFSET;
        for (uint256 i; i < count; ++i) {
            bool isFound;
            for (uint256 j; j < count; ++j) {
                uint256 bit = PACKAGE_BIT << j;
                if ((remaining & bit) == EMPTY_MASK || (parents[j] & remaining) != EMPTY_MASK) continue;
                result[i] = uint16(j);
                remaining &= ~bit;
                isFound = FOUND_FLAG;
                break;
            }
            if (!isFound) revert DependencyCycle(remaining);
        }
    }

    function findReachingSources(State memory state, uint256 count, uint256 sources, uint256 target, uint256 depth)
        internal
        pure
        returns (uint256 result)
    {
        if ((sources & (PACKAGE_BIT << target)) != EMPTY_MASK || depth == NO_DISTANCE) return EMPTY_MASK;
        for (uint256 i; i < count; ++i) {
            uint16 distance = state.distances[i * count + target];
            if ((sources & (PACKAGE_BIT << i)) != EMPTY_MASK && distance != NO_DISTANCE && distance <= depth) {
                result |= PACKAGE_BIT << i;
            }
        }
    }

    function isLess(bytes memory left, bytes memory right) private pure returns (bool) {
        uint256 length = left.length < right.length ? left.length : right.length;
        for (uint256 i; i < length; ++i) {
            if (left[i] != right[i]) return uint8(left[i]) < uint8(right[i]);
        }
        return left.length < right.length;
    }
}
