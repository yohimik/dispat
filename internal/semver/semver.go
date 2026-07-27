// Package semver implements the strict MAJOR.MINOR.PATCH subset of semantic
// versioning used for release tags, without regular expressions.
package semver

import (
	"errors"
	"fmt"
	"strings"
)

// Bump is the kind of version increment a release requires.
type Bump uint8

const (
	BumpNone Bump = iota
	BumpPatch
	BumpMinor
	BumpMajor
)

func (b Bump) String() string {
	switch b {
	case BumpPatch:
		return "patch"
	case BumpMinor:
		return "minor"
	case BumpMajor:
		return "major"
	default:
		return "none"
	}
}

// Max returns the greater of two bumps.
func Max(a, b Bump) Bump {
	if a > b {
		return a
	}
	return b
}

// Version is a strict MAJOR.MINOR.PATCH semantic version.
type Version struct {
	Major, Minor, Patch int
}

var errFormat = errors.New(`semver: expected "MAJOR.MINOR.PATCH"`)

// Parse parses "1.2.3". An optional leading "v" is tolerated.
func Parse(s string) (Version, error) {
	s = strings.TrimPrefix(s, "v")
	var (
		v   Version
		err error
	)
	if v.Major, s, err = cutInt(s, false); err != nil {
		return Version{}, err
	}
	if v.Minor, s, err = cutInt(s, false); err != nil {
		return Version{}, err
	}
	if v.Patch, _, err = cutInt(s, true); err != nil {
		return Version{}, err
	}
	return v, nil
}

// cutInt reads a non-negative decimal integer from the front of s. Unless last
// is set it also consumes the '.' separator that must follow.
func cutInt(s string, last bool) (int, string, error) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, "", errFormat
	}
	n := 0
	for _, c := range []byte(s[:i]) {
		n = n*10 + int(c-'0')
		if n < 0 { // overflow
			return 0, "", errFormat
		}
	}
	rest := s[i:]
	if last {
		if rest != "" {
			return 0, "", errFormat
		}
		return n, "", nil
	}
	if len(rest) == 0 || rest[0] != '.' {
		return 0, "", errFormat
	}
	return n, rest[1:], nil
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Bumped returns the version incremented by b.
func (v Version) Bumped(b Bump) Version {
	switch b {
	case BumpMajor:
		return Version{Major: v.Major + 1}
	case BumpMinor:
		return Version{Major: v.Major, Minor: v.Minor + 1}
	case BumpPatch:
		return Version{Major: v.Major, Minor: v.Minor, Patch: v.Patch + 1}
	default:
		return v
	}
}

// Compare returns -1, 0 or 1 when v is respectively lower than, equal to or
// higher than o.
func (v Version) Compare(o Version) int {
	pairs := [3][2]int{{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Patch, o.Patch}}
	for _, p := range pairs {
		switch {
		case p[0] < p[1]:
			return -1
		case p[0] > p[1]:
			return 1
		}
	}
	return 0
}
