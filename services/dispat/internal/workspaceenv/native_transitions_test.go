package workspaceenv

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func nativeTransitionFixture(t *testing.T) (NativeTransitionScope, *LivePinStore) {
	t.Helper()
	root, config, owners, env := livePinFixture(t)
	store, err := NewLivePins(root, config, owners, []string{"source-a", "source-b"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return NativeTransitionScope{Root: root, ConfigPath: config,
		Environment: append(env, store.Environment())}, store
}

func nativeTransitionIntent(prior string) NativeTransition {
	return NativeTransition{Kind: NativeTransitionSourceRecord, Owner: "source-a", Package: "lib", Version: "1.2.3",
		PriorHead: prior, TagRequested: true, PushRequired: true}
}

func completeNativeTransition(intent NativeTransition, next string) NativeTransition {
	intent.NewHead = next
	intent.PushComplete = true
	intent.Tags = []NativeTagRef{{Name: "lib@1.2.3", Object: strings.Repeat("c", 40), Commit: next}}
	return intent
}

func TestNativeTransitionsExposePendingThenExactCompletion(t *testing.T) {
	scope, store := nativeTransitionFixture(t)
	prior, next := strings.Repeat("a", 40), strings.Repeat("b", 40)
	intent := nativeTransitionIntent(prior)
	before, err := store.NativeJournalRevision("source-a")
	require.NoError(t, err)
	assert.False(t, before.Exists)
	token, err := BeginNativeTransition(scope, intent)
	require.NoError(t, err)
	require.Len(t, token, 64)
	pendingRevision, err := store.NativeJournalRevision("source-a")
	require.NoError(t, err)
	assert.True(t, pendingRevision.Exists)
	assert.EqualValues(t, 32, pendingRevision.Size)
	assert.NotEqual(t, before, pendingRevision, "Begin announces pending before Git mutation")
	_, found, err := store.NativeTransition("source-a", strings.Repeat("0", 64))
	require.NoError(t, err)
	assert.False(t, found, "missing token is distinct from malformed evidence")

	records, err := store.NativeTransitions("source-a")
	require.NoError(t, err)
	require.Len(t, records, 1, "pending is visible before a native command can mutate Git")
	assert.Equal(t, NativeTransitionPending, records[0].State)
	assert.Equal(t, prior, records[0].PriorHead)
	assert.Empty(t, records[0].NewHead)
	exact, found, err := store.NativeTransition("source-a", token)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, NativeTransitionPending, exact.State)

	completed := completeNativeTransition(intent, next)
	require.NoError(t, CompleteNativeTransition(scope, token, completed))
	completedRevision, err := store.NativeJournalRevision("source-a")
	require.NoError(t, err)
	assert.EqualValues(t, 64, completedRevision.Size)
	assert.NotEqual(t, pendingRevision, completedRevision)
	reader, err := OpenLivePins(scope.Root, scope.ConfigPath, scope.Environment)
	require.NoError(t, err)
	records, err = reader.NativeTransitions("source-a")
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, NativeTransitionCompleted, records[0].State)
	assert.Equal(t, token, records[0].Token)
	assert.Equal(t, completed, records[0].NativeTransition)
	exact, found, err = store.NativeTransition("source-a", token)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, NativeTransitionCompleted, exact.State)
	assert.ErrorContains(t, CompleteNativeTransition(scope, token, completed), "no longer pending")
}

func TestNativeTransitionsRetainEveryCompletedStepAndFailedAttempt(t *testing.T) {
	scope, store := nativeTransitionFixture(t)
	first, second, third := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("d", 40)
	firstIntent := nativeTransitionIntent(first)
	firstToken, err := BeginNativeTransition(scope, firstIntent)
	require.NoError(t, err)
	require.NoError(t, CompleteNativeTransition(scope, firstToken, completeNativeTransition(firstIntent, second)))

	secondIntent := NativeTransition{Kind: NativeTransitionSourceRecord, Owner: "source-a", Package: "tool", Version: "2.0.0",
		PriorHead: second, TagRequested: false, PushRequired: false}
	secondToken, err := BeginNativeTransition(scope, secondIntent)
	require.NoError(t, err)
	secondComplete := secondIntent
	secondComplete.NewHead = third
	require.NoError(t, CompleteNativeTransition(scope, secondToken, secondComplete))

	failedToken, err := BeginNativeTransition(scope, nativeTransitionIntent(third))
	require.NoError(t, err)
	require.NoError(t, FailNativeTransition(scope, failedToken, "source-a"))
	records, err := store.NativeTransitions("source-a")
	require.NoError(t, err)
	require.Len(t, records, 3, "an owner may have a chain and a later failed attempt")
	byToken := make(map[string]NativeTransitionRecord, len(records))
	for _, record := range records {
		byToken[record.Token] = record
	}
	assert.Equal(t, NativeTransitionCompleted, byToken[firstToken].State)
	assert.Equal(t, NativeTransitionCompleted, byToken[secondToken].State)
	assert.Equal(t, second, byToken[secondToken].PriorHead)
	assert.Equal(t, third, byToken[secondToken].NewHead)
	assert.Empty(t, byToken[secondToken].Tags, "tagless native settlement remains explicit")
	assert.Equal(t, NativeTransitionFailed, byToken[failedToken].State)
	assert.ErrorContains(t, CompleteNativeTransition(scope, failedToken,
		completeNativeTransition(nativeTransitionIntent(third), strings.Repeat("e", 40))), "no longer pending")
}

