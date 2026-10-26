// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import (
	"fmt"

	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// rehomeChoreographyExports materializes only authenticated typed output
// paths. The caller invokes it after the signed Result and declared output
// manifest have passed first-node validation. Literal export strings remain
// unchanged even if they resemble a path token.
func rehomeChoreographyExports(result *Result, repository, packageName, sourceRoot string,
	roots []string) error {
	for i := range result.Exports {
		export := &result.Exports[i]
		if export.DeclaredPath == nil {
			continue
		}
		ref := export.DeclaredPath
		if result.Protocol != ChoreographyProtocolVersion || export.Value != "" ||
			ref.Repository != repository || ref.Package != packageName {
			return fmt.Errorf("execution: typed export %s lacks matching signed output identity", export.Name)
		}
		value, err := release.RehomeDeclaredOutputPath(release.DeclaredOutputPath{
			Root: ref.Root, Relative: ref.Relative}, sourceRoot, roots)
		if err != nil {
			return fmt.Errorf("execution: typed export %s: %w", export.Name, err)
		}
		export.Value = value
		export.DeclaredPath = nil
	}
	return nil
}
