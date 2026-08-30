package config

// What can go wrong, as values rather than as sentences.
//
// Every error this package returns renders one fixed sentence, because a
// configuration error is read by the person who wrote the file and the file is
// open in front of them: naming the key and what belongs under it is the whole
// job, and repeating the file back at them is not. The structured types carry
// the parts of that sentence, so a caller wrapping this package in wording of
// its own reaches for the fields instead of parsing the text.
//
// The sentinels are for errors.Is. A caller that has to tell a cycle from a
// depth cap, or a typo from a value of the wrong shape, matches on those and
// never on the message.

import (
	"errors"
	"fmt"
	"strings"
)

// The sentinels every error of this package can be matched against.
var (
	// ErrUnsupportedFormat is what a file no entry of Options.Formats claims
	// returns. The extension decides the format, here as everywhere else.
	ErrUnsupportedFormat = errors.New("unsupported config file format")

	// ErrRefCycle is a file that reaches itself, directly or through another.
	ErrRefCycle = errors.New("$ref cycle")

	// ErrRefDepth is a reference chain nested further than anyone means it to
	// be.
	ErrRefDepth = errors.New("$ref nesting too deep")

	// ErrRefTarget is a reference naming anything but a file, or a list of
	// them.
	ErrRefTarget = errors.New("$ref must name another config file, or a list of them")

	// ErrUnknownKey is a key no fields table holds: the field does not exist,
	// which is where a typo lands.
	ErrUnknownKey = errors.New("unknown key")

	// ErrFoldCollision is one object giving a name two spellings.
	ErrFoldCollision = errors.New("keys collide case-insensitively")

	// ErrTOMLEdit reports that the config is TOML, which cannot be rewritten
	// format-preservingly; render a paste-ready snippet instead.
	ErrTOMLEdit = errors.New("a TOML config cannot be rewritten in place")

	// ErrRefEdit reports a key whose value is composed from a referenced file
	// and the keys written beside the reference. There is no single file the
	// new value could be written to, so the caller explains the two ways out
	// rather than picking one.
	ErrRefEdit = errors.New("a key composed from a $ref and the keys beside it cannot be rewritten in place")

	// ErrMultiRefEdit reports a key whose value is merged from several
	// referenced files. No one of them holds the value, so the caller explains
	// the ways out rather than choosing a file to write to.
	ErrMultiRefEdit = errors.New("a key merged from several $ref files cannot be rewritten in place")

	// ErrNoConfig is the ascent finding no config file anywhere above the
	// directory it started in.
	ErrNoConfig = errors.New("no config file found")
)

// FileError names the file a failure is about. It is the outermost thing a
// read failure carries, so a caller that has to say which file broke reads
// Path rather than the message.
type FileError struct {
	Path string
	Err  error
}

func (e *FileError) Error() string { return "cannot read " + e.Path + ": " + e.Err.Error() }
func (e *FileError) Unwrap() error { return e.Err }

// KeyError places a failure inside a file: the file, the key path within it,
// and what went wrong there. Key is the path from the document's root, or the
// document itself when the failure has no key above it.
type KeyError struct {
	File string
	Key  string
	Err  error
}

func (e *KeyError) Error() string { return e.File + ": " + e.Key + ": " + e.Err.Error() }
func (e *KeyError) Unwrap() error { return e.Err }

// RefChainError is a cycle or a chain too deep to be meant. It carries every
// file involved, which is why the frames it unwinds through pass it on
// untouched instead of prefixing it with hops it already names.
type RefChainError struct {
	// Err is ErrRefCycle or ErrRefDepth.
	Err error
	// Chain is the files being read, each labelled with the key that carried
	// the reference out of it, ending on the file that closed the chain.
	Chain []string
	// Depth is the cap a chain too deep exceeded.
	Depth int
}

func (e *RefChainError) Error() string {
	text := strings.Join(e.Chain, " -> ")
	if errors.Is(e.Err, ErrRefDepth) {
		return fmt.Sprintf("$ref nesting is more than %d files deep: %s", e.Depth, text)
	}
	return fmt.Sprintf("$ref cycle: %s; a file cannot reference itself, directly or through another", text)
}

func (e *RefChainError) Unwrap() error { return e.Err }

// UnknownKeyError is a key the fields table has no setter for, named by its
// full path from the root of the file: a typo the loader accepted would be
// configuration that silently never applies, so it names where to look.
type UnknownKeyError struct {
	Key string
}

func (e *UnknownKeyError) Error() string { return fmt.Sprintf("unknown key %q", e.Key) }
func (e *UnknownKeyError) Unwrap() error { return ErrUnknownKey }

// FoldCollisionError is one object holding two keys that fold together. Every
// name is matched case-insensitively, so the two are one name written twice,
// and which of them a lookup found would be whatever the map iteration handed
// it.
type FoldCollisionError struct {
	// At names the object: a key path, a document, or an env layer's label.
	At string
	// First and Second are the two spellings, in the order they were seen.
	First, Second string
}

func (e *FoldCollisionError) Error() string {
	return fmt.Sprintf("%s: keys %q and %q collide case-insensitively", e.At, e.First, e.Second)
}

func (e *FoldCollisionError) Unwrap() error { return ErrFoldCollision }

// NoConfigError is the ascent's failure: the directory it started in, and the
// names it looked for on the way up.
type NoConfigError struct {
	Dir   string
	Names []string
}

func (e *NoConfigError) Error() string {
	return fmt.Sprintf("no config file found in %s or any parent directory (tried %s)",
		e.Dir, strings.Join(e.Names, ", "))
}

func (e *NoConfigError) Unwrap() error { return ErrNoConfig }

// Wants is the one sentence a value of the wrong shape earns: the key, and
// what belongs under it. Saying what was written instead would repeat the file
// back at a reader who has it open.
func Wants(at, what string) error {
	return fmt.Errorf("%s: wants %s", at, what)
}
