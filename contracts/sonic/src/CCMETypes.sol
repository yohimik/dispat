// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.28;

library CCMETypes {
    uint256 internal constant MAX_PACKAGES = 64;
    uint256 internal constant MAX_COMMITS = 256;
    uint256 internal constant MAX_UNITS = 256;
    uint256 internal constant MAX_TAGS = 512;
    uint256 internal constant MAX_EDGES = 512;

    enum Bump {
        None,
        Patch,
        Minor,
        Major
    }
    enum ReleaseKind {
        Unset,
        Hold,
        Auto,
        Exact
    }
    enum ChannelMode {
        None,
        Set,
        Transition,
        Inherit,
        AnyPrerelease
    }

    // Zero channel is stable; other channels are left-aligned ASCII, padded with zeros.
    struct Version {
        uint64 major;
        uint64 minor;
        uint64 patch;
        bytes32 channel;
        uint64 counter;
    }

    struct Package {
        string name;
        Version initialVersion;
        bool preserveMajorZero;
    }

    struct Commit {
        bytes32 id;
        uint256 parents;
    }

    struct Tag {
        uint16 packageIndex;
        uint16 commitIndex;
        Version version;
    }

    struct ChannelDirective {
        ChannelMode mode;
        bytes32 from;
        bytes32 to;
    }

    struct Unit {
        uint16 commitIndex;
        uint16 unitIndex;
        uint256 scope;
        Bump bump;
        Bump propagate;
        uint16 depth;
        uint256 propagateScope;
        ChannelDirective channel;
        ChannelDirective propagateChannel;
        uint16 channelDepth;
        uint256 channelScope;
        ReleaseKind releaseKind;
        Version pin;
        bool isCancel;
        bool isRelease;
        bool correctionScopeInherited;
        bool correctionWildcard;
        uint256 correctionTargets;
    }

    struct Edge {
        uint16 provider;
        uint16 consumer;
        bool propagates;
        bool ordersPublish;
    }

    struct Snapshot {
        Package[] packages;
        Commit[] commits;
        Tag[] tags;
        Unit[] units;
        Edge[] edges;
    }

    struct PackagePlan {
        Version baseline;
        Version next;
        bytes32 channel;
        Bump directBump;
        Bump propagatedBump;
        Bump trainBump;
        uint256 owedSources;
        bool hasBaseline;
        bool isHeld;
        bool isPlanned;
        bool isCatchUp;
        bool isChannelOnly;
        bool hasChannelEntryPatch;
    }

    struct Diagnostic {
        uint16 code;
        uint16 packageIndex;
        uint16 unitIndex;
    }

    struct Plan {
        PackagePlan[] packages;
        uint16[] publishOrder;
        Diagnostic[] diagnostics;
    }

    error InvalidInput(bytes32 field, uint256 index);
    error CCMEError(uint16 code, uint256 packageIndex, uint256 unitIndex);
}
