package ccme

import "strings"

// Canonical footer keys from the registry in §8.1.
const (
	FooterBreakingChange   = "BREAKING CHANGE"
	FooterPropagate        = "Propagate"
	FooterPropagateDepth   = "Propagate-Depth"
	FooterPropagateScope   = "Propagate-Scope"
	FooterPropagateChannel = "Propagate-Channel"
	// FooterPropagateChannelDepth and FooterPropagateChannelScope are the
	// channel axis's counterparts of Propagate-Depth and Propagate-Scope
	// (§8.3a, §9.3).
	FooterPropagateChannelDepth = "Propagate-Channel-Depth"
	FooterPropagateChannelScope = "Propagate-Channel-Scope"
	FooterChannel               = "Channel"
	FooterReleaseAs             = "Release-As"
	FooterReverts               = "Reverts"
)

// footerBreakingHyphen is the hyphenated alias of BREAKING CHANGE, accepted by
// the base specification. Like the spaced form it is case-sensitive (§8.1.1).
const footerBreakingHyphen = "BREAKING-CHANGE"

// footerRegistry is the §8.1 table, minus the two BREAKING CHANGE spellings.
// Every key here is matched case-insensitively but not hyphen-insensitively:
// "propagate-depth" matches, "PropagateDepth" does not.
//
// It is a slice rather than a map because it has nine entries and lookups
// must be case-insensitive: an ASCII fold-compare over nine short strings is
// faster than lowercasing the key, and unlike a map lookup it allocates
// nothing.
var footerRegistry = [...]struct{ key, canonical string }{
	{"Propagate", FooterPropagate},
	{"Propagate-Depth", FooterPropagateDepth},
	{"Propagate-Scope", FooterPropagateScope},
	{"Propagate-Channel", FooterPropagateChannel},
	{"Propagate-Channel-Depth", FooterPropagateChannelDepth},
	{"Propagate-Channel-Scope", FooterPropagateChannelScope},
	{"Channel", FooterChannel},
	{"Release-As", FooterReleaseAs},
	{"Reverts", FooterReverts},
}

// keyResolution is what lookupFooterKey reports about a written footer key.
type keyResolution int

const (
	// keyUnknown is not in the §8.1 registry.
	keyUnknown keyResolution = iota
	// keyKnown is a registry key, matched case-insensitively.
	keyKnown
	// keyMiscasedBreaking equals BREAKING CHANGE or BREAKING-CHANGE
	// case-insensitively but not exactly. It is NOT breaking, and is the
	// silent failure W155 exists to surface (§8.1.1).
	keyMiscasedBreaking
)

// lookupFooterKey resolves a written key to its canonical spelling.
//
// BREAKING CHANGE is the one key compared twice: exactly first, because
// Conventional Commits 1.0.0 requires uppercase and a miscased spelling must
// not silently ship a major change as a minor one; then case-insensitively, to
// catch that mistake and report it.
func lookupFooterKey(key string) (string, keyResolution) {
	if key == FooterBreakingChange || key == footerBreakingHyphen {
		return FooterBreakingChange, keyKnown
	}
	if equalFoldASCII(key, FooterBreakingChange) || equalFoldASCII(key, footerBreakingHyphen) {
		return key, keyMiscasedBreaking
	}
	for _, e := range footerRegistry {
		if equalFoldASCII(e.key, key) {
			return e.canonical, keyKnown
		}
	}
	return key, keyUnknown
}

// isBreakingKey reports whether key is exactly one of the two accepted
// spellings. It avoids the registry scan on the hot path in isFooterBlock.
func isBreakingKey(key string) bool {
	return key == FooterBreakingChange || key == footerBreakingHyphen
}

// miscasedBreakingPrefix reports whether a line opens with something that
// equals BREAKING CHANGE case-insensitively — but not exactly — followed by
// ": ". Such a line is not a footer at all, because the generic key loop halts
// at the space, so it can only be caught by looking for it (§20.5).
//
// The hyphenated miscasing is not detected here: it contains only footer-key
// characters, so it parses as a well-formed footer and is caught at key
// resolution instead.
func miscasedBreakingPrefix(line string) bool {
	const n = len(FooterBreakingChange)
	if len(line) < n || !strings.HasPrefix(line[n:], ":") {
		return false
	}
	head := line[:n]
	return head != FooterBreakingChange && equalFoldASCII(head, FooterBreakingChange)
}

// containsFold reports whether list holds key, compared case-insensitively.
func containsFold(list []string, key string) bool {
	for _, e := range list {
		if equalFoldASCII(e, key) {
			return true
		}
	}
	return false
}

