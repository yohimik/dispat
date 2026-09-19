// Command namingcheck enforces the naming rules CONTRIBUTING.md states, over
// the first-party Go code of this workspace.
//
// Three of those rules are mechanical, and only those three are checked here:
//
//   - predicate: an exported function or method whose single result is a bool
//     is a predicate and starts with "Is". A lookup returning a value and a
//     presence boolean has two results and keeps its action name, so it is not
//     a predicate and is never reported.
//   - interface: an exported interface is named for a capability, which is an
//     adjective ("Configurable") or an "x" suffix ("Gitx").
//   - type: an exported named type that is not an interface is a noun. What is
//     mechanical about that is the vocabulary it must not borrow: the
//     adjective suffixes interfaces use, and the predicate prefixes.
//
// Whether a noun is the right noun is a review question, not a check. The gate
// exists to stop the vocabulary drifting, not to name things.
//
// Everything else is an exception, and exceptions are written down:
// naming-exceptions.txt beside this file carries the external contracts
// (Error, String, sort.Interface, the standard library's own interfaces) and
// the deprecated forwarders that exist precisely because a published name
// cannot change. A single declaration may also carry a "//namingcheck:exempt"
// marker in its doc comment, with the reason on the same line.
package main

import (
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// checkedTrees are the first-party folders the gate reads. Everything else in
// the workspace is a dependency, a fixture or generated.
var checkedTrees = []string{"pkg", "services/dispat", "tools", "tests/integration"}

// rule names one of the three mechanical rules, and is what an exception line
// has to name so that an exception for a predicate cannot silently excuse a
// type.
type rule string

const (
	rulePredicate rule = "predicate"
	ruleInterface rule = "interface"
	ruleType      rule = "type"
)

// adjectiveSuffixes are the endings that make a name read as a capability
// rather than as a thing. An interface may end in one; an implementation may
// not.
var adjectiveSuffixes = []string{"able", "ible"}

// predicatePrefixes are the openings that make a name read as a question. A
// type may not start with one.
var predicatePrefixes = []string{"Is", "Has", "Can", "Should"}

// finding is one declaration that breaks one rule.
type finding struct {
	Rule rule
	File string
	Line int
	Name string
	Why  string
}

func (f finding) String() string {
	return fmt.Sprintf("%s:%d: %s: %s %s", f.File, f.Line, f.Rule, f.Name, f.Why)
}

// exception is one written-down permission to break one rule.
//
// A reason beginning with "pending" marks a name that should still change: the
// declaration belongs to a review that owns that tree, and the exception is
// the backlog entry rather than a verdict. The gate counts those separately so
// the list cannot quietly become permanent.
type exception struct {
	Rule    rule
	Path    string
	Name    string
	Reason  string
	Pending bool
}

// isMatch says whether the exception covers this finding. A "*" or "**" path
// covers every file; any other path is a shell pattern over the
// repository-relative path. A "*" name covers every declaration in that file,
// which is how a whole compatibility file is excused at once.
func (e exception) isMatch(f finding) bool {
	if e.Rule != f.Rule {
		return false
	}
	if e.Name != "*" && e.Name != f.Name {
		return false
	}
	if e.Path == "*" || e.Path == "**" {
		return true
	}
	ok, err := filepath.Match(e.Path, f.File)
	return err == nil && ok
}

// readExceptions parses the exceptions file: blank lines and "#" comments are
// skipped, and every other line is "rule path name" with an optional trailing
// "# reason" the file keeps for its readers.
func readExceptions(path string) ([]exception, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var exceptions []exception
	for i, line := range strings.Split(string(body), "\n") {
		reason := ""
		if cut := strings.Index(line, "#"); cut >= 0 {
			reason = strings.TrimSpace(line[cut+1:])
			line = line[:cut]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 3 {
			return nil, fmt.Errorf("%s:%d: an exception is `rule path name`, followed by `# reason`", path, i+1)
		}
		r := rule(fields[0])
		switch r {
		case rulePredicate, ruleInterface, ruleType:
		default:
			return nil, fmt.Errorf("%s:%d: %q is not one of predicate, interface, type", path, i+1, fields[0])
		}
		if reason == "" {
			return nil, fmt.Errorf("%s:%d: an exception needs a reason after `#`", path, i+1)
		}
		exceptions = append(exceptions, exception{
			Rule: r, Path: fields[1], Name: fields[2], Reason: reason,
			Pending: strings.HasPrefix(strings.ToLower(reason), "pending"),
		})
	}
	return exceptions, nil
}

// isExempt reads the marker a declaration may carry in its own doc comment.
func isExempt(doc *ast.CommentGroup) bool {
	if doc == nil {
		return false
	}
	for _, line := range doc.List {
		if strings.Contains(line.Text, "namingcheck:exempt") {
			return true
		}
	}
	return false
}

// isBoolResult says whether a signature returns exactly one bool, which is
// what makes a function a predicate rather than a lookup.
func isBoolResult(fn *ast.FuncType) bool {
	if fn.Results == nil || len(fn.Results.List) != 1 {
		return false
	}
	field := fn.Results.List[0]
	if len(field.Names) > 1 {
		return false
	}
	ident, ok := field.Type.(*ast.Ident)
	return ok && ident.Name == "bool"
}

func hasAnySuffix(name string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func hasAnyPrefix(name string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// inspectFile reports every rule break one file declares.
func inspectFile(root, path string) ([]finding, error) {
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, path, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return nil, err
	}
	rel = filepath.ToSlash(rel)
	at := func(pos token.Pos) int { return set.Position(pos).Line }

	var findings []finding
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !d.Name.IsExported() || isExempt(d.Doc) || !isBoolResult(d.Type) {
				continue
			}
			if strings.HasPrefix(d.Name.Name, "Is") {
				continue
			}
			findings = append(findings, finding{
				Rule: rulePredicate, File: rel, Line: at(d.Pos()), Name: d.Name.Name,
				Why: "returns one bool, so it is a predicate and starts with Is",
			})
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() {
					continue
				}
				if isExempt(d.Doc) || isExempt(ts.Doc) {
					continue
				}
				name := ts.Name.Name
				if _, isInterface := ts.Type.(*ast.InterfaceType); isInterface {
					if strings.HasSuffix(name, "x") || hasAnySuffix(name, adjectiveSuffixes) {
						continue
					}
					findings = append(findings, finding{
						Rule: ruleInterface, File: rel, Line: at(ts.Pos()), Name: name,
						Why: "is an interface, so it is an adjective or carries the x suffix",
					})
					continue
				}
				switch {
				case hasAnySuffix(name, adjectiveSuffixes):
					findings = append(findings, finding{
						Rule: ruleType, File: rel, Line: at(ts.Pos()), Name: name,
						Why: "is not an interface, so it is a noun rather than a capability adjective",
					})
				case hasAnyPrefix(name, predicatePrefixes):
					findings = append(findings, finding{
						Rule: ruleType, File: rel, Line: at(ts.Pos()), Name: name,
						Why: "is not an interface, so it is a noun rather than a question",
					})
				}
			}
		}
	}
	return findings, nil
}

