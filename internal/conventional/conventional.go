// Package conventional is a minimal conventional-commits subject parser
// implemented without regular expressions.
//
// Recognized forms (SCOPE is the package name):
//
//	fix(SCOPE): description              -> patch
//	feat(SCOPE): description             -> minor
//	BREAKING CHANGE(SCOPE): description  -> major
//	BREAKING-CHANGE(SCOPE): description  -> major
//	anytype(SCOPE)!: description         -> major ("!" marks a breaking change)
//
// Everything else is classified as KindOther and ignored for versioning.
package conventional

import (
	"strings"

	"github.com/yohimik/monorel/internal/semver"
)

// Kind classifies a commit subject.
type Kind uint8

const (
	KindOther Kind = iota
	KindFix
	KindFeat
	KindBreaking
)

func (k Kind) String() string {
	switch k {
	case KindFix:
		return "fix"
	case KindFeat:
		return "feat"
	case KindBreaking:
		return "breaking"
	default:
		return "other"
	}
}

// Bump maps a commit kind to the version bump it demands.
func (k Kind) Bump() semver.Bump {
	switch k {
	case KindFix:
		return semver.BumpPatch
	case KindFeat:
		return semver.BumpMinor
	case KindBreaking:
		return semver.BumpMajor
	default:
		return semver.BumpNone
	}
}

// Commit is one parsed commit subject.
type Commit struct {
	Kind        Kind
	Scope       string
	Description string
	Raw         string
}

// Parse classifies a single commit subject line. It never fails: subjects that
// do not match the recognized forms come back as KindOther.
func Parse(subject string) Commit {
	c := Commit{Kind: KindOther, Raw: subject}
	colon := strings.IndexByte(subject, ':')
	if colon <= 0 {
		return c
	}
	head := subject[:colon]
	breaking := strings.HasSuffix(head, "!")
	if breaking {
		head = head[:len(head)-1]
	}
	if !strings.HasSuffix(head, ")") {
		return c
	}
	open := strings.IndexByte(head, '(')
	if open <= 0 {
		return c
	}
	typ := head[:open]
	scope := head[open+1 : len(head)-1]
	if scope == "" || strings.ContainsAny(scope, " \t()") {
		return c
	}

	kind := KindOther
	switch typ {
	case "fix":
		kind = KindFix
	case "feat":
		kind = KindFeat
	case "BREAKING CHANGE", "BREAKING-CHANGE":
		kind = KindBreaking
	default:
		if !breaking || !isTypeWord(typ) {
			return c
		}
	}
	if breaking {
		kind = KindBreaking
	}

	c.Kind = kind
	c.Scope = scope
	c.Description = strings.TrimSpace(subject[colon+1:])
	return c
}

// isTypeWord reports whether s looks like a conventional commit type (letters
// only), so that "chore(pkg)!:" counts as breaking but "foo bar(pkg)!:" does not.
func isTypeWord(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return true
}
