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
	// BuildScript and PublishScript are resolved shell commands (not script
	// names) executed inside the package folder.
	BuildScript   string
	PublishScript string
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
