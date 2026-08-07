package ccme

import (
	"errors"
	"fmt"
	"strings"
)

// Version is a parsed SemVer 2.0.0 version. It exists so that an exact
// Release-As value can be validated at parse time; the release engine needs
// the same type when it compares against a baseline.
type Version struct {
	Major      uint64
	Minor      uint64
	Patch      uint64
	Prerelease []string
	Build      []string
	Raw        string
}

// ErrInvalidVersion is returned by ParseVersion for any malformed input.
var ErrInvalidVersion = errors.New("ccme: invalid semantic version")

// String renders the version without its build metadata, which is never
// carried into a computed version (§12.1).
func (v Version) String() string {
	var b strings.Builder
	b.WriteString(utoa(v.Major))
	b.WriteByte('.')
	b.WriteString(utoa(v.Minor))
	b.WriteByte('.')
	b.WriteString(utoa(v.Patch))
	if len(v.Prerelease) > 0 {
		b.WriteByte('-')
		b.WriteString(strings.Join(v.Prerelease, "."))
	}
	return b.String()
}

// IsPrerelease reports whether the version carries a prerelease component.
func (v Version) IsPrerelease() bool { return len(v.Prerelease) > 0 }

// Core strips the prerelease and build components, leaving the
// MAJOR.MINOR.PATCH triple: 1.0.1-beta.4 -> 1.0.1.
func (v Version) Core() Version {
	return Version{Major: v.Major, Minor: v.Minor, Patch: v.Patch}
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

// ParseVersion parses a SemVer 2.0.0 version with a single left-to-right scan
// and no regular expression (§20.6). A leading "v" is rejected, as are leading
// zeros in numeric identifiers.
func ParseVersion(s string) (Version, error) {
	v := Version{Raw: s}
	sc := &scanner{s: s}

	var err error
	if v.Major, err = readNumericCore(sc); err != nil {
		return Version{}, err
	}
	if !sc.accept('.') {
		return Version{}, fmt.Errorf("%w: %q: expected '.' after major", ErrInvalidVersion, s)
	}
	if v.Minor, err = readNumericCore(sc); err != nil {
		return Version{}, err
	}
	if !sc.accept('.') {
		return Version{}, fmt.Errorf("%w: %q: expected '.' after minor", ErrInvalidVersion, s)
	}
	if v.Patch, err = readNumericCore(sc); err != nil {
		return Version{}, err
	}

	if sc.accept('-') {
		ids, err := readIdentifiers(sc, "+")
		if err != nil {
			return Version{}, fmt.Errorf("%w: %q: %s", ErrInvalidVersion, s, err.Error())
		}
		for _, id := range ids {
			if isNumericIdentifier(id) && hasLeadingZero(id) {
				return Version{}, fmt.Errorf(
					"%w: %q: numeric prerelease identifier %q has a leading zero", ErrInvalidVersion, s, id)
			}
		}
		v.Prerelease = ids
	}

	if sc.accept('+') {
		ids, err := readIdentifiers(sc, "")
		if err != nil {
			return Version{}, fmt.Errorf("%w: %q: %s", ErrInvalidVersion, s, err.Error())
		}
		v.Build = ids
	}

	if !sc.eof() {
		return Version{}, fmt.Errorf("%w: %q: unexpected %q", ErrInvalidVersion, s, string(sc.peek()))
	}
	return v, nil
}

// Compare orders two versions by SemVer precedence. Build metadata is ignored.
// It returns -1, 0 or 1.
func (v Version) Compare(o Version) int {
	if c := compareUint(v.Major, o.Major); c != 0 {
		return c
	}
	if c := compareUint(v.Minor, o.Minor); c != 0 {
		return c
	}
	if c := compareUint(v.Patch, o.Patch); c != 0 {
		return c
	}
	switch {
	case len(v.Prerelease) == 0 && len(o.Prerelease) == 0:
		return 0
	case len(v.Prerelease) == 0:
		return 1 // a release outranks a prerelease
	case len(o.Prerelease) == 0:
		return -1
	}
	for i := 0; i < len(v.Prerelease) && i < len(o.Prerelease); i++ {
		if c := comparePrereleaseIdentifier(v.Prerelease[i], o.Prerelease[i]); c != 0 {
			return c
		}
	}
	return compareInt(len(v.Prerelease), len(o.Prerelease))
}

func comparePrereleaseIdentifier(a, b string) int {
	an, bn := isNumericIdentifier(a), isNumericIdentifier(b)
	switch {
	case an && bn:
		// Numeric identifiers compare numerically, which is why the counter is
		// a separate identifier (§11.3).
		if len(a) != len(b) {
			return compareInt(len(a), len(b))
		}
		return strings.Compare(a, b)
	case an:
		return -1 // numeric identifiers always have lower precedence
	case bn:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

// readNumericCore reads one of the three core numbers: digits, no leading zero
// unless the number is exactly "0".
func readNumericCore(sc *scanner) (uint64, error) {
	digits := sc.readWhile(isDigit)
	if digits == "" {
		return 0, fmt.Errorf("%w: expected a number at offset %d", ErrInvalidVersion, sc.i)
	}
	if hasLeadingZero(digits) {
		return 0, fmt.Errorf("%w: %q has a leading zero", ErrInvalidVersion, digits)
	}
	var n uint64
	for i := 0; i < len(digits); i++ {
		if n > (1<<63)/10 {
			return 0, fmt.Errorf("%w: %q overflows", ErrInvalidVersion, digits)
		}
		n = n*10 + uint64(digits[i]-'0')
	}
	return n, nil
}

// readIdentifiers reads dot-separated identifiers, stopping at any byte in
// stop or at eof.
func readIdentifiers(sc *scanner, stop string) ([]string, error) {
	var out []string
	for {
		id := sc.readUntilAny("." + stop)
		if id == "" {
			return nil, errors.New("empty identifier")
		}
		for i := 0; i < len(id); i++ {
			if !isIdentifierChar(id[i]) {
				return nil, fmt.Errorf("illegal character %q in identifier %q", string(id[i]), id)
			}
		}
		out = append(out, id)
		if !sc.accept('.') {
			return out, nil
		}
	}
}

func isIdentifierChar(c byte) bool { return isASCIILetter(c) || isDigit(c) || c == '-' }

func isNumericIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}

func hasLeadingZero(s string) bool { return len(s) > 1 && s[0] == '0' }

func compareUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func utoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
