package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// gateCommit is the revision every fixture below claims to have been measured
// at. It is a full-length id because that is what a stamp holds.
const gateCommit = "0123456789abcdef0123456789abcdef01234567"

// productionBlockFiles names one instrumented file per production module,
// spelled the way a coverage profile spells it: by import path.
var productionBlockFiles = map[string]string{
	"ccme":     "github.com/yohimik/dispat/pkg/ccme/x.go",
	"config":   "github.com/yohimik/dispat/pkg/config/x.go",
	"manifest": "github.com/yohimik/dispat/pkg/manifest/x.go",
	"models":   "github.com/yohimik/dispat/pkg/models/x.go",
	"scanner":  "github.com/yohimik/dispat/pkg/scanner/x.go",
	"writer":   "github.com/yohimik/dispat/pkg/writer/x.go",
	"dispat":   "github.com/yohimik/dispat/services/dispat/x.go",
}

// productionProfileNames are the per-module profiles a full run writes, in the
// order the fixtures build them, so a failure names the same one every time.
var productionProfileNames = []string{"ccme", "config", "manifest", "models", "scanner", "writer", "dispat"}

// gateFolder lays out the evidence a complete release run leaves behind: one
// coverage profile per module and the integration profile, each beside the
// stamp naming the commit it was measured at.
//
// miss is how many blocks of pkg/ccme both layers hold and neither reached,
// which is the one knob that moves the percentages off 100 without touching
// the inventory: the denominator is the same either way, so a test about a
// minimum is not also a test about what was measured.
func gateFolder(t *testing.T, commit string, miss int) string {
	t.Helper()
	dir := t.TempDir()
	var integration strings.Builder
	// A blank line, because `go tool covdata textfmt` writes one and a gate that
	// choked on it would refuse every real run.
	integration.WriteString("mode: set\n\n")
	for _, name := range productionProfileNames {
		file := productionBlockFiles[name]
		unit := "mode: atomic\n" + fmt.Sprintf("%s:1.1,2.2 1 1\n", file)
		integration.WriteString(fmt.Sprintf("%s:1.1,2.2 1 1\n", file))
		if name == "ccme" {
			for i := 0; i < miss; i++ {
				block := fmt.Sprintf("%s:%d.1,%d.2 1 0\n", file, 10+i, 11+i)
				unit += block
				integration.WriteString(block)
			}
		}
		write(t, filepath.Join(dir, name+".out"), unit)
		write(t, filepath.Join(dir, name+".commit"), commit+"\n")
	}
	write(t, filepath.Join(dir, "tools.out"),
		"mode: atomic\ngithub.com/yohimik/dispat/tools/testreport/x.go:1.1,2.2 1 1\n")
	write(t, filepath.Join(dir, "tools.commit"), commit+"\n")
	write(t, filepath.Join(dir, "integration.out"), integration.String())
	write(t, filepath.Join(dir, "integration.commit"), commit+"\n")
	return dir
}

// TestCoverageCommandBindsEveryFigureToTheTestedCommit: a percentage with no
// revision beside it is a number somebody quotes later about a tree it was
// never taken from, so the gate prints the commit its stamps were checked
// against, then the two layers separately, then each production module's own
// integration figure.
func TestCoverageCommandBindsEveryFigureToTheTestedCommit(t *testing.T) {
	dir := gateFolder(t, gateCommit, 0)
	var out, errs strings.Builder
	code := run([]string{"coverage", "-coverage", dir, "-commit", gateCommit,
		"-minimum-total", "95", "-minimum-integration", "95"}, &out, &errs)
	if code != 0 || errs.Len() != 0 {
		t.Fatalf("coverage = %d, stderr %q", code, errs.String())
	}
	got := out.String()
	for _, want := range []string{
		"commit " + gateCommit + "\n",
		"combined 8 8 100.0%\n",
		"integration 7 7 100.0%\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("coverage printed:\n%s\nwant %q in it", got, want)
		}
	}
	for _, module := range productionModules {
		want := "integration-module " + module + " 1 1 100.0%\n"
		if module == "pkg/ccme" {
			want = "integration-module pkg/ccme 1 1 100.0%\n"
		}
		if !strings.Contains(got, want) {
			t.Errorf("coverage printed:\n%s\nwant a per-module line %q", got, want)
		}
	}
	if strings.Contains(got, "integration-module tools") {
		t.Errorf("coverage reported a module that is not production:\n%s", got)
	}
}

