// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.28;

import {CCMETypes as Types} from "./CCMETypes.sol";

library CCMEVersion {
    int256 private constant COMPARISON_LESS_ORDER = -1;
    int256 private constant COMPARISON_EQUAL_ORDER = 0;
    int256 private constant COMPARISON_GREATER_ORDER = 1;
    uint64 private constant RESET_CORE_COMPONENT_VALUE = 0;
    uint64 private constant UNSTABLE_MAJOR_VALUE = 0;
    uint64 private constant STABLE_VERSION_COUNTER_VALUE = 0;
    uint64 private constant VERSION_INCREMENT_AMOUNT = 1;
    uint256 private constant MAXIMUM_PIN_MAJOR_JUMP_COUNT = 1;
    uint256 private constant CHANNEL_LENGTH_BYTES = 32;
    uint256 private constant CHANNEL_FIRST_CHARACTER_INDEX = 0;
    uint256 private constant INVALID_CHANNEL_DEFAULT_INDEX = 0;
    uint256 private constant VERSION_DIRECTIVE_DEFAULT_UNIT_INDEX = 0;
    bytes32 private constant STABLE_CHANNEL_VALUE = bytes32(0);
    bytes32 private constant RESERVED_STABLE_CHANNEL_NAME = "stable";
    bytes32 private constant RESERVED_LATEST_CHANNEL_NAME = "latest";
    bytes32 private constant RESERVED_INHERIT_CHANNEL_NAME = "inherit";
    bytes32 private constant RESERVED_NONE_CHANNEL_NAME = "none";
    bytes1 private constant ASCII_LOWERCASE_FIRST_BYTE = "a";
    bytes1 private constant ASCII_LOWERCASE_LAST_BYTE = "z";
    bytes1 private constant ASCII_DIGIT_FIRST_BYTE = "0";
    bytes1 private constant ASCII_DIGIT_LAST_BYTE = "9";
    bytes1 private constant ASCII_HYPHEN_BYTE = "-";
    bytes1 private constant CHANNEL_PADDING_BYTE = bytes1(0);
    bool private constant CHANNEL_PADDING_PRESENT_FLAG = true;
    bool private constant CHANNEL_ENTRY_PATCH_APPLIED_FLAG = true;
    bool private constant CHANNEL_ENTRY_PATCH_ABSENT_FLAG = false;
    uint16 private constant GRADUATION_REGRESSION_ERROR_CODE = 185;
    uint16 private constant VERSION_REGRESSION_ERROR_CODE = 195;
    uint16 private constant PIN_NOT_GREATER_ERROR_CODE = 153;
    uint16 private constant PIN_BELOW_CORE_ERROR_CODE = 156;
    uint16 private constant PIN_MAJOR_JUMP_ERROR_CODE = 157;
    bytes32 private constant STABLE_COUNTER_FIELD_NAME = "stable-counter";
    bytes32 private constant RESERVED_CHANNEL_FIELD_NAME = "reserved-channel";
    bytes32 private constant CHANNEL_START_FIELD_NAME = "channel-start";
    bytes32 private constant CHANNEL_PADDING_FIELD_NAME = "channel-padding";
    bytes32 private constant CHANNEL_CHARACTER_FIELD_NAME = "channel-character";
    bytes32 private constant INITIAL_CHANNEL_FIELD_NAME = "initial-channel";
    bytes32 private constant STABLE_BASELINE_FIELD_NAME = "stable-baseline";
    bytes32 private constant VERSION_OVERFLOW_FIELD_NAME = "version-overflow";

    struct Input {
        Types.Version baseline;
        Types.Version stableBaseline;
        Types.Version initialVersion;
        bool hasBaseline;
        bool hasStableBaseline;
        bool preserveMajorZero;
        Types.Bump bump;
        bytes32 channel;
        bool hasPin;
        Types.Version pin;
        uint256 packageIndex;
    }

    function compareCore(Types.Version memory left, Types.Version memory right) internal pure returns (int256) {
        if (left.major != right.major) {
            return left.major < right.major ? COMPARISON_LESS_ORDER : COMPARISON_GREATER_ORDER;
        }
        if (left.minor != right.minor) {
            return left.minor < right.minor ? COMPARISON_LESS_ORDER : COMPARISON_GREATER_ORDER;
        }
        if (left.patch != right.patch) {
            return left.patch < right.patch ? COMPARISON_LESS_ORDER : COMPARISON_GREATER_ORDER;
        }
        return COMPARISON_EQUAL_ORDER;
    }

    function compare(Types.Version memory left, Types.Version memory right) internal pure returns (int256) {
        int256 coreOrder = compareCore(left, right);
        if (coreOrder != COMPARISON_EQUAL_ORDER) return coreOrder;
        if (left.channel == STABLE_CHANNEL_VALUE && right.channel == STABLE_CHANNEL_VALUE) {
            return COMPARISON_EQUAL_ORDER;
        }
        if (left.channel == STABLE_CHANNEL_VALUE) return COMPARISON_GREATER_ORDER;
        if (right.channel == STABLE_CHANNEL_VALUE) return COMPARISON_LESS_ORDER;
        if (left.channel != right.channel) {
            return left.channel < right.channel ? COMPARISON_LESS_ORDER : COMPARISON_GREATER_ORDER;
        }
        if (left.counter != right.counter) {
            return left.counter < right.counter ? COMPARISON_LESS_ORDER : COMPARISON_GREATER_ORDER;
        }
        return COMPARISON_EQUAL_ORDER;
    }

    function validate(Types.Version memory version) internal pure {
        validateChannel(version.channel);
        if (version.channel == STABLE_CHANNEL_VALUE && version.counter != STABLE_VERSION_COUNTER_VALUE) {
            revert Types.InvalidInput(STABLE_COUNTER_FIELD_NAME, version.counter);
        }
    }

    function validateChannel(bytes32 channel) internal pure {
        if (channel == STABLE_CHANNEL_VALUE) return;
        if (
            channel == RESERVED_STABLE_CHANNEL_NAME || channel == RESERVED_LATEST_CHANNEL_NAME
                || channel == RESERVED_INHERIT_CHANNEL_NAME || channel == RESERVED_NONE_CHANNEL_NAME
        ) revert Types.InvalidInput(RESERVED_CHANNEL_FIELD_NAME, INVALID_CHANNEL_DEFAULT_INDEX);
        if (
            channel[CHANNEL_FIRST_CHARACTER_INDEX] < ASCII_LOWERCASE_FIRST_BYTE
                || channel[CHANNEL_FIRST_CHARACTER_INDEX] > ASCII_LOWERCASE_LAST_BYTE
        ) {
            revert Types.InvalidInput(CHANNEL_START_FIELD_NAME, INVALID_CHANNEL_DEFAULT_INDEX);
        }
        bool hasPadding;
        for (uint256 i; i < CHANNEL_LENGTH_BYTES; ++i) {
            bytes1 character = channel[i];
            if (character == CHANNEL_PADDING_BYTE) {
                hasPadding = CHANNEL_PADDING_PRESENT_FLAG;
                continue;
            }
            if (hasPadding) revert Types.InvalidInput(CHANNEL_PADDING_FIELD_NAME, i);
            if (
                (character >= ASCII_LOWERCASE_FIRST_BYTE && character <= ASCII_LOWERCASE_LAST_BYTE)
                    || (character >= ASCII_DIGIT_FIRST_BYTE && character <= ASCII_DIGIT_LAST_BYTE)
                    || character == ASCII_HYPHEN_BYTE
            ) continue;
            revert Types.InvalidInput(CHANNEL_CHARACTER_FIELD_NAME, i);
        }
    }

    function compute(Input memory versionInput)
        internal
        pure
        returns (Types.Version memory next, bool hasChannelEntryPatch)
    {
        _validateVersionInput(versionInput);
        Types.Version memory target = _applyBump(versionInput, versionInput.bump);

        // Pins are checked against the core before ordinary regression checks: a pin can repair a raised train.
        if (versionInput.hasPin) return (_applyPin(versionInput, target), CHANNEL_ENTRY_PATCH_ABSENT_FLAG);

        next = _assignChannel(versionInput, target);
        if (
            versionInput.hasBaseline && versionInput.bump == Types.Bump.None
                && versionInput.channel != versionInput.baseline.channel
                && compare(next, versionInput.baseline) <= COMPARISON_EQUAL_ORDER
        ) {
            next = _assignChannel(versionInput, _applyBump(versionInput, Types.Bump.Patch));
            hasChannelEntryPatch = CHANNEL_ENTRY_PATCH_APPLIED_FLAG;
        }
        if (
            versionInput.hasBaseline && versionInput.baseline.channel != STABLE_CHANNEL_VALUE
                && versionInput.channel == STABLE_CHANNEL_VALUE
                && compareCore(next, versionInput.baseline) < COMPARISON_EQUAL_ORDER
        ) {
            revert Types.CCMEError(
                GRADUATION_REGRESSION_ERROR_CODE, versionInput.packageIndex, VERSION_DIRECTIVE_DEFAULT_UNIT_INDEX
            );
        }
        if (versionInput.hasBaseline && compare(next, versionInput.baseline) <= COMPARISON_EQUAL_ORDER) {
            revert Types.CCMEError(
                VERSION_REGRESSION_ERROR_CODE, versionInput.packageIndex, VERSION_DIRECTIVE_DEFAULT_UNIT_INDEX
            );
        }
    }

    function _validateVersionInput(Input memory versionInput) private pure {
        validate(versionInput.initialVersion);
        validateChannel(versionInput.channel);
        if (versionInput.initialVersion.channel != STABLE_CHANNEL_VALUE) {
            revert Types.InvalidInput(INITIAL_CHANNEL_FIELD_NAME, versionInput.packageIndex);
        }
        if (versionInput.hasBaseline) validate(versionInput.baseline);
        if (versionInput.hasStableBaseline) {
            validate(versionInput.stableBaseline);
            if (
                !versionInput.hasBaseline || versionInput.stableBaseline.channel != STABLE_CHANNEL_VALUE
                    || compare(versionInput.stableBaseline, versionInput.baseline) > COMPARISON_EQUAL_ORDER
            ) revert Types.InvalidInput(STABLE_BASELINE_FIELD_NAME, versionInput.packageIndex);
        }
        if (versionInput.hasPin) validate(versionInput.pin);
    }

    function _applyBump(Input memory versionInput, Types.Bump requested) private pure returns (Types.Version memory) {
        if (!versionInput.hasStableBaseline) return _stripToCore(versionInput.initialVersion);
        Types.Version memory base = versionInput.stableBaseline;
        Types.Bump applied = requested;
        if (versionInput.preserveMajorZero && base.major == UNSTABLE_MAJOR_VALUE) {
            if (requested == Types.Bump.Major) applied = Types.Bump.Minor;
            if (requested == Types.Bump.Minor) applied = Types.Bump.Patch;
        }
        if (applied == Types.Bump.Major) {
            return Types.Version(
                _increment(base.major, versionInput.packageIndex),
                RESET_CORE_COMPONENT_VALUE,
                RESET_CORE_COMPONENT_VALUE,
                STABLE_CHANNEL_VALUE,
                STABLE_VERSION_COUNTER_VALUE
            );
        }
        if (applied == Types.Bump.Minor) {
            return Types.Version(
                base.major,
                _increment(base.minor, versionInput.packageIndex),
                RESET_CORE_COMPONENT_VALUE,
                STABLE_CHANNEL_VALUE,
                STABLE_VERSION_COUNTER_VALUE
            );
        }
        if (applied == Types.Bump.Patch) {
            return Types.Version(
                base.major,
                base.minor,
                _increment(base.patch, versionInput.packageIndex),
                STABLE_CHANNEL_VALUE,
                STABLE_VERSION_COUNTER_VALUE
            );
        }
        return _stripToCore(base);
    }

    function _assignChannel(Input memory versionInput, Types.Version memory target)
        private
        pure
        returns (Types.Version memory)
    {
        if (versionInput.channel == STABLE_CHANNEL_VALUE) return target;
        uint64 counter;
        if (
            versionInput.hasBaseline && versionInput.baseline.channel == versionInput.channel
                && compareCore(versionInput.baseline, target) == COMPARISON_EQUAL_ORDER
        ) {
            counter = _increment(versionInput.baseline.counter, versionInput.packageIndex);
        }
        return Types.Version(target.major, target.minor, target.patch, versionInput.channel, counter);
    }

    function _applyPin(Input memory versionInput, Types.Version memory target)
        private
        pure
        returns (Types.Version memory)
    {
        if (compare(versionInput.pin, versionInput.baseline) <= COMPARISON_EQUAL_ORDER) {
            revert Types.CCMEError(
                PIN_NOT_GREATER_ERROR_CODE, versionInput.packageIndex, VERSION_DIRECTIVE_DEFAULT_UNIT_INDEX
            );
        }
        if (compareCore(versionInput.pin, target) < COMPARISON_EQUAL_ORDER) {
            revert Types.CCMEError(
                PIN_BELOW_CORE_ERROR_CODE, versionInput.packageIndex, VERSION_DIRECTIVE_DEFAULT_UNIT_INDEX
            );
        }
        if (uint256(versionInput.pin.major) > uint256(target.major) + MAXIMUM_PIN_MAJOR_JUMP_COUNT) {
            revert Types.CCMEError(
                PIN_MAJOR_JUMP_ERROR_CODE, versionInput.packageIndex, VERSION_DIRECTIVE_DEFAULT_UNIT_INDEX
            );
        }
        return versionInput.pin;
    }

    function _stripToCore(Types.Version memory version) private pure returns (Types.Version memory) {
        return
            Types.Version(
                version.major, version.minor, version.patch, STABLE_CHANNEL_VALUE, STABLE_VERSION_COUNTER_VALUE
            );
    }

    function _increment(uint64 value, uint256 packageIndex) private pure returns (uint64) {
        if (value == type(uint64).max) revert Types.InvalidInput(VERSION_OVERFLOW_FIELD_NAME, packageIndex);
        return value + VERSION_INCREMENT_AMOUNT;
    }
}
