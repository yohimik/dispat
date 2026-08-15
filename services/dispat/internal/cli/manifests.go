package cli

// Command-line syntax for the `dispat writer` edits and the `dispat replacer`
// replacements. It lives here rather than in the app package so the
// application never sees a flag spelling: the controller turns text into
// pkg/writer values, and a malformed spec is a usage error like any other bad
// command line.

import (
	"fmt"
	"strings"

	"github.com/yohimik/dispat/pkg/manifest"
	"github.com/yohimik/dispat/pkg/writer"
)

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
		// Only a non-empty kind word claims the prefix, which is what lets a
		// Maven `group:artifact` coordinate keep its colon.
		if k, isKind := manifest.ParseKind(prefix); isKind && prefix != "" {
			kind, name = k, rest
		}
	}
	if name == "" {
		return writer.Edit{}, fmt.Errorf("--set %q: no dependency name", spec)
	}
	if rng == "" {
		return writer.Edit{}, fmt.Errorf("--set %q: no version range (to remove a redirect use --link)", spec)
	}
	return writer.Edit{Name: name, Kind: kind, Range: rng}, nil
}

// parseLinkSpec reads one `--link` value: `name=path`, where an empty
// path removes the redirect and lets the declaration resolve normally again.
func parseLinkSpec(spec string) (writer.Link, error) {
	name, path, ok := strings.Cut(spec, "=")
	if !ok {
		return writer.Link{}, fmt.Errorf("--link %q: want name=path (an empty path removes the redirect)", spec)
	}
	if name == "" {
		return writer.Link{}, fmt.Errorf("--link %q: no dependency name", spec)
	}
	return writer.Link{Name: name, Path: path}, nil
}

// replaceSeparator joins the two halves of a `--replace` value. It is two characters
// rather than "=" because both halves are arbitrary literal text and a version
// string carries "=" often enough (">=1.0", "VERSION=1.2.3") that splitting on
// it would refuse ordinary replacements.
const replaceSeparator = "=>"

// parseReplaceSpec reads one `--replace` value: `find=>write`.
//
// The split is at the first separator, so a "=>" inside the replacement text
// survives; a "=>" inside the text being searched for cannot be expressed, and
// says so rather than silently truncating.
func parseReplaceSpec(spec string) (writer.Replacement, error) {
	find, write, ok := strings.Cut(spec, replaceSeparator)
	if !ok {
		return writer.Replacement{}, fmt.Errorf("--replace %q: want find%swrite", spec, replaceSeparator)
	}
	if find == "" {
		return writer.Replacement{}, fmt.Errorf("--replace %q: no text to find", spec)
	}
	return writer.Replacement{Find: find, Write: write}, nil
}

// parseReplaceSpecs reads every `--replace` value of one invocation, keeping the order
// they were given in, which is the order they apply in.
func parseReplaceSpecs(specs []string) ([]writer.Replacement, error) {
	reps := make([]writer.Replacement, 0, len(specs))
	for _, spec := range specs {
		rep, err := parseReplaceSpec(spec)
		if err != nil {
			return nil, err
		}
		reps = append(reps, rep)
	}
	return reps, nil
}

// parseEditSpecs reads every `--set` and `--link` value of one invocation,
// reporting the first malformed one.
func parseEditSpecs(sets, links []string) (edits []writer.Edit, out []writer.Link, err error) {
	for _, spec := range sets {
		edit, err := parseEditSpec(spec)
		if err != nil {
			return nil, nil, err
		}
		edits = append(edits, edit)
	}
	for _, spec := range links {
		link, err := parseLinkSpec(spec)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, link)
	}
	return edits, out, nil
}