// TestCoverageCommandSkipsTheBadgeScriptsOwnMergeOutputs: the badge merges the
// profiles back into the same folder it read them from. Those files are not a
// package's evidence, so folding them in would list the badge's working files
// as a run nobody made, and refusing them as unexpected would make the gate
// fail whenever it runs after the badge.
func TestCoverageCommandSkipsTheBadgeScriptsOwnMergeOutputs(t *testing.T) {
	dir := gateFolder(t, gateCommit, 0)
	for name := range mergeOutputs {
		write(t, filepath.Join(dir, name),
			"mode: set\ngithub.com/yohimik/dispat/pkg/ccme/x.go:1.1,2.2 1 1\n")
	}
	var out strings.Builder
	if err := coverageCheck([]string{"-coverage", dir, "-commit", gateCommit,
		"-minimum-total", "100", "-minimum-integration", "100"}, &out); err != nil {
		t.Fatalf("the gate refused its own merge outputs: %v", err)
	}
	if !strings.Contains(out.String(), "combined 8 8 100.0%\n") {
		t.Errorf("a merge output changed the denominator:\n%s", out.String())
	}
}

// TestCoverageCommandEnforcesBothMinimumsSeparately: the two gates are separate
// because the combined figure includes the unit profiles and the integration
// one does not, so a run can meet either and fail the other. Each has to fail
// on its own terms and name its own ratio.
func TestCoverageCommandEnforcesBothMinimumsSeparately(t *testing.T) {
	t.Run("a run that meets both passes", func(t *testing.T) {
		dir := gateFolder(t, gateCommit, 0)
		var out strings.Builder
		if err := coverageCheck([]string{"-coverage", dir, "-commit", gateCommit,
			"-minimum-total", "100", "-minimum-integration", "100"}, &out); err != nil {
			t.Fatalf("a complete run at 100%% was refused: %v", err)
		}
	})

	t.Run("the combined gate names the combined ratio", func(t *testing.T) {
		dir := gateFolder(t, gateCommit, 1)
		var out strings.Builder
		err := coverageCheck([]string{"-coverage", dir, "-commit", gateCommit,
			"-minimum-total", "95", "-minimum-integration", "95"}, &out)
		if err == nil || !strings.Contains(err.Error(), "combined coverage 8/9 (88.9%) is below 95.0%") {
			t.Fatalf("combined gate = %v", err)
		}
	})

	t.Run("the integration gate names the integration ratio", func(t *testing.T) {
		dir := gateFolder(t, gateCommit, 1)
		var out strings.Builder
		err := coverageCheck([]string{"-coverage", dir, "-commit", gateCommit,
			"-minimum-integration", "95"}, &out)
		if err == nil || !strings.Contains(err.Error(), "integration coverage 7/8 (87.5%) is below 95.0%") {
			t.Fatalf("integration gate = %v", err)
		}
	})

	t.Run("a gate nobody asked for admits everything", func(t *testing.T) {
		dir := gateFolder(t, gateCommit, 4)
		var out strings.Builder
		if err := coverageCheck([]string{"-coverage", dir, "-commit", gateCommit}, &out); err != nil {
			t.Fatalf("a run with no minimum was refused: %v", err)
		}
	})

	t.Run("the failure comes back as a non-zero exit", func(t *testing.T) {
		dir := gateFolder(t, gateCommit, 1)
		var out, errs strings.Builder
		if code := run([]string{"coverage", "-coverage", dir, "-commit", gateCommit,
			"-minimum-total", "95"}, &out, &errs); code != 1 {
			t.Fatalf("run = %d, want 1", code)
		}
		if !strings.Contains(out.String(), "commit "+gateCommit) {
			t.Errorf("a failing gate printed no provenance:\n%s", out.String())
		}
	})
}

// TestIsMinimumMetComparesWithoutRounding: the gate is an unrounded ratio,
// because 94.96% rounds to 95.0% and a release that shipped on the rounding is
// a release that did not meet the gate.
func TestIsMinimumMetComparesWithoutRounding(t *testing.T) {
	for _, c := range []struct {
		name    string
		stats   Stats
		minimum float64
		want    bool
	}{
		{"exactly at the gate", Stats{Covered: 95, Statements: 100}, 95, true},
		{"one statement under", Stats{Covered: 9499, Statements: 10000}, 95, false},
		{"one statement over", Stats{Covered: 9501, Statements: 10000}, 95, true},
		{"a ratio that rounds up to the gate", Stats{Covered: 9496, Statements: 10000}, 95, false},
		{"the release's own unrounded ratio", Stats{Covered: 19305, Statements: 20317}, 95, true},
		{"no gate at all", Stats{Covered: 0, Statements: 100}, 0, true},
		{"a negative gate", Stats{}, -1, true},
		{"nothing measured against a gate", Stats{}, 95, false},
		{"everything measured and everything covered", Stats{Covered: 3, Statements: 3}, 100, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := IsMinimumMet(c.stats, c.minimum); got != c.want {
				t.Fatalf("IsMinimumMet(%+v, %v) = %v, want %v", c.stats, c.minimum, got, c.want)
			}
		})
	}
}

