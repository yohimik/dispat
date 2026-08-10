package cli

// Command-line syntax for the `dispat writer` edits. It lives here rather than
// in the app package so the application never sees a flag spelling: the
// controller turns text into pkg/writer values, and a malformed spec is a
// usage error like any other bad command line.

import (
	"fmt"
	"strings"

	"github.com/yohimik/dispat/pkg/manifest"
	"github.com/yohimik/dispat/pkg/writer"
)

// kindWords maps the spellings a `--set` spec may prefix a name with onto the
// manifest vocabulary. Only these four words are read as a kind, which is what
// lets a Maven `group:artifact` coordinate keep its colon.
var kindWords = map[string]manifest.Kind{
	"dependencies":         manifest.KindDependencies,
	"devDependencies":      manifest.KindDevDependencies,
	"peerDependencies":     manifest.KindPeerDependencies,
	"optionalDependencies": manifest.KindOptionalDependencies,
}

// parseEditSpec reads one `--set` value: `[kind:]name=range`.
//
// The name and the range split at the first "=", because a range legitimately
// contains one (">=1.0"), while a dependency name never does. The prefix
// before the first ":" is a kind only when it is one of the four kind words;
// anything else stays part of the name, so `--set com.acme:core=1.2.0` names
// the Maven artifact it looks like. Without a prefix the edit targets the
// plain dependencies field.
func parseEditSpec(spec string) (writer.Edit, error) {
	name, rng, ok := strings.Cut(spec, "=")
	if !ok {
		return writer.Edit{}, fmt.Errorf("--set %q: want [kind:]name=range", spec)
	}
	kind := manifest.KindDependencies
	if prefix, rest, found := strings.Cut(name, ":"); found {
		if k, isKind := kindWords[prefix]; isKind {
			kind, name = k, rest
		}
	}
	if name == "" {
		return writer.Edit{}, fmt.Errorf("--set %q: no dependency name", spec)
	}
	if rng == "" {
		return writer.Edit{}, fmt.Errorf("--set %q: no version range (to remove a redirect use --replace)", spec)
	}
	return writer.Edit{Name: name, Kind: kind, Range: rng}, nil
}

// parseReplaceSpec reads one `--replace` value: `name=path`, where an empty
// path removes the redirect and lets the declaration resolve normally again.
func parseReplaceSpec(spec string) (writer.Replacement, error) {
	name, path, ok := strings.Cut(spec, "=")
	if !ok {
		return writer.Replacement{}, fmt.Errorf("--replace %q: want name=path (an empty path removes the redirect)", spec)
	}
	if name == "" {
		return writer.Replacement{}, fmt.Errorf("--replace %q: no dependency name", spec)
	}
	return writer.Replacement{Name: name, Path: path}, nil
}

// parseEditSpecs reads every `--set` and `--replace` value of one invocation,
// reporting the first malformed one.
func parseEditSpecs(sets, replaces []string) (edits []writer.Edit, repls []writer.Replacement, err error) {
	for _, spec := range sets {
		edit, err := parseEditSpec(spec)
		if err != nil {
			return nil, nil, err
		}
		edits = append(edits, edit)
	}
	for _, spec := range replaces {
		repl, err := parseReplaceSpec(spec)
		if err != nil {
			return nil, nil, err
		}
		repls = append(repls, repl)
	}
	return edits, repls, nil
}
