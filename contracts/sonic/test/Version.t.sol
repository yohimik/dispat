// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.28;

import {CCMETypes as Types} from "../src/CCMETypes.sol";
import {CCMEVersion as Versioning} from "../src/CCMEVersion.sol";

contract VersionHarness {
    function compute(Versioning.Input memory versionInput) external pure returns (Types.Version memory, bool) {
        return Versioning.compute(versionInput);
    }

    function validate(Types.Version memory version) external pure {
        Versioning.validate(version);
    }
}

contract VersionTest {
    function testStableBumpsAndInitialRelease() public pure {
        Versioning.Input memory versionInput = _createStableInput(1, 2, 3);
        versionInput.bump = Types.Bump.Minor;
        (Types.Version memory next,) = Versioning.compute(versionInput);
        _assertVersion(next, _createVersion(1, 3, 0, bytes32(0), 0));

        versionInput.hasBaseline = false;
        versionInput.hasStableBaseline = false;
        versionInput.bump = Types.Bump.Major;
        (next,) = Versioning.compute(versionInput);
        _assertVersion(next, _createVersion(0, 1, 0, bytes32(0), 0));
    }

    function testMajorZeroPolicy() public pure {
        Versioning.Input memory versionInput = _createStableInput(0, 4, 1);
        versionInput.preserveMajorZero = true;
        versionInput.bump = Types.Bump.Major;
        (Types.Version memory next,) = Versioning.compute(versionInput);
        _assertVersion(next, _createVersion(0, 5, 0, bytes32(0), 0));
        versionInput.bump = Types.Bump.Minor;
        (next,) = Versioning.compute(versionInput);
        _assertVersion(next, _createVersion(0, 4, 2, bytes32(0), 0));
        versionInput.preserveMajorZero = false;
        versionInput.bump = Types.Bump.Major;
        (next,) = Versioning.compute(versionInput);
        _assertVersion(next, _createVersion(1, 0, 0, bytes32(0), 0));
    }

    function testTrainCounterAndCoreReset() public pure {
        Versioning.Input memory versionInput = _createStableInput(1, 2, 3);
        versionInput.baseline = _createVersion(1, 3, 0, "beta", 9);
        versionInput.channel = "beta";
        versionInput.bump = Types.Bump.Minor;
        (Types.Version memory next,) = Versioning.compute(versionInput);
        _assertVersion(next, _createVersion(1, 3, 0, "beta", 10));
        versionInput.bump = Types.Bump.Major;
        (next,) = Versioning.compute(versionInput);
        _assertVersion(next, _createVersion(2, 0, 0, "beta", 0));
    }

    function testTrainWithoutStableBaselineUsesInitialCore() public pure {
        Versioning.Input memory versionInput = _createStableInput(0, 0, 0);
        versionInput.hasStableBaseline = false;
        versionInput.baseline = _createVersion(0, 1, 0, "beta", 2);
        versionInput.channel = "beta";
        versionInput.bump = Types.Bump.Major;
        (Types.Version memory next,) = Versioning.compute(versionInput);
        _assertVersion(next, _createVersion(0, 1, 0, "beta", 3));
    }

    function testSwitchTrainAndGraduate() public pure {
        Versioning.Input memory versionInput = _createStableInput(1, 2, 3);
        versionInput.baseline = _createVersion(1, 3, 0, "beta", 9);
        versionInput.bump = Types.Bump.Minor;
        versionInput.channel = "rc";
        (Types.Version memory next,) = Versioning.compute(versionInput);
        _assertVersion(next, _createVersion(1, 3, 0, "rc", 0));
        versionInput.channel = bytes32(0);
        (next,) = Versioning.compute(versionInput);
        _assertVersion(next, _createVersion(1, 3, 0, bytes32(0), 0));
    }

    function testChannelOnlyEntryPatchAndGraduation() public pure {
        Versioning.Input memory versionInput = _createStableInput(2, 0, 0);
        versionInput.channel = "beta";
        (Types.Version memory next, bool hasChannelEntryPatch) = Versioning.compute(versionInput);
        require(hasChannelEntryPatch, "entry patch absent");
        _assertVersion(next, _createVersion(2, 0, 1, "beta", 0));
        versionInput.baseline = next;
        versionInput.channel = bytes32(0);
        (next, hasChannelEntryPatch) = Versioning.compute(versionInput);
        require(hasChannelEntryPatch, "graduation patch absent");
        _assertVersion(next, _createVersion(2, 0, 1, bytes32(0), 0));
    }

    function testPinPrereleaseComparedByCore() public pure {
        Versioning.Input memory versionInput = _createStableInput(1, 2, 3);
        versionInput.bump = Types.Bump.Minor;
        versionInput.hasPin = true;
        versionInput.pin = _createVersion(1, 3, 0, "rc", 0);
        (Types.Version memory next, bool hasChannelEntryPatch) = Versioning.compute(versionInput);
        _assertVersion(next, versionInput.pin);
        require(!hasChannelEntryPatch, "pin hasChannelEntryPatch");
    }

    function testPinCanRepairRaisedTrainGraduation() public pure {
        Versioning.Input memory versionInput = _createStableInput(1, 2, 3);
        versionInput.baseline = _createVersion(2, 0, 0, "rc", 3);
        versionInput.hasPin = true;
        versionInput.pin = _createVersion(2, 0, 0, bytes32(0), 0);
        (Types.Version memory next,) = Versioning.compute(versionInput);
        _assertVersion(next, versionInput.pin);
    }

    function testPinAndRegressionErrors() public {
        VersionHarness harness = new VersionHarness();
        Versioning.Input memory versionInput = _createStableInput(1, 2, 3);
        versionInput.hasPin = true;
        versionInput.pin = versionInput.baseline;
        _assertError(harness, versionInput, 153);
        versionInput.bump = Types.Bump.Major;
        versionInput.pin = _createVersion(1, 3, 0, bytes32(0), 0);
        _assertError(harness, versionInput, 156);
        versionInput.bump = Types.Bump.Minor;
        versionInput.pin = _createVersion(5, 0, 0, bytes32(0), 0);
        _assertError(harness, versionInput, 157);

        versionInput.hasPin = false;
        versionInput.baseline = _createVersion(3, 0, 0, "beta", 0);
        versionInput.channel = bytes32(0);
        _assertError(harness, versionInput, 185);
        versionInput.channel = "beta";
        _assertError(harness, versionInput, 195);
    }

    function testChannelEntryPatchCannotHideRealRegression() public {
        VersionHarness harness = new VersionHarness();
        Versioning.Input memory versionInput = _createStableInput(1, 2, 3);
        versionInput.baseline = _createVersion(2, 0, 0, "beta", 0);
        versionInput.channel = "rc";
        _assertError(harness, versionInput, 195);
        versionInput.channel = bytes32(0);
        _assertError(harness, versionInput, 185);
    }

    function testSemverOrdering() public pure {
        require(
            Versioning.compare(_createVersion(1, 2, 0, "beta", 10), _createVersion(1, 2, 0, "beta", 9)) > 0,
            "numeric counter"
        );
        require(
            Versioning.compare(_createVersion(1, 2, 0, "beta", 99), _createVersion(1, 2, 0, "rc", 0)) < 0,
            "channel order"
        );
        require(
            Versioning.compare(_createVersion(1, 2, 0, "rc", 99), _createVersion(1, 2, 0, bytes32(0), 0)) < 0,
            "stable order"
        );
        require(
            Versioning.compare(_createVersion(1, 2, 0, "a", 0), _createVersion(1, 2, 0, "aa", 0)) < 0, "channel prefix"
        );
    }

    function testRejectMalformedChannelsAndStableCounter() public {
        VersionHarness harness = new VersionHarness();
        bytes32[7] memory invalid = [
            bytes32("stable"),
            bytes32("latest"),
            bytes32("none"),
            bytes32("inherit"),
            bytes32("Beta"),
            bytes32("1beta"),
            bytes32("beta.rc")
        ];
        for (uint256 i; i < invalid.length; ++i) {
            (bool isSuccessful,) =
                address(harness).call(abi.encodeCall(harness.validate, (_createVersion(1, 0, 0, invalid[i], 0))));
            require(!isSuccessful, "invalid channel isAccepted");
        }
        bytes32 interiorNull = bytes32(uint256(bytes32("beta")) | uint256(1));
        (bool isAccepted,) =
            address(harness).call(abi.encodeCall(harness.validate, (_createVersion(1, 0, 0, interiorNull, 0))));
        require(!isAccepted, "interior null isAccepted");
        (isAccepted,) =
            address(harness).call(abi.encodeCall(harness.validate, (_createVersion(1, 0, 0, bytes32(0), 1))));
        require(!isAccepted, "stable counter isAccepted");
        Versioning.validate(_createVersion(1, 0, 0, "abcdefghijklmnopqrstuvwxyz012345", 0));
    }

    function testOverflowDoesNotWrapVersionsOrCounters() public {
        VersionHarness harness = new VersionHarness();
        Versioning.Input memory versionInput = _createStableInput(1, 2, type(uint64).max);
        versionInput.bump = Types.Bump.Patch;
        (bool isSuccessful,) = address(harness).call(abi.encodeCall(harness.compute, (versionInput)));
        require(!isSuccessful, "patch overflow isAccepted");
        versionInput = _createStableInput(1, 2, 3);
        versionInput.baseline = _createVersion(1, 3, 0, "beta", type(uint64).max);
        versionInput.channel = "beta";
        versionInput.bump = Types.Bump.Minor;
        (isSuccessful,) = address(harness).call(abi.encodeCall(harness.compute, (versionInput)));
        require(!isSuccessful, "counter overflow isAccepted");
    }

    function testFuzzComparisonIsAntisymmetric(Types.Version memory left, Types.Version memory right) public pure {
        _normalizeChannel(left);
        _normalizeChannel(right);
        require(Versioning.compare(left, right) == -Versioning.compare(right, left), "comparison symmetry");
        require(Versioning.compare(left, left) == 0, "comparison identity");
    }

    function testFuzzComparisonIsTransitive(
        Types.Version memory left,
        Types.Version memory right,
        Types.Version memory last
    ) public pure {
        _normalizeChannel(left);
        _normalizeChannel(right);
        _normalizeChannel(last);
        if (Versioning.compare(left, right) <= 0 && Versioning.compare(right, last) <= 0) {
            require(Versioning.compare(left, last) <= 0, "comparison transitivity");
        }
    }

    function testFuzzPatchAdvancesExactlyOnce(uint64 major, uint64 minor, uint64 patch, bool preserveMajorZero)
        public
        pure
    {
        if (patch == type(uint64).max) return;
        Versioning.Input memory versionInput = _createStableInput(major, minor, patch);
        versionInput.preserveMajorZero = preserveMajorZero;
        versionInput.bump = Types.Bump.Patch;
        (Types.Version memory next, bool hasChannelEntryPatch) = Versioning.compute(versionInput);
        require(Versioning.compare(next, versionInput.baseline) > 0, "patch must advance");
        require(next.major == major && next.minor == minor && next.patch == patch + 1, "patch changed other components");
        require(
            next.channel == bytes32(0) && next.counter == 0 && !hasChannelEntryPatch, "stable patch changed channel"
        );
    }

    function testFuzzTrainCounterPreservesCore(uint64 major, uint64 minor, uint64 patch, uint64 counter) public pure {
        if (patch == type(uint64).max || counter == type(uint64).max) return;
        Versioning.Input memory versionInput = _createStableInput(major, minor, patch);
        versionInput.baseline = _createVersion(major, minor, patch + 1, "beta", counter);
        versionInput.channel = "beta";
        versionInput.bump = Types.Bump.Patch;
        (Types.Version memory next, bool hasChannelEntryPatch) = Versioning.compute(versionInput);
        require(Versioning.compare(next, versionInput.baseline) > 0, "counter must advance");
        require(
            Versioning.compareCore(next, versionInput.baseline) == 0 && next.counter == counter + 1,
            "train changed core"
        );
        require(next.channel == bytes32("beta") && !hasChannelEntryPatch, "train changed channel");
    }

    function testFuzzInitialVersionIgnoresBump(uint64 major, uint64 minor, uint64 patch, uint8 requested) public pure {
        Versioning.Input memory versionInput;
        versionInput.initialVersion = _createVersion(major, minor, patch, bytes32(0), 0);
        versionInput.bump = Types.Bump(requested % 4);
        (Types.Version memory next,) = Versioning.compute(versionInput);
        _assertVersion(next, versionInput.initialVersion);
    }

    function _normalizeChannel(Types.Version memory version) private pure {
        uint256 choice = uint256(version.channel) % 4;
        if (choice == 0) {
            version.channel = bytes32(0);
            version.counter = 0;
            return;
        }
        if (choice == 1) {
            version.channel = "alpha";
            return;
        }
        if (choice == 2) {
            version.channel = "beta";
            return;
        }
        version.channel = "rc";
    }

    function _assertError(VersionHarness harness, Versioning.Input memory versionInput, uint16 code) private {
        (bool isSuccessful, bytes memory reason) =
            address(harness).call(abi.encodeCall(harness.compute, (versionInput)));
        require(!isSuccessful, "expected error");
        require(
            keccak256(reason)
                == keccak256(
                    abi.encodeWithSelector(Types.CCMEError.selector, code, versionInput.packageIndex, uint256(0))
                ),
            "wrong error"
        );
    }

    function _createStableInput(uint64 major, uint64 minor, uint64 patch)
        private
        pure
        returns (Versioning.Input memory versionInput)
    {
        versionInput.baseline = _createVersion(major, minor, patch, bytes32(0), 0);
        versionInput.stableBaseline = versionInput.baseline;
        versionInput.initialVersion = _createVersion(0, 1, 0, bytes32(0), 0);
        versionInput.hasBaseline = true;
        versionInput.hasStableBaseline = true;
    }

    function _createVersion(uint64 major, uint64 minor, uint64 patch, bytes32 channel, uint64 counter)
        private
        pure
        returns (Types.Version memory)
    {
        return Types.Version(major, minor, patch, channel, counter);
    }

    function _assertVersion(Types.Version memory actual, Types.Version memory expected) private pure {
        require(keccak256(abi.encode(actual)) == keccak256(abi.encode(expected)), "version mismatch");
    }
}
