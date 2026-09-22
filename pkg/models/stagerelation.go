package models

import (
	"encoding/json"
	"fmt"
	"strings"
)

// This file is the value side of the `isBuildWaitingPublish` key: the shapes
// it may be written in, and the one shape it is written back in.
//
// The key began as a boolean, and a boolean can only answer the question it
// was asked: does a consumer's build wait for its provider's publish. Two
// deliverables released from one repository often need the other answer as
// well. A Terraform stack and the front end deployed onto it are built from
// the same checkout with nothing passing between the two builds, so the builds
// may run side by side; what must still follow the provider is the deployment.
// Saying that takes two values rather than one, so the key additionally
// accepts an object. Everything downstream of decoding sees a relation and
// never learns which form the file used.

// What a consumer's version and build stage waits for on each of its changed
// providers. The three values are a chain rather than a set: waiting for a
// provider's publish implies waiting for its build, because a provider
// publishes what it built.
type StageWait string

const (
	// StageWaitNone starts a consumer's build without waiting for the provider
	// at all. It fits a deploy-order relation, where nothing the provider's
	// build or publish produces reaches the consumer's build and only the
	// publications must follow one another. dispat cannot check that claim, so
	// the value is never inferred and never a default.
	StageWaitNone StageWait = "none"
	// StageWaitBuild waits for the provider's build, which is what a consumer
	// reading the provider's local build output needs. It is what
	// `isBuildWaitingPublish: false` has always meant, and the relation a
	// package that states nothing is under.
	StageWaitBuild StageWait = "build"
	// StageWaitPublish waits for the provider's publish, which is what a
	// consumer resolving the provider from a registry needs: a lock file
	// regenerated against the new version, an image built FROM a published
	// one. It is what `isBuildWaitingPublish: true` has always meant.
	StageWaitPublish StageWait = "publish"
)

// StageRelation is the value of `isBuildWaitingPublish`: what a provider's
// space imposes on the consumers of its packages. It is read on the provider's
// side of every edge, because what a consumer may do with a provider is a
// property of what that provider produces.
//
// It carries two independent decisions, which is why the key outgrew a
// boolean. Build orders the consumer's version and build stage against the
// provider's stages. IsBlocking decides what a provider that failed or was
// skipped does to consumers that have a release reason of their own. A
// consumer's own publish waits for its providers' publishes under all three
// relations, and that never becomes a choice: publishing against a version
// that was never published is invalid however the builds were ordered.
type StageRelation struct {
	// Build is what a consumer's version and build stage waits for. It is
	// required in the object form: it is the whole of what the relation says
	// about the consumer's build, and an object leaving it out would state a
	// failure rule for a wait nobody named.
	Build StageWait `json:"build,omitempty"`
	// IsBlocking says whether a package of this space that failed or was
	// skipped skips its consumers unconditionally, outranking a release reason
	// of their own.
	//
	// It is a pointer because an unstated value is not the same under every
	// relation: it is false under `build`, which is what
	// `isBuildWaitingPublish: false` has always meant and what an existing
	// configuration must keep, and false under `none` as well, where a
	// consumer with work of its own publishes that work whatever became of the
	// provider. Under `publish` it is true and may not be written otherwise,
	// because the consumer's build takes the provider's publish as its input
	// and a publish that never happened leaves it nothing to build from.
	// Stating it true under `none` or `build` is the stricter opt-in, for a
	// space whose consumers are never meaningful on their own.
	IsBlocking *bool `json:"isBlocking,omitempty"`
}

// StageRelationOf renders the relation a bare `isBuildWaitingPublish` boolean
// states, and is the one place the two original values are written down: true
// is the publish relation, false is the build relation. The decoder and a
// program authoring a configuration in Go both go through it, so the boolean
// cannot come to mean two things.
func StageRelationOf(isBuildWaitingPublish bool) *StageRelation {
	if isBuildWaitingPublish {
		return &StageRelation{Build: StageWaitPublish}
	}
	return &StageRelation{Build: StageWaitBuild}
}

// ResolveBuildWait returns what a consumer's version and build stage waits
// for. Nil-safe, and a relation nobody stated is the one the key meant before
// it had any other value: the consumer waits for the provider's build.
func (r *StageRelation) ResolveBuildWait() StageWait {
	if r == nil || r.Build == "" {
		return StageWaitBuild
	}
	return r.Build
}

// IsProviderBlocking reports whether a provider under this relation skips its
// consumers unconditionally when it failed or was skipped. Nil-safe. Unstated,
// it is false under `none` and `build`: a consumer with a release reason of its
// own proceeds, because its own work is what it publishes. Under `publish` it
// is true, because the consumer's build takes the provider's publish as its
// input and no work of the consumer's own can substitute for an input that
// never existed.
func (r *StageRelation) IsProviderBlocking() bool {
	if r != nil && r.IsBlocking != nil {
		return *r.IsBlocking
	}
	return r.ResolveBuildWait() == StageWaitPublish
}

