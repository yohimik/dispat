// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.28;

import {CCMETypes as Types} from "./CCMETypes.sol";
import {CCMEGraph as Graph} from "./CCMEGraph.sol";

library DispatTaskGraph {
    uint256 private constant EMPTY_MASK = 0;
    uint256 private constant TASK_BIT = 1;
    uint256 private constant PACKAGE_BIT = 1;
    uint256 private constant INDEX_OFFSET = 1;
    uint256 private constant UNSET_TASK_POSITION = 0;
    bytes32 private constant EMPTY_COMMAND_HASH = bytes32(0);

    enum Kind {
        Command,
        Publish,
        Record
    }

    struct Descriptor {
        uint16 packageIndex;
        Kind kind;
        bytes32 commandHash;
        uint256 dependencies;
    }

    error InvalidTaskGraph(uint256 index);

    function validate(Types.Snapshot calldata snapshot, Types.Plan memory plan, Descriptor[] calldata descriptors)
        internal
        pure
    {
        uint256 count = snapshot.packages.length;
        uint256[] memory commands = new uint256[](count);
        uint256[] memory publications = new uint256[](count);
        uint256[] memory records = new uint256[](count);
        uint256[] memory ancestors = new uint256[](descriptors.length);
        for (uint256 i; i < descriptors.length; ++i) {
            Descriptor calldata descriptor = descriptors[i];
            uint256 packageIndex = descriptor.packageIndex;
            if (
                packageIndex >= count || !plan.packages[packageIndex].isPlanned
                    || descriptor.commandHash == EMPTY_COMMAND_HASH || descriptor.dependencies >> i != EMPTY_MASK
            ) revert InvalidTaskGraph(i);
            ancestors[i] = descriptor.dependencies;
            for (uint256 j; j < i; ++j) {
                if ((descriptor.dependencies & (TASK_BIT << j)) != EMPTY_MASK) ancestors[i] |= ancestors[j];
            }
            if (descriptor.kind == Kind.Command) {
                commands[packageIndex] |= TASK_BIT << i;
            } else if (descriptor.kind == Kind.Publish) {
                if (publications[packageIndex] != UNSET_TASK_POSITION) revert InvalidTaskGraph(i);
                publications[packageIndex] = i + INDEX_OFFSET;
            } else {
                if (records[packageIndex] != UNSET_TASK_POSITION) revert InvalidTaskGraph(i);
                records[packageIndex] = i + INDEX_OFFSET;
            }
        }
        uint256[] memory providers = providerClosure(snapshot);
        for (uint256 packageIndex; packageIndex < count; ++packageIndex) {
            if (!plan.packages[packageIndex].isPlanned) continue;
            if (
                commands[packageIndex] == EMPTY_MASK || publications[packageIndex] == UNSET_TASK_POSITION
                    || records[packageIndex] == UNSET_TASK_POSITION
            ) {
                revert InvalidTaskGraph(packageIndex);
            }
            uint256 publication = publications[packageIndex] - INDEX_OFFSET;
            uint256 record = records[packageIndex] - INDEX_OFFSET;
            if (
                (ancestors[publication] & commands[packageIndex]) != commands[packageIndex]
                    || descriptors[record].dependencies != (TASK_BIT << publication)
            ) revert InvalidTaskGraph(publication);
            for (uint256 provider; provider < count; ++provider) {
                if (
                    (providers[packageIndex] & (PACKAGE_BIT << provider)) == EMPTY_MASK
                        || !plan.packages[provider].isPlanned
                ) continue;
                if (
                    records[provider] == UNSET_TASK_POSITION
                        || (ancestors[publication] & (TASK_BIT << (records[provider] - INDEX_OFFSET))) == EMPTY_MASK
                ) {
                    revert InvalidTaskGraph(publication);
                }
                for (uint256 i; i < descriptors.length; ++i) {
                    if ((commands[packageIndex] & (TASK_BIT << i)) == EMPTY_MASK) continue;
                    if ((ancestors[i] & commands[provider]) != commands[provider]) revert InvalidTaskGraph(i);
                }
            }
        }
    }

    function providerClosure(Types.Snapshot calldata snapshot) private pure returns (uint256[] memory providers) {
        Graph.State memory graph = Graph.analyze(snapshot);
        providers = new uint256[](snapshot.packages.length);
        for (uint256 i; i < snapshot.edges.length; ++i) {
            Types.Edge calldata edge = snapshot.edges[i];
            if (edge.ordersPublish) providers[edge.consumer] |= PACKAGE_BIT << edge.provider;
        }
        for (uint256 i; i < graph.publishOrder.length; ++i) {
            uint256 target = graph.publishOrder[i];
            for (uint256 provider; provider < providers.length; ++provider) {
                if ((providers[target] & (PACKAGE_BIT << provider)) != EMPTY_MASK) {
                    providers[target] |= providers[provider];
                }
            }
        }
    }
}
