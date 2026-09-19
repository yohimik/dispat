// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package models

// The published names this module carried before its boolean predicates took
// the `Is` prefix the rest of the workspace uses. Each one forwards to the
// preferred spelling and is kept because this module is imported by
// configurations written outside this repository: a rename that breaks them
// would be a breaking release for a spelling change.
//
// pkg/ccme, pkg/manifest, pkg/scanner and pkg/writer carry the same file for
// the same reason.

// AllPackagesEnabled is retained for source compatibility.
//
// Deprecated: use IsAllPackagesEnabled.
func (c *GitHubConfig) AllPackagesEnabled() bool { return c.IsAllPackagesEnabled() }

// DraftEnabled is retained for source compatibility.
//
// Deprecated: use IsDraftEnabled.
func (c *GitHubConfig) DraftEnabled() bool { return c.IsDraftEnabled() }

// PushEnabled is retained for source compatibility.
//
// Deprecated: use IsPushEnabled.
func (c *CommitConfig) PushEnabled() bool { return c.IsPushEnabled() }

// ForceEnabled is retained for source compatibility.
//
// Deprecated: use IsForceEnabled.
func (c *CommitConfig) ForceEnabled() bool { return c.IsForceEnabled() }

// VerifyEnabled is retained for source compatibility.
//
// Deprecated: use IsVerifyEnabled.
func (c *CommitConfig) VerifyEnabled() bool { return c.IsVerifyEnabled() }

// ForceEnabled is retained for source compatibility.
//
// Deprecated: use IsForceEnabled.
func (a AliasTagConfig) ForceEnabled(runDefault bool) bool { return a.IsForceEnabled(runDefault) }

// AppliesTo is retained for source compatibility.
//
// Deprecated: use IsApplicableTo.
func (a AliasTagConfig) AppliesTo(channel string) bool { return a.IsApplicableTo(channel) }

// WriteVersionEnabled is retained for source compatibility.
//
// Deprecated: use IsWriteVersionEnabled.
func (c *AutoVersionConfig) WriteVersionEnabled() bool { return c.IsWriteVersionEnabled() }

// UpdateCheckEnabled is retained for source compatibility.
//
// Deprecated: use IsUpdateCheckEnabled.
func (c *File) UpdateCheckEnabled() bool { return c.IsUpdateCheckEnabled() }

// KnownWebhookPattern is retained for source compatibility.
//
// Deprecated: use IsKnownWebhookPattern.
func KnownWebhookPattern(p string) bool { return IsKnownWebhookPattern(p) }

// KnownWebhookFormatField is retained for source compatibility.
//
// Deprecated: use IsKnownWebhookFormatField.
func KnownWebhookFormatField(name string) bool { return IsKnownWebhookFormatField(name) }

// MatchWebhookEvent is retained for source compatibility.
//
// Deprecated: use IsWebhookEventAdmitted.
func MatchWebhookEvent(pattern, event string) bool { return IsWebhookEventAdmitted(pattern, event) }
