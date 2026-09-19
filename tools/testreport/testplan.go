package main

// The traceability gate. Two documents describe what the suite proves: the
// integration test plan, which gives every integration test one goal, and the
// release-candidate requirement matrix, which maps each requirement to the
// assertions that demonstrate it. Both are prose until something checks that
// the names in them are the names in the tree, which is what this verb does.
//
// It is Go rather than shell because the gate runs on hosts that carry no
// ripgrep, and because the matrix arithmetic is a claim about the release: a
// percentage nobody can recompute is not evidence.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// validatedModules are the workspace folders whose test functions a document
// may name. A reference outside them has nothing to resolve against.
var validatedModules = []string{
	"pkg/ccme", "pkg/config", "pkg/manifest", "pkg/models", "pkg/scanner",
	"pkg/writer", "services/dispat", "tests/integration", "tools",
}

// integrationPrefix is the folder whose tests must each carry a plan goal.
const integrationPrefix = "tests/integration/"

const (
	defaultPlanPath         = "tests/integration/docs/test-plan.md"
	defaultRequirementsPath = "tests/integration/docs/qa-requirements.md"
)

// testRef is one reference as a document wrote it: the optional
// repository-relative file, the function name, and where it was read from.
type testRef struct {
	file   string
	name   string
	source string
	line   int
}

func (r testRef) String() string {
	if r.file == "" {
		return r.name
	}
	return r.file + "::" + r.name
}

// refPattern matches a reference inside backticks: a bare `TestName`, or a
// `path/to/file_test.go::TestName` qualified by the file that defines it.
var refPattern = regexp.MustCompile("`(?:([^`\\s]+_test\\.go)::)?(Test[A-Za-z0-9_]+)")

// funcPattern matches a test function declaration at the start of a line.
var funcPattern = regexp.MustCompile(`^func (Test[A-Za-z0-9_]+)\s*\(`)

// definitions is every test function the validated modules declare, keyed by
// function name and carrying the repository-relative files that declare it.
type definitions struct {
	files map[string][]string
	pairs map[string]bool // "file\tname"
}

func (d definitions) isDefinedIn(file, name string) bool { return d.pairs[file+"\t"+name] }

// collectDefinitions reads every `_test.go` file under the validated modules.
//
// The declarations are found by pattern rather than by parsing: a gate that
// fails to build because one module is mid-edit would stop reporting the thing
// it exists to report, and a test function is a line with a fixed shape.
func collectDefinitions(root string) (definitions, error) {
	defs := definitions{files: map[string][]string{}, pairs: map[string]bool{}}
	for _, module := range validatedModules {
		dir := filepath.Join(root, filepath.FromSlash(module))
		info, err := os.Stat(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return definitions{}, err
		}
		if !info.IsDir() {
			continue
		}
		err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if entry.Name() == "testdata" || entry.Name() == "node_modules" {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			for _, line := range strings.Split(string(body), "\n") {
				match := funcPattern.FindStringSubmatch(line)
				if match == nil {
					continue
				}
				name := match[1]
				if !defs.pairs[rel+"\t"+name] {
					defs.pairs[rel+"\t"+name] = true
					defs.files[name] = append(defs.files[name], rel)
				}
			}
			return nil
		})
		if err != nil {
			return definitions{}, err
		}
	}
	for name := range defs.files {
		sort.Strings(defs.files[name])
	}
	return defs, nil
}

// readRefs extracts every backticked test reference from a document.
func readRefs(root, rel string) ([]testRef, error) {
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var refs []testRef
	for i, line := range strings.Split(string(body), "\n") {
		for _, match := range refPattern.FindAllStringSubmatch(line, -1) {
			refs = append(refs, testRef{file: match[1], name: match[2], source: rel, line: i + 1})
		}
	}
	return refs, nil
}

// resolve says what a reference means against the declarations: whether it
// exists, whether a bare name is ambiguous, and which declarations it covers.
func resolve(defs definitions, ref testRef) (covered []string, missing bool, ambiguous bool) {
	if ref.file != "" {
		if defs.isDefinedIn(ref.file, ref.name) {
			return []string{ref.file + "\t" + ref.name}, false, false
		}
		return nil, true, false
	}
	files := defs.files[ref.name]
	switch len(files) {
	case 0:
		return nil, true, false
	case 1:
		return []string{files[0] + "\t" + ref.name}, false, false
	default:
		return nil, false, true
	}
}

// planReport is what the gate found in the two documents.
type planReport struct {
	References   int
	Integration  int
	Missing      []string
	Ambiguous    []string
	Unassigned   []string
	Requirements requirementReport
}

