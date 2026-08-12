package config

// The static `env` objects and their one complication: viper lowercases every
// map key when it reads a config file. That is harmless — useful, even — for
// every other map in the model, because script names, spaces, packages and
// initials all match case-insensitively. Environment variable names do not:
// PATH and Path are two variables. So the env objects alone are decoded a
// second time with the file format's own parser, which keeps the spelling,
// and the exact-case maps replace the lowercased ones at every level that can
// hold one.
//
// There are five such levels, and they merge key by key with the most local
// winning:
//
//	the root file's `env`
//	  the root file's `spaces.<space>.env`
//	    the space folder's own config file `env`
//	      the root file's `packages.<pkg>.env` and `spaces.<s>.packages.<p>.env`
//	        the package folder's own config file `env`
//
// The root file's four paths are restored in one pass by restoreEnvCase,
// which parses the file once; the two in-folder files are restored by
// envCaseFromFile as they are loaded.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
	yaml "gopkg.in/yaml.v3"
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
// own, or colliding case-insensitively — viper folds two such keys into one
// before the exact-case pass can tell them apart, so the survivor would be
// whichever the file happened to write last. Keys are checked in sorted order
// so a config with several mistakes always reports the same one first.
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

// envRestorer re-reads one config file with the format's own parser and hands
// back the env objects it holds with their keys spelled as written. The parsed
// tree is kept, so a file carrying env at four levels is read and parsed once
// rather than four times.
type envRestorer struct {
	path string
	tree map[string]any
}

// newEnvRestorer parses path into a generic tree.
func newEnvRestorer(path string) (*envRestorer, error) {
	tree, err := decodeFileTree(path)
	if err != nil {
		return nil, err
	}
	return &envRestorer{path: path, tree: tree}, nil
}

// envAt returns the exact-case env object at a key path ("env" — or "spaces",
// "libs", "env"), or nil when the path holds no object. Names along the path
// are matched case-insensitively with the package's own lookupFold: the caller
// looks them up with the lowercased keys viper produced, while this tree came
// from the raw file, where a space may well be spelled "Libs".
func (r *envRestorer) envAt(path ...string) map[string]string {
	node := r.tree
	for _, name := range path[:len(path)-1] {
		child, ok := lookupFold(node, name)
		if !ok {
			return nil
		}
		if node, ok = child.(map[string]any); !ok {
			return nil
		}
	}
	raw, ok := lookupFold(node, path[len(path)-1])
	if !ok {
		return nil
	}
	return envFromTree(raw)
}

// restoreEnvCase replaces the root config's viper-lowercased env maps with
// exact-case ones read straight from the file, at all four levels the root
// file can hold one. A file that configures no env at all is never re-read,
// so the second parse is a cost only the feature's users pay.
func restoreEnvCase(path string, cfg *File) error {
	if !hasAnyEnv(cfg) {
		return nil
	}
	r, err := newEnvRestorer(path)
	if err != nil {
		return err
	}
	cfg.Env = r.envAt("env")
	for name, sc := range cfg.Spaces {
		sc.Env = r.envAt("spaces", name, "env")
		for pkg, pc := range sc.Packages {
			pc.Env = r.envAt("spaces", name, "packages", pkg, "env")
			sc.Packages[pkg] = pc
		}
		cfg.Spaces[name] = sc
	}
	for name, pc := range cfg.Packages {
		pc.Env = r.envAt("packages", name, "env")
		cfg.Packages[name] = pc
	}
	return nil
}

// hasAnyEnv reports whether the decoded config configures env anywhere the
// root file could hold it. It reads the lowercased maps, which is fine: it
// only decides whether the exact-case pass is worth running.
func hasAnyEnv(cfg *File) bool {
	if len(cfg.Env) > 0 {
		return true
	}
	for _, s := range cfg.Spaces {
		if len(s.Env) > 0 {
			return true
		}
		for _, p := range s.Packages {
			if len(p.Env) > 0 {
				return true
			}
		}
	}
	for _, p := range cfg.Packages {
		if len(p.Env) > 0 {
			return true
		}
	}
	return false
}

// envCaseFromFile reads an in-folder config file and returns its top-level env
// object with exact key case — the space-folder and package-folder
// counterpart of restoreEnvCase.
func envCaseFromFile(path string) (map[string]string, error) {
	r, err := newEnvRestorer(path)
	if err != nil {
		return nil, err
	}
	return r.envAt("env"), nil
}

// decodeFileTree parses a config file into a generic tree with the format's
// own parser, preserving key case.
func decodeFileTree(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	tree := map[string]any{}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		err = json.Unmarshal(data, &tree)
	case ".yaml", ".yml":
		err = yaml.Unmarshal(data, &tree)
	case ".toml":
		err = toml.Unmarshal(data, &tree)
	default:
		return nil, fmt.Errorf(
			"%s: env requires a json, yaml or toml config file (environment variable names are case-sensitive, and only these formats can be re-read case-exactly)",
			path)
	}
	if err != nil {
		return nil, fmt.Errorf("re-reading %s for env key case: %w", path, err)
	}
	return tree, nil
}

// envFromTree converts a raw env object to map[string]string with the same
// weak typing viper's decode applies, so a bare 1 or true is a fine value and
// means the same thing whichever pass read it.
func envFromTree(v any) map[string]string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, val := range m {
		out[k] = weakEnvString(val)
	}
	return out
}

// weakEnvString renders a scalar the way viper's weakly typed decode would.
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
