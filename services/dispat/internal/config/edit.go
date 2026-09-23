package config

// Preparing edits to the config file that holds each key. The rendering and
// format-preserving round trips are pkg/config's; the caller owns the later
// atomic write and backup.

import (
	"context"

	"github.com/pelletier/go-toml/v2"

	lib "github.com/yohimik/dispat/pkg/config"
)

// The refusals a caller has to tell apart, under this package's names.
var (
	// ErrTOMLEdit reports that the config is TOML, which dispat cannot rewrite
	// format-preservingly; the caller renders a paste-ready snippet instead.
	ErrTOMLEdit = lib.ErrTOMLEdit
	// ErrRefEdit reports a key whose value is composed from a referenced file
	// and the keys written beside the reference. There is no single file the
	// new value could be written to, so the caller explains the two ways out
	// rather than picking one.
	ErrRefEdit = lib.ErrRefEdit
	// ErrMultiRefEdit reports a key whose value is merged from several
	// referenced files. No one of them holds the value, so the caller explains
	// the ways out rather than choosing a file to write to.
	ErrMultiRefEdit = lib.ErrMultiRefEdit
)

// BackupSuffix is appended to the config file name for the pre-edit copy.
const BackupSuffix = lib.BackupSuffix

// Edit is one key of a config file and the value to write there.
type Edit = lib.Edit

// PreparedEdit is one file's fully rendered replacement, ready to commit.
type PreparedEdit = lib.PreparedEdit

// PrepareKeys renders every edit against the file's current bytes without
// writing anything. The caller owns the later atomic write and backup.
func PrepareKeys(path string, edits []Edit) (*PreparedEdit, error) {
	return lib.PrepareEdits(context.Background(), path, edits)
}

// RenderDependenciesTOML renders the dependencies as a `dependencies` table
// keyed by consumer — the paste-ready fallback for TOML configs, in the same
// consumer-keyed form the JSON and YAML writers splice in.
//
// Every provider is written as a table, even one carrying nothing but its
// name. The other two formats shorten that to the bare name, but an array
// holding a name beside a table is not something a TOML encoder will write,
// and one uniform shape beats a rendering that fails on the configs that most
// need the fallback.
func RenderDependenciesTOML(deps Dependencies) (string, error) {
	grouped := deps.Grouped()
	rows := make(map[string][]map[string]any, len(grouped))
	for consumer, providers := range grouped {
		for _, p := range providers {
			// Lowercase keys, because DependencyConfig has no toml tags and
			// the file's keys are the json ones.
			row := map[string]any{"provider": p.Provider}
			if p.Kind != "" {
				row["kind"] = p.Kind
			}
			if p.Keep {
				row["keep"] = true
			}
			if p.External {
				row["external"] = true
			}
			rows[consumer] = append(rows[consumer], row)
		}
	}
	out, err := toml.Marshal(map[string]any{"dependencies": rows})
	return string(out), err
}

// RenderKeyTOML renders one value nested under its key path — the paste-ready
// fallback for TOML configs, used for a package-level dependency list and for
// the initials map alike.
func RenderKeyTOML(keyPath []string, value any) (string, error) {
	return lib.RenderKeyTOML(keyPath, value)
}

// ResolveEdit reports which file holds a key, and under which key path there.
//
// A `$ref` crossed on the way down moves the edit into the file it names, with
// the rest of the path: a configuration split across files is written where
// each key is written, and the reference itself survives the write. A key
// written beside a reference keeps the edit in the file that wrote it, which
// is the same rule the loader reads those keys by.
//
// The returned key path is empty when the key *is* the reference: the whole
// referenced document is the value, so that file is rewritten rather than
// spliced. A key no file holds comes back unchanged, for the writers to create
// or refuse as they already do.
func ResolveEdit(path string, keyPath []string) (string, []string, error) {
	return loader.ResolveEdit(context.Background(), path, keyPath)
}

// StringMapAt reads the string map at keyPath of the config file at path,
// exactly as the file spells it. It exists because the loaded *File is a
// merged, validated view rather than the file: a write has to start from what
// the file holds, key for key, so the entries it already carries come back
// untouched. A key the file does not carry is no error and returns a nil map;
// a value that is not a map of scalars is.
//
// Unlike the writers this reads TOML too: a TOML config still needs its
// current entries to render the paste-ready block.
func StringMapAt(path string, keyPath []string) (map[string]string, error) {
	return loader.StringMapAt(context.Background(), path, keyPath)
}