// TestCoverageCommandRefusesEvidenceItCannotTrust walks every way the evidence
// can be wrong: stale, missing, mixed, orphaned, and malformed. Each one has to
// stop the gate, because a gate that reads whatever it finds is a gate that
// passes on a run nobody made.
func TestCoverageCommandRefusesEvidenceItCannotTrust(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(t *testing.T, dir string)
		commit  string
		want    string
		noStamp bool
	}{
		{
			name:   "no tested commit was named",
			commit: "-",
			want:   "coverage requires -commit",
		},
		{
			name:   "a profile measured at another revision",
			mutate: func(t *testing.T, dir string) { write(t, filepath.Join(dir, "writer.commit"), "older\n") },
			want:   "writer.out was measured at older",
		},
		{
			name: "two profiles measured at different revisions",
			mutate: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "writer.commit"), "older\n")
				write(t, filepath.Join(dir, "scanner.commit"), "newer\n")
			},
			want: "was measured at",
		},
		{
			name: "a profile the run never wrote",
			mutate: func(t *testing.T, dir string) {
				if err := os.Remove(filepath.Join(dir, "models.out")); err != nil {
					t.Fatal(err)
				}
			},
			want: "missing coverage profile models.out",
		},
		{
			name: "a profile with no stamp beside it",
			mutate: func(t *testing.T, dir string) {
				if err := os.Remove(filepath.Join(dir, "models.commit")); err != nil {
					t.Fatal(err)
				}
			},
			want: "has no tested-commit stamp",
		},
		{
			name: "a profile left over from another run",
			mutate: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "legacy.out"), "mode: atomic\np/a.go:1.1,2.2 1 1\n")
			},
			want: "unexpected coverage profile legacy.out from a mixed run",
		},
		{
			name:   "a stamp whose profile is gone",
			mutate: func(t *testing.T, dir string) { write(t, filepath.Join(dir, "legacy.commit"), gateCommit+"\n") },
			want:   "orphan coverage stamp",
		},
		{
			name: "a profile line that is not one",
			mutate: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "models.out"),
					"mode: atomic\ngithub.com/yohimik/dispat/pkg/models/x.go:1.1,2.2 1\n")
			},
			want: "expected `<block> <statements> <count>`",
		},
		{
			name: "a statement count that is not a number",
			mutate: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "models.out"),
					"mode: atomic\ngithub.com/yohimik/dispat/pkg/models/x.go:1.1,2.2 many 1\n")
			},
			want: "statement count",
		},
		{
			name: "an execution count that is not a number",
			mutate: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "models.out"),
					"mode: atomic\ngithub.com/yohimik/dispat/pkg/models/x.go:1.1,2.2 1 often\n")
			},
			want: "execution count",
		},
		{
			name: "a header naming a mode the toolchain does not write",
			mutate: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "models.out"),
					"mode: guessed\ngithub.com/yohimik/dispat/pkg/models/x.go:1.1,2.2 1 1\n")
			},
			want: `"guessed" is not a coverage mode`,
		},
		{
			name: "a profile with no header at all",
			mutate: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "models.out"),
					"github.com/yohimik/dispat/pkg/models/x.go:1.1,2.2 1 1\n")
			},
			want: "before any mode header",
		},
		{
			name: "a production module measured by nothing",
			mutate: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "models.out"), "mode: atomic\n")
				body, err := os.ReadFile(filepath.Join(dir, "integration.out"))
				if err != nil {
					t.Fatal(err)
				}
				var kept []string
				for _, line := range strings.Split(string(body), "\n") {
					if !strings.Contains(line, "/pkg/models/") {
						kept = append(kept, line)
					}
				}
				write(t, filepath.Join(dir, "integration.out"), strings.Join(kept, "\n"))
			},
			want: "coverage inventory is missing production module pkg/models",
		},
		{
			name: "an integration run that reached no production module",
			mutate: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "integration.out"),
					"mode: set\ngithub.com/yohimik/dispat/tools/testreport/x.go:1.1,2.2 1 1\n")
			},
			want: "integration coverage is missing production module",
		},
		{
			name: "an integration denominator missing a production package",
			mutate: func(t *testing.T, dir string) {
				write(t, filepath.Join(dir, "config.out"), "mode: atomic\n"+
					"github.com/yohimik/dispat/pkg/config/x.go:1.1,2.2 1 1\n"+
					"github.com/yohimik/dispat/pkg/config/watch/watch.go:1.1,2.2 17 1\n")
			},
			want: "pkg/config/watch (17 statements)",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := gateFolder(t, gateCommit, 0)
			if c.mutate != nil {
				c.mutate(t, dir)
			}
			args := []string{"-coverage", dir, "-minimum-total", "95", "-minimum-integration", "95"}
			if c.commit != "-" {
				args = append(args, "-commit", gateCommit)
			}
			var out strings.Builder
			err := coverageCheck(args, &out)
			if err == nil {
				t.Fatalf("the gate accepted the run; it printed:\n%s", out.String())
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %q, want it to mention %q", err.Error(), c.want)
			}
		})
	}
}

