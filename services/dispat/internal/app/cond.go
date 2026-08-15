package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yohimik/dispat/services/dispat/internal/globx"
	"github.com/yohimik/dispat/services/dispat/internal/release"
)

// The conditions `dispat if` branches on. A condition is a value: parsing it
// and evaluating it are separate, and evaluation takes the lookup rather than
// reading the process environment itself, so the whole grammar is testable
// without touching os.Getenv and the command stays a thin caller.

// condOp is what a condition asks about the variable it names.
type condOp int

const (
	opSet     condOp = iota // NAME: set and non-empty
	opUnset                 // !NAME: unset or empty
	opEq                    // NAME=value
	opNe                    // NAME!=value
	opGlob                  // NAME~glob
	opNotGlob               // NAME!~glob
	opHeld                  // answered true before the chain ran
	opNotHeld               // answered false before the chain ran
)

// Condition is one parsed condition: the variable, the question, and the value
// the question compares against (empty for the two that take none). Spec is
// the text it was parsed from, kept so a log line or an error can quote what
// the user actually typed rather than reassembling it.
type Condition struct {
	Name  string
	op    condOp
	Value string
	Spec  string
}

// The operators. Order in this slice does not decide anything: the operator
// that separates the name from the value is the *leftmost* one in the spec,
// and the longest at that position. Anything after it is value, so a value may
// contain any operator it likes ("URL=a~b" is URL equals "a~b", not a glob).
// The longest-at-a-position rule is what keeps "!=" from parsing as "=" and
// "!~" from parsing as "~".
var condOps = []struct {
	token string
	op    condOp
}{
	{"!~", opNotGlob},
	{"!=", opNe},
	{"~", opGlob},
	{"=", opEq},
}

// ParseCondition reads one condition spec. Every rejection names the spec,
// because a condition that silently evaluated false would look exactly like a
// variable that was not set, and the two need telling apart.
func ParseCondition(spec string) (Condition, error) {
	if spec == "" {
		return Condition{}, fmt.Errorf("empty condition (want NAME, !NAME, NAME=value, NAME!=value, NAME~glob or NAME!~glob)")
	}
	at, token, op := -1, "", opSet
	for _, o := range condOps {
		i := strings.Index(spec, o.token)
		switch {
		case i < 0:
			continue
		case at < 0, i < at, i == at && len(o.token) > len(token):
			at, token, op = i, o.token, o.op
		}
	}
	if at >= 0 {
		name := spec[:at]
		if err := checkCondName(name, spec); err != nil {
			return Condition{}, err
		}
		// The value is taken verbatim, empty included: NAME= is how a spec asks
		// whether a variable is set but empty, which no other form can say.
		return Condition{Name: name, op: op, Value: spec[at+len(token):], Spec: spec}, nil
	}
	if name, ok := strings.CutPrefix(spec, "!"); ok {
		if err := checkCondName(name, spec); err != nil {
			return Condition{}, err
		}
		return Condition{Name: name, op: opUnset, Spec: spec}, nil
	}
	if err := checkCondName(spec, spec); err != nil {
		return Condition{}, err
	}
	return Condition{Name: spec, op: opSet, Spec: spec}, nil
}

// ResolvedCondition is a condition whose answer was computed before the chain
// ran: the leading branch is always evaluated first, so a question the
// environment cannot answer — a changed selection, a file test — is asked
// eagerly and carried into the chain as its result. Spec keeps what the user
// typed ("--changed", "-f path"), so the "condition matched" log line quotes
// their words like it does for any other condition.
func ResolvedCondition(spec string, held bool) Condition {
	op := opNotHeld
	if held {
		op = opHeld
	}
	return Condition{op: op, Spec: spec}
}

// FileCondition answers the -f and -d file tests: the path exists and is a
// regular file, or a directory. A relative path is joined onto dir, the folder
// the chosen script runs in, so the test and the script read the same name the
// same way. Like the shell's [ -f ] and [ -d ], a path that cannot be read is
// simply false: the question was "is it there", and it is not.
func FileCondition(dir, path string, wantDir bool) Condition {
	spec := "-f " + path
	if wantDir {
		spec = "-d " + path
	}
	full := path
	if !filepath.IsAbs(full) {
		full = filepath.Join(dir, path)
	}
	info, err := os.Stat(full)
	held := err == nil && info.IsDir()
	if !wantDir {
		held = err == nil && info.Mode().IsRegular()
	}
	return ResolvedCondition(spec, held)
}

// checkCondName rejects a name no environment could carry, quoting the whole
// spec so the message points at what the user typed rather than at the
// fragment parsing happened to isolate.
func checkCondName(name, spec string) error {
	if name == "" {
		return fmt.Errorf("condition %q names no variable", spec)
	}
	if !release.ValidEnvName(name) {
		return fmt.Errorf("condition %q: %q is not a variable name ([A-Za-z_][A-Za-z0-9_]*)", spec, name)
	}
	return nil
}

// Match answers the condition against a lookup of the variable's value.
//
// The lookup reports the value alone, with no "was it set" flag, because no
// operator here distinguishes unset from empty: "set" means set and non-empty,
// matching the shell's [ -n "$NAME" ], since a CI system that exports an empty
// variable has not answered yes. An unset variable therefore equals only the
// empty value, exactly as $NAME expands to nothing in the shell.
func (c Condition) Match(lookup func(string) string) bool {
	// A resolved condition already holds its answer and names no variable, so
	// it is the one kind that must not reach for the environment.
	switch c.op {
	case opHeld:
		return true
	case opNotHeld:
		return false
	}
	value := lookup(c.Name)
	switch c.op {
	case opSet:
		return value != ""
	case opUnset:
		return value == ""
	case opEq:
		return value == c.Value
	case opNe:
		return value != c.Value
	case opGlob:
		return globx.Match(c.Value, value)
	case opNotGlob:
		return !globx.Match(c.Value, value)
	}
	return false
}