// equalFoldASCII compares two strings ignoring ASCII case. Footer keys are
// ASCII by grammar (§20.5), so this is exact — and unlike strings.EqualFold it
// does no Unicode work.
func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca == cb {
			continue
		}
		if isUpperASCII(ca) {
			ca += 'a' - 'A'
		}
		if isUpperASCII(cb) {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// Footer is one git trailer in a unit's final paragraph (§8.1).
type Footer struct {
	// Key is the key exactly as written.
	Key string
	// CanonicalKey is the registry spelling for a known key, or Key otherwise.
	CanonicalKey string
	// Value is the trailer value, with continuation lines already joined.
	Value string
	// Separator is ": " or " #" (§20.5).
	Separator string
	// Position is where the key begins.
	Position Position
	// Known reports whether the key is in the §8.1 registry.
	Known bool
	// MiscasedBreaking reports a key that equals BREAKING CHANGE or
	// BREAKING-CHANGE case-insensitively but not exactly. Such a footer is
	// NOT breaking — it is an unknown key — and carries W155 (§8.1.1).
	MiscasedBreaking bool
	// MessageLevel reports an authorship or review trailer, which the release
	// engine ignores wherever it appears (§4.5).
	MessageLevel bool
	// IssueReference reports an issue-reference trailer, ignored for
	// versioning but available to a changelog (§4.5).
	IssueReference bool
}

// IsBreakingChange reports whether this footer is a BREAKING CHANGE trailer.
func (f Footer) IsBreakingChange() bool { return f.CanonicalKey == FooterBreakingChange }

// footerKeyEnd implements §20.5. It returns the index at which the key ends,
// or -1 if the line is not a footer start. No pattern matching is involved.
//
// The literal BREAKING CHANGE is tested first because it is the only key
// containing a space; the generic loop below would halt there. The comparison
// is case-sensitive, and deliberately not generalised — treating keys as
// space-bearing would make ordinary prose like "Note this is important: ..."
// parse as a footer (§8.1.1).
func footerKeyEnd(line string) int {
	if strings.HasPrefix(line, FooterBreakingChange) &&
		isKeySeparator(line[len(FooterBreakingChange):]) {
		return len(FooterBreakingChange)
	}
	i := 0
	for i < len(line) && isFooterKeyChar(line[i]) {
		i++
	}
	if i == 0 {
		return -1
	}
	rest := line[i:]
	if strings.HasPrefix(rest, " #") {
		return i
	}
	if isKeySeparator(rest) && isBreakingKey(line[:i]) {
		// Only BREAKING CHANGE may carry an empty value (§8.1.1, edge case
		// 19e): "BREAKING CHANGE: " survives §4.1 normalisation as
		// "BREAKING CHANGE:", and must still be breaking. Generalising this to
		// every key would reclassify body prose ending in a colon as a footer.
		return i
	}
	if strings.HasPrefix(rest, ": ") {
		return i
	}
	return -1
}

// isKeySeparator reports whether what follows a key is ": ", or a bare ":" at
// end of line — the empty-value form.
func isKeySeparator(rest string) bool {
	return rest == ":" || strings.HasPrefix(rest, ": ")
}

// isFooterBlock reports whether every line of a paragraph is a footer start or
// a footer continuation (§4.4, §20.5).
func isFooterBlock(lines []string) bool {
	if len(lines) == 0 {
		return false
	}
	end := footerKeyEnd(lines[0])
	if end < 0 {
		return false
	}
	prevBreaking := isBreakingKey(lines[0][:end])
	for _, l := range lines[1:] {
		if e := footerKeyEnd(l); e >= 0 {
			prevBreaking = isBreakingKey(l[:e])
			continue
		}
		if strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") {
			continue // indented continuation
		}
		if prevBreaking {
			continue // free-form multi-line value
		}
		return false
	}
	return true
}

// nearlyFooterBlock reports the common typo of a body sentence appended after
// trailers: the first line is a footer start but the paragraph as a whole is
// not a footer block (§20.5). It produces W151.
func nearlyFooterBlock(lines []string) bool {
	if len(lines) == 0 {
		return false
	}
	return footerKeyEnd(lines[0]) >= 0 && !isFooterBlock(lines)
}

// parseFooters splits a footer block into trailers. Continuation lines are
// joined with a newline for BREAKING CHANGE, whose value is free text, and
// with a single space for every other key (§8.1).
//
// Values that have no continuation are substrings of the line, so the common
// case copies nothing.
func (p *Parser) parseFooters(lines []string, start Position) []Footer {
	footers := make([]Footer, 0, len(lines))
	contFrom := -1 // index of the first continuation line of the last footer

	closeLast := func(end int) {
		if contFrom < 0 {
			return
		}
		f := &footers[len(footers)-1]
		f.Value = joinContinuation(f.Value, lines[contFrom:end], f.IsBreakingChange())
		contFrom = -1
	}

	for i, l := range lines {
		if end := footerKeyEnd(l); end >= 0 {
			closeLast(i)
			key := l[:end]
			f := Footer{
				Key:      key,
				Position: Position{Line: start.Line + i, Column: 1},
			}
			switch rest := l[end:]; {
			case rest == ":":
				f.Separator, f.Value = ": ", "" // empty value (§8.1.1)
			case strings.HasPrefix(rest, ": "):
				f.Separator, f.Value = ": ", l[end+2:]
			default: // " #", the git issue-reference form
				f.Separator, f.Value = " #", l[end+1:]
			}
			canonical, resolution := lookupFooterKey(key)
			f.CanonicalKey = canonical
			f.Known = resolution == keyKnown
			f.MiscasedBreaking = resolution == keyMiscasedBreaking
			// Computed for registry keys too, so that a configuration listing
			// a registry key as a trailer still suppresses it (§4.5).
			f.MessageLevel = containsFold(p.messageTrailers, key)
			f.IssueReference = containsFold(p.issueTrailers, key)
			footers = append(footers, f)
			continue
		}
		if len(footers) == 0 {
			continue // unreachable for a validated footer block
		}
		if contFrom < 0 {
			contFrom = i
		}
	}
	closeLast(len(lines))

	return footers
}

// joinContinuation folds a trailer's continuation lines into its value.
func joinContinuation(value string, cont []string, breaking bool) string {
	var b strings.Builder
	if breaking {
		n := len(value)
		for _, c := range cont {
			n += len(c) + 1
		}
		b.Grow(n)
		b.WriteString(value)
		for _, c := range cont {
			b.WriteByte('\n')
			b.WriteString(c)
		}
		return b.String()
	}
	n := len(value)
	for _, c := range cont {
		n += len(c) + 1
	}
	b.Grow(n)
	b.WriteString(strings.TrimSpace(value))
	for _, c := range cont {
		if t := strings.TrimSpace(c); t != "" {
			b.WriteByte(' ')
			b.WriteString(t)
		}
	}
	return b.String()
}