func TestNativeTransitionsRejectIncompleteOrForeignClaims(t *testing.T) {
	scope, store := nativeTransitionFixture(t)
	prior, next := strings.Repeat("a", 40), strings.Repeat("b", 40)
	intent := nativeTransitionIntent(prior)
	token, err := BeginNativeTransition(scope, intent)
	require.NoError(t, err)
	complete := completeNativeTransition(intent, next)
	complete.PushComplete = false
	assert.ErrorContains(t, CompleteNativeTransition(scope, token, complete), "requested Git effects")
	complete.PushComplete = true
	complete.Tags[0].Commit = prior
	assert.ErrorContains(t, CompleteNativeTransition(scope, token, complete), "invalid exact tag")
	complete.Tags[0].Commit = next
	complete.Package = "foreign"
	assert.ErrorContains(t, CompleteNativeTransition(scope, token, complete), "does not match its pending intent")
	complete.Package = intent.Package
	require.NoError(t, CompleteNativeTransition(scope, token, complete))

	foreign := nativeTransitionIntent(prior)
	foreign.Owner = "unlisted"
	_, err = BeginNativeTransition(scope, foreign)
	assert.ErrorContains(t, err, "not a participating repository")
	_, err = store.NativeTransitions("unlisted")
	assert.ErrorContains(t, err, "not a participating repository")
	assert.ErrorContains(t, FailNativeTransition(scope, token, "source-b"), "reading native transition")
}

func TestNativeTransitionsIncludeOnlyDeclaredControlOwner(t *testing.T) {
	root, config, owners, env := livePinFixture(t)
	store, err := NewLivePins(root, config, owners, []string{"control", "source-a", "source-b"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	// The same metadata is inherited, while ordinary source pin exports stay
	// source-only even in a workspace with a control repository.
	env[4] = Repositories + `=["control","source-a","source-b"]`
	scope := NativeTransitionScope{Root: root, ConfigPath: config,
		Environment: append(env, store.Environment())}
	control := NativeTransition{Kind: NativeTransitionLinkSettlement, Owner: "control", Package: "lib", Version: "1.2.3",
		PriorHead: strings.Repeat("a", 40)}
	token, err := BeginNativeTransition(scope, control)
	require.NoError(t, err)
	control.NewHead = strings.Repeat("b", 40)
	require.NoError(t, CompleteNativeTransition(scope, token, control))
	records, err := store.NativeTransitions("control")
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, NativeTransitionCompleted, records[0].State)
	assert.Error(t, store.Remember("control", control.NewHead))
	reader, err := OpenLivePins(root, config, scope.Environment)
	require.NoError(t, err)
	_, err = reader.Pins("control")
	assert.Error(t, err, "control is never an ordinary source pin")

	withoutControl, _ := nativeTransitionFixture(t)
	_, err = BeginNativeTransition(withoutControl, control)
	assert.ErrorContains(t, err, "not a participating repository")
}

func TestNativeTransitionsRejectStaleContextAndMalformedEntry(t *testing.T) {
	scope, store := nativeTransitionFixture(t)
	intent := nativeTransitionIntent(strings.Repeat("a", 40))
	wrong := scope
	wrong.Root = t.TempDir()
	_, err := BeginNativeTransition(wrong, intent)
	assert.ErrorContains(t, err, "does not match this workspace")

	token, err := BeginNativeTransition(scope, intent)
	require.NoError(t, err)
	dir := strings.TrimPrefix(store.Environment(), LivePins+"=")
	path := filepath.Join(dir, nativeRecordName(intent.Owner, token))
	require.NoError(t, os.WriteFile(path, []byte(`{"token":"broken"}`), 0o600))
	_, err = store.NativeTransitions(intent.Owner)
	assert.Error(t, err, "malformed evidence cannot be silently skipped")
	_, found, err := store.NativeTransition(intent.Owner, token)
	assert.False(t, found)
	assert.Error(t, err, "exact polling cannot accept malformed evidence either")
	assert.Error(t, CompleteNativeTransition(scope, token, completeNativeTransition(intent, strings.Repeat("b", 40))))

	if runtime.GOOS == "windows" {
		return
	}
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Symlink(filepath.Join(dir, liveMetadataFile), path))
	_, err = store.NativeTransitions(intent.Owner)
	assert.ErrorContains(t, err, "not a regular file")
	_, found, err = store.NativeTransition(intent.Owner, token)
	assert.False(t, found)
	assert.ErrorContains(t, err, "not a regular file")
}

