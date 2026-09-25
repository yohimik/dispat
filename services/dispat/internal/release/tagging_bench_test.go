// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (c) 2026 yohimik

package release

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/yohimik/dispat/pkg/ccme"

	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/model"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// BenchmarkCreateReleaseTags measures what writing a run's release tags costs
// against a real repository whose packages have released many times before:
// the git processes per tag written (gitcalls/tag), and the time per tag. A
// release run writes one tag per releasing package, so the per-tag figure is
// what a wide release pays for tagging.
//
// The repository is written by `git fast-import` into the benchmark's own
// temporary folder: a history of 2,000 commits and 64 packages with 30
// releases each, so that every package's tag inventory is one a long-lived
// workspace really carries.
func BenchmarkCreateReleaseTags(b *testing.B) {
	const packages, releases, commits = 64, 30, 2000
	dir := b.TempDir()
	var stream bytes.Buffer
	for i := 1; i <= commits; i++ {
		fmt.Fprintf(&stream, "commit refs/heads/main\nmark :%d\n", i)
		fmt.Fprintf(&stream, "committer dev <dev@example.com> %d +0000\n", 1_600_000_000+60*i)
		message := fmt.Sprintf("fix: change %d", i)
		fmt.Fprintf(&stream, "data %d\n%s\n", len(message), message)
		if i > 1 {
			fmt.Fprintf(&stream, "from :%d\n", i-1)
		}
		content := fmt.Sprintf("change %d\n", i)
		fmt.Fprintf(&stream, "M 100644 inline pkgs/pkg-%02d/f.txt\ndata %d\n%s\n", i%packages, len(content), content)
	}
	for p := range packages {
		for r := range releases {
			fmt.Fprintf(&stream, "reset refs/tags/pkg-%02d@1.%d.0\nfrom :%d\n\n",
				p, r, 1+(r*commits/releases+p)%commits)
		}
	}
	stream.WriteString("done\n")
	run := func(stdin []byte, args ...string) {
		b.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if stdin != nil {
			cmd.Stdin = bytes.NewReader(stdin)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run(nil, "init", "-q", "-b", "main")
	run(stream.Bytes(), "fast-import", "--quiet", "--done")

	git := &gitx.LocalGitx{Dir: dir, Name: "dev", Email: "dev@example.com"}
	space := &model.Space{Name: "libs"}
	rels := make([]*plan.Release, packages)
	for p := range rels {
		rels[p] = &plan.Release{Pkg: &model.Package{Name: fmt.Sprintf("pkg-%02d", p), Dir: dir, Space: space},
			Bump: ccme.BumpMinor, NewWork: true}
	}
	ctx := context.Background()
	written := 0
	before := gitx.GitInvocations()
	b.ReportAllocs()
	for b.Loop() {
		// A version no earlier iteration wrote, so every tag is a new one.
		for _, rel := range rels {
			rel.Next = ccme.Version{Major: 2, Minor: uint64(written / packages)}
			if err := CreateReleaseTag(ctx, git, rel, false, zerolog.Nop()); err != nil {
				b.Fatal(err)
			}
			written++
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(gitx.GitInvocations()-before)/float64(written), "gitcalls/tag")
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(written), "ns/tag")
}
