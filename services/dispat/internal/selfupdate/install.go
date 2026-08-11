package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

const (
	// maxAsset bounds the download. The binary is about 15 MiB; this is the
	// point past which the answer is not a dispat release.
	maxAsset = 256 << 20
	// downloadTimeout is the default client's timeout. It covers the whole
	// transfer, so it is generous: a slow link is not a failure.
	downloadTimeout = 10 * time.Minute
	// smokeTimeout bounds the "does the new binary run" check.
	smokeTimeout = 30 * time.Second
)

// Installer puts a downloaded release in the running binary's place.
type Installer struct {
	// Exe is the binary to replace. Empty means the running one.
	Exe    string
	Client *http.Client // default: a 10m-timeout client
	Log    zerolog.Logger
}

func (i *Installer) exe() (string, error) {
	if i.Exe != "" {
		return i.Exe, nil
	}
	return Executable()
}

func (i *Installer) client() *http.Client {
	if i.Client != nil {
		return i.Client
	}
	return &http.Client{Timeout: downloadTimeout}
}

// Install downloads the asset, satisfies itself that what arrived is the
// binary it asked for, and puts it in place. It answers where the outgoing
// binary was kept.
//
// Nothing is moved until every check has passed, so a failed update leaves the
// working binary exactly where it was.
func (i *Installer) Install(ctx context.Context, a Asset, want string) (backup string, err error) {
	exe, err := i.exe()
	if err != nil {
		return "", fmt.Errorf("selfupdate: locating the running binary: %w", err)
	}
	dir := filepath.Dir(exe)

	// The temp file is created before the download and in the install
	// directory, for two reasons: a rename across filesystems fails, and a
	// directory that cannot be written to should cost a refusal rather than
	// fifteen megabytes.
	tmp, err := os.CreateTemp(dir, tempPattern(exe, "update"))
	if err != nil {
		return "", fmt.Errorf("selfupdate: %s is not writable (%w); "+
			"re-run with the rights to replace %s", dir, err, exe)
	}
	tmpName := tmp.Name()
	defer func() {
		// On every failure path the download is removed; on success it has
		// been renamed away and this finds nothing.
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()

	if err = i.download(ctx, a, tmp); err != nil {
		tmp.Close()
		return "", err
	}
	if err = tmp.Close(); err != nil {
		return "", fmt.Errorf("selfupdate: %s: %w", tmpName, err)
	}
	// The replacement inherits the mode of the binary it replaces, so an
	// install that was deliberately group-only stays that way rather than
	// being widened to whatever the umask allows. Owner-execute is the one
	// bit forced on, because the next step runs the file; the binary being
	// replaced is running, so in practice its mode already carries it.
	mode := os.FileMode(0o755)
	if info, statErr := os.Stat(exe); statErr == nil {
		mode = info.Mode().Perm() | 0o100
	}
	if err = os.Chmod(tmpName, mode); err != nil {
		return "", fmt.Errorf("selfupdate: %s: %w", tmpName, err)
	}
	if err = smokeTest(ctx, tmpName, want); err != nil {
		return "", err
	}
	return Replace(exe, tmpName)
}

// download streams the asset into f, checking as it goes that what arrives is
// the size the release advertised and hashes to the digest it published.
//
// The request carries no Authorization header on purpose. The download URL
// redirects to object storage, and Go forwards headers across redirects, so an
// authenticated request would arrive at a host that rejects it. A release
// asset needs no credentials anyway.
func (i *Installer) download(ctx context.Context, a Asset, f *os.File) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return fmt.Errorf("selfupdate: %w", err)
	}
	resp, err := i.client().Do(req)
	if err != nil {
		return fmt.Errorf("selfupdate: downloading %s: %w", a.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("selfupdate: downloading %s: unexpected status %s", a.Name, resp.Status)
	}

	sum := sha256.New()
	n, err := io.Copy(f, io.LimitReader(io.TeeReader(resp.Body, sum), maxAsset+1))
	if err != nil {
		return fmt.Errorf("selfupdate: downloading %s: %w", a.Name, err)
	}
	if n > maxAsset {
		return fmt.Errorf("selfupdate: %s is larger than %d bytes", a.Name, int64(maxAsset))
	}
	if a.Size > 0 && n != a.Size {
		return fmt.Errorf("selfupdate: %s is %d bytes, the release says %d: the download is incomplete",
			a.Name, n, a.Size)
	}
	if want, ok := strings.CutPrefix(a.Digest, "sha256:"); ok {
		if got := hex.EncodeToString(sum.Sum(nil)); !strings.EqualFold(got, want) {
			return fmt.Errorf("selfupdate: %s hashes to %s, the release says %s: refusing to install it",
				a.Name, got, want)
		}
		i.Log.Debug().Str("asset", a.Name).Msg("selfupdate: checksum matches the release digest")
	}
	return nil
}

// smokeTest runs the binary before trusting it with the running one's place.
// A file that downloaded intact can still be the wrong thing entirely, and
// finding that out after the swap means finding it out with no dispat left.
func smokeTest(ctx context.Context, path, want string) error {
	ctx, cancel := context.WithTimeout(ctx, smokeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	// The binary being tested is about to check for updates otherwise, which
	// is a network call nobody asked for in the middle of an install.
	cmd.Env = append(os.Environ(), "DISPAT_UPDATE_CHECK=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("selfupdate: the downloaded binary does not run: %w", err)
	}
	if want != "" && !strings.Contains(string(out), want) {
		return fmt.Errorf("selfupdate: the downloaded binary reports a different version than %s", want)
	}
	return nil
}
