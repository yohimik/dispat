package main

import (
	"strings"
	"testing"
)

// The failure this fences: the merged profile is a concatenation, so a block
// compiled by two test binaries appears twice. Summing the lines would report
// four statements where the source has two, and `go tool cover` — which the
// badge is read off — would disagree with the docs table on every package the
// integration suite also reaches.
func TestCoverageCountsARepeatedBlockOnce(t *testing.T) {
	const profile = `mode: atomic
github.com/yohimik/dispat/pkg/ccme/parse.go:10.1,12.2 2 1
github.com/yohimik/dispat/pkg/ccme/parse.go:14.1,16.2 2 0
mode: set
github.com/yohimik/dispat/pkg/ccme/parse.go:10.1,12.2 2 3
github.com/yohimik/dispat/pkg/ccme/parse.go:14.1,16.2 2 0
`
	c := newCoverage()
	if err := c.add(strings.NewReader(profile)); err != nil {
		t.Fatal(err)
	}
	got := c.stats()
	if got.Statements != 4 || got.Covered != 2 || got.Percent != 50 {
		t.Fatalf("got %+v, want 4 statements, 2 covered, 50%%", got)
	}
}

// A block only one profile reached is covered: the layers are merged, not
// intersected. This is the other half of the dedup — getting it wrong the
// other way would erase everything only the black-box suite exercises.
func TestCoverageMergesRatherThanIntersects(t *testing.T) {
	c := newCoverage()
	if err := c.add(strings.NewReader("mode: atomic\np/a.go:1.1,2.2 3 0\n")); err != nil {
		t.Fatal(err)
	}
	if err := c.add(strings.NewReader("mode: set\np/a.go:1.1,2.2 3 1\n")); err != nil {
		t.Fatal(err)
	}
	if got := c.stats(); got.Covered != 3 {
		t.Fatalf("got %+v, want the block covered", got)
	}
}

func TestCoverageGroupsIntoModulesAndPackages(t *testing.T) {
	const profile = `mode: atomic
github.com/yohimik/dispat/pkg/ccme/parse.go:1.1,2.2 4 1
github.com/yohimik/dispat/services/dispat/internal/plan/plan.go:1.1,2.2 1 1
github.com/yohimik/dispat/services/dispat/internal/plan/plan.go:4.1,5.2 1 0
github.com/yohimik/dispat/services/dispat/main.go:1.1,2.2 2 1
`
	c := newCoverage()
	if err := c.add(strings.NewReader(profile)); err != nil {
		t.Fatal(err)
	}
	mods := c.modules()
	if len(mods) != 2 || mods[0].Path != "pkg/ccme" || mods[1].Path != "services/dispat" {
		t.Fatalf("got %+v, want pkg/ccme then services/dispat", mods)
	}
	dispat := mods[1]
	if dispat.Statements != 4 || dispat.Covered != 3 || dispat.Percent != 75 {
		t.Fatalf("got module %+v, want 4 statements, 3 covered, 75%%", dispat.Stats)
	}
	if len(dispat.Packages) != 2 ||
		dispat.Packages[0].Path != "services/dispat" ||
		dispat.Packages[1].Path != "services/dispat/internal/plan" {
		t.Fatalf("got packages %+v, want the module root then internal/plan", dispat.Packages)
	}
	if got := dispat.Packages[1]; got.Percent != 50 {
		t.Fatalf("got %+v, want internal/plan at 50%%", got)
	}
}

func TestCoverageRejectsACorruptProfile(t *testing.T) {
	for name, profile := range map[string]string{
		"short line":       "mode: atomic\np/a.go:1.1,2.2 3\n",
		"statement count":  "mode: atomic\np/a.go:1.1,2.2 x 1\n",
		"execution count":  "mode: atomic\np/a.go:1.1,2.2 3 x\n",
		"trailing garbage": "mode: atomic\np/a.go:1.1,2.2 3 1 1\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := newCoverage().add(strings.NewReader(profile)); err == nil {
				t.Fatal("want an error rather than a skewed total")
			}
		})
	}
}

func TestPercentRoundsToATenth(t *testing.T) {
	for _, tc := range []struct {
		covered, statements int
		want                float64
	}{
		{0, 0, 0},
		{1, 3, 33.3},
		{2, 3, 66.7},
		{953, 1000, 95.3},
		{7, 7, 100},
	} {
		if got := percent(tc.covered, tc.statements); got != tc.want {
			t.Errorf("percent(%d, %d) = %v, want %v", tc.covered, tc.statements, got, tc.want)
		}
	}
}

func TestPackageAndModulePaths(t *testing.T) {
	const block = "github.com/yohimik/dispat/services/dispat/internal/plan/plan.go:1.1,2.2"
	if got := packageOf(block); got != "services/dispat/internal/plan" {
		t.Fatalf("packageOf = %q", got)
	}
	if got := moduleOf(packageOf(block)); got != "services/dispat" {
		t.Fatalf("moduleOf = %q", got)
	}
	if got := moduleOf("tools"); got != "tools" {
		t.Fatalf("moduleOf of a one-segment path = %q", got)
	}
}
