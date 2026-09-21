package config

// The repository root as a package folder. A standalone `packages` entry may
// name "." (or "./"), which is how a single-package repository says that its
// one package is the repository: its manifest, its changelog and its sources
// sit at the top, and it owns every file no deeper package owns.
//
// Two things follow from the folder being the root rather than a folder
// inside it, and both live here rather than being discovered later. The
// package has no in-folder configuration layer, because the file in that
// folder is the root configuration file itself (discovery skips it for the
// same reason a space rooted at the repository has no space-file layer). And
// the one setting whose contract the root folder cannot honour, revertOnFail,
// is refused rather than obeyed.

import "fmt"

// refuseRootPackageRevert refuses revertOnFail on a package whose folder is
// the repository root. The promise the setting makes is to roll back the
// package folder when the package fails; for the root that folder is the
// whole working tree, so keeping it would discard the release files every
// other package of the same run has already written, and a run would undo
// work it reported as published. The refusal names the entry, and the remedy
// is an explicit `false` on it, because the true it is being held to may have
// been inherited from the root configuration rather than written here.
//
// label names the package the way the rest of discovery names it.
func refuseRootPackageRevert(label string, sc SpaceConfig) error {
	if sc.RevertOnFail == nil || !*sc.RevertOnFail {
		return nil
	}
	return fmt.Errorf(
		"config: %s: revertOnFail cannot be used by a package whose path is the repository root: "+
			"reverting its folder discards every local change in the working tree, "+
			"including the release files the other packages of the same run have written; "+
			"set revertOnFail: false on this entry",
		label)
}