// requirementReport is the matrix arithmetic: how many requirements name an
// assertion that exists, and how the critical subset fares.
type requirementReport struct {
	Present       bool
	Total         int
	Mapped        int
	Critical      int
	CriticalMappd int
	Problems      []string
}

func (r requirementReport) mappedPercent() float64 {
	if r.Total == 0 {
		return 0
	}
	return float64(r.Mapped) * 100 / float64(r.Total)
}

func (r requirementReport) criticalPercent() float64 {
	if r.Critical == 0 {
		return 0
	}
	return float64(r.CriticalMappd) * 100 / float64(r.Critical)
}

// requirement is one matrix row.
type requirement struct {
	ID        string
	Critical  bool
	Goal      string
	Assertion string
	Refs      []testRef
	Status    string
	Line      int
}

var idPattern = regexp.MustCompile(`^[A-Z]{3}-[0-9]{2}$`)

// statusMapped and statusUncovered are the whole status vocabulary. A row
// either names an assertion this tool can find or it does not; whether that
// assertion passed is the suite run's answer, recorded in the release
// evidence, and not something a document may assert on its own.
const (
	statusMapped    = "mapped"
	statusUncovered = "uncovered"
)

// parseRequirements reads the matrix rows out of the requirement document.
//
// The rows are ordinary Markdown tables, so the parser takes any line with six
// cells whose first cell looks like a requirement id. Headings, prose and the
// separator rows are simply not rows.
func parseRequirements(root, rel string) ([]requirement, bool, error) {
	path := filepath.Join(root, filepath.FromSlash(rel))
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var requirements []requirement
	for i, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}
		cells := splitRow(trimmed)
		if len(cells) != 6 || !idPattern.MatchString(cells[0]) {
			continue
		}
		req := requirement{
			ID:        cells[0],
			Critical:  strings.EqualFold(cells[1], "yes"),
			Goal:      cells[2],
			Assertion: cells[3],
			Status:    strings.ToLower(cells[5]),
			Line:      i + 1,
		}
		for _, match := range refPattern.FindAllStringSubmatch(cells[4], -1) {
			req.Refs = append(req.Refs, testRef{file: match[1], name: match[2], source: rel, line: i + 1})
		}
		requirements = append(requirements, req)
	}
	return requirements, true, nil
}

