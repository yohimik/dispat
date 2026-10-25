package app

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/ccme"
)

// TestSincePackagesReadTheInputsThePlanUsed: --since asks the planner which
// packages rev..HEAD addresses with the inputs the plan was computed from,
// rather than assembling them again. The initials entry naming no package is
// the witness: assembling the inputs is what warns about it.
func TestSincePackagesReadTheInputsThePlanUsed(t *testing.T) {
	f := newTagFixture(t, "", map[string][]string{"core": {"core@1.0.0"}}, []string{"core", "web"})
	var logs bytes.Buffer
	f.app.log = zerolog.New(&logs)
	f.app.cfg.InitialVersions = map[string]ccme.Version{"ghost": {Major: 1}}

	pl, err := f.app.plan(t.Context())
	require.NoError(t, err)
	selected, err := f.app.sincePackages(t.Context(), pl, "HEAD~1")
	require.NoError(t, err)

	assert.Empty(t, selected, "the newest commit is empty and addresses nothing")
	assert.Equal(t, 1, strings.Count(logs.String(), "initials entry matches no discovered package"))
}

// BenchmarkSincePackages measures resolving --since HEAD~1 against the plan
// of a 64-package repository, which a step command does after planning.
func BenchmarkSincePackages(b *testing.B) {
	const packages = 64
	tags := make(map[string][]string, packages)
	order := make([]string, packages)
	for i := range packages {
		name := fmt.Sprintf("pkg%02d", i)
		order[i] = name
		tags[name] = []string{name + "@1.0.0"}
	}
	f := newTagFixture(b, "", tags, order)
	ctx := context.Background()
	pl, err := f.app.plan(ctx)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := f.app.sincePackages(ctx, pl, "HEAD~1"); err != nil {
			b.Fatal(err)
		}
	}
}
