package download

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeEnv is a machine that answers whatever a scenario needs, so the folder
// rule can be exercised without touching the real /usr/local/bin.
type fakeEnv struct {
	vars     map[string]string
	writable map[string]bool
	goos     string
}

func (e fakeEnv) Getenv(key string) string { return e.vars[key] }
func (e fakeEnv) Writable(dir string) bool { return e.writable[dir] }
func (e fakeEnv) GOOS() string {
	if e.goos == "" {
		return "linux"
	}
	return e.goos
}

var acme = Repository{Owner: "acme", Repo: "tool"}

// TestResolveTargetWalksTheFolderRule: the same ladder install.sh climbs, so a
// machine that already answers "where do binaries go" answers it once for
// both. Each rung is asked only when the one above it said nothing.
func TestResolveTargetWalksTheFolderRule(t *testing.T) {
	home := "/home/dev"
	for name, tc := range map[string]struct {
		flag string
		env  fakeEnv
		want string
	}{
		"the flag wins outright": {
			flag: "/opt/bin",
			env:  fakeEnv{vars: map[string]string{BinDirEnv: "/ignored", "HOME": home}, writable: map[string]bool{SystemBinDir: true}},
			want: "/opt/bin",
		},
		"then the variable": {
			env:  fakeEnv{vars: map[string]string{BinDirEnv: "/opt/bin", "HOME": home}, writable: map[string]bool{SystemBinDir: true}},
			want: "/opt/bin",
		},
		"then the system folder, when it takes a file": {
			env:  fakeEnv{vars: map[string]string{"HOME": home}, writable: map[string]bool{SystemBinDir: true}},
			want: SystemBinDir,
		},
		"then the user's own": {
			env:  fakeEnv{vars: map[string]string{"HOME": home}},
			want: filepath.Join(home, UserBinDir),
		},
		"and Windows names the home folder differently": {
			env:  fakeEnv{vars: map[string]string{"USERPROFILE": `C:\Users\dev`}, goos: "windows"},
			want: filepath.Join(`C:\Users\dev`, UserBinDir),
		},
	} {
		t.Run(name, func(t *testing.T) {
			target, err := ResolveTarget(tc.flag, "", acme, tc.env)
			require.NoError(t, err)
			assert.Equal(t, tc.want, target.Dir)
		})
	}
}

// TestResolveTargetSaysSoWithNowhereToPutIt: a machine whose system folder
// belongs to root and which has no home folder either cannot be guessed at,
// and the refusal names both flags that answer it.
func TestResolveTargetSaysSoWithNowhereToPutIt(t *testing.T) {
	_, err := ResolveTarget("", "", acme, fakeEnv{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--bin-dir")
	assert.Contains(t, err.Error(), BinDirEnv)
}

// TestResolveTargetNamesTheToolAfterItsRepository: which is what a project's
// binary is nearly always called, and what makes the common invocation need no
// flag at all.
func TestResolveTargetNamesTheToolAfterItsRepository(t *testing.T) {
	env := fakeEnv{vars: map[string]string{BinDirEnv: "/opt/bin"}}

	target, err := ResolveTarget("", "", acme, env)
	require.NoError(t, err)
	assert.Equal(t, "tool", target.Name)
	assert.Equal(t, filepath.Join("/opt/bin", "tool"), target.Path())

	target, err = ResolveTarget("", "gh", acme, env)
	require.NoError(t, err)
	assert.Equal(t, "gh", target.Name, "a project whose binary is not its repository is what --as is for")
}

// TestResolveTargetGivesWindowsAnExtension: a file with none is not a program
// there, so a name that carries none gains one and a name that carries its own
// keeps it.
func TestResolveTargetGivesWindowsAnExtension(t *testing.T) {
	env := fakeEnv{vars: map[string]string{BinDirEnv: `C:\bin`}, goos: "windows"}

	target, err := ResolveTarget("", "", acme, env)
	require.NoError(t, err)
	assert.Equal(t, "tool.exe", target.Name)

	target, err = ResolveTarget("", "tool.bat", acme, env)
	require.NoError(t, err)
	assert.Equal(t, "tool.bat", target.Name, "a name that says what it is keeps saying it")
}

// TestResolveTargetRefusesANameThatIsAPath: --bin-dir is what says where a
// tool goes. A name reaching out of it would install somewhere the reader
// never read, which is the one way a download can surprise a machine.
func TestResolveTargetRefusesANameThatIsAPath(t *testing.T) {
	env := fakeEnv{vars: map[string]string{BinDirEnv: "/opt/bin"}}
	for _, name := range []string{"../evil", "sub/tool", ".", ".."} {
		_, err := ResolveTarget("", name, acme, env)
		require.Error(t, err, "name: %q", name)
	}
}

// TestResolveTargetMakesTheFolderAbsolute: the folder is reported back and
// then written to, and "install to bin/tool" says nothing about which bin.
func TestResolveTargetMakesTheFolderAbsolute(t *testing.T) {
	target, err := ResolveTarget("./bin", "", acme, fakeEnv{})
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(target.Dir), "got %q", target.Dir)
}

// TestInstalledComparesTheFileAgainstTheRelease: what makes a download
// idempotent. A provisioning script may run the same command on every boot and
// pay for the transfer once.
func TestInstalledComparesTheFileAgainstTheRelease(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool")
	body := []byte("the released binary")
	require.NoError(t, os.WriteFile(path, body, 0o755))
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])

	got, err := Installed(path, digest)
	require.NoError(t, err)
	assert.True(t, got)

	got, err = Installed(path, "sha256:"+hex.EncodeToString(sum[:len(sum)-1])+"00")
	require.NoError(t, err)
	assert.False(t, got, "a different release is not this one")

	got, err = Installed(filepath.Join(dir, "absent"), digest)
	require.NoError(t, err)
	assert.False(t, got, "a path holding nothing is simply not installed")

	// GitHub writes the digest in lower case and nothing says it must stay
	// that way, so the comparison does not care.
	got, err = Installed(path, "sha256:"+hex.EncodeToString(sum[:]))
	require.NoError(t, err)
	assert.True(t, got)
}

