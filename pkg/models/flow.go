package models

import (
	"encoding/json"
	"fmt"
	"strings"
)

// This file is the shape side of the `flow` key: every form an entry may be
// written in, the stages a configuration adds to the built-in ones, and the
// one shape the object is written back in.
//
// A flow entry began as a script name or a list of them, and that is still
// what almost every entry is. An entry may also be an object, which is how a
// stage gets a label and a place in the order: `script` is the list the
// string form stands for, `label` is what the logs and the events show, and
// `before`, `after` and `parallel` each name another stage of the same
// package. A key that is neither a built-in stage nor a hook defines a stage
// of that name, with `before<Name>` and `post<Name>` hooks like the built-in
// ones. Everything downstream of decoding sees typed fields and never learns
// which form the file used.

// StageConfig is the object form of one flow stage entry.
//
// For a built-in stage the script half lives in the typed field the key has
// always had (SpaceFlowConfig.Build and its siblings), so decoding leaves
// Script empty there and only the label and the constraints are carried
// here; a Script a Go caller sets there is written as the stage's scripts
// when the typed field is unset. For a stage the configuration defines,
// Script is the stage's own commands.
type StageConfig struct {
	// Script is the script name or names the stage runs, in order: what the
	// string form of the entry states on its own.
	Script []string `json:"script,omitempty"`
	// Label is the name the logs, the events and the summary show for the
	// stage. DISPAT_STAGE and the hook names keep the key.
	Label string `json:"label,omitempty"`
	// Before and After place the stage against other stages of the same
	// package, which drops its default place; Parallel names a stage it may
	// run at once with and leaves it in its default place. A nil list says
	// nothing; an empty list clears an inherited one.
	Before   []string `json:"before,omitempty"`
	After    []string `json:"after,omitempty"`
	Parallel []string `json:"parallel,omitempty"`
}

// IsPlaced reports whether the stage states its own place: a before or an
// after. A parallel constraint alone leaves the stage in its default place.
// The resolver decides placement after dropping the built-in stages a package
// lacks, so a stage placed here may still keep its default place there.
func (c StageConfig) IsPlaced() bool {
	return len(c.Before) > 0 || len(c.After) > 0
}

// The built-in stages a flow may place: the ones a package runs in its default
// order between its preparation and its records.
const (
	FlowStageBuild    = "build"
	FlowStageTest     = "test"
	FlowStagePublish  = "publish"
	FlowStageAnnounce = "announce"
)

// The preparing stages: they rewrite the files every later stage reads, so the
// engine runs them before every other stage whatever the flow says.
const (
	FlowStageSign      = "sign"
	FlowStageVersion   = "version"
	FlowStagePropagate = "propagate"
	FlowStageSyncLock  = "syncLock"
)

// FlowStageRecord is the name of a package's release records in its stage
// order: the tag, the changelog entry and the GitHub release, written as one
// step after the publish. A flow may order a stage against it only through
// the publish, and may not define a stage of this name.
const FlowStageRecord = "record"

// flowStageKeys are the stage entries an object form may be written for, in
// the order the stages run by default.
var flowStageKeys = []string{
	FlowStageSign, FlowStageVersion, FlowStagePropagate,
	FlowStageBuild, FlowStageTest, FlowStagePublish, FlowStageAnnounce,
}

// FlowEntryKeys lists every key the `flow` object has a typed field for, as
// the json tags spell them: the built-in stages, the login, and the hooks.
func FlowEntryKeys() []string {
	return append([]string(nil), flowEntryOrder...)
}

// flowEntryOrder is the typed entries in the order the object is written
// back in.
var flowEntryOrder = []string{
	"build", "test", "publish", "version", "propagate", "sign", "login", "announce",
	"beforeAll", "beforeSign", "postSign", "beforeVersion", "postVersion",
	"beforePropagate", "postPropagate", "beforeBuild", "postBuild", "beforeTest", "postTest",
	"beforePublish", "postPublish", "beforeAnnounce", "postAnnounce", "onFail", "onSkip",
}

// entryFields maps each typed entry's folded key onto the field holding it.
func (f *SpaceFlowConfig) entryFields() map[string]*[]string {
	return map[string]*[]string{
		"build": &f.Build, "test": &f.Test, "publish": &f.Publish,
		"version": &f.Version, "propagate": &f.Propagate, "sign": &f.Sign,
		"login": &f.Login, "announce": &f.Announce,
		"beforeall":  &f.BeforeAll,
		"beforesign": &f.BeforeSign, "postsign": &f.PostSign,
		"beforeversion": &f.BeforeVersion, "postversion": &f.PostVersion,
		"beforepropagate": &f.BeforePropagate, "postpropagate": &f.PostPropagate,
		"beforebuild": &f.BeforeBuild, "postbuild": &f.PostBuild,
		"beforetest": &f.BeforeTest, "posttest": &f.PostTest,
		"beforepublish": &f.BeforePublish, "postpublish": &f.PostPublish,
		"beforeannounce": &f.BeforeAnnounce, "postannounce": &f.PostAnnounce,
		"onfail": &f.OnFail, "onskip": &f.OnSkip,
	}
}