// MarshalJSON writes the shortest shape that carries the whole relation: the
// boolean whenever the relation is one of the two a boolean names, and the
// object otherwise, with a field that only restates a default omitted. A
// config that wrote a boolean goes back out as one, so a config dispat
// rewrites does not grow objects around the key it left alone.
func (r StageRelation) MarshalJSON() ([]byte, error) { return json.Marshal(r.canonical()) }

// MarshalYAML is MarshalJSON's counterpart for a YAML config.
func (r StageRelation) MarshalYAML() (any, error) { return r.canonical(), nil }

// canonical renders the relation in the shape both marshallers write.
func (r StageRelation) canonical() any {
	wait, isBlocking := r.ResolveBuildWait(), r.IsProviderBlocking()
	if wait == StageWaitBuild && !isBlocking {
		return false
	}
	if wait == StageWaitPublish && isBlocking {
		return true
	}
	out := map[string]any{"build": string(wait)}
	if isBlocking != (wait == StageWaitPublish) {
		out["isBlocking"] = isBlocking
	}
	return out
}

// UnmarshalJSON accepts both shapes the key may be written in. A null states
// nothing and leaves the relation alone, which is how encoding/json's own
// decoding of an absent value behaves.
func (r *StageRelation) UnmarshalJSON(data []byte) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out, err := NormalizeStageRelation(raw, "isBuildWaitingPublish")
	if err != nil {
		return err
	}
	if out != nil {
		*r = *out
	}
	return nil
}

// NormalizeStageRelation expands an `isBuildWaitingPublish` value into the
// relation everything downstream works with. where names the key for error
// messages, since the same key is read at four levels and the reader has to be
// told which one is wrong.
//
// It is the single implementation behind both entry points: UnmarshalJSON
// here, and the CLI's config reader, whose decode table hands over the object
// and lifts a scalar through its own weak typing first, exactly as it does for
// every other boolean key. Two readers of one syntax would be two syntaxes
// eventually.
func NormalizeStageRelation(raw any, where string) (*StageRelation, error) {
	switch x := raw.(type) {
	case nil:
		return nil, nil
	case bool:
		return StageRelationOf(x), nil
	}
	fields, isObject := stringKeyed(raw)
	if !isObject {
		return nil, fmt.Errorf("%s: wants true or false, or an object naming build and isBlocking", where)
	}
	return stageRelationObject(fields, where)
}

// stageRelationObject reads the object form. Keys are matched folded, like
// every key of the config language, and visited in sorted order for the reason
// the generic decoder sorts its own: an object with more than one thing wrong
// with it has to report the same one first on every run.
func stageRelationObject(fields map[string]any, where string) (*StageRelation, error) {
	var out StageRelation
	for _, key := range sortedMapKeys(fields) {
		value := fields[key]
		switch strings.ToLower(key) {
		case "build":
			wait, err := readStageWait(value, where)
			if err != nil {
				return nil, err
			}
			out.Build = wait
		case "isblocking":
			isBlocking, isBool := value.(bool)
			if !isBool {
				return nil, fmt.Errorf("%s: isBlocking wants true or false", where)
			}
			out.IsBlocking = &isBlocking
		default:
			return nil, fmt.Errorf(
				"%s: unknown key %q; a relation names build and isBlocking", where, key)
		}
	}
	if out.Build == "" {
		return nil, fmt.Errorf(
			"%s: build is required; it names what a consumer's build waits for: none, build or publish", where)
	}
	if out.Build == StageWaitPublish && out.IsBlocking != nil && !*out.IsBlocking {
		return nil, fmt.Errorf(
			"%s: build: publish cannot state isBlocking: false; the consumer's build takes the provider's "+
				"publish as input, so when that publish never happened the input does not exist and no release "+
				"reason of the consumer's own substitutes for it", where)
	}
	return &out, nil
}

// readStageWait reads the `build` value. It is matched exactly rather than
// folded, as logLevel, role and commitErrors are, so a misspelling is refused
// rather than guessed at.
func readStageWait(value any, where string) (StageWait, error) {
	name, isString := value.(string)
	if !isString {
		return "", fmt.Errorf("%s: build wants none, build or publish", where)
	}
	switch StageWait(name) {
	case StageWaitNone, StageWaitBuild, StageWaitPublish:
		return StageWait(name), nil
	}
	return "", fmt.Errorf("%s: build %q is not one of none, build or publish", where, name)
}
