package main

import (
	"os"
	"path/filepath"
	"testing"
)

// workspace writes a minimal go.work + module with one test into a temp dir
// and chdirs into the module folder, the way a package's `tests` script
// stands inside its package. t.Chdir restores the working directory.
func workspace(t *testing.T, testBody string) string {
	t.Helper()
	root := t.TempDir()
	mod := filepath.Join(root, "mod")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(root, "go.work"):    "go 1.26\n\nuse ./mod\n",
		filepath.Join(mod, "go.mod"):      "module example.test/mod\n\ngo 1.26\n",
		filepath.Join(mod, "mod.go"):      "package mod\n",
		filepath.Join(mod, "mod_test.go"): testBody,
	}
	for name, body := range files {
		if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(mod)
	return root
}

// TestGoTestUsage: the argument shape is <name> -- <args...>, and anything
// else is a usage error (2), because running nothing silently is how a typo
// hides.
func TestGoTestUsage(t *testing.T) {
	for name, args := range map[string][]string{
		"no arguments":   nil,
		"no separator":   {"unit", "./..."},
		"empty log name": {"", "--", "./..."},
	} {
		t.Run(name, func(t *testing.T) {
			code, err := goTest(args)
			if code != 2 || err == nil {
				t.Fatalf("goTest(%q) = %d, %v; want 2 and a usage error", args, code, err)
			}
		})
	}
}

// TestGoTestPassWritesLog: a passing run exits 0 and leaves the -json stream
// as coverage/testlog/<name>.json at the workspace root, readable by the same
// parser `build` folds it with.
func TestGoTestPassWritesLog(t *testing.T) {
	root := workspace(t, "package mod\n\nimport \"testing\"\n\nfunc TestOK(t *testing.T) {}\n")

	code, err := goTest([]string{"unit", "--", "./..."})
	if err != nil || code != 0 {
		t.Fatalf("goTest = %d, %v; want 0, nil", code, err)
	}
	log, err := readLogFile("unit", filepath.Join(root, "coverage", "testlog", "unit.json"))
	if err != nil {
		t.Fatalf("the written log does not parse: %v", err)
	}
	if log.Tests != 1 || log.Passed != 1 || log.Failed != 0 {
		t.Fatalf("log counts = %d tests, %d passed, %d failed; want 1, 1, 0", log.Tests, log.Passed, log.Failed)
	}
}

// TestGoTestFailurePropagates: the exit code is the test run's own, and the
// log is still written — a failing suite is exactly the run the report must
// not lose.
func TestGoTestFailurePropagates(t *testing.T) {
	root := workspace(t, "package mod\n\nimport \"testing\"\n\nfunc TestNo(t *testing.T) { t.Fatal(\"no\") }\n")

	code, err := goTest([]string{"unit", "--", "./..."})
	if err != nil {
		t.Fatalf("goTest error = %v; a test failure is a code, not an error", err)
	}
	if code == 0 {
		t.Fatal("goTest = 0; want the failing run's own nonzero code")
	}
	log, err := readLogFile("unit", filepath.Join(root, "coverage", "testlog", "unit.json"))
	if err != nil {
		t.Fatalf("the written log does not parse: %v", err)
	}
	if log.Failed != 1 {
		t.Fatalf("log counts %d failed; want 1", log.Failed)
	}
}

// TestRepoRootNotFound: outside any workspace the command refuses with a
// message naming what it looked for, rather than writing a log to a guessed
// folder.
func TestRepoRootNotFound(t *testing.T) {
	t.Chdir(t.TempDir())
	code, err := goTest([]string{"unit", "--", "./..."})
	if code != 1 || err == nil {
		t.Fatalf("goTest = %d, %v; want 1 and the no-go.work error", code, err)
	}
}
