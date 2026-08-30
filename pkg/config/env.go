package config

// Environment layers: maps of KEY=value a configuration declares, merged key
// by key with the most local winning.
//
// The keys arrive spelled exactly as their file wrote them, like every other
// map key in a configuration, and here that is not a nicety: PATH and Path are
// two variables, and a process handed one when it asked for the other would
// fail in a way nothing here could explain.

import (
	"fmt"
	"sort"
	"strings"
)

// EnvPairs flattens an env map to sorted KEY=value pairs — the deterministic
// form a child process receives, so two runs of the same configuration build
// the same environment in the same order.
func EnvPairs(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	pairs := make([]string, 0, len(env))
	for k, v := range env {
		pairs = append(pairs, k+"="+v)
	}
	sort.Strings(pairs)
	return pairs
}

// MergeEnv overlays one env layer onto another, key by key, and returns a new
// map: the single merge rule every level shares. A nil overlay leaves the base
// alone, which is what "the layer says nothing" has to mean.
func MergeEnv(base, over map[string]string) map[string]string {
	if len(over) == 0 {
		return base
	}
	merged := make(map[string]string, len(base)+len(over))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range over {
		merged[k] = v
	}
	return merged
}

// ValidateEnv rejects the env keys that could never reach a process intact:
// empty, carrying "=", claiming one of the reserved prefixes, or colliding
// case-insensitively. The last one is the decode's own rule for every object
// applied once more here, because an env layer is also merged with the layers
// around it, and two keys that fold together across layers would leave the
// survivor to luck.
//
// label names the layer in the error. reserved are prefixes a program keeps
// for the variables it computes itself, matched case-insensitively; a key
// claiming one could never win against the computed value anyway, so
// configuring one is a mistake worth naming rather than a silent no-op.
//
// Keys are checked in sorted order so a configuration with several mistakes
// always reports the same one first.
func ValidateEnv(label string, env map[string]string, reserved ...string) error {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	seen := make(map[string]string, len(keys))
	for _, k := range keys {
		switch {
		case k == "":
			return fmt.Errorf("%s contains an empty key", label)
		case strings.Contains(k, "="):
			return fmt.Errorf("%s: key %q must not contain '='", label, k)
		}
		upper := strings.ToUpper(k)
		for _, prefix := range reserved {
			if strings.HasPrefix(upper, strings.ToUpper(prefix)) {
				return fmt.Errorf("%s: key %q uses the reserved %s prefix", label, k, prefix)
			}
		}
		lower := Fold(k)
		if prev, dup := seen[lower]; dup {
			return &FoldCollisionError{At: label, First: prev, Second: k}
		}
		seen[lower] = k
	}
	return nil
}