// TestInstalledCannotAnswerWithoutADigest: older GitHub Enterprise versions
// publish none, and the honest answer is then that there is no answer. Saying
// "already installed" would leave such a machine on an old binary forever.
func TestInstalledCannotAnswerWithoutADigest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o755))

	_, err := Installed(path, "")
	assert.ErrorIs(t, err, ErrNoDigest)

	_, err = Installed(path, "md5:abc")
	assert.ErrorIs(t, err, ErrNoDigest, "a checksum dispat does not compute is no checksum to it")

	_, err = Installed(dir, "sha256:"+hex.EncodeToString(make([]byte, 32)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a folder")

	// A destination that cannot be read is not "not installed": answering
	// that would send the install into a file it is about to fail on anyway,
	// with a worse message than the one it would have given.
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		locked := filepath.Join(dir, "locked")
		require.NoError(t, os.WriteFile(locked, []byte("x"), 0o000))
		_, err = Installed(locked, "sha256:"+hex.EncodeToString(make([]byte, 32)))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "reading")
	}
}

// TestReplaceableGuardsWhatStandsAtTheDestination: an install is two renames,
// and the first would move whatever is there out of the way. A folder in a
// shared bin directory belongs to somebody, and --force skips every
// comparison, so this is asked of the target rather than of the comparison.
func TestReplaceableGuardsWhatStandsAtTheDestination(t *testing.T) {
	dir := t.TempDir()

	assert.NoError(t, Replaceable(filepath.Join(dir, "absent")), "nothing is in the way")

	file := filepath.Join(dir, "tool")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o755))
	assert.NoError(t, Replaceable(file), "an ordinary file is what a download replaces")

	folder := filepath.Join(dir, "folder")
	require.NoError(t, os.Mkdir(folder, 0o755))
	err := Replaceable(folder)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a folder")

	if runtime.GOOS == "windows" {
		return
	}
	// A link on PATH pointing at a binary is an ordinary way to install one,
	// so replacing the link is what was asked for. A link to anything else is
	// that thing.
	toFile := filepath.Join(dir, "link-to-file")
	require.NoError(t, os.Symlink(file, toFile))
	assert.NoError(t, Replaceable(toFile))

	toFolder := filepath.Join(dir, "link-to-folder")
	require.NoError(t, os.Symlink(folder, toFolder))
	err = Replaceable(toFolder)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "link to something that is not a file")

	broken := filepath.Join(dir, "broken")
	require.NoError(t, os.Symlink(filepath.Join(dir, "nowhere"), broken))
	require.Error(t, Replaceable(broken), "a link to nothing is not a file either")

	// A path whose parent is a file cannot be read at all, which is neither
	// "nothing is there" nor "something is".
	err = Replaceable(filepath.Join(file, "under-a-file"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be read")
}

// TestKindOfNamesWhatStandsInTheWay: "not a regular file" tells a reader
// nothing they can act on, so each shape a destination can be is named.
func TestKindOfNamesWhatStandsInTheWay(t *testing.T) {
	for want, mode := range map[string]os.FileMode{
		"folder":                               os.ModeDir,
		"link to something that is not a file": os.ModeSymlink,
		"device":                               os.ModeDevice,
		"socket":                               os.ModeSocket,
		"named pipe":                           os.ModeNamedPipe,
		"special file":                         os.ModeIrregular,
	} {
		assert.Equal(t, want, kindOf(mode))
	}
}

// TestEnsureDirCreatesTheInstallFolder: a first install into the user's own
// bin folder must not have to be preceded by a mkdir.
func TestEnsureDirCreatesTheInstallFolder(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	require.NoError(t, EnsureDir(dir))
	assert.DirExists(t, dir)
	require.NoError(t, EnsureDir(dir), "and creating one that is there is not an error")

	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		locked := t.TempDir()
		require.NoError(t, os.Chmod(locked, 0o500))
		t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
		err := EnsureDir(filepath.Join(locked, "nope"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot create")
	}
}

// TestOSEnvironmentAnswersTheRealMachine: the production Environment, whose
// one interesting answer is whether a folder actually takes a file. Asking the
// mode bits instead would answer for the wrong user under sudo and for no user
// at all on a read-only mount.
func TestOSEnvironmentAnswersTheRealMachine(t *testing.T) {
	env := OSEnvironment{OS: runtime.GOOS}
	assert.Equal(t, runtime.GOOS, env.GOOS())

	t.Setenv(BinDirEnv, "/opt/bin")
	assert.Equal(t, "/opt/bin", env.Getenv(BinDirEnv))
	assert.Empty(t, env.Getenv("DISPAT_NOTHING_SETS_THIS"))

	dir := t.TempDir()
	assert.True(t, env.Writable(dir))
	assert.False(t, env.Writable(filepath.Join(dir, "absent")))
	assert.False(t, env.Writable(filepath.Join(dir, "..")+string(filepath.Separator)+"absent"))

	file := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(file, nil, 0o644))
	assert.False(t, env.Writable(file), "a file is not a folder to install into")

	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		locked := t.TempDir()
		require.NoError(t, os.Chmod(locked, 0o500))
		t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
		assert.False(t, env.Writable(locked))
	}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "the probe leaves nothing behind: %v", entries)
}
