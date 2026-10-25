package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// BenchmarkChangelogStepSkip measures the changelog step on a package whose
// 2 MB CHANGELOG.md already carries the entry, which every re-run of the
// step meets: the work is deciding the skip.
func BenchmarkChangelogStepSkip(b *testing.B) {
	dir := b.TempDir()
	rel := &plan.Release{
		Pkg: &model.Package{Name: "core", Dir: dir, Space: &model.Space{Name: "libs"},
			Changelog: model.ChangelogSpec{Enabled: true}},
		Next: ccme.Version{Major: 2}, Channel: ccme.ChannelStable, Bump: ccme.BumpMajor, NewWork: true,
	}
	var history strings.Builder
	history.WriteString("## " + rel.TagName() + " (2026-10-25)\n\n- the entry\n\n")
	for i := 0; history.Len() < 2<<20; i++ {
		fmt.Fprintf(&history, "## core@1.0.%d (2026-01-01)\n\n### Fixes\n\n- fix number %d with a body line\n\n", i, i)
	}
	if err := os.WriteFile(filepath.Join(dir, "CHANGELOG.md"), []byte(history.String()), 0o644); err != nil {
		b.Fatal(err)
	}
	w := &changelogWork{app: New(dir, &config.File{}, zerolog.Nop())}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		run, err := w.resolve(ctx, rel)
		if err != nil || run == nil {
			b.Fatalf("the step has a task: %v", err)
		}
		if err := run(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
