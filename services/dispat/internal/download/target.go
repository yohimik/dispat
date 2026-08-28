package download

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// BinDirEnv names the folder downloads are installed into when no flag does.
// It is install.sh's variable, so a machine that already answers "where do
// dispat's binaries go" answers it once for both.
const BinDirEnv = "DISPAT_BIN_DIR"

// SystemBinDir is the first folder a download is installed into when nothing
// names one, and UserBinDir is where it goes when that folder cannot be
// written to. The pair mirrors install.sh, whose rule is the one a reader has
// already met.
const (
	SystemBinDir = "/usr/local/bin"
	UserBinDir   = ".local/bin"
)

// Target is where a downloaded tool ends up: a folder on PATH and the name the
// tool is known by inside it.
type Target struct {
	Dir  string
	Name string
}

// Path is the file itself.
func (t Target) Path() string { return filepath.Join(t.Dir, t.Name) }

// Environment is what a Target needs to resolve itself. It is an interface so
// a test can ask for a machine it is not running on: the answer depends on
// three things the process does not choose, and passing all three is what
// keeps the rule testable without touching the real /usr/local/bin.
type Environment interface {
	// Getenv answers a variable, empty when it is unset.
	Getenv(key string) string
	// Writable reports whether a folder exists and can be written to.
	Writable(dir string) bool
	// GOOS is the platform the binary is being installed for.
	GOOS() string
}

// OSEnvironment is the real machine.
type OSEnvironment struct{ OS string }

// Getenv reads the process environment.
func (e OSEnvironment) Getenv(key string) string { return os.Getenv(key) }

// GOOS is the platform this environment installs for.
func (e OSEnvironment) GOOS() string { return e.OS }

// Writable reports whether dir exists and accepts a file, by writing one.
// Asking the mode bits instead would answer for the wrong user under sudo and
// for no user at all on a read-only mount, and the one thing this decides is
// whether the very next step can create a file there.
func (e OSEnvironment) Writable(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	probe, err := os.CreateTemp(dir, ".dispat-probe-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	probe.Close()
	_ = os.Remove(name)
	return true
}

// ResolveTarget decides where a download is installed.
//
// dir and name are what the command line said, either of which may be empty:
// the folder then falls through DISPAT_BIN_DIR, /usr/local/bin when it can be
// written to, and the user's own bin folder, and the name is the repository's,
// which is what a project's binary is nearly always called. On Windows a name
// with no extension gains .exe, because a file without one is not a program
// there.
func ResolveTarget(dir, name string, repo Repository, env Environment) (Target, error) {
	resolved, err := resolveDir(dir, env)
	if err != nil {
		return Target{}, err
	}
	if name == "" {
		name = repo.Repo
	}
	if err := ValidName(name); err != nil {
		return Target{}, err
	}
	if env.GOOS() == "windows" && filepath.Ext(name) == "" {
		name += ".exe"
	}
	return Target{Dir: resolved, Name: name}, nil
}

// resolveDir walks the folder rule until something answers.
func resolveDir(dir string, env Environment) (string, error) {
	if dir == "" {
		dir = env.Getenv(BinDirEnv)
	}
	if dir != "" {
		// Absolute, because the folder is reported back and then written to:
		// "install to bin/tool" says nothing about which bin, and the report
		// and the write must name the same one however the process moves.
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", fmt.Errorf("download: %s is not a folder dispat can resolve: %w", dir, err)
		}
		return abs, nil
	}
	if env.Writable(SystemBinDir) {
		return SystemBinDir, nil
	}
	if home := env.Getenv("HOME"); home != "" {
		return filepath.Join(home, UserBinDir), nil
	}
	// Windows names the same folder differently, and a machine with neither is
	// one that has to be told.
	if profile := env.Getenv("USERPROFILE"); profile != "" {
		return filepath.Join(profile, UserBinDir), nil
	}
	return "", fmt.Errorf("download: nowhere to install: %s is not writable and no home folder is set; "+
		"name one with --bin-dir or %s", SystemBinDir, BinDirEnv)
}

