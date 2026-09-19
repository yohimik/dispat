// SPDX-License-Identifier: MIT
// Copyright (c) 2026 yohimik

package scanner

// AtPackageRoot is retained for source compatibility.
//
// Deprecated: use IsAtPackageRoot.
func (m Manifest) AtPackageRoot() bool { return m.IsAtPackageRoot() }

// SkipDir is retained for source compatibility.
//
// Deprecated: use IsSkippedDir.
func SkipDir(name string) bool { return IsSkippedDir(name) }

// SkipWorkspaceDir is retained for source compatibility.
//
// Deprecated: use IsSkippedWorkspaceDir.
func SkipWorkspaceDir(name string) bool { return IsSkippedWorkspaceDir(name) }

// Scanner is retained for source compatibility.
//
// Deprecated: use Scannerx.
type Scanner = Scannerx
