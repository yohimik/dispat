package config

// The static `env` objects: five layers of KEY=value, merged key by key with
// the most local winning.
//
//	the root file's `env`
//	  the root file's `spaces.<space>.env`
//	    the space folder's own config file `env`
//	      the root file's `packages.<pkg>.env` and `spaces.<s>.packages.<p>.env`
//	        the package folder's own config file `env`
//
// The keys arrive spelled exactly as their file wrote them, like every other
// map key in the configuration, and here that is not a nicety: PATH and Path
// are two variables, and a script handed one when it asked for the other would
// fail in a way nothing here could explain. There used to be a second pass
// reading the parsed tree back to undo the lowercasing the decode applied; the
// decode does not lowercase, so there is nothing to undo.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// reservedEnvPrefix is the namespace the computed variables own. A static key
// could never win against one anyway (StaticEnv places the computed set
// last), so configuring one is a mistake worth naming rather than a silent
// no-op.
const reservedEnvPrefix = "DISPAT_"

// EnvPairs flattens an env map to sorted KEY=value pairs — the deterministic
// form scripts receive, so two runs of the same configuration build the same
// environment in the same order.
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
//
// Exported for `dispat exec`, which layers a space's declared env over the
// file's outside the package build, and must do it the same way the build
// does or the same configuration would mean two things.
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

// validateEnv rejects the env keys that could never reach a script intact:
// empty, carrying "=", claiming the DISPAT_ namespace the computed variables
// own, or colliding case-insensitively. The last one is the decode's own rule
// for every object (see refuseFoldDuplicates) applied once more here, because
// an env layer is also merged with the layers around it, and two keys that fold
// together across layers would leave the survivor to luck. Keys are checked in
// sorted order so a config with several mistakes always reports the same one
// first.
func validateEnv(label string, env map[string]string) error {
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
		case strings.HasPrefix(strings.ToUpper(k), reservedEnvPrefix):
			return fmt.Errorf("%s: key %q uses the reserved %s prefix", label, k, reservedEnvPrefix)
		}
		lower := strings.ToLower(k)
		if prev, dup := seen[lower]; dup {
			return fmt.Errorf("%s: keys %q and %q collide case-insensitively", label, prev, k)
		}
		seen[lower] = k
	}
	return nil
}

// weakEnvString renders a scalar the way the weakly typed decode would. It is
// also what a generic map's key becomes on the way into the decode's view of
// the tree, so the two renderings can never disagree.
// The numeric cases go through strconv rather than fmt.Sprint: a large float
// formatted with %v would come out in scientific notation, which is not what
// the file said.
func weakEnvString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprint(t)
	}
}
