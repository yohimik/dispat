package models

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// This file is the value side of a `versionGroups` entry's `versioning` key:
// the two shapes it may be written in, and the one shape it is written back
// in.
//
// A group's rule has three axes, and for most groups two of them are the
// default, so the key keeps its original scalar form: `versioning: fixed`
// states the semver axis and leaves the other two alone. A group that wants
// them apart writes the object instead. Everything downstream of decoding
// sees three values and never learns which form the file used.

// Sharing values of a versioning group's counter and channels axes.
//
// The semver axis is the Versioning* value the group holds in common, which
// decides how much of the version its members share. The other two decide
// whether the members that share that prefix also share one prerelease
// counter and one channel:
//
//	versionGroups:
//	  cli:
//	    versioning:
//	      semver: fixedMajorMinor
//	      counter: independent
//	      channels: independent
//
// An axis nobody writes is SharingFixed, which is what a group written as a
// bare mode has always done.
const (
	// SharingFixed holds the axis in common across the group: one prerelease
	// counter for the whole group, or one channel for the whole group. It is
	// the default and the zero value.
	SharingFixed = "fixed"
	// SharingIndependent leaves the axis to each member: its own prerelease
	// counter continuing from its own baseline, or the channel its own
	// baseline and its own directives put it on.
	SharingIndependent = "independent"
)

// versionGroupAxes is the object form of the `versioning` key, and the shape
// both halves of this file read and write it through.
type versionGroupAxes struct {
	Semver   string `json:"semver,omitempty"`
	Counter  string `json:"counter,omitempty"`
	Channels string `json:"channels,omitempty"`
}

// MarshalJSON writes the shortest shape that carries the whole rule: the bare
// mode while both sharing axes are unset, the object otherwise, with an unset
// axis omitted. A group that came in as a mode goes back out as one, so a
// config dispat rewrites does not grow an object around every entry it left
// alone.
func (c VersionGroupConfig) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.entry())
}

// MarshalYAML is MarshalJSON's counterpart for a YAML config.
func (c VersionGroupConfig) MarshalYAML() (any, error) {
	return c.entry(), nil
}

// entry renders the whole declaration as both marshallers write it. A group
// that states no rule at all writes no key, rather than a versioning of null
// that reads as a rule nobody can name.
func (c VersionGroupConfig) entry() map[string]any {
	value := c.canonical()
	if value == nil {
		return map[string]any{}
	}
	return map[string]any{"versioning": value}
}

// canonical renders the versioning value in the shape both marshallers write.
func (c VersionGroupConfig) canonical() any {
	if c.Counter == "" && c.Channels == "" {
		if c.Versioning == "" {
			return nil
		}
		return c.Versioning
	}
	return versionGroupAxes{Semver: c.Versioning, Counter: c.Counter, Channels: c.Channels}
}

// UnmarshalJSON accepts both shapes of the `versioning` key and refuses every
// other key of the entry, exactly as the config loader does: a group declares
// its versioning rule and nothing else, so an unknown key is a typo the load
// has to report rather than ignore.
func (c *VersionGroupConfig) UnmarshalJSON(data []byte) error {
	var entry struct {
		Versioning any `json:"versioning"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&entry); err != nil {
		return err
	}
	out, err := NormalizeVersionGroupVersioning(entry.Versioning, "versionGroups: versioning")
	if err != nil {
		return err
	}
	*c = out
	return nil
}

// NormalizeVersionGroupVersioning expands a `versionGroups` entry's
// `versioning` value onto the three axes it carries. where names the entry for
// error messages, since every group in the file writes the same key.
//
// It is the single implementation behind both entry points: UnmarshalJSON
// here, and the CLI's config reader, whose decode table hands it the value
// under the same key. Two readers of one syntax would be two syntaxes
// eventually.
func NormalizeVersionGroupVersioning(raw any, where string) (VersionGroupConfig, error) {
	switch x := raw.(type) {
	case nil:
		return VersionGroupConfig{}, nil
	case string:
		return VersionGroupConfig{Versioning: x}, nil
	case map[string]any:
		return versionGroupObject(x, where)
	}
	return VersionGroupConfig{}, fmt.Errorf(
		"%s: wants a versioning mode, or an object naming semver, counter and channels", where)
}

// versionGroupObject reads the object form. Keys are matched folded, like
// every key of the config language, and two spellings of one axis are refused
// rather than resolved by whichever the runtime handed over first.
//
// The keys are visited in sorted order for the reason the generic decoder
// sorts its own: an object with more than one thing wrong with it has to
// report the same one first on every run, and a pair of spellings has to be
// named in the same order every time.
func versionGroupObject(fields map[string]any, where string) (VersionGroupConfig, error) {
	var out VersionGroupConfig
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	seen := make(map[string]string, len(fields))
	for _, key := range keys {
		val := fields[key]
		axis := strings.ToLower(key)
		target, ok := map[string]*string{
			"semver":   &out.Versioning,
			"counter":  &out.Counter,
			"channels": &out.Channels,
		}[axis]
		if !ok {
			return VersionGroupConfig{}, fmt.Errorf(
				"%s: unknown key %q; a versioning object names semver, counter and channels", where, key)
		}
		if written, twice := seen[axis]; twice {
			return VersionGroupConfig{}, fmt.Errorf(
				"%s: %q and %q are the same key", where, written, key)
		}
		s, isString := val.(string)
		if !isString {
			return VersionGroupConfig{}, fmt.Errorf("%s: %s wants a value", where, key)
		}
		seen[axis], *target = key, s
	}
	return out, nil
}
