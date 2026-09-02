package install

import (
	"fmt"
	"sort"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/globx"
	"github.com/yohimik/dispat/services/dispat/internal/selfupdate"
)

// Fields are what an asset pattern may name. They are the four things that
// differ between one release's asset and the next one's, and the reason a
// pattern beats a literal name: "gh_{version}_{os}_{arch}.tar.gz" keeps
// working across releases, and the name it expands to does not.
type Fields struct {
	OS      string
	Arch    string
	Version string
	Tag     string
	// Name is the repository's own name, which is what most projects call
	// their binary.
	Name string
}

// placeholders maps what a pattern may write to what it becomes. Listing them
// once is what lets an unknown one be refused by name rather than silently
// left in the middle of a filename nobody published.
func (f Fields) placeholders() map[string]string {
	return map[string]string{
		"os": f.OS, "arch": f.Arch, "version": f.Version, "tag": f.Tag, "name": f.Name,
	}
}

// Expand renders a pattern: {os}, {arch}, {version}, {tag} and {name} become
// the fields, and anything else in braces is a mistake worth naming.
//
// Refusing an unknown placeholder rather than leaving it in place is the whole
// value of the check: "{arch64}" would otherwise become part of a filename the
// release never carried, and the failure would read as a missing asset instead
// of as the typo it is.
func Expand(pattern string, f Fields) (string, error) {
	values := f.placeholders()
	var b strings.Builder
	b.Grow(len(pattern))
	rest := pattern
	for {
		before, after, ok := strings.Cut(rest, "{")
		b.WriteString(before)
		if !ok {
			return b.String(), nil
		}
		name, remainder, closed := strings.Cut(after, "}")
		if !closed {
			return "", fmt.Errorf("install: %q has a %q that is never closed", pattern, "{")
		}
		value, known := values[name]
		if !known {
			return "", fmt.Errorf("install: %q is not something an asset name can carry; it knows %s",
				"{"+name+"}", strings.Join(placeholderNames(values), ", "))
		}
		b.WriteString(value)
		rest = remainder
	}
}

// placeholderNames lists what a pattern may write, in a fixed order so the
// refusal reads the same every time.
func placeholderNames(values map[string]string) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, "{"+name+"}")
	}
	sort.Strings(names)
	return names
}

// DefaultAssetPattern is what a release is looked in for when nothing said
// which file to take: the repository's own name, the platform, and on Windows
// the extension a program has to carry. It is the convention this repository
// publishes under, and the one the Go release tooling around it produces, so a
// project following it needs no --asset at all.
const DefaultAssetPattern = "{name}-{os}-{arch}"

// DefaultAssetName renders that convention for a platform. The extension is
// appended exactly as selfupdate.AssetName appends it, because the two are the
// same convention seen from two sides: what dispat publishes for itself and
// what dispat looks for in somebody else's release.
//
// It renders the constant rather than spelling the same thing again, so the
// two cannot drift: the pattern is what the documentation quotes and what a
// reader would copy into --asset to say the default out loud.
func DefaultAssetName(f Fields) string {
	// The pattern is a literal this package owns, and Expand only fails on a
	// placeholder nothing defines, so this cannot be an error at runtime; a
	// hand-edit that made it one falls back to the pattern itself, which
	// matches no asset and is refused with the release's files listed.
	name, err := Expand(DefaultAssetPattern, f)
	if err != nil {
		return DefaultAssetPattern
	}
	if f.OS == "windows" {
		name += ".exe"
	}
	return name
}

// SelectAsset picks the file to download out of what the release carries.
//
// A pattern names it: expanded first, then matched by name, then as a glob, so
// "*linux-amd64*" reaches an asset whose exact spelling nobody wants to type.
//
// Without a pattern the choice has to be unambiguous. A release carrying one
// asset is that; a release carrying several is answered by the convention
// first, DefaultAssetName, matched exactly and never as a glob, so a project
// naming its binaries after its repository needs no flag. Only when the
// convention names nothing the release carries is the choice refused, and the
// refusal says which name was looked for as well as what is there: guessing
// which of nine files is the binary is how the wrong thing gets installed
// globally, and a reader who has to pick needs to know what dispat already
// tried.
func SelectAsset(rel selfupdate.Release, pattern string, f Fields) (selfupdate.Asset, error) {
	if len(rel.Assets) == 0 {
		return selfupdate.Asset{}, fmt.Errorf("install: %s carries no files to download", rel.Tag)
	}
	if pattern == "" {
		if len(rel.Assets) == 1 {
			return rel.Assets[0], nil
		}
		want := DefaultAssetName(f)
		for _, a := range rel.Assets {
			if a.Name == want {
				return a, nil
			}
		}
		return selfupdate.Asset{}, fmt.Errorf(
			"install: %s carries %d files and none is called %s, so --asset has to say which one: %s",
			rel.Tag, len(rel.Assets), want, strings.Join(rel.AssetNames(), ", "))
	}
	want, err := Expand(pattern, f)
	if err != nil {
		return selfupdate.Asset{}, err
	}
	for _, a := range rel.Assets {
		if a.Name == want {
			return a, nil
		}
	}
	var matched []selfupdate.Asset
	for _, a := range rel.Assets {
		if globx.Match(want, a.Name) {
			matched = append(matched, a)
		}
	}
	switch len(matched) {
	case 1:
		return matched[0], nil
	case 0:
		return selfupdate.Asset{}, fmt.Errorf("install: %s carries no %s: it has %s",
			rel.Tag, want, strings.Join(rel.AssetNames(), ", "))
	default:
		return selfupdate.Asset{}, fmt.Errorf("install: %s matches %d of %s's files: %s",
			want, len(matched), rel.Tag, strings.Join(names(matched), ", "))
	}
}

// names lists the assets a refusal has to name.
func names(assets []selfupdate.Asset) []string {
	out := make([]string, 0, len(assets))
	for _, a := range assets {
		out = append(out, a.Name)
	}
	return out
}
