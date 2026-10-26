// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"fmt"
	"path/filepath"

	"github.com/yohimik/dispat/services/dispat/internal/release"
)

const (
	choreographyExportMissingValueErrorFormat = "execution: typed export %s lacks matching signed output identity"
	choreographyExportRehomeErrorFormat       = "execution: typed export %s: %w"
	choreographyExportMissingValue            = ""
)

type choreographyExportRehomeOptions struct {
	Result     *Result
	Repository string
	Package    string
	SourceRoot string
	Roots      []string
}

// rehomeChoreographyExports materializes only authenticated typed output
// paths. The caller invokes it after the signed Result and declared output
// manifest have passed first-node validation. Literal export strings remain
// unchanged even if they resemble a path token.
func rehomeChoreographyExports(opts choreographyExportRehomeOptions) error {
	// Shells report a resolved working directory (for example /private/var on
	// macOS). Give an authenticated declared path that same local spelling.
	sourceRoot := opts.SourceRoot
	if resolved, err := filepath.EvalSymlinks(sourceRoot); err == nil {
		sourceRoot = resolved
	}
	for i := range opts.Result.Exports {
		export := &opts.Result.Exports[i]
		if export.DeclaredPath == nil {
			continue
		}
		ref := export.DeclaredPath
		if opts.Result.Protocol != ChoreographyProtocolVersion || export.Value != choreographyExportMissingValue ||
			ref.Repository != opts.Repository || ref.Package != opts.Package {
			return fmt.Errorf(choreographyExportMissingValueErrorFormat, export.Name)
		}
		value, err := release.RehomeDeclaredOutputPath(release.DeclaredOutputPath{
			Root: ref.Root, Relative: ref.Relative}, sourceRoot, opts.Roots)
		if err != nil {
			return fmt.Errorf(choreographyExportRehomeErrorFormat, export.Name, err)
		}
		export.Value = value
		export.DeclaredPath = nil
	}
	return nil
}
