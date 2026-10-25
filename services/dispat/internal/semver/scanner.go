package semver

import (
	"errors"
	"fmt"
)

// scanner is byte-oriented because SemVer delimiters and allowed identifiers
// are ASCII. Identifier validation adds only a bounded number of scans, so
// parsing remains linear in the input length.
type scanner struct {
	s string
	i int
}

func (sc *scanner) eof() bool { return sc.i >= len(sc.s) }

func (sc *scanner) peek() byte { return sc.s[sc.i] }

func (sc *scanner) accept(c byte) bool {
	if sc.eof() || sc.s[sc.i] != c {
		return false
	}
	sc.i++
	return true
}

func (sc *scanner) readWhile(pred func(byte) bool) string {
	start := sc.i
	for sc.i < len(sc.s) && pred(sc.s[sc.i]) {
		sc.i++
	}
	return sc.s[start:sc.i]
}

func (sc *scanner) readUntilAny(chars string) string {
	start := sc.i
	for sc.i < len(sc.s) && !containsByte(chars, sc.s[sc.i]) {
		sc.i++
	}
	return sc.s[start:sc.i]
}

func containsByte(s string, c byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return true
		}
	}
	return false
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isASCIILetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

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

func readIdentifiers(sc *scanner, stop string) ([]string, error) {
	stops := "." + stop
	var out []string
	for {
		id := sc.readUntilAny(stops)
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
