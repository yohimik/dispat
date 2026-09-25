package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// livePinFixture is a control repository pinning one source, with the source
// checkout advanced one commit past the pin: exactly what a nested command
// sees after the release around it committed into the source.
type livePinFixture struct {
	root, path string
	check      livePinCheck
	pinned     string
	advanced   string
}

func newLivePinFixture(t *testing.T) livePinFixture {
	t.Helper()
	sdk := workspaceRepo(t, "sdk", nil)
	root, path := workspaceControl(t, map[string]string{"sdk": sdk},
		File{Polyrepo: true, Packages: map[string]PackageConfig{"app": {Path: "sources/sdk/pkgs/sdk"}}})
	checkout := filepath.Join(root, "sources", "sdk")
	pinned := strings.TrimSpace(workspaceGit(t, checkout, "rev-parse", "HEAD"))
	workspaceGit(t, checkout, "config", "user.name", "Test")
	workspaceGit(t, checkout, "config", "user.email", "test@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "later.txt"), []byte("later"), 0o644))
	workspaceGit(t, checkout, "add", ".")
	workspaceGit(t, checkout, "commit", "-m", "feat: advance source")
	advanced := strings.TrimSpace(workspaceGit(t, checkout, "rev-parse", "HEAD"))
	controlHead := strings.TrimSpace(workspaceGit(t, root, "rev-parse", "HEAD"))
	resolvedCheckout, err := filepath.EvalSymlinks(checkout)
	require.NoError(t, err)
	return livePinFixture{
		root: root, path: path, pinned: pinned, advanced: advanced,
		check: livePinCheck{
			controlRoot: root, controlRevision: controlHead,
			module: submodule{Name: "sdk", Path: "sources/sdk", Root: resolvedCheckout},
		},
	}
}

// scriptedPins answers one scripted reply per read, repeating the last one,
// and counts the reads.
type scriptedPins struct {
	mu      sync.Mutex
	replies [][]string
	reads   int
}

func (s *scriptedPins) resolve(string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reply := s.replies[min(s.reads, len(s.replies)-1)]
	s.reads++
	return reply, nil
}

func (s *scriptedPins) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

// fastLivePinReads keeps the retry tests quick; the policy itself is what they
// pass in, never a changed default.
var fastLivePinReads = livePinPolicy{attempts: 3, interval: time.Millisecond}

// TestComposeWorkspaceWaitsForTheLivePinOfARecordInProgress: the release
// around a nested command has committed into the source and not yet published
// the revision. Composition reads again rather than refusing, accepts the
// revision once the pin names it, and keeps that revision as the source's
// composition head.
func TestComposeWorkspaceWaitsForTheLivePinOfARecordInProgress(t *testing.T) {
	fixture := newLivePinFixture(t)
	loaded, err := Load(fixture.path, nil)
	require.NoError(t, err)
	pins := &scriptedPins{replies: [][]string{nil, nil, {fixture.advanced}}}

	started := time.Now()
	workspace, err := ComposeWorkspaceWithPinResolver(t.Context(), loaded, fixture.path, fixture.root, nil, nil, pins.resolve)

	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(started), livePinReads.interval, "the second read waited one interval")
	assert.Equal(t, 4, pins.count(), "two reads for the unadmitted head, two for the admitted one")
	var head string
	for _, repository := range workspace.Repositories {
		if repository.Name == "sdk" {
			head = repository.CompositionHead
		}
	}
	assert.Equal(t, fixture.advanced, head, "the accepted head is the one the stable pin admitted")
}

// TestStableLivePinNeedsTwoReadsThatAgree: a pin read before HEAD that admits
// it proves nothing when the pin read after it disagrees, because HEAD may
// belong to either side of the release's write. A pin that keeps moving is
// refused once the attempts run out, as a pin rather than as a checkout.
func TestStableLivePinNeedsTwoReadsThatAgree(t *testing.T) {
	fixture := newLivePinFixture(t)
	other := strings.Repeat("e", 40)

	t.Run("a pair that moved is read again", func(t *testing.T) {
		pins := &scriptedPins{replies: [][]string{{other}, {fixture.advanced}, {fixture.advanced}}}
		check := fixture.check
		check.resolve = pins.resolve

		head, err := requireStableLivePin(t.Context(), check, fastLivePinReads)

		require.NoError(t, err)
		assert.Equal(t, fixture.advanced, head)
		assert.Equal(t, 4, pins.count())
	})

	t.Run("a pin that never settles is refused", func(t *testing.T) {
		var mu sync.Mutex
		reads := 0
		check := fixture.check
		check.resolve = func(string) ([]string, error) {
			mu.Lock()
			defer mu.Unlock()
			reads++
			if reads%2 == 1 {
				return []string{fixture.advanced}, nil
			}
			return []string{other}, nil
		}

		_, err := requireStableLivePin(t.Context(), check, fastLivePinReads)

		require.ErrorContains(t, err, "live run pin kept moving")
		requireWorkspaceDiagnostic(t, err, DiagnosticRepositoryInvalid)
		assert.Equal(t, 2*fastLivePinReads.attempts, reads)
	})
}

