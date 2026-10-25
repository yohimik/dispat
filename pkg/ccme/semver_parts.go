package ccme

import "fmt"

// VersionParts is the lexical shape of a SemVer 2.0.0 version. Decimal core
// components remain strings because the SemVer grammar sets no integer limit.
// The release engine's Version separately uses uint64 core values.
type VersionParts struct {
	Major      string
	Minor      string
	Patch      string
	Prerelease []string
	Build      []string
	Raw        string
}

// ParseVersionParts validates the full SemVer 2.0.0 spelling while retaining
// arbitrarily long core numbers and build metadata. It does not change the
// release engine's bounded ParseVersion or its build-dropping Version.String.
func ParseVersionParts(raw string) (VersionParts, error) {
	parts := VersionParts{Raw: raw}
	sc := &scanner{s: raw}
	var err error
	if parts.Major, err = readNumericCoreDigits(sc); err != nil {
		return VersionParts{}, err
	}
	if !sc.accept('.') {
		return VersionParts{}, fmt.Errorf("%w: %q: expected '.' after major", ErrInvalidVersion, raw)
	}
	if parts.Minor, err = readNumericCoreDigits(sc); err != nil {
		return VersionParts{}, err
	}
	if !sc.accept('.') {
		return VersionParts{}, fmt.Errorf("%w: %q: expected '.' after minor", ErrInvalidVersion, raw)
	}
	if parts.Patch, err = readNumericCoreDigits(sc); err != nil {
		return VersionParts{}, err
	}
	if sc.accept('-') {
		parts.Prerelease, err = readIdentifiers(sc, "+")
		if err != nil {
			return VersionParts{}, fmt.Errorf("%w: %q: %s", ErrInvalidVersion, raw, err)
		}
		if err := validatePrereleaseIdentifiers(raw, parts.Prerelease); err != nil {
			return VersionParts{}, err
		}
	}
	if sc.accept('+') {
		parts.Build, err = readIdentifiers(sc, "")
		if err != nil {
			return VersionParts{}, fmt.Errorf("%w: %q: %s", ErrInvalidVersion, raw, err)
		}
	}
	if !sc.eof() {
		return VersionParts{}, fmt.Errorf("%w: %q: unexpected %q", ErrInvalidVersion, raw, string(sc.peek()))
	}
	return parts, nil
}

func readNumericCoreDigits(sc *scanner) (string, error) {
	digits := sc.readWhile(isDigit)
	if digits == "" {
		return "", fmt.Errorf("%w: expected a number at offset %d", ErrInvalidVersion, sc.i)
	}
	if hasLeadingZero(digits) {
		return "", fmt.Errorf("%w: %q has a leading zero", ErrInvalidVersion, digits)
	}
	return digits, nil
}

func validatePrereleaseIdentifiers(raw string, ids []string) error {
	for _, id := range ids {
		if isNumericIdentifier(id) && hasLeadingZero(id) {
			return fmt.Errorf("%w: %q: numeric prerelease identifier %q has a leading zero", ErrInvalidVersion, raw, id)
		}
	}
	return nil
}