// splitRow splits one Markdown table row into its trimmed cells.
func splitRow(line string) []string {
	line = strings.TrimSuffix(strings.TrimPrefix(line, "|"), "|")
	cells := strings.Split(line, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

// checkDocuments is the gate's body: it resolves every reference in both
// documents, decides which integration tests carry a goal, and computes the
// requirement matrix arithmetic.
func checkDocuments(root, planPath, requirementsPath string) (planReport, error) {
	defs, err := collectDefinitions(root)
	if err != nil {
		return planReport{}, err
	}
	planRefs, err := readRefs(root, planPath)
	if err != nil {
		return planReport{}, err
	}

	report := planReport{}
	assigned := map[string]bool{}
	seenRef := map[string]bool{}
	note := func(set *[]string, text string) { *set = append(*set, text) }

	consider := func(ref testRef, assign bool) {
		key := ref.source + "\t" + ref.String()
		if seenRef[key] {
			return
		}
		seenRef[key] = true
		covered, missing, ambiguous := resolve(defs, ref)
		switch {
		case missing:
			note(&report.Missing, fmt.Sprintf("%s:%d: %s", ref.source, ref.line, ref))
		case ambiguous:
			note(&report.Ambiguous, fmt.Sprintf("%s:%d: %s is declared in %s",
				ref.source, ref.line, ref.name, strings.Join(defs.files[ref.name], ", ")))
		case assign:
			for _, key := range covered {
				assigned[key] = true
			}
		}
	}

	unique := map[string]bool{}
	for _, ref := range planRefs {
		unique[ref.String()] = true
		consider(ref, true)
	}
	report.References = len(unique)

	requirements, present, err := parseRequirements(root, requirementsPath)
	if err != nil {
		return planReport{}, err
	}
	report.Requirements.Present = present
	if present && len(requirements) == 0 {
		report.Requirements.Problems = append(report.Requirements.Problems,
			fmt.Sprintf("%s: no requirement rows; a row is `| ID | critical | goal | assertion | tests | status |`",
				requirementsPath))
	}
	seenID := map[string]int{}
	for _, req := range requirements {
		report.Requirements.Total++
		if req.Critical {
			report.Requirements.Critical++
		}
		if previous, ok := seenID[req.ID]; ok {
			report.Requirements.Problems = append(report.Requirements.Problems,
				fmt.Sprintf("%s:%d: requirement %s repeats the id first used on line %d",
					requirementsPath, req.Line, req.ID, previous))
		}
		seenID[req.ID] = req.Line
		if req.Goal == "" || req.Assertion == "" {
			report.Requirements.Problems = append(report.Requirements.Problems,
				fmt.Sprintf("%s:%d: requirement %s needs both a goal and a behavioral assertion",
					requirementsPath, req.Line, req.ID))
		}
		mapped := false
		for _, ref := range req.Refs {
			consider(ref, false)
			if _, missing, ambiguous := resolve(defs, ref); !missing && !ambiguous {
				mapped = true
			}
		}
		if mapped {
			report.Requirements.Mapped++
			if req.Critical {
				report.Requirements.CriticalMappd++
			}
		}
		want := statusUncovered
		if mapped {
			want = statusMapped
		}
		if req.Status != want {
			report.Requirements.Problems = append(report.Requirements.Problems,
				fmt.Sprintf("%s:%d: requirement %s is recorded as %q but its named assertions are %s",
					requirementsPath, req.Line, req.ID, req.Status, want))
		}
	}

	var integration []string
	for name, files := range defs.files {
		for _, file := range files {
			if !strings.HasPrefix(file, integrationPrefix) || name == "TestMain" {
				continue
			}
			integration = append(integration, file+"\t"+name)
		}
	}
	sort.Strings(integration)
	report.Integration = len(integration)
	for _, key := range integration {
		if !assigned[key] {
			report.Unassigned = append(report.Unassigned, strings.ReplaceAll(key, "\t", "::"))
		}
	}
	sort.Strings(report.Missing)
	sort.Strings(report.Ambiguous)
	sort.Strings(report.Unassigned)
	return report, nil
}

// testPlanCheck is the verb: it reports what the documents claim and fails on
// a claim that is not true of the tree.
func testPlanCheck(args []string, out io.Writer) error {
	set := flag.NewFlagSet("testplan", flag.ContinueOnError)
	set.SetOutput(out)
	plan := set.String("plan", defaultPlanPath, "the integration test plan")
	requirements := set.String("requirements", defaultRequirementsPath, "the release-candidate requirement matrix")
	minimumMapped := set.Float64("minimum-mapped", 0, "minimum percentage of requirements naming an existing assertion")
	minimumCritical := set.Float64("minimum-critical", 0, "minimum percentage of critical requirements naming an existing assertion")
	if err := set.Parse(args); err != nil {
		return err
	}
	root := "."
	switch set.NArg() {
	case 0:
	case 1:
		root = set.Arg(0)
	default:
		return fmt.Errorf("testplan takes at most one repository root")
	}

	report, err := checkDocuments(root, *plan, *requirements)
	if err != nil {
		return err
	}

	var problems []error
	if len(report.Missing) > 0 {
		problems = append(problems, fmt.Errorf("references to tests that do not exist:\n  %s",
			strings.Join(report.Missing, "\n  ")))
	}
	if len(report.Ambiguous) > 0 {
		problems = append(problems, fmt.Errorf("ambiguous bare test names; qualify them as path/to/file_test.go::TestName:\n  %s",
			strings.Join(report.Ambiguous, "\n  ")))
	}
	if len(report.Unassigned) > 0 {
		problems = append(problems, fmt.Errorf("integration tests without an explicit test-plan goal:\n  %s",
			strings.Join(report.Unassigned, "\n  ")))
	}
	if len(report.Requirements.Problems) > 0 {
		problems = append(problems, fmt.Errorf("requirement matrix:\n  %s",
			strings.Join(report.Requirements.Problems, "\n  ")))
	}

	fmt.Fprintf(out, "test plan: %d referenced tests, %d integration goal assignments\n",
		report.References, report.Integration)
	if report.Requirements.Present {
		req := report.Requirements
		fmt.Fprintf(out, "requirements: %d/%d mapped (%.1f%%), critical %d/%d mapped (%.1f%%)\n",
			req.Mapped, req.Total, req.mappedPercent(),
			req.CriticalMappd, req.Critical, req.criticalPercent())
		if *minimumMapped > 0 && req.mappedPercent()+1e-9 < *minimumMapped {
			problems = append(problems, fmt.Errorf("mapped requirement coverage %.1f%% is below %.1f%%",
				req.mappedPercent(), *minimumMapped))
		}
		if *minimumCritical > 0 && req.criticalPercent()+1e-9 < *minimumCritical {
			problems = append(problems, fmt.Errorf("critical requirement coverage %.1f%% is below %.1f%%",
				req.criticalPercent(), *minimumCritical))
		}
	} else if *minimumMapped > 0 || *minimumCritical > 0 {
		problems = append(problems, fmt.Errorf("no requirement matrix at %s to measure", *requirements))
	}
	return errors.Join(problems...)
}
