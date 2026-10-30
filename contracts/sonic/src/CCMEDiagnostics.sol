// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.28;

import {CCMETypes as Types} from "./CCMETypes.sol";

library CCMEDiagnostics {
    uint256 private constant BUFFER_RESERVE_COUNT = 16;
    uint256 private constant CAPACITY_GROWTH_FACTOR = 2;
    uint256 private constant HEAP_BRANCH_COUNT = 2;
    uint256 private constant ROOT_INDEX = 0;
    uint256 private constant INDEX_OFFSET = 1;
    uint256 private constant PACKAGE_SHIFT_BITS = 32;
    uint256 private constant UNIT_SHIFT_BITS = 16;
    uint256 private constant UNIT_ORDER_LIMIT = 256;
    uint16 private constant PACKAGE_DIAGNOSTIC_INDEX = type(uint16).max;

    struct Buffer {
        Types.Diagnostic[] items;
        uint256 count;
    }

    function create(Types.Diagnostic[] memory initial) internal pure returns (Buffer memory buffer) {
        buffer.items = new Types.Diagnostic[](initial.length + BUFFER_RESERVE_COUNT);
        for (uint256 i; i < initial.length; ++i) {
            buffer.items[i] = initial[i];
        }
        buffer.count = initial.length;
    }

    function add(Buffer memory buffer, uint16 code, uint256 packageIndex, uint256 unitIndex) internal pure {
        if (buffer.count == buffer.items.length) {
            Types.Diagnostic[] memory expanded = new Types.Diagnostic[](buffer.items.length * CAPACITY_GROWTH_FACTOR);
            for (uint256 i; i < buffer.count; ++i) {
                expanded[i] = buffer.items[i];
            }
            buffer.items = expanded;
        }
        buffer.items[buffer.count++] = Types.Diagnostic(code, uint16(packageIndex), uint16(unitIndex));
    }

    function finish(Buffer memory buffer) internal pure returns (Types.Diagnostic[] memory result) {
        result = buffer.items;
        uint256 count = buffer.count;
        assembly ("memory-safe") { mstore(result, count) }
        for (uint256 i = count / HEAP_BRANCH_COUNT; i > ROOT_INDEX; --i) {
            sift(result, i - INDEX_OFFSET, count);
        }
        for (uint256 i = count; i > INDEX_OFFSET; --i) {
            (result[ROOT_INDEX], result[i - INDEX_OFFSET]) = (result[i - INDEX_OFFSET], result[ROOT_INDEX]);
            sift(result, ROOT_INDEX, i - INDEX_OFFSET);
        }
    }

    function sift(Types.Diagnostic[] memory items, uint256 position, uint256 length) private pure {
        while (position * HEAP_BRANCH_COUNT + INDEX_OFFSET < length) {
            uint256 child = position * HEAP_BRANCH_COUNT + INDEX_OFFSET;
            if (child + INDEX_OFFSET < length && orderKey(items[child]) < orderKey(items[child + INDEX_OFFSET])) {
                ++child;
            }
            if (orderKey(items[position]) >= orderKey(items[child])) return;
            (items[position], items[child]) = (items[child], items[position]);
            position = child;
        }
    }

    function orderKey(Types.Diagnostic memory diagnostic) private pure returns (uint256) {
        uint256 position = diagnostic.unitIndex == PACKAGE_DIAGNOSTIC_INDEX
            ? PACKAGE_DIAGNOSTIC_INDEX
            : UNIT_ORDER_LIMIT - diagnostic.unitIndex;
        return
            (uint256(diagnostic.packageIndex) << PACKAGE_SHIFT_BITS) | (position << UNIT_SHIFT_BITS) | diagnostic.code;
    }
}
