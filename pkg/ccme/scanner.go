package ccme

// scanner is the single left-to-right index scan mandated by §20.1. It has a
// fixed lookahead of one byte, never backtracks, and allocates nothing beyond
// the substrings it returns.
//
// The scanner is byte-oriented on purpose. Every structural character in the
// grammar is ASCII, and no byte of a multi-byte UTF-8 sequence can collide
// with an ASCII byte, so a byte scan is exactly equivalent to a rune scan here
// while remaining O(1) per step.
type scanner struct {
	s string
	i int
}

func (sc *scanner) eof() bool { return sc.i >= len(sc.s) }

// peek returns the byte at the cursor. It must not be called at eof.
func (sc *scanner) peek() byte { return sc.s[sc.i] }

// next consumes and returns the byte at the cursor. It must not be called at
// eof.
func (sc *scanner) next() byte {
	c := sc.s[sc.i]
	sc.i++
	return c
}

// accept consumes c if it is at the cursor and reports whether it did.
func (sc *scanner) accept(c byte) bool {
	if sc.eof() || sc.s[sc.i] != c {
		return false
	}
	sc.i++
	return true
}

// readWhile consumes bytes for as long as pred holds and returns them.
func (sc *scanner) readWhile(pred func(byte) bool) string {
	start := sc.i
	for sc.i < len(sc.s) && pred(sc.s[sc.i]) {
		sc.i++
	}
	return sc.s[start:sc.i]
}

// readUntilAny consumes bytes until one of chars is reached, or eof.
func (sc *scanner) readUntilAny(chars string) string {
	start := sc.i
	for sc.i < len(sc.s) && !containsByte(chars, sc.s[sc.i]) {
		sc.i++
	}
	return sc.s[start:sc.i]
}

// rest consumes and returns everything left.
func (sc *scanner) rest() string {
	r := sc.s[sc.i:]
	sc.i = len(sc.s)
	return r
}

// Character predicates are byte comparisons, not Unicode classes (§20.1).

func isLower(c byte) bool { return c >= 'a' && c <= 'z' }

func isUpperASCII(c byte) bool { return c >= 'A' && c <= 'Z' }

func isASCIILetter(c byte) bool { return isLower(c) || isUpperASCII(c) }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// isChannelChar matches the channel-name charset of §11.2.
func isChannelChar(c byte) bool { return isLower(c) || isDigit(c) || c == '-' }

// isFooterKeyChar matches the footer-key charset of §20.5.
func isFooterKeyChar(c byte) bool { return isASCIILetter(c) || isDigit(c) || c == '-' }

// isSigil reports whether c introduces an inline directive (§5.3).
func isSigil(c byte) bool { return c == '^' || c == '+' || c == '@' }

// isTypeTerminator reports whether c may legally follow the type (§20.3).
func isTypeTerminator(c byte) bool {
	switch c {
	case '(', '^', '+', '@', '!', ':':
		return true
	default:
		return false
	}
}

// inlineStopChars bounds an inline directive value: the next sigil, the
// breaking marker, or the colon (§20.3).
const inlineStopChars = "^+@!:"

func containsByte(s string, c byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return true
		}
	}
	return false
}

// hasUpperASCII reports whether s contains an ASCII uppercase letter.
func hasUpperASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if isUpperASCII(s[i]) {
			return true
		}
	}
	return false
}

// toLowerASCII lowercases the ASCII letters of s, leaving other bytes alone.
func toLowerASCII(s string) string {
	if !hasUpperASCII(s) {
		return s
	}
	b := []byte(s)
	for i := range b {
		if isUpperASCII(b[i]) {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}
