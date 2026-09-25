package app

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/services/dispat/internal/config"
	"github.com/yohimik/dispat/services/dispat/internal/gitx"
	"github.com/yohimik/dispat/services/dispat/internal/plan"
)

// E201 asks Git two questions: before publication, whether the head a
// provider would be tagged at is behind a consumer's baseline, and after it,
// where the provider's tag landed. Both answer with ancestry, and a question
// Git could not answer is an error rather than a quiet "no".

// TestResolveHeadReachAnswersAncestry: a head equal to the boundary or behind
// it is reached, a head past it is not, and the head is read once.
func TestResolveHeadReachAnswersAncestry(t *testing.T) {
	ctx := context.Background()
	root, a := guardRepo(t, &config.File{Run: &config.RunConfig{}})
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	first := git("rev-parse", "HEAD")
	git("commit", "-q", "--allow-empty", "-m", "chore: later")
	second := git("rev-parse", "HEAD")

	isHeadReached, readErr := a.resolveHeadReach(ctx, &plan.Plan{})
	assert.True(t, isHeadReached("", second), "a tag at the head sits on the boundary")
	assert.False(t, isHeadReached("", first), "a tag at the head is past an older boundary")
	require.NoError(t, readErr())

	git("checkout", "-q", first)
	isHeadReached, readErr = a.resolveHeadReach(ctx, &plan.Plan{})
	assert.True(t, isHeadReached("", second), "a head behind the boundary is reached by it")
	require.NoError(t, readErr())
}

// TestResolveHeadReachReportsAnUnreadableHead: a head that cannot be read is
// an error, never a head that reaches nothing.
func TestResolveHeadReachReportsAnUnreadableHead(t *testing.T) {
	_, a := guardRepo(t, &config.File{Run: &config.RunConfig{}})
	a.git = &gitx.LocalGitx{Dir: t.TempDir(), Log: a.log}
	isHeadReached, readErr := a.resolveHeadReach(context.Background(), &plan.Plan{})
	isHeadReached("", "0123456789abcdef0123456789abcdef01234567")
	assert.Error(t, readErr())
}

// ancestryAnswers is a scripted releaseAncestry.
type ancestryAnswers struct {
	released  string
	resolve   error
	isWritten bool
	exists    error
	isBehind  bool
	ancestry  error
}

func (f ancestryAnswers) ResolveCommit(context.Context, string) (string, error) {
	return f.released, f.resolve
}

func (f ancestryAnswers) TagExists(context.Context, string) (bool, error) {
	return f.isWritten, f.exists
}

func (f ancestryAnswers) IsAncestor(context.Context, string, string) (bool, error) {
	return f.isBehind, f.ancestry
}

// TestLocateProviderReleaseSkipsOnlyATagNeverWritten: a tag known to be absent
// is no release, and every other failure keeps the question open.
func TestLocateProviderReleaseSkipsOnlyATagNeverWritten(t *testing.T) {
	failed := errors.New("git failed")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for name, tc := range map[string]struct {
		ctx      context.Context
		git      ancestryAnswers
		isBehind bool
		isError  bool
	}{
		"a tag never written":            {git: ancestryAnswers{resolve: failed}},
		"a tag at the boundary":          {git: ancestryAnswers{released: "c2", isBehind: true}, isBehind: true},
		"a tag past the boundary":        {git: ancestryAnswers{released: "c3"}},
		"a tag Git cannot resolve":       {git: ancestryAnswers{resolve: failed, isWritten: true}, isError: true},
		"a tag whose presence is unread": {git: ancestryAnswers{resolve: failed, exists: failed}, isError: true},
		"a cancelled read":               {ctx: cancelled, git: ancestryAnswers{resolve: failed}, isError: true},
		"an ancestry Git cannot answer":  {git: ancestryAnswers{released: "c2", ancestry: failed}, isError: true},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := tc.ctx
			if ctx == nil {
				ctx = context.Background()
			}
			_, isBehind, err := locateProviderRelease(ctx, tc.git, "core@1.1.0", "c2")
			assert.Equal(t, tc.isError, err != nil, "error: %v", err)
			assert.Equal(t, tc.isBehind, isBehind)
		})
	}
}
