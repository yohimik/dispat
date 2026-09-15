package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func polyrepoGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func TestPushReleaseRefusesTagsAfterBranchRejection(t *testing.T) {
	root, g := initRepo(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	polyrepoGit(t, root, "init", "-q", "--bare", remote)
	polyrepoGit(t, root, "remote", "add", "origin", remote)
	polyrepoGit(t, root, "push", "-q", "origin", "HEAD:refs/heads/main")
	require.NoError(t, os.WriteFile(filepath.Join(root, "next"), []byte("change"), 0o644))
	polyrepoGit(t, root, "add", "next")
	polyrepoGit(t, root, "commit", "-qm", "feat: change")
	require.NoError(t, g.CreateTag(context.Background(), "pkg@1.0.0", "release pkg@1.0.0", "HEAD"))
	hook := "#!/bin/sh\ncase \"$1\" in refs/heads/*) exit 1;; esac\n"
	require.NoError(t, os.WriteFile(filepath.Join(remote, "hooks", "update"), []byte(hook), 0o755))
	require.Error(t, g.PushRelease(context.Background(), "origin", "main", []string{"pkg@1.0.0"}, nil))
	assert.Empty(t, polyrepoGit(t, remote, "tag", "--list"), "a rejected branch cannot still publish the immutable tag")
	require.NoError(t, os.Remove(filepath.Join(remote, "hooks", "update")))
	require.NoError(t, g.PushRelease(context.Background(), "origin", "main", []string{"pkg@1.0.0"}, nil))
	sha, err := g.HeadSHA(context.Background())
	require.NoError(t, err)
	require.NoError(t, g.VerifyRemoteRelease(context.Background(), "origin", "pkg@1.0.0", sha))
	require.Error(t, g.VerifyRemoteRelease(context.Background(), "origin", "missing", sha))
	require.Error(t, g.VerifyRemoteRelease(context.Background(), "origin", "pkg@1.0.0", "wrong"))
}

func TestPushReleaseDetachedTagOnlyAndImmutableTags(t *testing.T) {
	root, g := initRepo(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	polyrepoGit(t, root, "init", "-q", "--bare", remote)
	polyrepoGit(t, root, "remote", "add", "origin", remote)
	polyrepoGit(t, root, "checkout", "-q", "--detach")
	require.NoError(t, g.CreateTag(context.Background(), "pkg@1.0.0", "release", "HEAD"))
	require.NoError(t, g.PushRelease(context.Background(), "origin", "", []string{"pkg@1.0.0"}, nil))
	before := polyrepoGit(t, remote, "rev-parse", "refs/tags/pkg@1.0.0")
	require.NoError(t, g.CreateTagForce(context.Background(), "pkg@1.0.0", "changed message", "HEAD"))
	require.Error(t, g.PushRelease(context.Background(), "origin", "", []string{"pkg@1.0.0"}, nil))
	assert.Equal(t, before, polyrepoGit(t, remote, "rev-parse", "refs/tags/pkg@1.0.0"))
	require.NoError(t, g.PushRelease(context.Background(), "origin", "", nil, []string{"pkg@1.0.0"}))
	assert.NotEqual(t, before, polyrepoGit(t, remote, "rev-parse", "refs/tags/pkg@1.0.0"))
}
