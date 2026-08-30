package config

// The flags that override a config key, as the loader's override layer.
//
// A flag the caller actually passed replaces what the file says; a flag left
// alone carries its default, and writing a default over a configured value
// would make every run look like it had been asked for the default.

import (
	"github.com/spf13/pflag"

	lib "github.com/yohimik/dispat/pkg/config"
)

// boundFlags are the config keys an explicitly set flag overrides, and the
// flags that override them.
var boundFlags = map[string]string{
	"concurrency": "concurrency",
	"logLevel":    "log-level",
	"logFormat":   "log-format",
}

// flagOverrides renders the flags the caller passed as the override layer the
// loader writes over the file. A nil flag set, and a flag set nobody touched,
// are both no overrides at all.
//
// The overlay replaces the file's spelling rather than sitting beside it —
// a file writing `logLevel` and an overlay writing `loglevel` would otherwise
// be two keys the decode refuses as a collision, over a flag the operator
// passed correctly — which is the rule pkg/config applies to every override.
func flagOverrides(flags *pflag.FlagSet) lib.Overrides {
	if flags == nil {
		return nil
	}
	var out lib.Overrides
	for key, flagName := range boundFlags {
		f := flags.Lookup(flagName)
		if f == nil || !f.Changed {
			continue
		}
		if out == nil {
			out = make(lib.Overrides, len(boundFlags))
		}
		out[key] = flagValue(f)
	}
	return out
}

// flagValue renders an explicitly set flag as the value the decode reads. A
// list-valued flag hands over its elements rather than its printed form:
// --concurrency 4,2 prints as "[4,2]", which is a string no list field can be
// weakly typed out of.
func flagValue(f *pflag.Flag) any {
	if sv, ok := f.Value.(pflag.SliceValue); ok {
		return sv.GetSlice()
	}
	return f.Value.String()
}
