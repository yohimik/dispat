package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixture lays out a miniature workspace: files keyed by their
// repository-relative path, so each case reads as the tree it is about.
func writeFixture(t *testing.T, files map[string]string) string {
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

func runTestPlan(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := testPlanCheck(append(args, root), &out)
	return out.String(), err
}

const integrationTest = "package integration\n\nfunc TestCovered(t *testing.T) {}\n"

// goalOne is the heading every plan fixture files its rows under: a test is
// given its goal by a row below a numbered goal heading and nowhere else.
const goalOne = "### Goal 1: the fixture\n\n"

// TestTestPlanAcceptsAReferencedIntegrationTest is the success case the gate
// exists to allow: one integration test, one plan row naming it.
func TestTestPlanAcceptsAReferencedIntegrationTest(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"tests/integration/a_test.go":         integrationTest,
		"tests/integration/docs/test-plan.md": goalOne + "| `TestCovered` | the assertion |\n",
	})
	out, err := runTestPlan(t, root)
	if err != nil {
		t.Fatalf("checker refused a valid plan: %v\n%s", err, out)
	}
	if !strings.Contains(out, "1 referenced tests, 1 integration goal assignments") {
		t.Fatalf("summary does not count the reference and the assignment: %s", out)
	}
}

// TestTestPlanRefusesAnAmbiguousBareName is the reason references may be
// qualified at all: one name, two files, no way to tell which was meant.
func TestTestPlanRefusesAnAmbiguousBareName(t *testing.T) {
	files := map[string]string{
		"tests/integration/a_test.go":         "package integration\n\nfunc TestSame(t *testing.T) {}\n",
		"services/dispat/a_test.go":           "package dispat\n\nfunc TestSame(t *testing.T) {}\n",
		"tests/integration/docs/test-plan.md": goalOne + "| `TestSame` | the assertion |\n",
	}
	root := writeFixture(t, files)
	_, err := runTestPlan(t, root)
	if err == nil || !strings.Contains(err.Error(), "ambiguous bare test names") {
		t.Fatalf("an ambiguous name was accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "services/dispat/a_test.go") {
		t.Fatalf("the refusal does not name both declarations: %v", err)
	}

	files["tests/integration/docs/test-plan.md"] = goalOne + "| `tests/integration/a_test.go::TestSame` | the assertion |\n"
	root = writeFixture(t, files)
	if out, err := runTestPlan(t, root); err != nil {
		t.Fatalf("qualifying the reference did not settle it: %v\n%s", err, out)
	}
}

// TestTestPlanRefusesAnUnassignedIntegrationTest keeps the plan a complete
// index rather than a sample of the suite.
func TestTestPlanRefusesAnUnassignedIntegrationTest(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"tests/integration/a_test.go":         "package integration\n\nfunc TestUnassigned(t *testing.T) {}\nfunc TestMain(m *testing.M) {}\n",
		"tests/integration/docs/test-plan.md": "",
	})
	_, err := runTestPlan(t, root)
	if err == nil || !strings.Contains(err.Error(), "without an explicit test-plan goal") {
		t.Fatalf("an unassigned integration test was accepted: %v", err)
	}
	if strings.Contains(err.Error(), "TestMain") {
		t.Fatalf("TestMain is harness plumbing and needs no goal: %v", err)
	}
}

// TestTestPlanRefusesATestNamedTwice holds the plan to one goal per test: a
// second row, in the same goal or another, is a second claim about what the
// test proves, and the two drift apart.
func TestTestPlanRefusesATestNamedTwice(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"tests/integration/a_test.go": integrationTest,
		"tests/integration/docs/test-plan.md": goalOne + "| `TestCovered` | the assertion |\n\n" +
			"### Goal 2: another\n\nThe prose of goal 2 mentions `TestCovered` again.\n",
	})
	_, err := runTestPlan(t, root)
	if err == nil || !strings.Contains(err.Error(), "names more than once") {
		t.Fatalf("a test named twice was accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "test-plan.md:3, tests/integration/docs/test-plan.md:7") {
		t.Fatalf("the refusal does not name both lines: %v", err)
	}
}

