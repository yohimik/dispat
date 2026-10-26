// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package execution

import "slices"

// Prepared-input failure follows declared package provenance, including
// transitive providers, rather than every execution-order edge in the graph.
func choreographyPreparedProviderFor(spec ChoreographyTaskSpec, provider string) bool {
	return spec.Package == provider || slices.Contains(spec.PreparedProviders, provider)
}
