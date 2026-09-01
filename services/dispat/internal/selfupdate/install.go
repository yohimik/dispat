package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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

// Validator inspects a downloaded file before an installer commits to it.
//
// It is a strategy rather than a step because the two callers can trust
// different things: replacing dispat with dispat can insist the new binary
// runs and reports the version the release promised, and installing an
// unknown tool can insist on nothing at all, since a foreign binary need not
// answer --version and one downloaded for another platform cannot run here.
type Validator interface {
	Validate(ctx context.Context, path string) error
}

// VersionValidator runs the downloaded binary and requires its output to name
// the version that was asked for. A file that downloaded intact can still be
// the wrong thing entirely, and finding that out after the swap means finding
// it out with no working binary left.
type VersionValidator struct{ Want string }

// Validate runs path and reports what it found, if anything is wrong with it.
func (v VersionValidator) Validate(ctx context.Context, path string) error {
	return smokeTest(ctx, path, v.Want)
}

// Installer puts a downloaded release asset in a binary's place.
type Installer struct {
	// Exe is the binary to replace. Empty means the running one. A path
	// nothing occupies yet is a first install rather than a replacement.
	Exe    string
	Client *http.Client // default: a 10m-timeout client
	// Validator is what the downloaded file has to satisfy before anything is
	// moved. Nil accepts whatever arrived, which is all an installer can do
	// for a binary it knows nothing about.
	Validator Validator
	// Command is the command word this installer's failures name. Empty is
	// "selfupdate"; see commandOr.
	Command string
	// Token, when set, sends the download to Asset.APIURL with the same
	// bearer credential Source uses for the listing. See download.
	Token string
	Log   zerolog.Logger
}

// what names this installer in the errors it returns.
func (i *Installer) what() string { return commandOr(i.Command) }

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

// Fetch downloads the asset into dir and answers the file it wrote.
//
// The file is staged in dir rather than in the system temp folder for two
// reasons: a rename out of it must not cross a filesystem, and a directory
// that cannot be written to should cost a refusal rather than fifteen
// megabytes. On every failure the staged file is removed; on success it
// belongs to the caller, to move, to read or to delete.
//
// target is the file the download is destined for. Only its extension is read,
// and it matters on Windows: the staged file keeps it, because Windows will
// not execute a file that carries none, and both the version check and the
// pipe run what was staged.
func (i *Installer) Fetch(ctx context.Context, a Asset, dir, target string) (path string, err error) {
	tmp, err := os.CreateTemp(dir, tempPattern(target, "download"))
	if err != nil {
		return "", fmt.Errorf("%s: %s: %w (%v)", i.what(), dir, ErrNotWritable, err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()

	if err = i.download(ctx, a, tmp); err != nil {
		tmp.Close()
		return "", err
	}
	if err = tmp.Close(); err != nil {
		return "", fmt.Errorf("%s: %s: %w", i.what(), tmpName, err)
	}
	i.Log.Debug().Str("asset", a.Name).Str("staged", tmpName).
		Msg(i.what() + ": asset downloaded and verified")
	return tmpName, nil
}

// Install downloads the asset, satisfies itself that what arrived is the
// binary it asked for, and puts it in place. It answers where the outgoing
// binary was kept, which is empty when the path held nothing to keep.
//
// Nothing is moved until every check has passed, so a failed update leaves the
// working binary exactly where it was.
func (i *Installer) Install(ctx context.Context, a Asset) (backup string, err error) {
	exe, err := i.exe()
	if err != nil {
		return "", fmt.Errorf("%s: locating the running binary: %w", i.what(), err)
	}
	dir := filepath.Dir(exe)

	tmpName, err := i.Fetch(ctx, a, dir, exe)
	if err != nil {
		if errors.Is(err, ErrNotWritable) {
			return "", fmt.Errorf("%w; re-run with the rights to replace %s", err, exe)
		}
		return "", err
	}
	defer func() {
		// On every failure path below the download is removed; on success it
		// has been renamed away and this finds nothing.
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()

	// The replacement inherits the mode of the binary it replaces, so an
	// install that was deliberately group-only stays that way rather than
	// being widened to whatever the umask allows. Owner-execute is the one
	// bit forced on, because the next step runs the file; the binary being
	// replaced is running, so in practice its mode already carries it. A path
	// nothing occupies yet has no mode to inherit and takes 0755.
	mode := os.FileMode(0o755)
	if info, statErr := os.Stat(exe); statErr == nil {
		mode = info.Mode().Perm() | 0o100
	}
	if err = os.Chmod(tmpName, mode); err != nil {
		return "", fmt.Errorf("%s: %s: %w", i.what(), tmpName, err)
	}
	if i.Validator != nil {
		if err = i.Validator.Validate(ctx, tmpName); err != nil {
			return "", err
		}
	}
	return Replace(exe, tmpName)
}

// download streams the asset into f, checking as it goes that what arrives is
// the size the release advertised and hashes to the digest it published.
//
// Without a token the request goes to the public download URL and carries no
// Authorization header: that URL redirects to object storage, and a public
// asset needs no credentials. With a token it goes to the asset's API
// endpoint asking for application/octet-stream — the address that answers
// when the repository is not public — and the redirect to object storage is
// safe because Go strips the Authorization header when a redirect changes
// hosts.
func (i *Installer) download(ctx context.Context, a Asset, f *os.File) error {
	url := a.URL
	authed := i.Token != "" && a.APIURL != ""
	if authed {
		url = a.APIURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("%s: %w", i.what(), err)
	}
	if authed {
		req.Header.Set("Accept", "application/octet-stream")
		req.Header.Set("Authorization", "Bearer "+i.Token)
	}
	resp, err := i.client().Do(req)
	if err != nil {
		return fmt.Errorf("%s: downloading %s: %w", i.what(), a.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: downloading %s: unexpected status %s", i.what(), a.Name, resp.Status)
	}

	sum := sha256.New()
	n, err := io.Copy(f, io.LimitReader(io.TeeReader(resp.Body, sum), maxAsset+1))
	if err != nil {
		return fmt.Errorf("%s: downloading %s: %w", i.what(), a.Name, err)
	}
	if n > maxAsset {
		return fmt.Errorf("%s: %s is larger than %d bytes", i.what(), a.Name, int64(maxAsset))
	}
	if a.Size > 0 && n != a.Size {
		return fmt.Errorf("%s: %s is %d bytes, the release says %d: the download is incomplete",
			i.what(), a.Name, n, a.Size)
	}
	want, ok := strings.CutPrefix(a.Digest, "sha256:")
	if !ok {
		// Older GitHub Enterprise versions publish no digest. The size check
		// above is then the whole of what stands between a truncated transfer
		// and an install, which is worth saying out loud.
		i.Log.Warn().Str("asset", a.Name).
			Msg(i.what() + ": the release publishes no checksum for this asset; only its size is verified")
		return nil
	}
	if got := hex.EncodeToString(sum.Sum(nil)); !strings.EqualFold(got, want) {
		return fmt.Errorf("%s: %s hashes to %s, the release says %s: refusing to install it",
			i.what(), a.Name, got, want)
	}
	i.Log.Debug().Str("asset", a.Name).Msg(i.what() + ": checksum matches the release digest")
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
