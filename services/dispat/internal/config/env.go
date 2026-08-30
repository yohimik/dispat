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
// fail in a way nothing here could explain.
//
// The flattening and the merge are pkg/config's, and so is the refusal; what
// is dispat's is the namespace the computed variables own.

import (
	lib "github.com/yohimik/dispat/pkg/config"
)

// reservedEnvPrefix is the namespace the computed variables own. A static key
// could never win against one anyway (StaticEnv places the computed set
// last), so configuring one is a mistake worth naming rather than a silent
// no-op.
const reservedEnvPrefix = "DISPAT_"

// EnvPairs flattens an env map to sorted KEY=value pairs — the deterministic
// form scripts receive, so two runs of the same configuration build the same
// environment in the same order.
func EnvPairs(env map[string]string) []string { return lib.EnvPairs(env) }

// MergeEnv overlays one env layer onto another, key by key, and returns a new
// map: the single merge rule every level shares. A nil overlay leaves the base
// alone, which is what "the layer says nothing" has to mean.
//
// Exported for `dispat exec`, which layers a space's declared env over the
// file's outside the package build, and must do it the same way the build
// does or the same configuration would mean two things.
func MergeEnv(base, over map[string]string) map[string]string { return lib.MergeEnv(base, over) }

// validateEnv rejects the env keys that could never reach a script intact:
// empty, carrying "=", claiming the DISPAT_ namespace the computed variables
// own, or colliding case-insensitively. The last one is the decode's own rule
// for every object applied once more here, because an env layer is also merged
// with the layers around it, and two keys that fold together across layers
// would leave the survivor to luck.
func validateEnv(label string, env map[string]string) error {
	return lib.ValidateEnv(label, env, reservedEnvPrefix)
}

// weakEnvString renders a scalar the way the weakly typed decode would, which
// is also what a generic map's key becomes on the way into the tree, so the
// two renderings can never disagree.
func weakEnvString(v any) string { return lib.WeakScalarString(v) }
