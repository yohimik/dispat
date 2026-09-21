package gitx

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mergeHistory builds a history with two side branches merged back, under the
// commit dates dateOf assigns, and returns the commit of every label.
//
//	r - a - b - c ------- m1 - f - m2 - h   (HEAD)
//	     \       \       /        /
//	      \       d --- e        /
//	       g ----------------- i
func mergeHistory(t *testing.T, dateOf func(label string) string) (*LocalGitx, map[string]string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	ids := make(map[string]string)
	git := func(label string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = os.Environ()
		if label != "" {
			date := dateOf(label)
			cmd.Env = append(cmd.Env, "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
		}
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		if label != "" {
			ids[label] = strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
		}
	}
	commit := func(label string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(root, label+".txt"), []byte(label), 0o644))
		git("", "add", ".")
		git(label, "commit", "-qm", "fix(core): "+label)
	}
	git("", "init", "-q", "-b", "main")
	git("", "config", "user.email", "test@example.com")
	git("", "config", "user.name", "Test")
	commit("r")
	commit("a")
	git("", "branch", "far")
	commit("b")
	commit("c")
	git("", "branch", "near")
	git("", "checkout", "-q", "near")
	commit("d")
	commit("e")
	git("", "checkout", "-q", "far")
	commit("g")
	commit("i")
	git("", "checkout", "-q", "main")
	git("m1", "merge", "-q", "--no-ff", "-m", "chore: merge near", "near")
	commit("f")
	git("m2", "merge", "-q", "--no-ff", "-m", "chore: merge far", "far")
	commit("h")
	return &LocalGitx{Dir: root}, ids
}

// TestCommitsSinceAnyIsTheUnionOfTheWindows pins the three promises of
// UnionHistoryx against git itself: the set, the order within every window,
// and the parent lists. Equal dates exercise git's tie-break and a skewed
// clock its date order, because the order promise rests on both.
func TestCommitsSinceAnyIsTheUnionOfTheWindows(t *testing.T) {
	clocks := map[string]func(string) string{
		"every commit in one second": func(string) string { return "2026-01-01T00:00:00Z" },
		"a clock running backwards": func(label string) string {
			return fmt.Sprintf("2026-01-%02dT00:00:00Z", 28-strings.Index("rabcdegim1fm2h", label[:1]))
		},
	}
	for name, dateOf := range clocks {
		t.Run(name, func(t *testing.T) {
			cli, id := mergeHistory(t, dateOf)
			ctx := context.Background()
			for _, labels := range [][]string{
				{"c"}, {"a", "c"}, {"c", "e"}, {"e", "i"}, {"c", "e", "i"}, {"b", "d", "g"},
				{"r", "h"}, {"h"}, {"m1", "i"}, {"c", "c", "e"},
			} {
				boundaries := make([]string, len(labels))
				for i, l := range labels {
					boundaries[i] = id[l]
				}
				union, err := cli.CommitsSinceAny(ctx, boundaries)
				require.NoError(t, err)
				at := make(map[string]int, len(union))
				for i, c := range union {
					at[c.SHA] = i
				}
				require.Len(t, at, len(union), "%v: a commit listed twice", labels)

				inSomeWindow := make(map[string]bool)
				for _, b := range boundaries {
					window, err := cli.Commits(ctx, b)
					require.NoError(t, err)
					last := -1
					for _, c := range window {
						pos, ok := at[c.SHA]
						require.Truef(t, ok, "%v: the union misses a commit of the window after %s", labels, b)
						require.Greaterf(t, pos, last, "%v: the window after %s is not a subsequence", labels, b)
						last = pos
						inSomeWindow[c.SHA] = true
						assert.Equal(t, c, union[pos], "the same record either way")
					}
				}
				assert.Len(t, inSomeWindow, len(union), "%v: the union holds a commit of no window", labels)
			}

			whole, err := cli.CommitsSinceAny(ctx, []string{id["c"], ""})
			require.NoError(t, err)
			everything, err := cli.Commits(ctx, "")
			require.NoError(t, err)
			assert.Equal(t, everything, whole, "a package with no baseline makes the union the whole history")

			_, err = cli.CommitsSinceAny(ctx, []string{"core@1.0.0"})
			assert.Error(t, err, "a boundary is a commit id, never a name")

			// A line HEAD never merged: its tip excludes commits of the union
			// (everything back to the fork) without being in it.
			runGit(t, cli.Dir, "checkout", "-q", "-b", "unmerged", id["f"])
			require.NoError(t, os.WriteFile(filepath.Join(cli.Dir, "stray.txt"), []byte("x"), 0o644))
			runGit(t, cli.Dir, "add", ".")
			runGit(t, cli.Dir, "commit", "-qm", "fix(core): never merged")
			stray := strings.TrimSpace(runGit(t, cli.Dir, "rev-parse", "HEAD"))
			runGit(t, cli.Dir, "checkout", "-q", "main")
			_, err = cli.CommitsSinceAny(ctx, []string{id["c"], stray})
			assert.ErrorIs(t, err, ErrBoundaryNotBehindHead)
		})
	}
}

// TestCommitsSinceAnyFoldsManyBoundaries drives the chunked merge-base fold
// with a chunk small enough for a fixed history to need several, over
// boundaries whose intermediate bases are more than one commit.
func TestCommitsSinceAnyFoldsManyBoundaries(t *testing.T) {
	cli, id := mergeHistory(t, func(string) string { return "2026-01-01T00:00:00Z" })
	ctx := context.Background()
	for _, labels := range [][]string{
		{"e", "i", "c"}, {"h", "m2", "f", "m1", "e", "i"}, {"d", "g", "b", "e", "i", "c", "f"},
	} {
		boundaries := make([]string, len(labels))
		for i, l := range labels {
			boundaries[i] = id[l]
		}
		want, err := cli.CommitsSinceAny(ctx, boundaries)
		require.NoError(t, err)
		for _, chunk := range []int{1, 2} {
			unionBoundaryChunk = chunk
			got, err := cli.CommitsSinceAny(ctx, boundaries)
			unionBoundaryChunk = 256
			require.NoError(t, err)
			assert.Equalf(t, want, got, "%v folded %d at a time", labels, chunk)
		}
	}
}