// TestStableLivePinAcceptsTheControlPinWhileTheLivePinMoves: a checkout at
// the revision control pins is admitted whatever the run pins, so a live pin
// that keeps moving around it is no reason to read again or to refuse it.
func TestStableLivePinAcceptsTheControlPinWhileTheLivePinMoves(t *testing.T) {
	fixture := newLivePinFixture(t)
	workspaceGit(t, fixture.check.module.Root, "checkout", "-q", fixture.pinned)
	reads := 0
	check := fixture.check
	check.resolve = func(string) ([]string, error) {
		reads++
		return []string{strings.Repeat(string(rune('a'+reads%6)), 40)}, nil
	}

	head, err := requireStableLivePin(t.Context(), check, fastLivePinReads)

	require.NoError(t, err)
	assert.Equal(t, fixture.pinned, head)
	assert.Equal(t, 2, reads, "one attempt is enough")
}

// TestStableLivePinRefusesACheckoutNobodyAdmitted: a checkout at a revision
// neither control nor the run admitted is the existing E330 refusal, reported
// after the bounded reads rather than on the first one.
func TestStableLivePinRefusesACheckoutNobodyAdmitted(t *testing.T) {
	fixture := newLivePinFixture(t)
	pins := &scriptedPins{replies: [][]string{nil}}
	check := fixture.check
	check.resolve = pins.resolve

	_, err := requireStableLivePin(t.Context(), check, fastLivePinReads)

	require.ErrorContains(t, err, "is checked out at "+fixture.advanced+" but control HEAD pins "+fixture.pinned)
	requireWorkspaceDiagnostic(t, err, DiagnosticRepositoryInvalid)
	assert.Equal(t, 2*fastLivePinReads.attempts, pins.count())

	t.Run("a static run pin needs no live one", func(t *testing.T) {
		admitted := fixture.check
		admitted.runPins = []string{fixture.advanced}
		admitted.resolve = (&scriptedPins{replies: [][]string{nil}}).resolve

		head, err := requireStableLivePin(t.Context(), admitted, fastLivePinReads)

		require.NoError(t, err)
		assert.Equal(t, fixture.advanced, head)
	})
}

// TestStableLivePinStopsWithItsContext: the wait between two reads belongs to
// the invocation, so an interrupt ends it at once and says which wait it was.
func TestStableLivePinStopsWithItsContext(t *testing.T) {
	fixture := newLivePinFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	check := fixture.check
	check.resolve = func(string) ([]string, error) {
		cancel()
		return nil, nil
	}

	started := time.Now()
	_, err := requireStableLivePin(ctx, check, livePinPolicy{attempts: 10, interval: time.Minute})

	require.ErrorIs(t, err, context.Canceled)
	require.ErrorContains(t, err, "waiting for a stable live run pin")
	requireWorkspaceDiagnostic(t, err, DiagnosticRepositoryInvalid)
	assert.Less(t, time.Since(started), 30*time.Second, "the interrupt ended the wait, not the interval")
}

// TestStableLivePinRefusesAnUnreadablePinAtOnce: a pin that cannot be read is
// not a release in progress, and waiting would only delay its refusal.
func TestStableLivePinRefusesAnUnreadablePinAtOnce(t *testing.T) {
	fixture := newLivePinFixture(t)
	reads := 0
	check := fixture.check
	check.resolve = func(string) ([]string, error) {
		reads++
		if reads == 2 {
			return nil, errors.New("coordinator record is malformed")
		}
		return nil, nil
	}

	_, err := requireStableLivePin(t.Context(), check, fastLivePinReads)

	require.ErrorContains(t, err, `source repository "sdk": reading live run pin`)
	require.ErrorContains(t, err, "coordinator record is malformed")
	assert.Equal(t, 2, reads, "the failed read ends validation")
}