// TestTestPlanRefusesATestNamedOutsideAGoal: a row under the architecture
// notes or an unnumbered section is a mention, not a goal.
func TestTestPlanRefusesATestNamedOutsideAGoal(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"tests/integration/a_test.go": integrationTest,
		"tests/integration/docs/test-plan.md": "## Coverage matrix\n\n### Goal 1: the fixture\n\n" +
			"## Coverage scenarios\n\n### Loose ends\n\n| `TestCovered` | the assertion |\n",
	})
	_, err := runTestPlan(t, root)
	if err == nil || !strings.Contains(err.Error(), "outside every numbered goal heading") {
		t.Fatalf("a test outside every goal was accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "test-plan.md:9: tests/integration/a_test.go::TestCovered") {
		t.Fatalf("the refusal does not name the line and the test: %v", err)
	}
}

// TestTestPlanLetsTheFencesCiteAnyTest: the fence sections name the tests
// that guard a defect, integration and unit alike, beside the goal row that
// assigns each one. A citation is neither the naming nor a second one, and a
// heading inside a code block is text.
func TestTestPlanLetsTheFencesCiteAnyTest(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"tests/integration/a_test.go": integrationTest,
		"services/dispat/a_test.go":   "package dispat\n\nfunc TestUnit(t *testing.T) {}\n",
		"tests/integration/docs/test-plan.md": goalOne + "| `TestCovered` | the assertion |\n\n" +
			"```\n## Bug fences\n```\n\n" +
			"## Regression fences\n\nGuarded by `TestCovered` and `TestUnit`.\n\n" +
			"## Bug fences\n\n| Defect | Guarded by | Where |\n|---|---|---|\n" +
			"| a defect | `TestCovered`, `TestUnit` | both suites |\n",
	})
	if out, err := runTestPlan(t, root); err != nil {
		t.Fatalf("a citation in a fence was counted as a naming: %v\n%s", err, out)
	}

	root = writeFixture(t, map[string]string{
		"tests/integration/a_test.go":         integrationTest,
		"tests/integration/docs/test-plan.md": "## Bug fences\n\n| a defect | `TestCovered` | here |\n",
	})
	_, err := runTestPlan(t, root)
	if err == nil || !strings.Contains(err.Error(), "without an explicit test-plan goal") {
		t.Fatalf("a test only a fence cites was given a goal: %v", err)
	}
}

// TestTestPlanRefusesAReferenceToNothing catches the rename that left a
// document naming a test the tree no longer has.
func TestTestPlanRefusesAReferenceToNothing(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"tests/integration/docs/test-plan.md": goalOne + "| `TestMissing` | the assertion |\n",
	})
	_, err := runTestPlan(t, root)
	if err == nil || !strings.Contains(err.Error(), "tests that do not exist") {
		t.Fatalf("a dangling reference was accepted: %v", err)
	}
}

// TestTestPlanRefusesAQualifiedReferenceToTheWrongFile proves qualification is
// checked rather than merely parsed.
func TestTestPlanRefusesAQualifiedReferenceToTheWrongFile(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"tests/integration/a_test.go":         integrationTest,
		"tests/integration/docs/test-plan.md": goalOne + "| `tests/integration/b_test.go::TestCovered` | the assertion |\n",
	})
	_, err := runTestPlan(t, root)
	if err == nil || !strings.Contains(err.Error(), "tests that do not exist") {
		t.Fatalf("a reference to the wrong file was accepted: %v", err)
	}
}

const matrixHeader = "| ID | Critical | Goal | Assertion | Tests | Status |\n|---|---|---|---|---|---|\n"

// TestRequirementMatrixCountsMappedAndCriticalCoverage is the arithmetic the
// release reports: it must be computed from the rows and the tree, not typed.
func TestRequirementMatrixCountsMappedAndCriticalCoverage(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"tests/integration/a_test.go":         integrationTest,
		"tests/integration/docs/test-plan.md": goalOne + "| `TestCovered` | the assertion |\n",
		"tests/integration/docs/qa-requirements.md": matrixHeader +
			"| CFG-01 | yes | configuration | the ladder folds | `TestCovered` | mapped |\n" +
			"| CFG-02 | no | configuration | a cycle is refused | none yet | uncovered |\n",
	})
	out, err := runTestPlan(t, root)
	if err != nil {
		t.Fatalf("a consistent matrix was refused: %v\n%s", err, out)
	}
	if !strings.Contains(out, "requirements: 1/2 mapped (50.0%), critical 1/1 mapped (100.0%)") {
		t.Fatalf("the matrix arithmetic is not reported: %s", out)
	}
}

