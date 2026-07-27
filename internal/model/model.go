// Package model holds the domain types shared across the tool.
package model

// Space groups packages that share build and publish behaviour.
type Space struct {
	Name string
	// Path of the space folder, relative to the monorepo root. Every direct
	// sub-folder is a package.
	Path string
	// BuildWaitsPublish: when true, consumers of packages from this space may
	// only start building after the provider has been published (not merely
	// built).
	BuildWaitsPublish bool
	// RevertOnFail: when true, all local changes inside the package folder
	// are rolled back (tracked files restored, untracked files removed) if
	// the package fails during its version, build or publish stage.
	RevertOnFail bool
	// BuildScript, PublishScript and VersionScript are resolved shell
	// commands (not script names) executed inside the package folder. Any of
	// them may be empty: the corresponding pipeline stage still runs (with
	// tags, changelogs and ordering intact) but executes no shell command.
	// VersionScript runs right before a consumer's build, only when the
	// consumer is released because of provider updates — its job is syncing
	// manifests (package.json, go.mod, ...) to the new provider versions.
	BuildScript   string
	PublishScript string
	VersionScript string
}

// Package is a single releasable folder inside a space.
type Package struct {
	Name  string
	Dir   string // folder in which scripts run
	Space *Space
}

// Dependency is a consumer -> provider relation between two packages.
type Dependency struct {
	Consumer string
	Provider string
}
