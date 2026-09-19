// SPDX-License-Identifier: MIT
// Copyright (c) 2026 yohimik

package manifest

// HasTag is retained for source compatibility.
//
// Deprecated: use IsTagged.
func (r ImageRef) HasTag() bool { return r.IsTagged() }

// Pinned is retained for source compatibility.
//
// Deprecated: use IsPinned.
func (r ImageRef) Pinned() bool { return r.IsPinned() }

// Interpolated is retained for source compatibility.
//
// Deprecated: use IsInterpolated.
func (r ImageRef) Interpolated() bool { return r.IsInterpolated() }

// ValidTag is retained for source compatibility.
//
// Deprecated: use IsValidTag.
func ValidTag(tag string) bool { return IsValidTag(tag) }

// Valid is retained for source compatibility.
//
// Deprecated: use IsValid.
func (k Kind) Valid() bool { return k.IsValid() }