// TestRequirementMatrixEnforcesItsThresholds is the gate the release runs.
func TestRequirementMatrixEnforcesItsThresholds(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"tests/integration/a_test.go":         integrationTest,
		"tests/integration/docs/test-plan.md": goalOne + "| `TestCovered` | the assertion |\n",
		"tests/integration/docs/qa-requirements.md": matrixHeader +
			"| CFG-01 | yes | configuration | the ladder folds | `TestCovered` | mapped |\n" +
			"| CFG-02 | no | configuration | a cycle is refused | none yet | uncovered |\n",
	})
	_, err := runTestPlan(t, root, "-minimum-mapped", "95")
	if err == nil || !strings.Contains(err.Error(), "mapped requirement coverage 50.0% is below 95.0%") {
		t.Fatalf("the mapped threshold did not hold: %v", err)
	}
	if _, err := runTestPlan(t, root, "-minimum-critical", "100"); err != nil {
		t.Fatalf("a fully mapped critical set was refused: %v", err)
	}
}

// TestRequirementMatrixRefusesAStatusTheTreeDoesNotSupport is what stops a row
// from claiming coverage it does not have.
func TestRequirementMatrixRefusesAStatusTheTreeDoesNotSupport(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"tests/integration/a_test.go":         integrationTest,
		"tests/integration/docs/test-plan.md": goalOne + "| `TestCovered` | the assertion |\n",
		"tests/integration/docs/qa-requirements.md": matrixHeader +
			"| CFG-01 | yes | configuration | the ladder folds | none yet | mapped |\n",
	})
	_, err := runTestPlan(t, root)
	if err == nil || !strings.Contains(err.Error(), `recorded as "mapped" but its named assertions are uncovered`) {
		t.Fatalf("an unsupported status was accepted: %v", err)
	}
}

// TestRequirementMatrixRefusesAnIncompleteRow keeps every requirement stating
// a goal and an assertion rather than one of them.
func TestRequirementMatrixRefusesAnIncompleteRow(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"tests/integration/a_test.go":         integrationTest,
		"tests/integration/docs/test-plan.md": goalOne + "| `TestCovered` | the assertion |\n",
		"tests/integration/docs/qa-requirements.md": matrixHeader +
			"| CFG-01 | yes |  |  | `TestCovered` | mapped |\n" +
			"| CFG-01 | no | configuration | it folds | `TestCovered` | mapped |\n",
	})
	_, err := runTestPlan(t, root)
	if err == nil {
		t.Fatal("an incomplete row was accepted")
	}
	if !strings.Contains(err.Error(), "needs both a goal and a behavioral assertion") {
		t.Fatalf("the empty cells were not reported: %v", err)
	}
	if !strings.Contains(err.Error(), "repeats the id") {
		t.Fatalf("the duplicate id was not reported: %v", err)
	}
}

// TestRequirementMatrixRefusesAReferenceToNothing holds the matrix to the same
// reference integrity as the plan.
func TestRequirementMatrixRefusesAReferenceToNothing(t *testing.T) {
	root := writeFixture(t, map[string]string{
		"tests/integration/a_test.go":         integrationTest,
		"tests/integration/docs/test-plan.md": goalOne + "| `TestCovered` | the assertion |\n",
		"tests/integration/docs/qa-requirements.md": matrixHeader +
			"| CFG-01 | yes | configuration | it folds | `TestGone` | mapped |\n",
	})
	_, err := runTestPlan(t, root)
	if err == nil || !strings.Contains(err.Error(), "tests that do not exist") {
		t.Fatalf("a dangling matrix reference was accepted: %v", err)
	}
}