func TestNativeTransitionRevisionRejectsMalformedOrSubstitutedFile(t *testing.T) {
	scope, store := nativeTransitionFixture(t)
	intent := nativeTransitionIntent(strings.Repeat("a", 40))
	token, err := BeginNativeTransition(scope, intent)
	require.NoError(t, err)
	dir := strings.TrimPrefix(store.Environment(), LivePins+"=")
	path := filepath.Join(dir, nativeRevisionName("source-a"))
	require.NoError(t, os.WriteFile(path, []byte("bad"), 0o600))
	_, err = store.NativeJournalRevision("source-a")
	assert.ErrorContains(t, err, "malformed")
	assert.ErrorContains(t, CompleteNativeTransition(scope, token,
		completeNativeTransition(intent, strings.Repeat("b", 40))), "malformed")
	record, found, err := store.NativeTransition("source-a", token)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, NativeTransitionPending, record.State,
		"an unannounced completion must not authorize a failed native command")
	assert.ErrorContains(t, FailNativeTransition(scope, token, "source-a"), "malformed")
	record, found, err = store.NativeTransition("source-a", token)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, NativeTransitionPending, record.State)
	if runtime.GOOS == "windows" {
		return
	}
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Symlink(filepath.Join(dir, liveMetadataFile), path))
	_, err = store.NativeJournalRevision("source-a")
	assert.ErrorContains(t, err, "malformed")
}

func TestNativeTransitionStandaloneAndLostContextAreDistinct(t *testing.T) {
	scope, _ := nativeTransitionFixture(t)
	standalone := scope
	standalone.Environment = nil
	intent := nativeTransitionIntent(strings.Repeat("a", 40))
	token, err := BeginNativeTransition(standalone, intent)
	require.NoError(t, err)
	assert.Empty(t, token)
	assert.NoError(t, CompleteNativeTransition(standalone, "", completeNativeTransition(intent, strings.Repeat("b", 40))))
	assert.NoError(t, FailNativeTransition(standalone, "", intent.Owner))

	token, err = BeginNativeTransition(scope, intent)
	require.NoError(t, err)
	assert.ErrorContains(t, CompleteNativeTransition(standalone, token,
		completeNativeTransition(intent, strings.Repeat("b", 40))), "context disappeared")
	assert.ErrorContains(t, FailNativeTransition(standalone, token, intent.Owner), "context disappeared")
}

func TestNativeTransitionRevisionCountsConcurrentProcesses(t *testing.T) {
	if packageName := os.Getenv("DISPAT_NATIVE_TRANSITION_TEST_CHILD"); packageName != "" {
		scope := NativeTransitionScope{Root: os.Getenv("DISPAT_NATIVE_TRANSITION_TEST_ROOT"),
			ConfigPath: os.Getenv("DISPAT_NATIVE_TRANSITION_TEST_CONFIG"), Environment: os.Environ()}
		_, err := BeginNativeTransition(scope, NativeTransition{
			Kind: NativeTransitionSourceRecord, Owner: "source-a", Package: packageName,
			Version: "1.2.3", PriorHead: strings.Repeat("a", 40),
		})
		require.NoError(t, err)
		return
	}
	scope, store := nativeTransitionFixture(t)
	const writers = 6
	var wait sync.WaitGroup
	errors := make(chan error, writers)
	for index := range writers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			command := exec.Command(os.Args[0], "-test.run=^TestNativeTransitionRevisionCountsConcurrentProcesses$")
			command.Env = append(append(os.Environ(), scope.Environment...),
				"DISPAT_NATIVE_TRANSITION_TEST_CHILD="+fmt.Sprintf("pkg-%d", index),
				"DISPAT_NATIVE_TRANSITION_TEST_ROOT="+scope.Root,
				"DISPAT_NATIVE_TRANSITION_TEST_CONFIG="+scope.ConfigPath)
			if output, err := command.CombinedOutput(); err != nil {
				errors <- fmt.Errorf("native child %d: %w: %s", index, err, output)
			}
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	revision, err := store.NativeJournalRevision("source-a")
	require.NoError(t, err)
	assert.EqualValues(t, writers*32, revision.Size,
		"each independent process appends one event without losing another's revision")
	records, err := store.NativeTransitions("source-a")
	require.NoError(t, err)
	assert.Len(t, records, writers)
}
