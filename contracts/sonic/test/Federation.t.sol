// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.28;

import {CCMETypes as Types} from "../src/CCMETypes.sol";
import {CCMEHistory as History} from "../src/CCMEHistory.sol";
import {DispatPlanner} from "../src/DispatPlanner.sol";
import {DispatFederation as Federation} from "../src/DispatFederation.sol";

contract FederationTest {
    DispatPlanner private planner = new DispatPlanner();
    Federation private federation = new Federation(planner);

    function _repository(bytes32 identity, string memory name, Types.Bump bump)
        private
        pure
        returns (Federation.Repository memory repository)
    {
        repository.identity = identity;
        repository.snapshot.packages = new Types.Package[](1);
        repository.snapshot.packages[0] = Types.Package(name, Types.Version(0, 1, 0, 0, 0), true);
        repository.snapshot.commits = new Types.Commit[](2);
        // Identical object IDs across repositories deliberately do not create ancestry.
        repository.snapshot.commits[0] = Types.Commit(bytes32(uint256(1)), 0);
        repository.snapshot.commits[1] = Types.Commit(bytes32(uint256(2)), 1);
        repository.snapshot.tags = new Types.Tag[](1);
        repository.snapshot.tags[0] = Types.Tag(0, 0, Types.Version(1, 0, 0, 0, 0));
        repository.snapshot.edges = new Types.Edge[](0);
        uint256 unitCount = bump == Types.Bump.None ? 0 : 1;
        repository.snapshot.units = new Types.Unit[](unitCount);
        repository.bumpTargets = new uint256[](unitCount);
        repository.channelTargets = new uint256[](unitCount);
        if (unitCount != 0) {
            repository.snapshot.units[0].commitIndex = 1;
            repository.snapshot.units[0].unitIndex = 1;
            repository.snapshot.units[0].scope = 1;
            repository.snapshot.units[0].bump = bump;
        }
    }

    function _input(uint256 count) private pure returns (Federation.Input memory input) {
        input.mode = Federation.Mode.Specified;
        input.specifiedRepositories = (uint256(1) << count) - 1;
        input.repositories = new Federation.Repository[](count);
        input.repositories[0] = _repository(bytes32("alpha"), "a", Types.Bump.Minor);
        if (count > 1) input.repositories[1] = _repository(bytes32("beta"), "b", Types.Bump.Minor);
        if (count > 2) input.repositories[2] = _repository(bytes32("gamma"), "c", Types.Bump.Minor);
        input.crossEdges = new Federation.CrossEdge[](0);
        input.boundaries = new Federation.Boundary[](0);
        input.observations = new Federation.Observation[](0);
    }

    function _boundary(uint16 consumer, uint16 source, uint16 sourceCommit)
        private
        pure
        returns (Federation.Boundary memory)
    {
        return Federation.Boundary(consumer, 0, Types.Version(1, 0, 0, 0, 0), source, sourceCommit);
    }

    function _catchUp() private pure returns (Federation.Input memory input) {
        input = _input(2);
        input.repositories[1] = _repository(bytes32("beta"), "b", Types.Bump.None);
        input.repositories[0].snapshot.units[0].propagate = Types.Bump.Patch;
        input.repositories[0].snapshot.units[0].depth = 1;
        input.repositories[0].bumpTargets[0] = 2;
        input.repositories[0].snapshot.tags = new Types.Tag[](2);
        input.repositories[0].snapshot.tags[0] = Types.Tag(0, 0, Types.Version(1, 0, 0, 0, 0));
        input.repositories[0].snapshot.tags[1] = Types.Tag(0, 1, Types.Version(1, 1, 0, 0, 0));
        input.crossEdges = new Federation.CrossEdge[](1);
        input.crossEdges[0] = Federation.CrossEdge(0, 0, 1, 0, true, true);
        input.boundaries = new Federation.Boundary[](1);
        input.boundaries[0] = _boundary(1, 0, 0);
    }

    function _assertCode(Federation.Input memory input, uint256 expected) private view {
        (bool success, bytes memory result) =
            address(federation).staticcall(abi.encodeCall(federation.compute, (input)));
        require(!success && result.length == 100, "missing CCME error");
        bytes4 selector;
        uint256 code;
        assembly {
            selector := mload(add(result, 32))
            code := mload(add(result, 36))
        }
        require(selector == Types.CCMEError.selector && code == expected, "wrong CCME error");
    }

    function _foreignWildcard() private pure returns (Federation.Input memory input) {
        input = _catchUp();
        input.repositories[1].snapshot.units = new Types.Unit[](1);
        input.repositories[1].snapshot.units[0].commitIndex = 1;
        input.repositories[1].snapshot.units[0].unitIndex = 1;
        input.repositories[1].snapshot.units[0].scope = 1;
        input.repositories[1].snapshot.units[0].correctionWildcard = true;
        input.repositories[1].bumpTargets = new uint256[](1);
        input.repositories[1].channelTargets = new uint256[](1);
    }

    function testForeignWildcardWithPendingDebtFailsClosed() public view {
        Federation.Input memory input = _foreignWildcard();
        (bool isSuccess, bytes memory result) =
            address(federation).staticcall(abi.encodeCall(federation.compute, (input)));
        require(!isSuccess && result.length == 68, "foreign wildcard silently retained debt");
        bytes4 selector;
        bytes32 field;
        assembly {
            selector := mload(add(result, 32))
            field := mload(add(result, 36))
        }
        require(
            selector == Types.InvalidInput.selector && field == bytes32("correction.foreignWildcard"),
            "foreign wildcard did not identify unsupported causal projection"
        );
    }

    function testForeignWildcardWithoutPendingContributionRemainsValid() public view {
        Federation.Input memory input = _foreignWildcard();
        input.boundaries[0].sourceCommit = 1;
        Federation.Result memory result = federation.compute(input);
        require(!result.plan.packages[1].isPlanned, "delivered foreign work resurrected");
        input.mode = Federation.Mode.Single;
        input.entryRepository = 1;
        input.specifiedRepositories = 0;
        result = federation.compute(input);
        require(result.plan.packages.length == 1 && !result.plan.packages[0].isPlanned, "single mode inspected peer");
    }

    function testVoidedForeignWildcardRestoresIncomingDebt() public view {
        Federation.Input memory input = _foreignWildcard();
        Types.Unit memory wildcard = input.repositories[1].snapshot.units[0];
        input.repositories[1].snapshot.commits = new Types.Commit[](3);
        input.repositories[1].snapshot.commits[0] = Types.Commit(bytes32(uint256(1)), 0);
        input.repositories[1].snapshot.commits[1] = Types.Commit(bytes32(uint256(2)), 1);
        input.repositories[1].snapshot.commits[2] = Types.Commit(bytes32(uint256(3)), 2);
        input.repositories[1].snapshot.units = new Types.Unit[](2);
        input.repositories[1].snapshot.units[0].commitIndex = 2;
        input.repositories[1].snapshot.units[0].unitIndex = 1;
        input.repositories[1].snapshot.units[0].scope = 1;
        input.repositories[1].snapshot.units[0].correctionTargets = 2;
        input.repositories[1].snapshot.units[1] = wildcard;
        input.repositories[1].bumpTargets = new uint256[](2);
        input.repositories[1].channelTargets = new uint256[](2);
        Federation.Result memory result = federation.compute(input);
        require(
            result.plan.packages[1].isPlanned && result.plan.packages[1].isCatchUp, "voided wildcard remained active"
        );
    }

    function testComposedInventoryTargetsRefreshNativeWildcardSuppression() public view {
        Federation.Input memory input = _input(1);
        Types.Snapshot memory snapshot;
        snapshot.packages = new Types.Package[](2);
        snapshot.packages[0] = Types.Package("a", Types.Version(0, 1, 0, 0, 0), true);
        snapshot.packages[1] = Types.Package("b", Types.Version(0, 1, 0, 0, 0), true);
        snapshot.commits = new Types.Commit[](3);
        snapshot.commits[0] = Types.Commit(bytes32(uint256(1)), 0);
        snapshot.commits[1] = Types.Commit(bytes32(uint256(2)), 1);
        snapshot.commits[2] = Types.Commit(bytes32(uint256(3)), 2);
        snapshot.tags = new Types.Tag[](2);
        snapshot.tags[0] = Types.Tag(0, 0, Types.Version(1, 0, 0, 0, 0));
        snapshot.tags[1] = Types.Tag(1, 0, Types.Version(1, 0, 0, 0, 0));
        snapshot.edges = new Types.Edge[](1);
        snapshot.edges[0] = Types.Edge(0, 1, true, true);
        snapshot.units = new Types.Unit[](2);
        snapshot.units[0].commitIndex = 2;
        snapshot.units[0].unitIndex = 1;
        snapshot.units[0].scope = 2;
        snapshot.units[0].correctionWildcard = true;
        snapshot.units[1].commitIndex = 1;
        snapshot.units[1].unitIndex = 1;
        snapshot.units[1].scope = 1;
        snapshot.units[1].bump = Types.Bump.Minor;
        snapshot.units[1].propagate = Types.Bump.Patch;
        snapshot.units[1].depth = 1;
        input.repositories[0].snapshot = snapshot;
        input.repositories[0].bumpTargets = new uint256[](2);
        input.repositories[0].bumpTargets[1] = 2;
        input.repositories[0].channelTargets = new uint256[](2);
        Federation.Result memory result = federation.compute(input);
        require(
            result.plan.packages[0].isPlanned && !result.plan.packages[1].isPlanned, "composed target escaped wildcard"
        );
        for (uint256 i; i < result.plan.diagnostics.length; ++i) {
            require(result.plan.diagnostics[i].code != 209, "composed suppression retained stale noop warning");
        }
    }

    function testModesSelectDifferentRepositorySets() public view {
        Federation.Input memory input = _input(3);
        input.mode = Federation.Mode.Single;
        input.specifiedRepositories = 0;
        Federation.Result memory result = federation.compute(input);
        require(result.selectedRepositories == 1 && result.plan.packages.length == 1, "single mode selected peers");
        input.mode = Federation.Mode.Specified;
        input.specifiedRepositories = 5;
        result = federation.compute(input);
        require(result.selectedRepositories == 5 && result.plan.packages.length == 2, "specified roster ignored");
        require(result.packageRepositories[1] == 2, "specified identity lost");
        input.mode = Federation.Mode.Discovered;
        input.specifiedRepositories = 0;
        input.repositories[0].peers = 2;
        input.repositories[1].peers = 1;
        result = federation.compute(input);
        require(result.selectedRepositories == 3 && result.plan.packages.length == 2, "peer component not discovered");
        input.entryRepository = 1;
        result = federation.compute(input);
        require(result.selectedRepositories == 3, "leaf cannot initiate discovery");
    }

    function testSingleModeDoesNotLoadUnselectedHistory() public view {
        Federation.Input memory input = _input(2);
        input.mode = Federation.Mode.Single;
        input.specifiedRepositories = 0;
        input.repositories[1].snapshot.commits[0].id = 0;
        Federation.Result memory result = federation.compute(input);
        require(result.plan.packages.length == 1, "inactive repository was validated");
    }

    function testSingleModePreservesNativePropagationScopes() public view {
        Federation.Input memory input = _input(2);
        input.mode = Federation.Mode.Single;
        input.specifiedRepositories = 0;
        input.repositories[0].snapshot.packages = new Types.Package[](2);
        input.repositories[0].snapshot.packages[0] = Types.Package("a", Types.Version(0, 1, 0, 0, 0), true);
        input.repositories[0].snapshot.packages[1] = Types.Package("b", Types.Version(0, 1, 0, 0, 0), true);
        input.repositories[0].snapshot.tags = new Types.Tag[](2);
        input.repositories[0].snapshot.tags[0] = Types.Tag(0, 0, Types.Version(1, 0, 0, 0, 0));
        input.repositories[0].snapshot.tags[1] = Types.Tag(1, 0, Types.Version(1, 0, 0, 0, 0));
        input.repositories[0].snapshot.edges = new Types.Edge[](1);
        input.repositories[0].snapshot.edges[0] = Types.Edge(0, 1, true, true);
        input.repositories[0].snapshot.units[0].propagate = Types.Bump.Patch;
        input.repositories[0].snapshot.units[0].depth = 1;
        input.repositories[0].snapshot.units[0].propagateScope = 2;
        input.repositories[0].snapshot.units[0].propagateChannel =
            Types.ChannelDirective(Types.ChannelMode.Set, 0, bytes32("beta"));
        input.repositories[0].snapshot.units[0].channelDepth = 1;
        input.repositories[0].snapshot.units[0].channelScope = 2;
        input.repositories[0].bumpTargets = new uint256[](0);
        input.repositories[0].channelTargets = new uint256[](0);
        Types.Plan memory expected = planner.compute(input.repositories[0].snapshot);
        Federation.Result memory result = federation.compute(input);
        require(
            keccak256(abi.encode(result.plan)) == keccak256(abi.encode(expected)),
            "standalone escape changed native propagation policy"
        );
    }

    function testPreparationKeepsRepositoryAncestrySeparate() public view {
        Federation.Input memory input = _input(2);
        (Types.Snapshot memory snapshot, History.State memory history) = federation.prepare(input);
        require(snapshot.commits[0].id == snapshot.commits[2].id, "fixture lost duplicate object IDs");
        require(
            snapshot.commits[2].parents == 0 && history.ancestors[1] == 3 && history.ancestors[3] == 12,
            "repository identity was replaced by synthetic ancestry"
        );
        require(snapshot.units[0].scope == 2 && snapshot.units[1].scope == 1, "local scopes crossed repositories");
    }

    function testSourceLocalCorrectionsSurviveComposition() public view {
        Federation.Input memory input = _input(2);
        input.repositories[0].snapshot.commits = new Types.Commit[](3);
        input.repositories[0].snapshot.commits[0] = Types.Commit(bytes32(uint256(1)), 0);
        input.repositories[0].snapshot.commits[1] = Types.Commit(bytes32(uint256(2)), 1);
        input.repositories[0].snapshot.commits[2] = Types.Commit(bytes32(uint256(3)), 2);
        input.repositories[0].snapshot.units = new Types.Unit[](2);
        input.repositories[0].bumpTargets = new uint256[](2);
        input.repositories[0].channelTargets = new uint256[](2);
        for (uint256 i; i < 2; ++i) {
            input.repositories[0].snapshot.units[i].commitIndex = uint16(2 - i);
            input.repositories[0].snapshot.units[i].unitIndex = 1;
            input.repositories[0].snapshot.units[i].scope = 1;
        }
        input.repositories[0].snapshot.units[0].bump = Types.Bump.Patch;
        input.repositories[0].snapshot.units[0].correctionTargets = 2;
        input.repositories[0].snapshot.units[1].bump = Types.Bump.Major;
        Federation.Result memory result = federation.compute(input);
        require(
            result.plan.packages[0].next.major == 1 && result.plan.packages[0].next.patch == 1,
            "local correction targeted wrong global unit"
        );
        require(result.plan.packages[1].next.minor == 1, "correction crossed repository identity");
    }

    function testCrossRepositoryCatchUpUsesExplicitBoundary() public view {
        Federation.Input memory input = _catchUp();
        Federation.Result memory result = federation.compute(input);
        require(!result.plan.packages[0].isPlanned, "published provider re-released");
        require(result.plan.packages[1].isPlanned && result.plan.packages[1].next.patch == 1, "consumer debt lost");
        require(result.plan.packages[1].isCatchUp, "catch-up reason missing");
        input.boundaries[0].sourceCommit = 1;
        result = federation.compute(input);
        require(!result.plan.packages[1].isPlanned, "incorporated provider release still owed");
    }

    function testConsumerAheadOfForeignProviderStillCatchesUp() public view {
        Federation.Input memory input = _catchUp();
        input.repositories[0].snapshot.commits = new Types.Commit[](3);
        input.repositories[0].snapshot.commits[0] = Types.Commit(bytes32(uint256(1)), 0);
        input.repositories[0].snapshot.commits[1] = Types.Commit(bytes32(uint256(2)), 1);
        input.repositories[0].snapshot.commits[2] = Types.Commit(bytes32(uint256(3)), 2);
        input.repositories[0].snapshot.tags[1].commitIndex = 2;
        input.boundaries[0].sourceCommit = 1;
        Federation.Result memory result = federation.compute(input);
        require(
            result.plan.packages[1].isPlanned && result.plan.packages[1].next.patch == 1,
            "consumer position falsely discharged unpublished source"
        );
    }

    function testCombinedPublishOrderCrossesRepositories() public view {
        Federation.Input memory input = _input(2);
        input.repositories[1].snapshot.units[0].propagate = Types.Bump.Patch;
        input.repositories[1].snapshot.units[0].depth = 1;
        input.repositories[1].bumpTargets[0] = 1;
        input.crossEdges = new Federation.CrossEdge[](1);
        input.crossEdges[0] = Federation.CrossEdge(1, 0, 0, 0, true, true);
        input.boundaries = new Federation.Boundary[](1);
        input.boundaries[0] = _boundary(0, 1, 0);
        Federation.Result memory result = federation.compute(input);
        require(
            result.plan.publishOrder.length == 2 && result.plan.publishOrder[0] == 1
                && result.plan.publishOrder[1] == 0,
            "foreign provider order lost"
        );
    }

    function testMissingAndDuplicateBoundaryFailClosed() public view {
        Federation.Input memory input = _catchUp();
        input.boundaries = new Federation.Boundary[](0);
        _assertCode(input, 333);
        input.boundaries = new Federation.Boundary[](2);
        input.boundaries[0] = _boundary(1, 0, 0);
        input.boundaries[1] = _boundary(1, 0, 0);
        _assertCode(input, 333);
    }

    function testBoundaryCannotNameMissingConsumerVersion() public view {
        Federation.Input memory input = _catchUp();
        input.boundaries[0].releaseVersion = Types.Version(99, 0, 0, 0, 0);
        _assertCode(input, 333);
    }

    function testOmittedRequiredProviderFailsSpecifiedMode() public view {
        Federation.Input memory input = _catchUp();
        input.entryRepository = 1;
        input.specifiedRepositories = 2;
        _assertCode(input, 332);
    }

    function testConsumerCancelUsesObservedForeignRevision() public view {
        Federation.Input memory input = _catchUp();
        input.repositories[1] = _repository(bytes32("beta"), "b", Types.Bump.Patch);
        input.repositories[1].snapshot.units[0].bump = Types.Bump.None;
        input.repositories[1].snapshot.units[0].isCancel = true;
        input.observations = new Federation.Observation[](1);
        input.observations[0] = Federation.Observation(1, 1, 0, 1);
        Federation.Result memory result = federation.compute(input);
        require(!result.plan.packages[1].isPlanned, "cancel did not clear foreign debt");
        input.observations[0].sourceCommit = 0;
        result = federation.compute(input);
        require(result.plan.packages[1].isPlanned, "cancel reached unseen source work");
        input.observations = new Federation.Observation[](0);
        _assertCode(input, 333);
    }

    function testForeignCancelReportsPublishedPrereleaseWork() public view {
        Federation.Input memory input = _catchUp();
        input.repositories[0].snapshot.tags[1].version = Types.Version(1, 1, 0, bytes32("beta"), 0);
        input.repositories[1] = _repository(bytes32("beta"), "b", Types.Bump.Patch);
        input.repositories[1].snapshot.commits = new Types.Commit[](3);
        input.repositories[1].snapshot.commits[0] = Types.Commit(bytes32(uint256(1)), 0);
        input.repositories[1].snapshot.commits[1] = Types.Commit(bytes32(uint256(2)), 1);
        input.repositories[1].snapshot.commits[2] = Types.Commit(bytes32(uint256(3)), 2);
        input.repositories[1].snapshot.tags = new Types.Tag[](2);
        input.repositories[1].snapshot.tags[0] = Types.Tag(0, 0, Types.Version(1, 0, 0, 0, 0));
        input.repositories[1].snapshot.tags[1] = Types.Tag(0, 1, Types.Version(1, 0, 1, bytes32("beta"), 0));
        input.repositories[1].snapshot.units[0].commitIndex = 2;
        input.repositories[1].snapshot.units[0].bump = Types.Bump.None;
        input.repositories[1].snapshot.units[0].isCancel = true;
        input.boundaries = new Federation.Boundary[](2);
        input.boundaries[0] = _boundary(1, 0, 0);
        input.boundaries[1] = Federation.Boundary(1, 0, Types.Version(1, 0, 1, bytes32("beta"), 0), 0, 1);
        input.observations = new Federation.Observation[](1);
        input.observations[0] = Federation.Observation(1, 2, 0, 1);
        Federation.Result memory result = federation.compute(input);
        require(!result.plan.packages[1].isPlanned, "cancel created a release");
        uint256 warningCount;
        for (uint256 i; i < result.plan.diagnostics.length; ++i) {
            if (result.plan.diagnostics[i].code == 171 && result.plan.diagnostics[i].packageIndex == 1) ++warningCount;
        }
        require(warningCount == 1, "foreign published train cancellation missing W171");
    }

    function _channelConflict() private pure returns (Federation.Input memory input) {
        input = _input(3);
        input.repositories[2] = _repository(bytes32("gamma"), "c", Types.Bump.None);
        for (uint256 i; i < 2; ++i) {
            input.repositories[i].snapshot.units[0].propagateChannel =
                Types.ChannelDirective(Types.ChannelMode.Set, 0, i == 0 ? bytes32("beta") : bytes32("rc"));
            input.repositories[i].snapshot.units[0].channelDepth = 1;
            input.repositories[i].channelTargets[0] = 4;
        }
        input.crossEdges = new Federation.CrossEdge[](2);
        input.crossEdges[0] = Federation.CrossEdge(0, 0, 2, 0, true, true);
        input.crossEdges[1] = Federation.CrossEdge(1, 0, 2, 0, true, true);
        input.boundaries = new Federation.Boundary[](2);
        input.boundaries[0] = _boundary(2, 0, 0);
        input.boundaries[1] = _boundary(2, 1, 0);
    }

    function testIncomparableChannelsRequireDirectResolution() public view {
        Federation.Input memory input = _channelConflict();
        _assertCode(input, 334);
        input.repositories[2] = _repository(bytes32("gamma"), "c", Types.Bump.Patch);
        input.repositories[2].snapshot.units[0].bump = Types.Bump.None;
        input.repositories[2].snapshot.units[0].isRelease = true;
        input.repositories[2].snapshot.units[0].channel =
            Types.ChannelDirective(Types.ChannelMode.Set, 0, bytes32("beta"));
        Federation.Result memory result = federation.compute(input);
        require(result.plan.packages[2].channel == bytes32("beta"), "direct channel did not resolve conflict");
        input.repositories[2].snapshot.units[0].channel.to = 0;
        _assertCode(input, 334);
    }

    function testAgreeingIncomparableChannelsAreAccepted() public view {
        Federation.Input memory input = _channelConflict();
        input.repositories[1].snapshot.units[0].propagateChannel.to = bytes32("beta");
        Federation.Result memory result = federation.compute(input);
        require(result.plan.packages[2].channel == bytes32("beta"), "agreement rejected");
    }

    function testDiscoveredCyclesAreRejected() public view {
        Federation.Input memory input = _input(3);
        input.mode = Federation.Mode.Discovered;
        input.specifiedRepositories = 0;
        input.repositories[0].peers = 6;
        input.repositories[1].peers = 5;
        input.repositories[2].peers = 3;
        _assertCode(input, 338);
    }

    function testDuplicateAndReservedIdentityAreRejected() public view {
        Federation.Input memory input = _input(2);
        input.repositories[1].identity = bytes32("ALPHA");
        _assertCode(input, 339);
        input.repositories[1].identity = bytes32("CONTROL");
        _assertCode(input, 339);
    }

    function testDiscoveredOneSidedLinkIsRejected() public view {
        Federation.Input memory input = _input(2);
        input.mode = Federation.Mode.Discovered;
        input.specifiedRepositories = 0;
        input.repositories[0].peers = 2;
        (bool success,) = address(federation).staticcall(abi.encodeCall(federation.compute, (input)));
        require(!success, "one-sided discovery link accepted");
    }

    function testSourceCannotScopeForeignPackage() public view {
        Federation.Input memory input = _input(2);
        input.repositories[0].snapshot.units[0].scope = 2;
        (bool success,) = address(federation).staticcall(abi.encodeCall(federation.compute, (input)));
        require(!success, "source directly addressed another repository");
    }
}