// ValidName refuses a name that is a path rather than a name. --bin-dir is
// what says where a tool goes, and an --as reaching out of it would install
// somewhere the reader did not read.
//
// Exported because it is a rule about a flag's value: the controller asks it
// in the phase where every other usage mistake is caught, so the refusal is
// the usage exit and costs no request, and ResolveTarget asks it again so the
// package is correct whoever calls it.
func ValidName(name string) error {
	if name == "." || name == ".." {
		return fmt.Errorf("download: %q is not a name for a tool", name)
	}
	if strings.ContainsRune(name, '/') || strings.ContainsRune(name, filepath.Separator) {
		return fmt.Errorf("download: --as takes a file name, not a path: %q; --bin-dir says where it goes", name)
	}
	return nil
}

// EnsureDir creates the install folder, so a first install into the user's own
// bin folder does not have to be preceded by a mkdir.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("download: cannot create %s: %w", dir, err)
	}
	return nil
}

// Replaceable reports whether the destination is a file a download may take
// the place of.
//
// A path holding nothing is one, and so is an ordinary file. Anything else
// belongs to somebody: an install is two renames, and the first of them would
// move a folder, a socket or a device out of the way to stand a binary where
// it was. The check is the target's rather than the comparison's because
// --force skips every comparison and must not skip this.
func Replaceable(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("download: %s cannot be read: %w", path, err)
	}
	if info.Mode().IsRegular() {
		return nil
	}
	// A symbolic link is followed, because a link on PATH pointing at a
	// binary is an ordinary way to install one, and replacing the link is
	// what was asked for. A link to anything else is that thing.
	if info.Mode()&os.ModeSymlink != 0 {
		if resolved, err := os.Stat(path); err == nil && resolved.Mode().IsRegular() {
			return nil
		}
	}
	return fmt.Errorf("download: %s is a %s, not a file dispat may replace", path, kindOf(info.Mode()))
}

// kindOf names what stands in the way, because "not a regular file" tells a
// reader nothing they can act on.
func kindOf(mode os.FileMode) string {
	switch {
	case mode.IsDir():
		return "folder"
	case mode&os.ModeSymlink != 0:
		return "link to something that is not a file"
	case mode&os.ModeDevice != 0:
		return "device"
	case mode&os.ModeSocket != 0:
		return "socket"
	case mode&os.ModeNamedPipe != 0:
		return "named pipe"
	}
	return "special file"
}

// ErrNoDigest is what Installed reports when the release published no checksum
// to compare against, which is the one case where "is it already installed"
// has no answer rather than a negative one.
var ErrNoDigest = errors.New("the release publishes no checksum")

// Installed reports whether the file at path already is this asset, by hashing
// it against the digest the release published.
//
// It is what makes a download idempotent: a provisioning script can run the
// same command on every boot and pay for the transfer once. A path holding
// nothing is simply not installed; a release with no digest cannot be compared
// and says so, so the caller can decide rather than be told a wrong answer.
func Installed(path, digest string) (bool, error) {
	want, ok := strings.CutPrefix(digest, "sha256:")
	if !ok || want == "" {
		return false, ErrNoDigest
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("download: reading %s: %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false, fmt.Errorf("download: reading %s: %w", path, err)
	}
	if info.IsDir() {
		return false, fmt.Errorf("download: %s is a folder, not a file dispat can replace", path)
	}
	sum := sha256.New()
	// Streamed rather than read: the file on the other end of this is a
	// binary, and one that is already installed is read on every check.
	if _, err := io.Copy(sum, f); err != nil {
		return false, fmt.Errorf("download: reading %s: %w", path, err)
	}
	return strings.EqualFold(hex.EncodeToString(sum.Sum(nil)), want), nil
}
