package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture lays out a miniature tree: files keyed by their repository-relative
// path, so each case reads as the code it is about.
func fixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func check(t *testing.T, root string, args ...string) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code, err := run(append(args, root, "pkg"), &out)
	if err != nil {
		t.Fatalf("namingcheck failed: %v", err)
	}
	return out.String(), code
}

// TestPredicateRuleReportsABoolFunctionWithoutIs is the rule CONTRIBUTING
// states most plainly, and the one a reviewer is least likely to catch by eye.
func TestPredicateRuleReportsABoolFunctionWithoutIs(t *testing.T) {
	root := fixture(t, map[string]string{"pkg/a/a.go": `package a

type Config struct{}

// Enabled is a predicate spelled as a participle.
func (c Config) Enabled() bool { return true }

// IsReady is spelled correctly.
func (c Config) IsReady() bool { return true }
`})
	out, code := check(t, root)
	if code != 1 {
		t.Fatalf("a bare bool predicate was accepted: %s", out)
	}
	if !strings.Contains(out, "predicate: Enabled") {
		t.Fatalf("the predicate was not named: %s", out)
	}
	if strings.Contains(out, "IsReady") {
		t.Fatalf("a correctly spelled predicate was reported: %s", out)
	}
}

// TestPredicateRuleLeavesLookupsAlone is the exemption CONTRIBUTING writes
// into the rule itself: a lookup returns a value and a presence boolean, and
// keeps its action name.
func TestPredicateRuleLeavesLookupsAlone(t *testing.T) {
	root := fixture(t, map[string]string{"pkg/a/a.go": `package a

type Set struct{}

// FindPackage is a lookup, not a predicate.
func (s Set) FindPackage(name string) (string, bool) { return "", false }

// Count is not a predicate either.
func (s Set) Count() int { return 0 }
`})
	out, code := check(t, root)
	if code != 0 {
		t.Fatalf("a lookup was reported as a predicate: %s", out)
	}
}

// TestInterfaceRuleWantsACapabilityName holds interfaces to the adjective or
// x-suffix vocabulary, and implementations out of it.
func TestInterfaceRuleWantsACapabilityName(t *testing.T) {
	root := fixture(t, map[string]string{"pkg/a/a.go": `package a

// Runner is named for what it does rather than for what it can do.
type Runner interface{ Run() }

// Gitx carries the x suffix.
type Gitx interface{ Head() string }

// Configurable is an adjective.
type Configurable interface{ Configure() }

// LocalGitx is an implementation of Gitx.
type LocalGitx struct{}

// Reloadable is a struct wearing an interface's clothes.
type Reloadable struct{}

// IsPresent is a struct asking a question.
type IsPresent struct{}
`})
	out, code := check(t, root)
	if code != 1 {
		t.Fatalf("the interface and type rules did not hold: %s", out)
	}
	for _, want := range []string{"interface: Runner", "type: Reloadable", "type: IsPresent"} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q is missing from the findings: %s", want, out)
		}
	}
	for _, unwanted := range []string{"Gitx", "Configurable", "LocalGitx"} {
		if strings.Contains(out, unwanted+" ") {
			t.Fatalf("%s was reported: %s", unwanted, out)
		}
	}
}

// TestExceptionsFileExcusesWhatItNames is the documented escape: an external
// contract or a deprecated forwarder stays, in writing, with a reason.
func TestExceptionsFileExcusesWhatItNames(t *testing.T) {
	root := fixture(t, map[string]string{
		"pkg/a/a.go": `package a

type Heap []string

// Less is sort.Interface.
func (h Heap) Less(i, j int) bool { return h[i] < h[j] }
`,
		"pkg/a/compatibility.go": `package a

// Enabled is retained for source compatibility.
//
// Deprecated: use IsEnabled.
func Enabled() bool { return true }
`,
		"exceptions.txt": "predicate pkg/a/a.go Less # sort.Interface\n" +
			"predicate pkg/*/compatibility.go * # deprecated forwarder\n",
	})
	out, code := check(t, root, "-exceptions", filepath.Join(root, "exceptions.txt"))
	if code != 0 {
		t.Fatalf("the written-down exceptions did not excuse their findings: %s", out)
	}
	if !strings.Contains(out, "2 excused") {
		t.Fatalf("the summary does not count the exceptions: %s", out)
	}
}

// TestPendingExceptionsAreCountedApart keeps the backlog visible: an exception
// that means "not yet" must not read like an exception that means "never".
func TestPendingExceptionsAreCountedApart(t *testing.T) {
	root := fixture(t, map[string]string{
		"pkg/a/a.go": `package a

// Ready is a predicate that has not been renamed yet.
func Ready() bool { return true }
`,
		"exceptions.txt": "predicate pkg/a/a.go Ready # pending rename, owned by the planner review\n",
	})
	out, code := check(t, root, "-exceptions", filepath.Join(root, "exceptions.txt"), "-pending")
	if code != 0 {
		t.Fatalf("a pending exception failed the gate: %s", out)
	}
	if !strings.Contains(out, "1 of them pending a rename") {
		t.Fatalf("the pending count is missing: %s", out)
	}
	if !strings.Contains(out, "pending rename, owned by the planner review") {
		t.Fatalf("-pending did not list the backlog entry: %s", out)
	}
}

// TestExceptionsNeedAReason stops the file becoming a list of names nobody can
// justify.
func TestExceptionsNeedAReason(t *testing.T) {
	root := fixture(t, map[string]string{"exceptions.txt": "predicate pkg/a/a.go Ready\n"})
	var out bytes.Buffer
	code, err := run([]string{"-exceptions", filepath.Join(root, "exceptions.txt"), root}, &out)
	if err == nil || code != 1 {
		t.Fatalf("a reasonless exception was accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "needs a reason") {
		t.Fatalf("the refusal does not say what is missing: %v", err)
	}
}

// TestSourceMarkerExemptsOneDeclaration is the in-source form of the same
// escape, for the single external contract that does not deserve a file entry.
func TestSourceMarkerExemptsOneDeclaration(t *testing.T) {
	root := fixture(t, map[string]string{"pkg/a/a.go": `package a

// Writable answers the install Environment contract.
//
//namingcheck:exempt the Environment contract names it
func Writable(dir string) bool { return true }
`})
	out, code := check(t, root)
	if code != 0 {
		t.Fatalf("the source marker did not exempt the declaration: %s", out)
	}
}
