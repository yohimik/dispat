// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package models

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// PublishConfig is a level's `publish` object: what the publish stage does
// beyond running its commands. It is space-shaped, stated at the root, on a
// space, in a space folder's file or on a package, and a level overlays the
// one below it field by field.
type PublishConfig struct {
	// OrderKinds selects dependency fields that constrain publication. Nil
	// inherits; a pointer to an empty list disables every publication edge.
	OrderKinds *[]string `json:"orderKinds,omitempty"`
	// BlockingKinds selects dependency fields that propagate publish failures.
	BlockingKinds *[]string `json:"blockingKinds,omitempty"`
	// AdoptPublished is what a release does when its publish command fails
	// and the destination already holds the planned version (CCME §19.4).
	// A level that states it replaces the inherited value whole.
	AdoptPublished *AdoptPublishedConfig `json:"adoptPublished,omitempty"`
}

// AdoptPublishedConfig decides whether a version the destination already
// holds may be adopted as this release, and how its identity is checked.
//
// It is written as a boolean or as an object. `false` turns adoption off,
// which makes a refused publish an ordinary publish failure; `true` and an
// absent key keep it on. The object names the scripts that check identity.
type AdoptPublishedConfig struct {
	// Enabled is nil when unstated, and adoption is then on.
	Enabled *bool `json:"enabled,omitempty"`
	// Verify names the scripts that compare the destination's copy with the
	// artefact this run would have published. Exit 0 means it is that
	// artefact, exit 3 means the destination does not hold the version, and
	// any other exit means it holds a version whose identity does not match or
	// cannot be established.
	Verify []string `json:"verify,omitempty"`
}

// AdoptPublishedOf renders the setting a bare `adoptPublished` boolean states.
func AdoptPublishedOf(isEnabled bool) *AdoptPublishedConfig {
	return &AdoptPublishedConfig{Enabled: &isEnabled}
}

// IsEnabled reports whether adoption is on (default true). Nil-safe.
func (c *AdoptPublishedConfig) IsEnabled() bool { return c == nil || c.Enabled == nil || *c.Enabled }

// UnmarshalJSON accepts the boolean and the object form.
func (c *AdoptPublishedConfig) UnmarshalJSON(data []byte) error {
	var isEnabled bool
	if err := json.Unmarshal(data, &isEnabled); err == nil {
		*c = *AdoptPublishedOf(isEnabled)
		return nil
	}
	type plain AdoptPublishedConfig
	var out plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return fmt.Errorf("adoptPublished: wants true or false, or an object naming enabled and verify: %w", err)
	}
	*c = AdoptPublishedConfig(out)
	return nil
}

// MarshalJSON writes the boolean when the setting says nothing but whether
// adoption is on, and the object otherwise.
func (c AdoptPublishedConfig) MarshalJSON() ([]byte, error) {
	if len(c.Verify) == 0 && c.Enabled != nil {
		return json.Marshal(*c.Enabled)
	}
	type plain AdoptPublishedConfig
	return json.Marshal(plain(c))
}
