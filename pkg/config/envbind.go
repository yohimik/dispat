package config

// Binding process environment variables onto configuration keys, for the
// programs that want it.
//
// It is opt-in and it is closed: a binding names the keys it will accept, and
// a variable that answers to none of them either says nothing or is a typo,
// depending on how strict the caller asked to be. That is the whole difference
// from the usual automatic binding, where any variable at all may or may not
// have set something and nobody can tell which.
//
// The derivation runs one way, from a declared key to a variable name, and
// never the other. Going the other way — splitting a variable name back into
// key levels — is where an automatic binding has to guess whether LOG_LEVEL is
// `log.level` or `logLevel`, and the guess is wrong for somebody. Here the key
// is given, so the name it derives is a fact rather than an inference, and the
// key lands in the override map spelled exactly as the caller declared it.

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
)

// EnvBinding is a set of configuration keys and the process environment they
// may be set from.
//
// The zero value binds nothing: without Keys there is nothing to bind, which
// is the safe direction for a feature that reads the whole environment.
type EnvBinding struct {
	// Prefix is the namespace the binding owns, conventionally ending in an
	// underscore: "APP_". It is what keeps a configuration key from being set
	// by a variable that was always about something else — PATH, HOME, TERM.
	//
	// An empty prefix binds unprefixed names, which is a choice a caller can
	// make and should think about first: Strict then reports every variable in
	// the environment as unmatched, so the two do not go together.
	Prefix string

	// Keys are the configuration key paths that may be bound, spelled the way
	// the caller wants them to land in the Overrides — a nested key with the
	// delimiter in it, as everywhere else. A key not named here cannot be set
	// from the environment at all.
	Keys []string

	// Bind derives the variable name for a key. A nil Bind uses EnvVarName,
	// which upper-cases the key and turns the delimiter and any dash into an
	// underscore. Replace it for a program whose variables were named before
	// its config keys were.
	Bind func(prefix, key string) string

	// Environ is the environment, as KEY=value pairs. Zero value: os.Environ().
	// A non-nil empty slice is an empty environment, which is what a test
	// wants.
	Environ []string

	// Strict makes a variable carrying Prefix that no declared key answers to
	// an error rather than a warning. It is what turns a typo in a deployment
	// manifest into a failure at startup instead of a setting that silently
	// never applied.
	Strict bool

	// KeyDelim is the separator Keys spell a nested path with. Zero value:
	// DefaultKeyDelim. It must be the delimiter the Overrides will be applied
	// with, since that is what the paths mean.
	KeyDelim string
}

// EnvVarName is the default derivation: the prefix, then the key upper-cased
// with the delimiter and any dash turned into an underscore.
//
//	EnvVarName("APP_", "log.level", ".") == "APP_LOG_LEVEL"
//	EnvVarName("APP_", "logLevel", ".")  == "APP_LOGLEVEL"
//
// It is deliberately not clever about camel case: a program whose keys are
// spelled `logLevel` and whose variables are spelled `APP_LOG_LEVEL` says so
// through Bind, rather than relying on a rule that would also split `URLs` in
// a place nobody wants.
func EnvVarName(prefix, key, delim string) string {
	name := key
	if delim != "" {
		name = strings.ReplaceAll(name, delim, "_")
	}
	name = strings.ReplaceAll(name, "-", "_")
	return prefix + strings.ToUpper(name)
}

// Overrides reads the environment and returns the values it sets, ready to be
// merged under whatever the caller sets explicitly:
//
//	ov, err := binding.Overrides(ctx)
//	settings := tree.Settings(loader, config.MergeOverrides(ov, flags))
//
// which is the precedence the layering wants: the file underneath, the
// environment over it, and what the operator typed on the command line over
// both.
//
// A variable set to the empty string is a value — the empty string — because
// unsetting a variable and setting it to nothing are two different things a
// deployment does on purpose. A variable that is not set at all leaves its key
// alone.
//
// The error is Strict's: a variable carrying the prefix that no declared key
// answers to. Without Strict those are logged as warnings and the load goes
// on.
func (b EnvBinding) Overrides(ctx context.Context) (Overrides, error) {
	log := GetLogger(ctx)
	delim := b.KeyDelim
	if delim == "" {
		delim = DefaultKeyDelim
	}
	bind := b.Bind
	if bind == nil {
		bind = func(prefix, key string) string { return EnvVarName(prefix, key, delim) }
	}

	// The declared keys, by the variable name each derives. Two keys deriving
	// one name is a binding nobody can read, so it is refused rather than
	// resolved by whichever came first.
	keys := make(map[string]string, len(b.Keys))
	for _, key := range b.Keys {
		name := bind(b.Prefix, key)
		if prev, dup := keys[name]; dup {
			return nil, fmt.Errorf("env binding: keys %q and %q both bind %s", prev, key, name)
		}
		keys[name] = key
	}

	environ := b.Environ
	if environ == nil {
		environ = os.Environ()
	}

	out := make(Overrides, len(b.Keys))
	var unmatched []string
	for _, pair := range environ {
		name, value, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		key, bound := keys[name]
		if !bound {
			if strings.HasPrefix(name, b.Prefix) {
				unmatched = append(unmatched, name)
			}
			continue
		}
		out[key] = value
		if log.Enabled(LevelTrace) {
			log.Log(LevelTrace, EventEnvBind, Str("key", key), Str("var", name))
		}
	}

	if len(unmatched) > 0 {
		sort.Strings(unmatched) // one environment, one first complaint
		if b.Strict {
			return nil, fmt.Errorf("env binding: %s sets no configuration key; the %s variables that do are %s",
				unmatched[0], b.Prefix, strings.Join(SortedKeys(keys), ", "))
		}
		if log.Enabled(LevelWarn) {
			for _, name := range unmatched {
				log.Log(LevelWarn, EventEnvUnmatched, Str("var", name))
			}
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