// TestCoverageCommandRefusesFlagsItDoesNotHave: a gate invoked with a flag it
// never had must stop rather than run with the flag ignored, which is how a
// renamed threshold silently stops being enforced.
func TestCoverageCommandRefusesFlagsItDoesNotHave(t *testing.T) {
	var out strings.Builder
	if err := coverageCheck([]string{"-minimum", "95"}, &out); err == nil {
		t.Fatal("the gate accepted a flag it does not have")
	}
}

// TestVerifyProductionInventoryRefusesAShrunkenDenominator: the inventory is
// the frozen statement of what a coverage figure is a figure about. A module
// that stopped being measured would make every percentage above it better for
// measuring less, so the gate refuses the run instead.
func TestVerifyProductionInventoryRefusesAShrunkenDenominator(t *testing.T) {
	complete := Coverage{}
	for _, path := range productionModules {
		complete.Modules = append(complete.Modules,
			Module{Path: path, Stats: Stats{Covered: 1, Statements: 1, Percent: 100}})
	}
	complete.Modules = append(complete.Modules,
		Module{Path: "tools", Stats: Stats{Covered: 1, Statements: 1, Percent: 100}})
	if err := verifyProductionInventory(complete); err != nil {
		t.Fatalf("a complete inventory was refused: %v", err)
	}

	dropped := Coverage{Modules: slices.Clone(complete.Modules[1:])}
	err := verifyProductionInventory(dropped)
	if err == nil || !strings.Contains(err.Error(), productionModules[0]) ||
		!strings.Contains(err.Error(), "the denominator shrank") {
		t.Fatalf("a dropped module = %v", err)
	}

	emptied := Coverage{Modules: slices.Clone(complete.Modules)}
	emptied.Modules[2].Stats = Stats{}
	if err := verifyProductionInventory(emptied); err == nil ||
		!strings.Contains(err.Error(), productionModules[2]) {
		t.Fatalf("an emptied module = %v", err)
	}

	if err := verifyProductionInventory(Coverage{}); err == nil {
		t.Fatal("an empty report passed the inventory check")
	} else if !strings.Contains(err.Error(), productionModules[len(productionModules)-1]) {
		t.Errorf("an empty report = %v, want every module named", err)
	}
}

// TestProductionInventoryHoldsEveryReleasedModule keeps the frozen list
// agreeing with the workspace: a module added to go.work and not to the
// inventory would be released without ever entering a coverage denominator,
// and one removed from the inventory would leave the gate silently.
func TestProductionInventoryHoldsEveryReleasedModule(t *testing.T) {
	// The two modules of the workspace that ship nothing: the test suite that
	// measures the others, and the tooling that reports on them.
	notReleased := map[string]bool{"tests/integration": true, "tools": true}
	declared := workspaceUse()
	if len(declared) == 0 {
		t.Skip("no go.work above this test to compare the inventory with")
	}
	var want []string
	for _, module := range declared {
		if !notReleased[module] {
			want = append(want, module)
		}
	}
	slices.Sort(want)
	got := slices.Clone(productionModules)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("productionModules = %v, want the workspace's released modules %v", got, want)
	}
	for _, module := range want {
		if !IsProductionModule(module) {
			t.Errorf("IsProductionModule(%q) = false", module)
		}
	}
	for module := range notReleased {
		if IsProductionModule(module) {
			t.Errorf("IsProductionModule(%q) = true, want the inventory to name released modules only", module)
		}
	}
	if IsProductionModule("") || IsProductionModule("pkg") || IsProductionModule("pkg/parser/v2") {
		t.Error("IsProductionModule matched something that is not a module of the inventory")
	}
}
