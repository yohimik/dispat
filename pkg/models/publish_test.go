// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package models

import (
	"encoding/json"
	"testing"
)

func TestAdoptPublishedJSON(t *testing.T) {
	for _, c := range []struct {
		written, canonical string
		isEnabled          bool
	}{
		{written: `false`, canonical: `false`},
		{written: `true`, canonical: `true`, isEnabled: true},
		{written: `{"verify":["check"]}`, canonical: `{"verify":["check"]}`, isEnabled: true},
		{written: `{"enabled":false,"verify":"check"}`, canonical: `{"enabled":false,"verify":["check"]}`},
	} {
		var got AdoptPublishedConfig
		err := json.Unmarshal([]byte(c.written), &got)
		if c.written == `{"enabled":false,"verify":"check"}` {
			if err == nil {
				t.Errorf("%s: a scalar verify is refused by encoding/json, as every list is", c.written)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", c.written, err)
		}
		if got.IsEnabled() != c.isEnabled {
			t.Errorf("%s: enabled %v", c.written, got.IsEnabled())
		}
		out, err := json.Marshal(got)
		if err != nil || string(out) != c.canonical {
			t.Errorf("%s: wrote %s (%v), want %s", c.written, out, err, c.canonical)
		}
	}
	var unknown AdoptPublishedConfig
	if err := json.Unmarshal([]byte(`{"verfy":["x"]}`), &unknown); err == nil {
		t.Error("an unknown key is refused")
	}
	if !(*AdoptPublishedConfig)(nil).IsEnabled() {
		t.Error("an unstated policy adopts")
	}
}

func TestIsTagPushEnabled(t *testing.T) {
	on := true
	for _, c := range []struct {
		commit *CommitConfig
		want   bool
	}{
		{commit: nil},
		{commit: &CommitConfig{}},
		{commit: &CommitConfig{Push: true}, want: true},
		{commit: &CommitConfig{Enabled: &on, Push: true}},
	} {
		if got := c.commit.IsTagPushEnabled(); got != c.want {
			t.Errorf("%+v: %v, want %v", c.commit, got, c.want)
		}
	}
}