// IsBuiltInFlowStage reports whether a folded key names a built-in stage
// entry, which is an entry the object form may be written for.
func IsBuiltInFlowStage(key string) bool {
	for _, stage := range flowStageKeys {
		if strings.EqualFold(stage, key) {
			return true
		}
	}
	return false
}

// IsFixedFlowStage reports whether a stage name is one of the preparing
// stages, which the engine keeps before every other stage.
func IsFixedFlowStage(name string) bool {
	switch strings.ToLower(name) {
	case FlowStageSign, FlowStageVersion, FlowStagePropagate, strings.ToLower(FlowStageSyncLock):
		return true
	}
	return false
}

// IsFlowStageName reports whether a word can name a stage a configuration
// defines: letters only, starting with a lower-case one, so that
// `before<Name>` and `post<Name>` spell its hooks and a node can read it back
// as a stage.
func IsFlowStageName(name string) bool {
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

// FlowHookStage answers the stage a hook key brackets and which side it is
// on: `beforeLint` is the before hook of `lint`, `postLint` its post hook. A
// key that is not spelled as a hook answers nothing.
func FlowHookStage(key string) (stage string, isBefore, ok bool) {
	for _, prefix := range []string{"before", "post"} {
		rest, found := strings.CutPrefix(key, prefix)
		if !found || rest == "" || rest[0] < 'A' || rest[0] > 'Z' {
			continue
		}
		return strings.ToLower(rest[:1]) + rest[1:], prefix == "before", true
	}
	return "", false, false
}

// FlowStageTitle is a stage name as it appears inside its hook names:
// `beforeLint` and `postLint` for `lint`.
func FlowStageTitle(name string) string {
	if name == "" {
		return ""
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// FlowListReader reads one entry's script list from the value the file wrote
// under it. The two readers of the config language differ exactly here: the
// CLI lifts a scalar into a one-element list the way it does for every list
// key, and a program decoding the public type through encoding/json takes a
// string or an array and nothing else.
type FlowListReader func(val any, where string) ([]string, error)

// NormalizeFlow reads one `flow` object into the typed configuration: the
// typed entries under their keys, the object form of a stage entry split into
// its script half and its stage half, and every other key as a stage the
// configuration defines or a hook of one. where names the object for error
// messages, since the same object is read at four levels.
//
// It is the single implementation behind both entry points, UnmarshalJSON
// here and the CLI's config reader, so a file and the public type cannot come
// to disagree about what a flow is. Keys are matched folded, like every key
// of the config language, and visited in sorted order so an object with more
// than one thing wrong with it reports the same one first on every run.
func NormalizeFlow(raw map[string]any, where string, readList FlowListReader) (*SpaceFlowConfig, error) {
	out := &SpaceFlowConfig{}
	entries := out.entryFields()
	seen := make(map[string]string, len(raw))
	for _, key := range sortedMapKeys(raw) {
		fold := strings.ToLower(key)
		replacement := ""
		switch fold {
		case "tests":
			replacement = "test"
		case "beforetests":
			replacement = "beforeTest"
		case "posttests":
			replacement = "postTest"
		}
		if replacement != "" {
			return nil, fmt.Errorf("%s.%s: use flow.%s; the optional stage is named by the verb test",
				where, key, replacement)
		}
		if first, isDuplicate := seen[fold]; isDuplicate {
			return nil, fmt.Errorf("%s: keys %q and %q are one name written twice", where, first, key)
		}
		seen[fold] = key
		value := raw[key]
		if value == nil {
			continue
		}
		at := where + "." + key
		if field, isTyped := entries[fold]; isTyped {
			if err := out.readTypedEntry(fold, field, value, at, readList); err != nil {
				return nil, err
			}
			continue
		}
		if err := out.readUserEntry(key, value, at, readList); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// readTypedEntry fills one entry the type has a field for. Only a stage entry
// has an object form; a hook, the login and the outcome scripts are lists and
// nothing else.
func (f *SpaceFlowConfig) readTypedEntry(fold string, field *[]string, value any, at string,
	readList FlowListReader) error {
	object, isObject := stringKeyed(value)
	if !isObject || !IsBuiltInFlowStage(fold) {
		list, err := readList(value, at)
		if err != nil {
			return err
		}
		*field = list
		return nil
	}
	stage, err := readStageObject(object, at, readList)
	if err != nil {
		return err
	}
	if stage.Script != nil {
		*field = stage.Script
		stage.Script = nil
	}
	f.setStage(fold, stage)
	return nil
}

// readUserEntry fills one entry the type has no field for: a hook of a stage
// the configuration defines, or that stage itself.
func (f *SpaceFlowConfig) readUserEntry(key string, value any, at string, readList FlowListReader) error {
	if stage, _, isHook := FlowHookStage(key); isHook {
		if !IsFlowStageName(stage) {
			return fmt.Errorf("%s: %q is not a stage a hook can bracket; a stage is named in letters, starting with a lower-case one", at, stage)
		}
		list, err := readList(value, at)
		if err != nil {
			return err
		}
		if f.StageHooks == nil {
			f.StageHooks = make(map[string][]string)
		}
		f.StageHooks[key] = list
		return nil
	}
	if !IsFlowStageName(key) {
		return fmt.Errorf("%s: %q is not a stage or hook name; a stage is named in letters, starting with a lower-case one", at, key)
	}
	if IsFixedFlowStage(key) {
		return fmt.Errorf("%s: %s is the lock-file stage, configured under autoVersion.syncLock", at, key)
	}
	if key == FlowStageRecord {
		return fmt.Errorf("%s: %s is the package's release records, which every flow has and none defines", at, key)
	}
	object, isObject := stringKeyed(value)
	if !isObject {
		list, err := readList(value, at)
		if err != nil {
			return err
		}
		f.setStage(key, StageConfig{Script: list})
		return nil
	}
	stage, err := readStageObject(object, at, readList)
	if err != nil {
		return err
	}
	f.setStage(key, stage)
	return nil
}

// setStage records one stage entry's object half under the key as written.
func (f *SpaceFlowConfig) setStage(name string, stage StageConfig) {
	if f.Stages == nil {
		f.Stages = make(map[string]StageConfig)
	}
	f.Stages[name] = stage
}

// readStageObject reads the object form of a stage entry.
func readStageObject(object map[string]any, at string, readList FlowListReader) (StageConfig, error) {
	var out StageConfig
	for _, key := range sortedMapKeys(object) {
		value := object[key]
		if value == nil {
			continue
		}
		path := at + "." + key
		var err error
		var refs []string
		switch strings.ToLower(key) {
		case "script":
			out.Script, err = readList(value, path)
		case "label":
			label, isString := value.(string)
			if !isString {
				return out, fmt.Errorf("%s: wants a string", path)
			}
			out.Label = label
		case "before":
			out.Before, err = readList(value, path)
			refs = out.Before
		case "after":
			out.After, err = readList(value, path)
			refs = out.After
		case "parallel":
			out.Parallel, err = readList(value, path)
			refs = out.Parallel
		default:
			return out, fmt.Errorf("%s: unknown key %q; a stage entry names script, label, before, after and parallel", at, key)
		}
		if err != nil {
			return out, err
		}
		for _, target := range refs {
			if strings.EqualFold(target, "tests") {
				return out, fmt.Errorf("%s: stage constraint %q was renamed to test", path, target)
			}
		}
	}
	return out, nil
}

// readFlowList is the public type's own list reader: a string or an array of
// strings, as encoding/json would read a []string, plus the bare string a flow
// entry has always been allowed to be in a file.
func readFlowList(val any, where string) ([]string, error) {
	switch x := val.(type) {
	case string:
		return []string{x}, nil
	case []any:
		out := make([]string, 0, len(x))
		for i, item := range x {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s[%d]: wants a script name", where, i)
			}
			out = append(out, s)
		}
		return out, nil
	case []string:
		return x, nil
	}
	return nil, fmt.Errorf("%s: wants a script name, an array of names, or a stage object", where)
}

// UnmarshalJSON accepts every form a flow entry may be written in.
func (f *SpaceFlowConfig) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out, err := NormalizeFlow(raw, "flow", readFlowList)
	if err != nil {
		return err
	}
	*f = *out
	return nil
}

// MarshalJSON writes the shortest shape that carries everything the flow
// says: a list for an entry that is only its scripts, and the object form for
// a stage that carries a label or a constraint. A stage the configuration
// defines is written under its own key, and its hooks under theirs.
func (f SpaceFlowConfig) MarshalJSON() ([]byte, error) { return json.Marshal(f.canonical()) }

// MarshalYAML is MarshalJSON's counterpart for a YAML config.
func (f SpaceFlowConfig) MarshalYAML() (any, error) { return f.canonical(), nil }

// canonical renders the flow in the shape both marshallers write: the typed
// entries under their keys, the defined stages under theirs and their hooks
// under theirs. The marshallers write a map's keys sorted, so the rendering is
// the same for the same flow.
func (f SpaceFlowConfig) canonical() map[string]any {
	entries := f.entryFields()
	out := make(map[string]any)
	for _, key := range flowEntryOrder {
		list := *entries[strings.ToLower(key)]
		stage, hasStage := f.Stages[key]
		if list == nil {
			list = stage.Script
		}
		if list == nil && !hasStage {
			continue
		}
		out[key] = canonicalStageEntry(list, stage)
	}
	for name, stage := range f.Stages {
		if _, isTyped := entries[strings.ToLower(name)]; isTyped {
			continue
		}
		out[name] = canonicalStageEntry(stage.Script, StageConfig{
			Label: stage.Label, Before: stage.Before, After: stage.After, Parallel: stage.Parallel})
	}
	for key, hook := range f.StageHooks {
		out[key] = hook
	}
	return out
}

// canonicalStageEntry is one stage entry's shortest rendering: the list alone
// when the stage carries nothing else, the object otherwise.
func canonicalStageEntry(script []string, stage StageConfig) any {
	if stage.Label == "" && stage.Before == nil && stage.After == nil && stage.Parallel == nil {
		if script == nil {
			return []string{}
		}
		return script
	}
	stage.Script = script
	return stage
}
