// SPDX-License-Identifier: MIT
// Copyright (c) 2026 yohimik

package ccme

// HasErrors is retained for source compatibility.
//
// Deprecated: use IsInvalid.
func (r *Result) HasErrors() bool { return r.IsInvalid() }

// HasExplicitScope is retained for source compatibility.
//
// Deprecated: use IsScopeExplicit.
func (u *Unit) HasExplicitScope() bool { return u.IsScopeExplicit() }
