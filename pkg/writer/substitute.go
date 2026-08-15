package writer

import (
	"bytes"
	"errors"
	"fmt"
)

// Substitute is the splicer with the format knowledge taken away: literal
// text in, literal text out, over any file at all. Nothing is parsed, so
// nothing has to be understood first, which is what makes it reach the places
// a manifest writer cannot: a Gradle coordinate built by a build script, a
// version in a Dockerfile, a README example, a CI workflow.
//
// The price of reaching everywhere is that it does exactly what it is told. A
// Find that also occurs somewhere unintended is replaced there too, so a
// substitution should carry enough context to be unambiguous: "acme-core:1.2.3"
// rather than "1.2.3".

// Substitution is one literal find/write pair.
type Substitution struct {
	// Find is the text to look for, matched byte for byte. It is not a
	// pattern: no globs, no regular expressions, no escapes. Empty is an
	// error, since it would match at every position.
	Find string
	// Write is the text to put in its place, also literal. Empty deletes
	// every occurrence of Find.
	Write string
}

// SubstituteResult reports what one file's substitutions did. It splits the
// same three ways Result does, and for the same reasons.
type SubstituteResult struct {
	// Path of the file the call targeted, echoed back so a caller batching
	// several keeps the correlation.
	Path string
	// Applied are the substitutions that changed the file.
	Applied []Substitution
	// Missing are the substitutions whose Find the file does not contain.
	Missing []Substitution
	// Skipped are the substitutions whose Find the file contains but which
	// would write it back unchanged, because Find and Write are the same
	// text. Re-running an already-substituted file is the ordinary way this
	// happens, so it is not worth a warning.
	Skipped []Substitution
	// Count is how many occurrences were replaced across every applied
	// substitution.
	Count int
}

// ErrBinaryFile marks a file refused for looking like binary data; test with
// errors.Is.
var ErrBinaryFile = errors.New("writer: file looks binary")

// ErrEmptyFind marks a substitution with nothing to find; test with errors.Is.
var ErrEmptyFind = errors.New("writer: substitution with no text to find")

// binarySniff is how many leading bytes decide whether a file is text. A NUL
// in a file's first few kilobytes is the same signal git, grep and diff all
// use, and it is what keeps a replacement out of an image or an archive that
// happens to contain the version text.
const binarySniff = 8 << 10

// Substitute replaces literal text inside the file at path and writes it back,
// atomically and only when something actually changed.
//
// The substitutions apply in order, each over what the one before it left, so
// a later Find may match text an earlier Write put there. That is occasionally
// what a caller wants and always worth knowing, which is why the order is the
// caller's to choose rather than sorted here.
//
// Every occurrence of a Find is replaced, not just the first: a version
// usually appears in a file more than once, and replacing one of them would
// leave the file disagreeing with itself.
//
// A file over the package's 16 MiB read cap gives ErrManifestTooLarge, and one
// that looks binary gives ErrBinaryFile. Both are sentinels, so a caller
// walking a folder can skip those files and carry on rather than fail.
func Substitute(path string, subs []Substitution) (SubstituteResult, error) {
	for i, s := range subs {
		if s.Find == "" {
			return SubstituteResult{}, fmt.Errorf("%w (substitution %d)", ErrEmptyFind, i+1)
		}
	}
	sp, err := openSplicer(path)
	if err != nil {
		return SubstituteResult{}, err
	}
	if looksBinary(sp.bytes()) {
		return SubstituteResult{}, fmt.Errorf("%s: %w", path, ErrBinaryFile)
	}

	out, counts := SubstituteBytes(sp.bytes(), subs)
	res := SubstituteResult{Path: path}
	for i, s := range subs {
		switch {
		case counts[i] == 0:
			res.Missing = append(res.Missing, s)
		case s.Find == s.Write:
			res.Skipped = append(res.Skipped, s)
		default:
			res.Applied = append(res.Applied, s)
			res.Count += counts[i]
		}
	}
	sp.setWhole(out)
	return res, sp.commit(nil)
}

// SubstituteBytes applies the substitutions in memory, returning the result
// and how many occurrences of each Find the text held when its turn came. A
// substitution whose Find is empty, or whose Write is the same text, counts
// its occurrences and changes nothing.
//
// The input is never modified: a substitution that matches builds a new slice,
// and one that does not returns what it was given.
func SubstituteBytes(data []byte, subs []Substitution) ([]byte, []int) {
	counts := make([]int, len(subs))
	for i, s := range subs {
		if s.Find == "" {
			continue // an empty pattern matches at every position and means nothing
		}
		find := []byte(s.Find)
		counts[i] = bytes.Count(data, find)
		if counts[i] == 0 || s.Find == s.Write {
			continue
		}
		data = bytes.ReplaceAll(data, find, []byte(s.Write))
	}
	return data, counts
}

// looksBinary reports a NUL byte in the file's first few kilobytes.
func looksBinary(data []byte) bool {
	head := data
	if len(head) > binarySniff {
		head = head[:binarySniff]
	}
	return bytes.IndexByte(head, 0) >= 0
}