// inspect walks the checked trees. Test files are left out: a fixture's
// vocabulary is the fixture's own, and the rules are about the API the
// workspace publishes to itself.
func inspect(root string, trees []string) ([]finding, error) {
	var findings []finding
	for _, tree := range trees {
		dir := filepath.Join(root, filepath.FromSlash(tree))
		info, err := os.Stat(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			continue
		}
		err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				switch entry.Name() {
				case "testdata", "node_modules", "vendor":
					return fs.SkipDir
				}
				return nil
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			found, err := inspectFile(root, path)
			if err != nil {
				return err
			}
			findings = append(findings, found...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})
	return findings, nil
}

// keep separates the findings nothing excuses from the ones an exception
// covers, and from the subset of those still waiting for a rename.
func keep(findings []finding, exceptions []exception) (kept, pending []finding) {
	for _, f := range findings {
		var excuse *exception
		for i := range exceptions {
			if exceptions[i].isMatch(f) {
				excuse = &exceptions[i]
				break
			}
		}
		switch {
		case excuse == nil:
			kept = append(kept, f)
		case excuse.Pending:
			f.Why = excuse.Reason
			pending = append(pending, f)
		}
	}
	return kept, pending
}

func main() {
	code, err := run(os.Args[1:], os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "namingcheck: %v\n", err)
	}
	os.Exit(code)
}

// run is the body, over the stream the caller hands it and with the exit code
// returned rather than taken, so a test can read both.
func run(args []string, out io.Writer) (int, error) {
	set := flag.NewFlagSet("namingcheck", flag.ContinueOnError)
	set.SetOutput(out)
	exceptionsPath := set.String("exceptions", "", "the written-down exceptions; tools/namingcheck/naming-exceptions.txt under the root when empty")
	report := set.Bool("report", false, "list the findings and exit 0, which is how an owner reads someone else's tree")
	listPending := set.Bool("pending", false, "also list the excused declarations still waiting for a rename")
	if err := set.Parse(args); err != nil {
		return 2, nil
	}
	root := "."
	trees := checkedTrees
	switch set.NArg() {
	case 0:
	case 1:
		root = set.Arg(0)
	default:
		root, trees = set.Arg(0), set.Args()[1:]
	}
	if *exceptionsPath == "" {
		*exceptionsPath = filepath.Join(root, "tools", "namingcheck", "naming-exceptions.txt")
	}
	exceptions, err := readExceptions(*exceptionsPath)
	if err != nil {
		return 1, err
	}
	findings, err := inspect(root, trees)
	if err != nil {
		return 1, err
	}
	kept, pending := keep(findings, exceptions)
	for _, f := range kept {
		fmt.Fprintln(out, f)
	}
	if *listPending {
		for _, f := range pending {
			fmt.Fprintln(out, f)
		}
	}
	fmt.Fprintf(out, "namingcheck: %d exported declarations break a naming rule, %d excused by naming-exceptions.txt (%d of them pending a rename)\n",
		len(kept), len(findings)-len(kept), len(pending))
	if len(kept) > 0 && !*report {
		return 1, nil
	}
	return 0, nil
}
