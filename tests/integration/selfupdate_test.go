package integration

// Area 20: dispat replacing its own binary, through the compiled binary and
// against a fake releases API. Everything here is real: two binaries built at
// two versions, one downloaded over HTTP, checked, and moved into the other's
// place, then run again to see which one answers. Nothing else in the suite
// can witness that, because nothing else in the suite replaces the file it is
// running from.

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yohimik/dispat/pkg/models"
	"github.com/yohimik/dispat/tests/integration/internal/harness"
)

// The two versions the scenarios move between.
const (
	suOld = "1.0.0"
	suNew = "1.1.0"
)

// suRepo is the fixture: a copy of the old binary in a directory of its own,
// so a self-update replaces the copy rather than the suite's shared build, and
// a fake API offering the new one for this platform.
//
// The copy matters. Every other test in this suite runs one cached binary, and
// this is the one that overwrites what it runs.
type suRepo struct {
	*harness.Repo
	exe    string // the copy under test
	backup string
	api    string
	assets map[string][]byte // asset name -> the binary that version serves
	// body is the release notes every served release carries. A scenario that
	// wants a different shape sets it and calls serve again.
	body string
	// hits records the paths the fake answered, in order, which is how a test
	// proves the notes were read before the binary was fetched. The handler
	// runs on the server's goroutines, so the lock is not decoration.
	mu sync.Mutex
	// token, when set, makes the repository private: the listing and the
	// asset's API endpoint answer only to that bearer credential, and the
	// public download URL answers with a sign-in page as github.com does.
	token string
	hits  []string
}

// requireToken makes the repository private, which is what a dispat fork a
// company releases only to itself looks like.
func (r *suRepo) requireToken(token string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.token = token
}

// requests is the paths the fake has answered so far.
func (r *suRepo) requests() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.hits...)
}

// suBody is the release body dispat's own releases carry: the change sections,
// then the rule and the install commands the release page closes with. The
// fixture is the real shape on purpose, because what the command has to get
// right is dropping the second half of it.
const suBody = "### Features\n\n- read a release's notes after an update\n\n" +
	"### Fixes\n\n- stop a truncated listing failing opaquely\n\n" +
	"### Release\n\n- commit: abc123\n\n---\n\n**Install this version:**\n\n" +
	"```sh\ncurl -fsSL https://example.invalid/install.sh | sh\n```\n\n" +
	"[Documentation](https://example.invalid/docs)\n"

func newSURepo(t *testing.T) *suRepo {
	return newSURepoVersions(t, map[string]string{suNew: harness.BuildVersioned(t, suNew)})
}

func newSURepoVersions(t *testing.T, versions map[string]string) *suRepo {
	t.Helper()
	r := &suRepo{Repo: harness.New(t), assets: map[string][]byte{}, body: suBody}
	r.exe = filepath.Join(t.TempDir(), "dispat"+exeSuffix())
	copyFile(t, harness.BuildVersioned(t, suOld), r.exe)
	r.backup = backupPath(r.exe)
	r.serve(t, versions)
	return r
}

// serve stands up the fake API over plain HTTP. versions maps a version to the
// binary its release hands out, which is what lets a scenario ask for a version
// and get that version rather than whatever is on disk.
func (r *suRepo) serve(t *testing.T, versions map[string]string) {
	t.Helper()
	r.serveOn(t, versions, false)
}

// serveTLS is serve over https, behind a certificate authority generated for
// this one server, and returns the path of the root's PEM. A scenario points
// the binary at that file with SSL_CERT_FILE to make the fake trustworthy, and
// omits it to see what an untrusted release host looks like.
//
// The URL names localhost rather than the listener's address, so the leaf's DNS
// name is what the client verifies and the handshake carries an SNI, which is
// how a real release host is reached.
func (r *suRepo) serveTLS(t *testing.T, versions map[string]string) string {
	t.Helper()
	return r.serveOn(t, versions, true)
}

// serveOn is the one fake both spellings run. It returns the CA PEM's path when
// secure is set and the empty string otherwise.
func (r *suRepo) serveOn(t *testing.T, versions map[string]string, secure bool) string {
	t.Helper()
	type entry struct {
		tag, version string
		prerelease   bool
		assetName    string
	}
	var entries []entry
	for version, path := range versions {
		data, err := os.ReadFile(path)
		require.NoError(t, err)
		name := assetName()
		r.assets[version] = data
		entries = append(entries, entry{
			tag: "services/dispat/v" + version, version: version,
			prerelease: strings.Contains(version, "-"), assetName: name,
		})
	}

	// The download URL has to name the server the handler is running in, so
	// the address is read off the listener and the server is only started once
	// every closure can see it.
	var base string
	release := func(e entry) map[string]any {
		sum := sha256.Sum256(r.assets[e.version])
		return map[string]any{
			"tag_name": e.tag, "draft": false, "prerelease": e.prerelease,
			"body":     r.body,
			"html_url": base + "/o/r/releases/tag/" + strings.ReplaceAll(e.tag, "/", "%2F"),
			// Both addresses, because every asset a real listing describes
			// carries both: the public browser URL and the asset's own REST
			// endpoint, which is the one that answers when a credential is
			// what makes the repository readable.
			"assets": []map[string]any{{
				"name": e.assetName, "size": len(r.assets[e.version]),
				"browser_download_url": base + "/dl/" + e.version,
				"url":                  base + "/assets/" + e.version,
				"digest":               "sha256:" + hex.EncodeToString(sum[:]),
			}},
		}
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.hits = append(r.hits, req.URL.Path)
		token := r.token
		r.mu.Unlock()
		authed := token == "" || req.Header.Get("Authorization") == "Bearer "+token
		if version, ok := strings.CutPrefix(req.URL.Path, "/dl/"); ok {
			data, known := r.assets[version]
			if !known {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if token != "" {
				// What github.com serves at the public URL of a private
				// repository: a page, under a 200, that is not the asset.
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, privatePage)
				return
			}
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			_, _ = w.Write(data)
			return
		}
		if version, ok := strings.CutPrefix(req.URL.Path, "/assets/"); ok {
			data, known := r.assets[version]
			if !known || !authed {
				w.WriteHeader(http.StatusNotFound)
				fmt.Fprint(w, `{"message":"Not Found"}`)
				return
			}
			if req.Header.Get("Accept") != "application/octet-stream" {
				// Asking this endpoint for anything else answers with the
				// asset's metadata rather than with the file.
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"name":"metadata, not the asset"}`)
				return
			}
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			_, _ = w.Write(data)
			return
		}
		if !authed {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, tag, ok := strings.Cut(req.URL.Path, "/releases/tags/"); ok {
			for _, e := range entries {
				if e.tag == tag {
					_ = json.NewEncoder(w).Encode(release(e))
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"Not Found"}`)
			return
		}
		list := make([]map[string]any, 0, len(entries)+1)
		// Another module's release, higher than any of dispat's: the listing
		// of a monorepo that publishes one release per package, and the reason
		// /releases/latest is no use here.
		list = append(list, map[string]any{"tag_name": "pkg/ccme/v9.9.9", "draft": false,
			"prerelease": false, "assets": []map[string]any{}})
		for _, e := range entries {
			list = append(list, release(e))
		}
		_ = json.NewEncoder(w).Encode(list)
	}))
	var ca string
	if secure {
		ca = filepath.Join(t.TempDir(), "ca.pem")
		leaf := suChain(t, ca)
		// Set before StartTLS, which only supplies its own certificate when
		// the configuration carries none.
		srv.TLS = &tls.Config{Certificates: []tls.Certificate{leaf}}
		_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
		require.NoError(t, err)
		base = "https://localhost:" + port
		srv.StartTLS()
	} else {
		base = "http://" + srv.Listener.Addr().String()
		srv.Start()
	}
	t.Cleanup(srv.Close)
	r.api = base
	return ca
}

// suChain generates the root a scenario asks the binary to trust and the leaf
// the fake API presents, and writes the root's PEM to caPath. Nothing outside
// the test process ever needs the keys, so they live only as long as the server
// does and the leaf is issued for the minutes the run takes.
//
// The shape mirrors what a release host presents: an ECDSA P-256 root that is a
// certificate authority, and a leaf carrying both the DNS name the URL uses and
// the loopback address the listener is on, so verification is exercised rather
// than skipped for want of a matching name.
func suChain(t *testing.T, caPath string) tls.Certificate {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "dispat integration root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caCert, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(caPath,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600))

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	require.NoError(t, err)
	// The root travels with the leaf, which is what a server that is not itself
	// a trust anchor has to send.
	return tls.Certificate{Certificate: [][]byte{leafDER, caDER}, PrivateKey: leafKey}
}

// update runs the binary under test with the fake API wired in.
func (r *suRepo) update(args ...string) harness.RunResult {
	r.T.Helper()
	full := append([]string{"self-update", "--api-url", r.api, "--owner", "o", "--repo", "r"}, args...)
	return r.CommandBin(r.exe, full...)
}

// version asks a binary which one it is.
func (r *suRepo) version(path string) string {
	r.T.Helper()
	res := r.CommandBin(path, "--version")
	require.Equal(r.T, 0, res.Code, "stderr:\n%s", res.Stderr)
	for _, line := range strings.Split(res.Stdout, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "dispat "); ok {
			v, _, _ := strings.Cut(rest, " (")
			return v
		}
	}
	r.T.Fatalf("no version line in:\n%s", res.Stdout)
	return ""
}

func assetName() string {
	name := "dispat-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func backupPath(exe string) string {
	if ext := filepath.Ext(exe); ext != "" {
		return strings.TrimSuffix(exe, ext) + ".backup" + ext
	}
	return exe + ".backup"
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(to, data, 0o755))
}

// TestSelfUpdateReplacesTheRunningBinary: the whole thing, over the process
// boundary. The binary downloads its successor, checks it against what the
// release published, runs it to be sure, and steps aside for it — and the
// proof is that the same path now answers with a different version.
func TestSelfUpdateReplacesTheRunningBinary(t *testing.T) {
	r := newSURepo(t)

	// --check first: it changes nothing and exits 1 because there is
	// something to install, which is what makes it a gate.
	res := r.update("--check")
	assert.Equal(t, 1, res.Code, "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout, "current   dispat "+suOld)
	assert.Contains(t, res.Stdout, "available dispat "+suNew)
	assert.Equal(t, suOld, r.version(r.exe), "--check touches nothing")
	assert.NoFileExists(t, r.backup)

	res = r.update()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "installed dispat "+suNew)
	assert.Equal(t, suNew, r.version(r.exe), "the path now runs the new binary")
	assert.Equal(t, suOld, r.version(r.backup), "and the old one is beside it")

	// Now current: the same command is a no-op that says so, and --check
	// agrees by exiting 0.
	res = r.update()
	require.Equal(t, 0, res.Code)
	assert.Contains(t, res.Stdout, "already the latest release")
	assert.Equal(t, 0, r.update("--check").Code)

	// --force installs it again anyway, which is how a damaged binary is
	// repaired, and the backup becomes the copy it just replaced.
	res = r.update("--force")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, suNew, r.version(r.exe))
	assert.Equal(t, suNew, r.version(r.backup))
}

// A second real update has to replace the rollback copy only after the new
// executable is installed. These distinct versions prove which file became
// the backup and that the older copy was not left parked in the directory.
func TestSelfUpdateSecondInstallKeepsOnlyTheVersionItReplaced(t *testing.T) {
	const next = "1.2.0"
	r := newSURepoVersions(t, map[string]string{
		suNew: harness.BuildVersioned(t, suNew),
		next:  harness.BuildVersioned(t, next),
	})
	require.Equal(t, 0, r.update("--release", suNew).Code)
	require.Equal(t, suOld, r.version(r.backup))

	res := r.update("--release", next)
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, next, r.version(r.exe))
	assert.Equal(t, suNew, r.version(r.backup))
	entries, err := os.ReadDir(filepath.Dir(r.exe))
	require.NoError(t, err)
	assert.Len(t, entries, 2, "the superseded backup was removed: %v", entries)
}

// A checksum-valid candidate can disappear after its smoke test. That forces
// the second rename to fail after the previous backup was parked, through the
// real CLI and download path rather than an injected filesystem error.
func TestSelfUpdateFailedSecondInstallPreservesBothBinaries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the self-removing candidate is a POSIX shell script; the swap regression runs on Windows in internal/selfupdate")
	}
	const next = "1.2.0"
	vanishing := filepath.Join(t.TempDir(), "vanishing")
	require.NoError(t, os.WriteFile(vanishing, []byte("#!/bin/sh\nrm -- \"$0\"\nprintf 'dispat "+next+" (test)\\n'\n"), 0o755))
	r := newSURepoVersions(t, map[string]string{
		suNew: harness.BuildVersioned(t, suNew),
		next:  vanishing,
	})
	require.Equal(t, 0, r.update("--release", suNew).Code)
	current, err := os.ReadFile(r.exe)
	require.NoError(t, err)
	previous, err := os.ReadFile(r.backup)
	require.NoError(t, err)

	res := r.update("--release", next)
	assert.NotEqual(t, 0, res.Code, "the missing candidate cannot commit")
	assert.Contains(t, res.Stdout+res.Stderr, "installing")
	gotCurrent, err := os.ReadFile(r.exe)
	require.NoError(t, err)
	gotPrevious, err := os.ReadFile(r.backup)
	require.NoError(t, err)
	assert.Equal(t, current, gotCurrent, "the current binary was restored")
	assert.Equal(t, previous, gotPrevious, "the previous rollback copy was restored")
	entries, err := os.ReadDir(filepath.Dir(r.exe))
	require.NoError(t, err)
	assert.Len(t, entries, 2, "no staged binary or parked backup was left behind: %v", entries)
}

// An occupied backup path is not ours to overwrite. The CLI must report the
// obstruction and its remedy before it downloads anything, both under --check
// and when asked to install, while retaining the current release byte for
// byte.
func TestSelfUpdateRefusesAnUnsafePreviousBackup(t *testing.T) {
	const next = "1.2.0"
	r := newSURepoVersions(t, map[string]string{
		suNew: harness.BuildVersioned(t, suNew),
		next:  harness.BuildVersioned(t, next),
	})
	require.Equal(t, 0, r.update("--release", suNew).Code)
	current, err := os.ReadFile(r.exe)
	require.NoError(t, err)
	require.NoError(t, os.Remove(r.backup))
	require.NoError(t, os.Mkdir(r.backup, 0o700))
	marker := filepath.Join(r.backup, "owned-by-another-process")
	require.NoError(t, os.WriteFile(marker, []byte("do not replace"), 0o600))

	// The binary names its backup by its own resolved path, which a temporary
	// folder behind a symbolic link spells differently, so the name is matched
	// from the backup's base onwards.
	remedy := filepath.Base(r.backup) + " is a folder where the previous binary is kept; move or remove it, then re-run"
	downloads := func() int { return strings.Count(strings.Join(r.requests(), "\n"), "/dl/") }
	before := downloads()

	res := r.update("--check", "--release", next)
	assert.Equal(t, 1, res.Code, "an update is still available")
	assert.Contains(t, res.Stdout, "install it with: dispat self-update")
	assert.Contains(t, res.Stdout, "self-update cannot install it yet: ")
	assert.Contains(t, res.Stdout, remedy)

	res = r.update("--release", next)
	assert.NotEqual(t, 0, res.Code)
	assert.Contains(t, res.Stdout+res.Stderr, remedy)
	assert.Equal(t, before, downloads(), "the refusal costs no download")
	got, err := os.ReadFile(r.exe)
	require.NoError(t, err)
	assert.Equal(t, current, got)
	markerBytes, err := os.ReadFile(marker)
	require.NoError(t, err)
	assert.Equal(t, []byte("do not replace"), markerBytes)
	entries, err := os.ReadDir(filepath.Dir(r.exe))
	require.NoError(t, err)
	assert.Len(t, entries, 2, "no download is left behind: %v", entries)
}

// TestSelfUpdateNamesWhatStandsInTheBackupPlace: whatever other than a
// regular file stands where the backup is kept is named by what it is, with
// its remedy, and moves nothing. An update is refused before its download,
// `--check` in JSON says so as a warning, and a rollback accepts a link to a
// binary, which is what an install that replaced a link on PATH keeps, while
// refusing a link to a folder and a folder itself.
func TestSelfUpdateNamesWhatStandsInTheBackupPlace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links and named pipes are POSIX fixtures here")
	}
	const next = "1.2.0"
	updated := func(t *testing.T) *suRepo {
		t.Helper()
		r := newSURepoVersions(t, map[string]string{
			suNew: harness.BuildVersioned(t, suNew),
			next:  harness.BuildVersioned(t, next),
		})
		require.Equal(t, 0, r.update("--release", suNew).Code, "the update that leaves a backup behind")
		return r
	}
	downloads := func(r *suRepo) int { return strings.Count(strings.Join(r.requests(), "\n"), "/dl/") }
	// The binary names its backup by its own resolved path, which a temporary
	// folder behind a symbolic link spells differently, so the name is matched
	// from the backup's base onwards.
	blocked := func(r *suRepo, kind string) string {
		return filepath.Base(r.backup) + " is a " + kind + " where the previous binary is kept; move or remove it, then re-run"
	}

	t.Run("an update is refused by a link or a named pipe before it downloads", func(t *testing.T) {
		for _, row := range []struct {
			kind  string
			place func(t *testing.T, backup string)
		}{
			{kind: "symbolic link", place: func(t *testing.T, backup string) {
				require.NoError(t, os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), backup))
			}},
			{kind: "named pipe", place: func(t *testing.T, backup string) {
				require.NoError(t, syscall.Mkfifo(backup, 0o600))
			}},
		} {
			t.Run(row.kind, func(t *testing.T) {
				r := updated(t)
				require.NoError(t, os.Remove(r.backup))
				row.place(t, r.backup)
				before := downloads(r)

				res := r.update("--check", "--log-format", "json", "--release", next)
				assert.Equal(t, 1, res.Code, "an update is still available\nstdout:\n%s", res.Stdout)
				warning := jsonLine(t, res, "the update cannot be installed until the backup's place is cleared")
				assert.Contains(t, warning.Str("error"), blocked(r, row.kind))

				res = r.update("--release", next)
				assert.NotEqual(t, 0, res.Code)
				assert.Contains(t, res.Stdout+res.Stderr, blocked(r, row.kind))
				assert.Equal(t, before, downloads(r), "the refusal costs no download")
				assert.Equal(t, suNew, r.version(r.exe), "and the working binary is where it was")
			})
		}
	})

	t.Run("a rollback restores through a link to a binary", func(t *testing.T) {
		r := updated(t)
		kept := filepath.Join(t.TempDir(), "dispat-kept"+exeSuffix())
		require.NoError(t, os.Rename(r.backup, kept))
		require.NoError(t, os.Symlink(kept, r.backup))

		res := r.CommandBin(r.exe, "self-update", "--rollback")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Equal(t, suOld, r.version(r.exe), "the linked binary is what runs now")
	})

	t.Run("a rollback is refused by a link to a folder and by a folder", func(t *testing.T) {
		for _, row := range []struct {
			kind  string
			place func(t *testing.T, backup string)
		}{
			{kind: "link to something that is not a file", place: func(t *testing.T, backup string) {
				require.NoError(t, os.Symlink(t.TempDir(), backup))
			}},
			{kind: "folder", place: func(t *testing.T, backup string) {
				require.NoError(t, os.Mkdir(backup, 0o700))
			}},
		} {
			t.Run(row.kind, func(t *testing.T) {
				r := updated(t)
				require.NoError(t, os.Remove(r.backup))
				row.place(t, r.backup)

				res := r.CommandBin(r.exe, "self-update", "--check", "--rollback")
				assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
				assert.Contains(t, res.Stdout+res.Stderr, blocked(r, row.kind))

				res = r.CommandBin(r.exe, "self-update", "--rollback")
				assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
				assert.Contains(t, res.Stdout+res.Stderr, blocked(r, row.kind))
				assert.Equal(t, suNew, r.version(r.exe), "nothing moved")
			})
		}
	})
}

// TestSelfUpdateLeavesTheLeftoversItCannotSettle: the housekeeping an update
// or a rollback does for a crashed update touches only what is abandoned and
// unambiguous. A parked rollback copy is dropped when a newer backup already
// holds its place, while a staging folder younger than an hour, one holding
// another binary's copy, and one holding more than one file are left for a
// later run; the rollback itself goes on.
func TestSelfUpdateLeavesTheLeftoversItCannotSettle(t *testing.T) {
	r := newSURepo(t)
	require.Equal(t, 0, r.update().Code)
	dir := filepath.Dir(r.exe)
	crashed := time.Now().Add(-2 * time.Hour)
	staging := func(name string, isAbandoned bool, files ...string) string {
		path := filepath.Join(dir, "dispat-previous-backup-"+name)
		require.NoError(t, os.Mkdir(path, 0o700))
		for _, file := range files {
			require.NoError(t, os.WriteFile(filepath.Join(path, file), []byte("an older binary"), 0o755))
		}
		if isAbandoned {
			require.NoError(t, os.Chtimes(path, crashed, crashed))
		}
		return path
	}
	superseded := staging("superseded", true, filepath.Base(r.backup))
	young := staging("young", false, filepath.Base(r.backup))
	foreign := staging("foreign", true, "other-tool.backup")
	crowded := staging("crowded", true, filepath.Base(r.backup), "notes.txt")

	res := r.CommandBin(r.exe, "self-update", "--rollback")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, suOld, r.version(r.exe), "the rollback went on")
	assert.Equal(t, suNew, r.version(r.backup), "and the newer backup kept its place")
	assert.NoDirExists(t, superseded, "a copy a newer backup supersedes is dropped")
	for _, kept := range []string{young, foreign, crowded} {
		assert.DirExists(t, kept, "left for a later run")
	}
}

// The downloaded candidate makes the executable directory unwritable during
// its smoke test. Parking the existing backup must fail before either live
// version moves, even though the download and validation already succeeded.
func TestSelfUpdateCannotParkPreviousBackup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the candidate uses a POSIX shell script and directory permissions")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can write into an unwritable directory")
	}
	const next = "1.2.0"
	locked := filepath.Join(t.TempDir(), "lock-directory")
	require.NoError(t, os.WriteFile(locked, []byte("#!/bin/sh\nchmod 0555 \"$(dirname \"$0\")\"\nprintf 'dispat "+next+" (test)\\n'\n"), 0o755))
	r := newSURepoVersions(t, map[string]string{
		suNew: harness.BuildVersioned(t, suNew),
		next:  locked,
	})
	require.Equal(t, 0, r.update("--release", suNew).Code)
	current, err := os.ReadFile(r.exe)
	require.NoError(t, err)
	previous, err := os.ReadFile(r.backup)
	require.NoError(t, err)
	dir := filepath.Dir(r.exe)
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	res := r.update("--release", next)
	require.NoError(t, os.Chmod(dir, 0o700))
	assert.NotEqual(t, 0, res.Code)
	assert.Contains(t, res.Stdout+res.Stderr, "parking the previous backup")
	assert.Contains(t, res.Stdout+res.Stderr, "could not remove staged download")
	gotCurrent, err := os.ReadFile(r.exe)
	require.NoError(t, err)
	gotPrevious, err := os.ReadFile(r.backup)
	require.NoError(t, err)
	assert.Equal(t, current, gotCurrent)
	assert.Equal(t, previous, gotPrevious)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var staged []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "dispat-download-") {
			staged = append(staged, filepath.Join(dir, entry.Name()))
		}
	}
	require.Len(t, staged, 1, "the failed cleanup names one retained candidate")
	assert.Contains(t, res.Stdout+res.Stderr, staged[0])
	require.NoError(t, os.Remove(staged[0]))
}

// A running POSIX binary can disappear from its path during the candidate's
// smoke test. If a rollback copy was already present, that is the only known
// working alternative and must survive the first-install branch.
func TestSelfUpdateKeepsPreviousBackupWhenCurrentDisappears(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps the running executable path occupied")
	}
	const next = "1.2.0"
	removeCurrent := filepath.Join(t.TempDir(), "remove-current")
	require.NoError(t, os.WriteFile(removeCurrent, []byte("#!/bin/sh\nset -e\ncase \"$(basename \"$0\")\" in\n  dispat-download-*)\n    rm -- \"$(dirname \"$0\")/dispat\"\n    touch -t 200001010000 \"$(dirname \"$0\")/dispat.backup\"\n    ;;\nesac\nprintf 'dispat "+next+" (test)\\n'\n"), 0o755))
	r := newSURepoVersions(t, map[string]string{
		suNew: harness.BuildVersioned(t, suNew),
		next:  removeCurrent,
	})
	require.Equal(t, 0, r.update("--release", suNew).Code)
	previous, err := os.ReadFile(r.backup)
	require.NoError(t, err)

	res := r.update("--release", next)
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "installed dispat "+next)
	assert.Contains(t, res.Stdout, r.backup)
	assert.Equal(t, next, r.version(r.exe))
	gotPrevious, err := os.ReadFile(r.backup)
	require.NoError(t, err)
	assert.Equal(t, previous, gotPrevious, "the only rollback binary remains intact")
	assert.Equal(t, suOld, r.version(r.backup), "the rollback copy remains runnable")
	info, err := os.Stat(r.backup)
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), info.ModTime(), time.Minute, "the retained backup gets a fresh pruning clock")
}

// A disappearing executable without an older rollback copy is a genuine
// first install. The CLI must not advertise a backup path or rollback action
// that does not exist after it installs the validated candidate.
func TestSelfUpdateWithoutPreviousBackupReportsNoRollback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps the running executable path occupied")
	}
	removeCurrent := filepath.Join(t.TempDir(), "remove-current")
	require.NoError(t, os.WriteFile(removeCurrent, []byte("#!/bin/sh\nset -e\ncase \"$(basename \"$0\")\" in\n  dispat-download-*) rm -- \"$(dirname \"$0\")/dispat\";;\nesac\nprintf 'dispat "+suNew+" (test)\\n'\n"), 0o755))
	r := newSURepoVersions(t, map[string]string{suNew: removeCurrent})

	res := r.update("--release", suNew)
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "installed dispat "+suNew)
	assert.NotContains(t, res.Stdout, "the previous binary is at")
	assert.NotContains(t, res.Stdout, "put it back")
	assert.Equal(t, suNew, r.version(r.exe))
	assert.NoFileExists(t, r.backup)
}

// updateEnv is update with extra environment pairs, which is how a scenario
// puts a credential in front of the run.
func (r *suRepo) updateEnv(env []string, args ...string) harness.RunResult {
	r.T.Helper()
	full := append([]string{"self-update", "--api-url", r.api, "--owner", "o", "--repo", "r"}, args...)
	return r.CommandBinEnv(r.exe, env, full...)
}

// TestSelfUpdateFromAPrivateRepository: a dispat fork a company releases only
// to itself, over the process boundary. The fake publishes nothing without the
// credential and answers the public download URL with a sign-in page, so a
// binary that has actually been replaced is proof that the token reached both
// the listing and the asset. The endpoint here was named with --api-url, so
// the conventional GITHUB_TOKEN is what authenticates it.
func TestSelfUpdateFromAPrivateRepository(t *testing.T) {
	r := newSURepo(t)
	r.requireToken("sesame")

	// Nothing is readable without it, and nothing was downloaded trying.
	res := r.update("--check")
	assert.NotEqual(t, 0, res.Code)
	assert.Contains(t, res.Stdout+res.Stderr, "404")
	assert.Zero(t, countDownloads(r.requests()))
	assert.Zero(t, countAssetAPI(r.requests()))

	res = r.updateEnv([]string{"GITHUB_TOKEN=sesame"})
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "installed dispat "+suNew)
	assert.Equal(t, suNew, r.version(r.exe), "the path now runs the new binary")
	assert.Equal(t, suOld, r.version(r.backup), "and the old one is beside it")
	assert.Equal(t, 1, countAssetAPI(r.requests()), "the asset came from its API endpoint")
	assert.Zero(t, countDownloads(r.requests()),
		"and the public URL, which would have served a page, was never asked")
}

// TestSelfUpdateFromAPrivateRepositoryWithANamedToken: --token-env is how a
// credential that is not in GITHUB_TOKEN reaches the release host, and it has
// to unlock the download as well as the listing.
func TestSelfUpdateFromAPrivateRepositoryWithANamedToken(t *testing.T) {
	r := newSURepo(t)
	r.requireToken("sesame")

	// The conventional variable is not consulted once another one is named,
	// so a wrong value in it changes nothing.
	env := []string{"GITHUB_TOKEN=wrong", "DISPAT_TOKEN=sesame"}
	res := r.updateEnv(env, "--token-env", "DISPAT_TOKEN")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, suNew, r.version(r.exe))
	assert.Equal(t, 1, countAssetAPI(r.requests()))
	assert.Zero(t, countDownloads(r.requests()))
}

// TestSelfUpdateWithoutATokenStaysOnThePublicURL: the fence around the change.
// Every release the fake publishes names an asset endpoint, as every real one
// does, and a public repository still downloads from the browser URL.
func TestSelfUpdateWithoutATokenStaysOnThePublicURL(t *testing.T) {
	r := newSURepo(t)

	res := r.update()
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, suNew, r.version(r.exe))
	assert.Equal(t, 1, countDownloads(r.requests()))
	assert.Zero(t, countAssetAPI(r.requests()),
		"no token, no reason to touch the endpoint that wants one")
}

// TestSelfUpdateRollsBackAndBackAgain: the backup is only useful if it can be
// restored, and the restore is only safe if it is itself reversible. Rolling
// back twice returns to where it started, so nobody has to be sure before
// pressing it.
func TestSelfUpdateRollsBackAndBackAgain(t *testing.T) {
	r := newSURepo(t)
	require.Equal(t, 0, r.update().Code)
	require.Equal(t, suNew, r.version(r.exe))

	res := r.CommandBin(r.exe, "self-update", "--check", "--rollback")
	assert.Equal(t, 1, res.Code, "there is something to restore")
	assert.Contains(t, res.Stdout, "is dispat "+suOld)
	assert.Equal(t, suNew, r.version(r.exe), "--check restores nothing")

	res = r.CommandBin(r.exe, "self-update", "--rollback")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "rolled back to dispat "+suOld)
	assert.Equal(t, suOld, r.version(r.exe))
	assert.Equal(t, suNew, r.version(r.backup), "the binary it replaced is the new backup")

	res = r.CommandBin(r.exe, "self-update", "--rollback")
	require.Equal(t, 0, res.Code)
	assert.Equal(t, suNew, r.version(r.exe), "a second rollback returns")
	assert.Equal(t, suOld, r.version(r.backup))

	entries, err := os.ReadDir(filepath.Dir(r.exe))
	require.NoError(t, err)
	assert.Len(t, entries, 2, "nothing is parked and forgotten between the renames")
}

// TestSelfUpdateRollbackRecoversABackupACrashLeftParked: an update killed
// after it parked the previous backup and before it discarded it leaves the
// only rollback copy in a staging directory and a download beside the
// binary. The next rollback puts the copy back and restores it, and clears
// the download, instead of reporting that there is nothing to roll back to.
func TestSelfUpdateRollbackRecoversABackupACrashLeftParked(t *testing.T) {
	r := newSURepo(t)
	require.Equal(t, 0, r.update().Code)
	dir := filepath.Dir(r.exe)
	staging := filepath.Join(dir, "dispat-previous-backup-1234")
	require.NoError(t, os.Mkdir(staging, 0o700))
	require.NoError(t, os.Rename(r.backup, filepath.Join(staging, filepath.Base(r.backup))))
	download := filepath.Join(dir, "dispat-download-5678"+exeSuffix())
	require.NoError(t, os.WriteFile(download, []byte("half a binary"), 0o600))
	crashed := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(staging, crashed, crashed))
	require.NoError(t, os.Chtimes(download, crashed, crashed))

	res := r.CommandBin(r.exe, "self-update", "--rollback")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "rolled back to dispat "+suOld)
	assert.Equal(t, suOld, r.version(r.exe))
	assert.Equal(t, suNew, r.version(r.backup))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 2, "the staging directory and the download are gone: %v", entries)
}

// TestSelfUpdateInstallsANamedVersion: --release reaches any published
// version, downgrades included, which is what makes a bad release recoverable
// after the week the backup lives for.
func TestSelfUpdateInstallsANamedVersion(t *testing.T) {
	r := newSURepo(t)
	// This fixture serves both versions, so a named older one really is an
	// older binary rather than the same file under another name.
	r.serve(t, map[string]string{
		suOld: harness.BuildVersioned(t, suOld),
		suNew: harness.BuildVersioned(t, suNew),
	})

	require.Equal(t, 0, r.update().Code)
	require.Equal(t, suNew, r.version(r.exe))

	res := r.update("--release", "v"+suOld)
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, suOld, r.version(r.exe), "a named version is installed even going backwards")

	res = r.update("--release", "9.9.9")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "services/dispat/v9.9.9")
	assert.Equal(t, suOld, r.version(r.exe), "a version nobody published changes nothing")
}

// TestSelfUpdateRefusesWhatItCannotTrust: the checks stand between a download
// and the only binary the user has. A release whose checksum does not describe
// what arrives, a file that is not a program, a program answering with another
// version, a download shorter than the release says, and an asset response
// that stops before its body ends are each refused with the working binary
// still in place, no backup made and nothing staged beside it.
func TestSelfUpdateRefusesWhatItCannotTrust(t *testing.T) {
	for _, row := range []struct {
		name string
		// payload is the file the release offers; serve is what the download
		// endpoint does with it.
		payload func(t *testing.T) []byte
		serve   func(w http.ResponseWriter, payload []byte)
		digest  string
		// noNotes serves the release without a body, as a release cut by hand
		// is: the update is judged on the binary alone.
		noNotes bool
		want    string
	}{
		{
			name:    "a checksum that describes something else",
			payload: func(t *testing.T) []byte { return suPayload(t, suNew) },
			digest:  "sha256:" + strings.Repeat("00", 32),
			noNotes: true,
			want:    "hashes to",
		},
		{
			name:    "a file that is not a program",
			payload: func(t *testing.T) []byte { return []byte("this release shipped a README by mistake\n") },
			want:    "does not run",
		},
		{
			name:    "a program answering with another version",
			payload: func(t *testing.T) []byte { return suPayload(t, suOld) },
			want:    "reports a different version",
		},
		{
			name:    "a download shorter than the release says",
			payload: func(t *testing.T) []byte { return suPayload(t, suNew) },
			serve: func(w http.ResponseWriter, payload []byte) {
				half := payload[:len(payload)/2]
				w.Header().Set("Content-Length", fmt.Sprint(len(half)))
				_, _ = w.Write(half)
			},
			want: "the download is incomplete",
		},
		{
			name:    "an asset response that stops before its body ends",
			payload: func(t *testing.T) []byte { return []byte("expected binary") },
			serve: func(w http.ResponseWriter, _ []byte) {
				w.Header().Set("Content-Length", "4096")
				_, _ = fmt.Fprint(w, "partial response")
			},
			want: "unexpected EOF",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			payload := row.payload(t)
			api := suServe(t, func(a *suAPI, w http.ResponseWriter, req *http.Request) {
				if strings.HasPrefix(req.URL.Path, "/dl/") || strings.HasPrefix(req.URL.Path, "/assets/") {
					if row.serve != nil {
						row.serve(w, payload)
						return
					}
					w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
					_, _ = w.Write(payload)
					return
				}
				release := suReleaseJSON(a.base, suNew, payload)
				if row.digest != "" {
					release["assets"].([]any)[0].(map[string]any)["digest"] = row.digest
				}
				if row.noNotes {
					delete(release, "body")
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode([]any{release})
			})
			r := harness.New(t)
			exe := suExe(t, suOld)

			res := r.CommandBin(exe, "self-update", "--api-url", api.base, "--owner", "o", "--repo", "r")
			assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
			assert.Contains(t, res.Stdout+res.Stderr, row.want)
			assert.Equal(t, suOld, versionOf(t, r, exe), "the working binary is untouched")
			assert.NoFileExists(t, backupPath(exe), "and nothing was moved, so there is no backup")
			entries, err := os.ReadDir(filepath.Dir(exe))
			require.NoError(t, err)
			assert.Len(t, entries, 1, "the refused download is cleaned up: %v", entries)
		})
	}
}

// TestSelfUpdateOverTLS: the release host every real invocation talks to is an
// https one, and every other scenario here reaches its fake over plain HTTP.
// This is the one that puts a certificate in the path: the API is served with a
// leaf issued for localhost by an authority made for this run, so the binary
// has to complete a handshake, present an SNI and verify a chain before it can
// say a word about versions.
//
// Both halves are the same invocation with one variable added or removed, which
// is what makes the pair say something: the trust decision is the only thing
// that differs between an answer and a refusal.
func TestSelfUpdateOverTLS(t *testing.T) {
	r := newSURepo(t)
	ca := r.serveTLS(t, map[string]string{suNew: harness.BuildVersioned(t, suNew)})
	args := []string{"self-update", "--check", "--api-url", r.api, "--owner", "o", "--repo", "r"}

	t.Run("trusting the authority", func(t *testing.T) {
		if runtime.GOOS == "darwin" && !harness.IsTinyGo() {
			// Stock Go on darwin verifies through the platform's own verifier,
			// which reads the system trust store and ignores SSL_CERT_FILE, so
			// there is no way to make a test authority trusted for the child
			// process. The refusal below is the half that still means something
			// here; the trusted path is proven on the platforms that honour the
			// variable. See coverage/tinygo-spike/darwin-selfupdate.log.
			t.Skip("darwin verifies through the platform verifier, which ignores SSL_CERT_FILE")
		}
		res := r.CommandBinEnv(r.exe, []string{"SSL_CERT_FILE=" + ca}, args...)
		assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout, "available dispat "+suNew,
			"the release was read off an https response")
		assert.Equal(t, suOld, r.version(r.exe), "--check over TLS installs nothing either")

		original, err := os.ReadFile(r.exe)
		require.NoError(t, err)
		res = r.CommandBinEnv(r.exe, []string{"SSL_CERT_FILE=" + ca},
			"self-update", "--api-url", r.api, "--owner", "o", "--repo", "r")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Equal(t, suNew, r.version(r.exe), "the TLS download replaces the executable")
		backup, err := os.ReadFile(r.backup)
		require.NoError(t, err)
		assert.Equal(t, sha256.Sum256(original), sha256.Sum256(backup), "the backup is the original executable")
		res = r.CommandBin(r.exe, "self-update", "--rollback", "--api-url", "http://127.0.0.1:1")
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Equal(t, suOld, r.version(r.exe), "rollback works without a release server")
		restored, err := os.ReadFile(r.exe)
		require.NoError(t, err)
		assert.Equal(t, sha256.Sum256(original), sha256.Sum256(restored), "rollback restores the exact original bytes")
	})

	// Verifier-independent, and so this half runs everywhere: with no authority
	// named, nothing in any trust store signed this leaf.
	t.Run("without the authority", func(t *testing.T) {
		res := r.CommandBinEnv(r.exe, nil, args...)
		assert.NotEqual(t, 0, res.Code, "an unverifiable host is not an update")
		assert.Contains(t, strings.ToLower(res.Stdout+res.Stderr), "certificate",
			"and the refusal says what could not be trusted")
		assert.Equal(t, suOld, r.version(r.exe))
	})
}

// TestSelfUpdateWithNothingForThisPlatform: a release cut before a platform
// joined the build matrix has no binary to offer it, and the refusal names
// what it does have rather than leaving the reader guessing.
func TestSelfUpdateWithNothingForThisPlatform(t *testing.T) {
	r := newSURepo(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"tag_name": "services/dispat/v" + suNew, "draft": false, "prerelease": false,
			"assets": []map[string]any{{"name": "dispat-plan9-386", "size": 1,
				"browser_download_url": "http://example.invalid/x"}},
		}})
	}))
	defer srv.Close()

	res := r.CommandBin(r.exe, "self-update", "--api-url", srv.URL, "--owner", "o", "--repo", "r")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, assetName(), "it says which binary it wanted")
	assert.Contains(t, res.Stdout, "dispat-plan9-386", "and which ones exist")
	assert.Equal(t, suOld, r.version(r.exe))
}

// TestSelfUpdateAndPrereleases: by default a release candidate is not an
// update, because a stable line should stay a stable line without anyone
// asking. --prerelease is how someone opts into the candidates, and --force
// is how they get back off that line.
func TestSelfUpdateAndPrereleases(t *testing.T) {
	const candidate = "1.2.0-rc.1"
	r := newSURepo(t)
	r.serve(t, map[string]string{
		suNew:     harness.BuildVersioned(t, suNew),
		candidate: harness.BuildVersioned(t, candidate),
	})

	require.Equal(t, 0, r.update().Code)
	assert.Equal(t, suNew, r.version(r.exe), "the candidate is passed over")

	res := r.update("--prerelease")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, candidate, r.version(r.exe), "asked for, it is installed")

	// Off the candidate line again: the stable is older, so only --force
	// reaches it.
	res = r.update()
	require.Equal(t, 0, res.Code)
	assert.Contains(t, res.Stdout, "already the latest release")
	assert.Equal(t, candidate, r.version(r.exe), "nothing downgrades on its own")

	res = r.update("--force")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Equal(t, suNew, r.version(r.exe), "--force is the way back to the stable line")
}

// TestSelfUpdateWithoutAStableRelease: the state dispat's own repository is in
// before 1.0.0, where every tag is a candidate. Saying "no matching release"
// and naming the flag that would find one beats saying "you are up to date".
func TestSelfUpdateWithoutAStableRelease(t *testing.T) {
	r := newSURepo(t)
	r.serve(t, map[string]string{"1.2.0-rc.1": harness.BuildVersioned(t, "1.2.0-rc.1")})

	res := r.update("--check")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "no matching release")
	assert.Contains(t, res.Stdout, "--prerelease")
	assert.Equal(t, suOld, r.version(r.exe))
}

// TestSelfUpdateBackupExpiresOnItsOwn: the copy is kept for a week and then
// removed by whatever dispat command runs next. Nothing has to be cleaned up
// by hand, and nothing else in the directory is ever touched.
func TestSelfUpdateBackupExpiresOnItsOwn(t *testing.T) {
	r := newSURepo(t)
	require.Equal(t, 0, r.update().Code)
	require.FileExists(t, r.backup)

	sixDays := time.Now().Add(-6 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(r.backup, sixDays, sixDays))
	require.Equal(t, 0, r.CommandBin(r.exe, "--version").Code)
	assert.FileExists(t, r.backup, "inside the week it stays, whatever runs")

	eightDays := time.Now().Add(-8 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(r.backup, eightDays, eightDays))
	require.Equal(t, 0, r.CommandBin(r.exe, "--version").Code)
	assert.NoFileExists(t, r.backup, "past the week the next command clears it")
	assert.Equal(t, suNew, r.version(r.exe), "and only the backup is ever removed")

	// With the backup gone there is nothing to roll back to, and the refusal
	// says how to get an old version anyway.
	res := r.CommandBin(r.exe, "self-update", "--rollback")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "--release")
}

// TestSelfUpdateNotice: the notice is the other half of this feature — the
// part that reaches someone who was not thinking about updating at all. It
// rides out on an ordinary command, says nothing when the output is meant for
// a machine, and says nothing when the configuration asked it not to.
func TestSelfUpdateNotice(t *testing.T) {
	r := newSURepo(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.LogFormat = "pretty"
	cfg.UpdateCheck = nil // the default, which is on
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")

	on := []string{"DISPAT_UPDATE_CHECK=1"}
	args := []string{"status", "--api-url", r.api, "--owner", "o", "--repo", "r"}

	res := r.CommandBinEnv(r.exe, on, args...)
	assert.Contains(t, res.Stdout, "a newer stable release is available: "+suNew,
		"an ordinary command carries the news")
	assert.Contains(t, res.Stdout, `run "dispat self-update" to install it`)

	// JSON output is read by something that cannot act on a suggestion, and
	// the suggestion must not turn up inside the stream either.
	res = r.CommandBinEnv(r.exe, on, append(args, "--log-format", "json")...)
	assert.NotContains(t, res.Stdout, "newer stable release")

	// And the configuration can simply say no.
	cfg.UpdateCheck = boolPtr(false)
	r.WriteConfigModel(cfg)
	res = r.CommandBinEnv(r.exe, on, args...)
	assert.NotContains(t, res.Stdout, "newer stable release")
}

func boolPtr(b bool) *bool { return &b }

// TestSelfUpdateCommandWordKeepsItsScript: every command word permanently
// shadows a run script of the same name, which is why the word is
// "self-update" and not "update". A script called self-update is unreachable
// by name, and that has to be a deliberate, tested fact rather than a
// surprise.
func TestSelfUpdateCommandWordKeepsItsScript(t *testing.T) {
	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.Scripts["self-update"] = models.Script{"echo the script ran"}
	r.WriteConfigModel(cfg)
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): first release")

	res := r.Command("self-update", "--check")
	assert.NotContains(t, res.Stdout, "the script ran", "the command word wins")

	res = r.RunScript("self-update", "--since", "all")
	assert.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Contains(t, res.Stdout, "the script ran", "the two-word spelling still reaches it")
}

// TestSelfUpdatePrintsWhatChanged: the question an update raises is "what did I
// just get", and the release body that answers it is already in the response
// that chose the release. What reaches the terminal is the change sections; the
// install commands the same body carries are for the release page, and a reader
// who is running dispat has already installed it.
func TestSelfUpdatePrintsWhatChanged(t *testing.T) {
	r := newSURepo(t)

	res := r.update()
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.Equal(t, suNew, r.version(r.exe), "the binary really was replaced")

	out := res.Stdout
	assertOrderedIn(t, out,
		"installed dispat "+suNew,
		"put it back with",
		"what changed in "+suNew,
		"Features",
		"- read a release's notes after an update",
		"Fixes",
		"- stop a truncated listing failing opaquely",
		"full changelog: ",
	)
	assert.NotContains(t, out, "curl -fsSL", "the install commands are the page's, not the terminal's")
	assert.NotContains(t, out, "Install this version")
	assert.NotContains(t, out, "[Documentation]", "and neither are the footer links")
	assert.Contains(t, out, "/blob/refs/tags/services/dispat/v"+suNew+"/services/dispat/CHANGELOG.md",
		"the changelog is linked at the tag that was installed, so it keeps saying this")
}

// TestSelfUpdateCheckShowsWhatWouldArrive: deciding whether to update is
// exactly when the changelog is worth reading, and --check is the invocation
// that changes nothing while you decide. It still gates: exit 1, and the binary
// on disk is untouched.
func TestSelfUpdateCheckShowsWhatWouldArrive(t *testing.T) {
	r := newSURepo(t)

	res := r.update("--check")
	assert.Equal(t, 1, res.Code, "still a gate")
	assertOrderedIn(t, res.Stdout,
		"available dispat "+suNew,
		"what changed in "+suNew,
		"- read a release's notes after an update",
		"full changelog: ",
		"install it with: dispat self-update",
	)
	assert.NotContains(t, res.Stdout, "curl -fsSL")
	assert.Equal(t, suOld, r.version(r.exe), "and nothing was installed")
	assert.NoFileExists(t, r.backup, "nor was a backup made")
}

// TestSelfUpdateNotesNeverBlockTheUpdate: the notes are a courtesy and the
// binary is the point. A body that is empty, that is nothing but the footer, or
// that is far longer than anything a release carries all end the same way: the
// new binary is in place and the link is there to fall back on.
func TestSelfUpdateNotesNeverBlockTheUpdate(t *testing.T) {
	for name, body := range map[string]string{
		"no body at all":                 "",
		"a footer and nothing else":      "---\n\n[Documentation](https://example.invalid/docs)\n",
		"markup dispat reads nothing in": "<h3>Features</h3><ul><li>streaming</li></ul>",
		"far more than a release carries": "### Features\n\n" +
			strings.Repeat("- a change with a reasonably long description\n", 20000),
	} {
		t.Run(name, func(t *testing.T) {
			r := newSURepo(t)
			r.body = body
			r.serve(t, map[string]string{suNew: harness.BuildVersioned(t, suNew)})

			res := r.update()
			require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
			assert.Equal(t, suNew, r.version(r.exe), "the update is what matters")
			assert.Contains(t, res.Stdout, "installed dispat "+suNew)
			assert.Contains(t, res.Stdout, "full changelog: ",
				"and the link carries the answer whatever the body did")
		})
	}
}

// TestSelfUpdateReadsTheNotesBeforeTheDownload: the notes describe the release
// that was chosen, so they are read off the response that chose it rather than
// from a second call afterwards. The fake records what it was asked for, in
// order, which is the only way to see that from outside the process.
func TestSelfUpdateReadsTheNotesBeforeTheDownload(t *testing.T) {
	r := newSURepo(t)

	res := r.update()
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)

	paths := r.requests()
	require.NotEmpty(t, paths)
	var listed, downloaded int
	for i, path := range paths {
		switch {
		case strings.Contains(path, "/releases"):
			if listed == 0 {
				listed = i + 1
			}
		case strings.HasPrefix(path, "/dl/"):
			downloaded = i + 1
		}
	}
	require.NotZero(t, listed, "the release was looked up")
	require.NotZero(t, downloaded, "and the binary fetched")
	assert.Less(t, listed, downloaded, "the notes arrive with the release, before the binary")
	assert.Equal(t, 1, strings.Count(strings.Join(paths, "\n"), "/dl/"),
		"and the binary is fetched exactly once")
}

// TestSelfUpdateNotesReachTheJSONStream: the report is for a person and the
// event is for the stream CI already ingests. A job that updates dispat can
// post what changed without scraping stdout.
func TestSelfUpdateNotesReachTheJSONStream(t *testing.T) {
	r := newSURepo(t)

	res := r.update("--log-format", "json")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	assert.NotContains(t, res.Stdout, "full changelog:", "the report stays out of the stream")
	for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		var event map[string]any
		assert.NoError(t, json.Unmarshal([]byte(line), &event),
			"every line is an event, so no report text leaked in beside them")
	}

	installed := suEvent(t, res.Stdout, "update installed")
	notes, _ := installed["notes"].(string)
	assert.Contains(t, notes, "what changed in "+suNew)
	assert.Contains(t, notes, "read a release's notes after an update")
	assert.NotContains(t, notes, "curl -fsSL")
	changelog, _ := installed["changelog"].(string)
	assert.Contains(t, changelog, "/blob/refs/tags/services/dispat/v"+suNew+
		"/services/dispat/CHANGELOG.md")
}

// suEvent picks one event out of a JSON run by its message. The stream carries
// more than the answer, so a test that wants one event has to name it.
func suEvent(t *testing.T, stream, message string) map[string]any {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(stream), "\n") {
		var event map[string]any
		if json.Unmarshal([]byte(line), &event) != nil {
			continue
		}
		if event["message"] == message {
			return event
		}
	}
	t.Fatalf("no %q event in:\n%s", message, stream)
	return nil
}

// TestSelfUpdateReadsNotesFromTheCurrentRenderer: the two halves of this
// feature are written years apart. A release body is produced by whichever
// dispat cut the release, and read by whichever dispat is being updated, so a
// change to the renderer is a change to an input the notes parser will meet
// for as long as that release exists.
//
// Every other scenario here hands the fake a body written by hand. This one
// hands it a body this build actually rendered — indented commit bodies, the
// release details, the footer rule — so the shape under test is the shape that
// will be published rather than a fixture somebody remembered to update.
func TestSelfUpdateReadsNotesFromTheCurrentRenderer(t *testing.T) {
	body := renderedReleaseBody(t)
	require.Contains(t, body, "\n  The first paragraph says why it was done.",
		"the renderer indents a commit body under its bullet:\n%s", body)

	r := newSURepo(t)
	r.body = body
	r.serve(t, map[string]string{suNew: harness.BuildVersioned(t, suNew)})

	res := r.update()
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)

	assertOrderedIn(t, res.Stdout,
		"what changed in "+suNew,
		"Features",
		"- add streaming",
		"The first paragraph says why it was done.",
		"The second paragraph says how.",
		"Fixes",
		"- close a leak",
	)
	// The cut still lands on the footer's rule: an indented body must not
	// carry the release details and the links into the terminal with it.
	assert.NotContains(t, res.Stdout, "Questions? open an issue.")
	assert.NotContains(t, res.Stdout, "### Release")
}

// renderedReleaseBody runs one real release into a fake GitHub API and returns
// the body it created: the notes fixture nobody has to keep in step with the
// renderer, because it is the renderer's own output.
func renderedReleaseBody(t *testing.T) string {
	t.Helper()
	srv, bodies := githubFake(t)

	r := harness.New(t)
	cfg := libsConfig(echoBuild, 1)
	cfg.GitHub = &models.GitHubConfig{
		Enabled: models.Bool(true), AllPackages: models.Bool(true),
		Owner: "acme", Repo: "mono", APIURL: srv.URL, TokenEnv: "DISPAT_IT_TOKEN",
		// The footer dispat's own releases carry: the rule first, which is
		// where the notes parser cuts.
		EntryFormatConfig: models.EntryFormatConfig{
			Footer: recordLines("---", "", "Questions? open an issue."),
		},
	}
	r.WriteConfigModel(cfg)
	t.Setenv("DISPAT_IT_TOKEN", "tkn")
	r.SeedPackage("packages", "core")
	r.Commit("feat(core): add streaming\n\n" +
		"The first paragraph says why it was done.\n\n" +
		"The second paragraph says how.\n---\nfix(core): close a leak")
	r.ReleaseOK()

	return bodyFor(t, bodies(), "core@0.1.0")
}

// TestSelfUpdateVersionReportsTheCheckOutcome: `--version` is the one
// invocation that states the check's answer either way. Behind, it is the
// ordinary notice; current, it says so, which is what makes `dispat --version`
// an answer to "am I up to date" rather than only to "what am I running".
func TestSelfUpdateVersionReportsTheCheckOutcome(t *testing.T) {
	r := newSURepo(t)
	on := []string{"DISPAT_UPDATE_CHECK=1"}
	args := []string{"--version", "--api-url", r.api, "--owner", "o", "--repo", "r"}

	res := r.CommandBinEnv(r.exe, on, args...)
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "dispat "+suOld, "the version line is still the point")
	assert.Contains(t, res.Stdout, "a newer stable release is available: "+suNew)

	// The same command on the release the fake publishes: nothing to install,
	// and the line says that rather than saying nothing.
	current := suExe(t, suNew)
	res = r.CommandBinEnv(current, on, args...)
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "this is the latest stable release")
	assert.NotContains(t, res.Stdout, "a newer stable release is available")
}

// TestSelfUpdateWalksAPaginatedListing: a repository with more releases than
// one page holds still answers "which version is current", and the Link header
// is how the walk continues. The next page's address is the server's own text,
// so one that leaves the configured host ends the listing instead of carrying
// the operator's token somewhere nobody configured.
func TestSelfUpdateWalksAPaginatedListing(t *testing.T) {
	payload := suPayload(t, suNew)

	t.Run("the release on the second page is found", func(t *testing.T) {
		api := suServe(t, func(a *suAPI, w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if req.URL.Query().Get("page") == "2" {
				_ = json.NewEncoder(w).Encode([]any{suReleaseJSON(a.base, suNew, payload)})
				return
			}
			w.Header().Set("Link", `<`+a.base+`/repos/o/r/releases?per_page=100&page=2>; rel="prev", `+
				`<`+a.base+`/repos/o/r/releases?per_page=100&page=2>; rel="next"`)
			_ = json.NewEncoder(w).Encode([]any{suForeignRelease})
		})
		r := harness.New(t)
		exe := suExe(t, suOld)

		res := r.CommandBin(exe, "self-update", "--check", "--api-url", api.base, "--owner", "o", "--repo", "r")
		assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout, "available dispat "+suNew)
		assert.Contains(t, strings.Join(api.requests(), "\n"), "page=2",
			"the Link header is what reached the page the release is on")
	})

	t.Run("a next page on another host ends the listing", func(t *testing.T) {
		api := suServe(t, func(a *suAPI, w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Link", `<http://127.0.0.1:1/repos/o/r/releases?per_page=100&page=2>; rel="next"`)
			_ = json.NewEncoder(w).Encode([]any{suForeignRelease})
		})
		r := harness.New(t)
		exe := suExe(t, suOld)

		res := r.CommandBin(exe, "self-update", "--check", "--api-url", api.base, "--owner", "o", "--repo", "r")
		assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
		out := res.Stdout + res.Stderr
		assert.Contains(t, out, "leaves the configured API host")
		assert.Contains(t, out, "no matching release")
		for _, path := range api.requests() {
			assert.NotContains(t, path, "page=2", "the foreign page was never requested")
		}
	})
}

// TestSelfUpdateFallsBackToThePublicDownloadURL: a credential that reads the
// listing and not the assets is a real shape — a fine-grained token, a proxy
// in front of the API — and it used to be an install that simply worked. The
// endpoint's refusal is said out loud, the public address is tried once with
// no credential, and the staged file starts empty again so the refusal's own
// body cannot end up in the installed binary.
func TestSelfUpdateFallsBackToThePublicDownloadURL(t *testing.T) {
	payload := suPayload(t, suNew)
	const refusal = `{"message":"Resource not accessible by personal access token"}`
	api := suServe(t, func(a *suAPI, w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.HasPrefix(req.URL.Path, "/assets/"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprint(w, refusal)
		case strings.HasPrefix(req.URL.Path, "/dl/"):
			w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
			_, _ = w.Write(payload)
		default:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]any{suReleaseJSON(a.base, suNew, payload)})
		}
	})
	r := harness.New(t)
	exe := suExe(t, suOld)

	res := r.CommandBinEnv(exe, []string{"GITHUB_TOKEN=sesame"},
		"self-update", "--api-url", api.base, "--owner", "o", "--repo", "r")
	require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "installed dispat "+suNew)
	assert.Contains(t, res.Stdout+res.Stderr, "trying the public download URL")
	assert.Equal(t, suNew, versionOf(t, r, exe), "the bytes that landed are the release's")

	paths := strings.Join(api.requests(), "\n")
	assert.Contains(t, paths, "/assets/", "the endpoint was tried first")
	assert.Contains(t, paths, "/dl/", "and the public address second")
}

// TestSelfUpdateNotesAreSafeToPrint: the notes are read out of somebody else's
// markdown, so what a terminal would act on is taken out rather than printed —
// whole sequences, not just the escape that opens them, because the tail of one
// is visible debris. An overlong line is cut on a rune boundary and a body with
// more in it than a summary holds says so and points at the changelog.
func TestSelfUpdateNotesAreSafeToPrint(t *testing.T) {
	r := newSURepo(t)
	r.body = hostileNotes
	r.serve(t, map[string]string{suNew: harness.BuildVersioned(t, suNew)})

	res := r.update("--check")
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "what changed in "+suNew)
	assert.Contains(t, res.Stdout, "Features", "the heading survives its colouring")
	assert.Contains(t, res.Stdout, "... the notes go on",
		"a body past the summary's bounds points at the changelog instead")

	for _, debris := range []string{"[1m", "[0m", "]0;", "titled", "38;2;255", "never.printed", "\x1b"} {
		assert.NotContains(t, res.Stdout, debris, "a terminal must not be handed %q", debris)
	}
	assert.Contains(t, res.Stdout, "a bullet with a two byte sequence",
		"a two byte sequence takes only itself")
	assert.Contains(t, res.Stdout, "a bullet opening on a bell and a delete",
		"bare control bytes are dropped and the text stays")
	assert.Contains(t, res.Stdout, " ...", "the overlong line is cut rather than printed whole")
	assert.NotContains(t, res.Stdout, "curl -fsSL", "and the footer is still not notes")
	assert.Equal(t, suOld, versionOf(t, r.Repo, r.exe), "--check still installs nothing")
}

// TestSelfUpdateRollbackNeedsAFolderItCanWriteTo: a rollback is three renames
// in the binary's own folder, so a folder that will not take a file is refused
// before anything moves, naming what the reader has to change. The binary that
// was running is still the one that runs.
func TestSelfUpdateRollbackNeedsAFolderItCanWriteTo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("folder permissions do not gate a rename on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes into a folder whatever its mode says")
	}
	r := newSURepo(t)
	require.Equal(t, 0, r.update().Code, "the update that leaves a backup to roll back to")
	require.Equal(t, suNew, versionOf(t, r.Repo, r.exe))

	dir := filepath.Dir(r.exe)
	require.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	res := r.CommandBin(r.exe, "self-update", "--rollback")
	assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
	assert.Contains(t, res.Stdout+res.Stderr, "rights to replace")
	assert.Equal(t, suNew, versionOf(t, r.Repo, r.exe), "and nothing was rotated")
}

// TestSelfUpdateReadsOnlyTheReleasesItCanInstall: the listing of a monorepo
// carries other modules' releases, drafts nobody published and tags that are
// not versions at all, and each is passed over for its own reason rather than
// failing the check. Past a point a listing stops being an answer to "which
// version is current" and is refused by size, before it is parsed.
func TestSelfUpdateReadsOnlyTheReleasesItCanInstall(t *testing.T) {
	payload := suPayload(t, suNew)

	t.Run("a draft and a tag with no version in it are passed over", func(t *testing.T) {
		api := suServe(t, func(a *suAPI, w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]any{
				// Higher than anything published, and a draft: a release
				// nobody can install yet.
				map[string]any{"tag_name": "services/dispat/v9.9.9", "draft": true,
					"prerelease": false, "assets": []any{}},
				// dispat's own prefix over something that is not a version.
				map[string]any{"tag_name": "services/dispat/vnightly", "draft": false,
					"prerelease": false, "assets": []any{}},
				suForeignRelease,
				suReleaseJSON(a.base, suNew, payload),
			})
		})
		r := harness.New(t)
		exe := suExe(t, suOld)

		res := r.CommandBin(exe, "self-update", "--check", "--log-level", "debug",
			"--api-url", api.base, "--owner", "o", "--repo", "r")
		assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		assert.Contains(t, res.Stdout, "available dispat "+suNew,
			"the draft is not a release this can install")
		assert.NotContains(t, res.Stdout, "9.9.9")
		assert.Contains(t, res.Stdout, "tag carries no version",
			"and the tag that is not a version says why it was passed over")
	})

	t.Run("a listing past the cap is refused by size", func(t *testing.T) {
		api := suServe(t, func(a *suAPI, w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			// A well-formed listing whose one release carries a body no
			// release has: read far enough to know it is over the bound, and
			// no further.
			_, _ = w.Write([]byte(`[{"tag_name":"services/dispat/v1.1.0","draft":false,"body":"`))
			_, _ = w.Write(bytes.Repeat([]byte("x"), 9<<20))
			_, _ = w.Write([]byte(`","assets":[]}]`))
		})
		r := harness.New(t)
		exe := suExe(t, suOld)

		res := r.CommandBin(exe, "self-update", "--check",
			"--api-url", api.base, "--owner", "o", "--repo", "r")
		assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
		assert.Contains(t, res.Stdout+res.Stderr, "larger than",
			"the bound is named rather than the parse failing further down")
	})
}

// TestSelfUpdateRollbackChecksTheBackupFirst: a rollback is only worth
// doing if the file it would put back is a working dispat, and finding out
// otherwise afterwards means finding out with no dispat at all. So the backup
// is run first: one that is not a program refuses the rollback outright, and
// one that runs without saying which version it is rolls back with the
// version left unstated rather than guessed.
func TestSelfUpdateRollbackChecksTheBackupFirst(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in backups are shell scripts")
	}
	updated := func(t *testing.T) *suRepo {
		t.Helper()
		r := newSURepo(t)
		require.Equal(t, 0, r.update().Code, "the update that leaves a backup behind")
		require.Equal(t, suNew, r.version(r.exe))
		return r
	}

	t.Run("a backup that is not a program refuses the rollback", func(t *testing.T) {
		r := updated(t)
		require.NoError(t, os.WriteFile(r.backup, []byte("this was never a binary\n"), 0o755))

		res := r.CommandBin(r.exe, "self-update", "--rollback")
		assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
		assert.Contains(t, res.Stdout+res.Stderr, "does not run")
		assert.Equal(t, suNew, r.version(r.exe), "and the working binary is where it was")
	})

	t.Run("a backup that says nothing rolls back with the version unstated", func(t *testing.T) {
		r := updated(t)
		require.NoError(t, os.WriteFile(r.backup, []byte("#!/bin/sh\nexit 0\n"), 0o755))

		res := r.CommandBin(r.exe, "self-update", "--check", "--rollback")
		assert.Equal(t, 1, res.Code, "there is still something to restore; stdout:\n%s", res.Stdout)
		assert.NotContains(t, res.Stdout, "is dispat "+suOld,
			"a version nothing stated is not one to print")
	})
}

// TestSelfUpdateRefusesAnAnswerThatIsNotARelease: the update check
// reads somebody else's server, so each way an answer can fail to be a release
// is refused on its own terms: a listing that is not a listing, a version
// that is not a version, and a release whose body is not a release.
func TestSelfUpdateRefusesAnAnswerThatIsNotARelease(t *testing.T) {
	t.Run("a listing that is not JSON", func(t *testing.T) {
		api := suServe(t, func(a *suAPI, w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`<html>a proxy sign-in page</html>`))
		})
		r := harness.New(t)
		exe := suExe(t, suOld)

		res := r.CommandBin(exe, "self-update", "--check", "--api-url", api.base, "--owner", "o", "--repo", "r")
		assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
		assert.Contains(t, res.Stdout+res.Stderr, "listing releases")
	})

	t.Run("a release name that is not a version", func(t *testing.T) {
		r := newSURepo(t)
		res := r.update("--release", "nightly")
		assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
		assert.Contains(t, res.Stdout+res.Stderr, "is not a version")
		assert.Equal(t, suOld, r.version(r.exe))
	})

	t.Run("a release whose body is not a release", func(t *testing.T) {
		api := suServe(t, func(a *suAPI, w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html>a proxy sign-in page</html>`))
		})
		r := harness.New(t)
		exe := suExe(t, suOld)

		res := r.CommandBin(exe, "self-update", "--release", "1.2.3",
			"--api-url", api.base, "--owner", "o", "--repo", "r")
		assert.NotEqual(t, 0, res.Code, "stdout:\n%s", res.Stdout)
		assert.Contains(t, res.Stdout+res.Stderr, "looking up")
	})
}

// TestSelfUpdateReadsItsOwnRepositoryByDefault: --owner and --repo
// exist for a fork, and leaving them out has to reach dispat's own repository
// rather than nothing. Pointed at a fake that publishes under those defaults,
// the plain command finds the release, which is what says the defaults are
// what a real run uses.
func TestSelfUpdateReadsItsOwnRepositoryByDefault(t *testing.T) {
	payload := suPayload(t, suNew)
	var asked []string
	api := suServe(t, func(a *suAPI, w http.ResponseWriter, req *http.Request) {
		asked = append(asked, req.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]any{suReleaseJSON(a.base, suNew, payload)})
	})
	r := harness.New(t)
	exe := suExe(t, suOld)

	res := r.CommandBin(exe, "self-update", "--check", "--api-url", api.base)
	assert.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	assert.Contains(t, res.Stdout, "available dispat "+suNew)
	assert.Contains(t, strings.Join(asked, "\n"), "/repos/yohimik/dispat/releases",
		"the defaults are dispat's own repository, not an empty pair")
}

// TestSelfUpdateReportsItselfAsJSON: the update check is a CI gate as
// often as a person's question, so each of its outcomes is one structured line
// carrying what the gate decides on — the version running, the version
// available, and whether the same invocation without --check would change the
// binary.
func TestSelfUpdateReportsItselfAsJSON(t *testing.T) {
	jsonArgs := func(args ...string) []string {
		return append([]string{"--log-format", "json"}, args...)
	}

	t.Run("a check that has something to install", func(t *testing.T) {
		r := newSURepo(t)
		res := r.update(jsonArgs("--check")...)
		assert.Equal(t, 1, res.Code, "the gate fails when there is something to do")
		line := jsonLine(t, res, "update check")
		assert.Equal(t, suOld, line.Str("version"))
		assert.Equal(t, suNew, line.Str("latest"))
		assert.Equal(t, true, line["pending"])
	})

	t.Run("an update with nothing to install", func(t *testing.T) {
		r := newSURepo(t)
		require.Equal(t, 0, r.update().Code, "the update that brings it up to date")
		require.Equal(t, suNew, r.version(r.exe))

		// The same command again: already current, and the JSON line says so
		// rather than the command saying nothing.
		res := r.CommandBin(r.exe, append([]string{"self-update", "--api-url", r.api,
			"--owner", "o", "--repo", "r"}, jsonArgs()...)...)
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		line := jsonLine(t, res, "already on the latest release")
		assert.Equal(t, suNew, line.Str("version"))
		assert.Equal(t, suNew, line.Str("latest"))
	})

	t.Run("a rollback with nothing to put back", func(t *testing.T) {
		r := newSURepo(t)
		res := r.CommandBin(r.exe, append([]string{"self-update", "--rollback", "--check"}, jsonArgs()...)...)
		require.Equal(t, 0, res.Code, "nothing to do is not a failure; stdout:\n%s", res.Stdout)
		line := jsonLine(t, res, "no backup to roll back to")
		assert.Equal(t, false, line["pending"])

		// And the sentence a person reads, for the same state.
		res = r.CommandBin(r.exe, "self-update", "--rollback", "--check")
		require.Equal(t, 0, res.Code, "stdout:\n%s", res.Stdout)
		assert.Contains(t, res.Stdout, "there is no backup to roll back to")
	})

	t.Run("a rollback with a backup to put back", func(t *testing.T) {
		r := newSURepo(t)
		require.Equal(t, 0, r.update().Code)
		require.Equal(t, suNew, r.version(r.exe))

		res := r.CommandBin(r.exe, append([]string{"self-update", "--rollback", "--check"}, jsonArgs()...)...)
		assert.Equal(t, 1, res.Code, "there is something to restore")
		line := jsonLine(t, res, "a backup is available")
		assert.Equal(t, suOld, line.Str("backup"))
		assert.Equal(t, true, line["pending"])

		res = r.CommandBin(r.exe, append([]string{"self-update", "--rollback"}, jsonArgs()...)...)
		require.Equal(t, 0, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
		line = jsonLine(t, res, "rolled back")
		assert.Equal(t, suNew, line.Str("from"))
		assert.Equal(t, suOld, line.Str("version"))
		assert.Equal(t, filepath.Base(r.exe), filepath.Base(line.Str("path")))
		assert.Equal(t, suOld, r.version(r.exe), "and the file itself is the one the line named")
	})
}

// TestSelfUpdateCheckCarriesTheNotesAsFields: a check that found
// something to install carries the release's own notes, so a job that opens a
// pull request with them does not have to fetch the release a second time to
// read what changed.
func TestSelfUpdateCheckCarriesTheNotesAsFields(t *testing.T) {
	payload := suPayload(t, suNew)
	api := suServe(t, func(a *suAPI, w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]any{suReleaseJSON(a.base, suNew, payload)})
	})
	r := harness.New(t)
	exe := suExe(t, suOld)

	res := r.CommandBin(exe, "self-update", "--check", "--log-format", "json",
		"--api-url", api.base, "--owner", "o", "--repo", "r")
	require.Equal(t, 1, res.Code, "stdout:\n%s\nstderr:\n%s", res.Stdout, res.Stderr)
	line := jsonLine(t, res, "update check")
	assert.Equal(t, "services/dispat/v"+suNew, line.Str("tag"))

	rendered := strings.Join([]string{line.Str("notes"), line.Str("changelog"), res.Stdout}, "\n")
	assert.Contains(t, rendered, "Features", "the notes reach the field, not only the terminal")
	assert.NotContains(t, rendered, "curl -fsSL",
		"and the install footer is no more notes here than it is on a terminal")
}

// suAPI is a releases API a scenario writes itself. The shared fake in
// selfupdate_test.go answers one shape well; these scenarios are about the
// other shapes, so the handler is the scenario's and this type only carries
// what every handler needs: the address the server ended up on, which the URLs
// inside a release have to name, and the requests it answered.
type suAPI struct {
	base string

	mu   sync.Mutex
	hits []string
}

// requests is the paths the fake has answered so far, query strings included,
// which is how a test sees that a Link header was followed.
func (a *suAPI) requests() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.hits...)
}

// suServe stands up the fake over plain HTTP and returns it with base
// filled in, so a handler may build absolute URLs for its own server.
func suServe(t *testing.T, handle func(a *suAPI, w http.ResponseWriter, req *http.Request)) *suAPI {
	t.Helper()
	api := &suAPI{}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		api.mu.Lock()
		api.hits = append(api.hits, req.URL.RequestURI())
		api.mu.Unlock()
		handle(api, w, req)
	}))
	srv.Start()
	t.Cleanup(srv.Close)
	api.base = "http://" + srv.Listener.Addr().String()
	return api
}

// suReleaseJSON renders one release exactly as the API describes one: both
// asset addresses, the published size and the digest of the bytes the fake
// will actually serve.
func suReleaseJSON(base, version string, payload []byte) map[string]any {
	sum := sha256.Sum256(payload)
	return map[string]any{
		"tag_name":   "services/dispat/v" + version,
		"draft":      false,
		"prerelease": strings.Contains(version, "-"),
		"body":       suBody,
		"html_url":   base + "/o/r/releases/tag/services%2Fdispat%2Fv" + version,
		"assets": []any{map[string]any{
			"name":                 assetName(),
			"size":                 len(payload),
			"browser_download_url": base + "/dl/" + version,
			"url":                  base + "/assets/" + version,
			"digest":               "sha256:" + hex.EncodeToString(sum[:]),
		}},
	}
}

// suForeignRelease is another module's release: what a monorepo listing is
// mostly made of, and what a page carrying nothing for dispat looks like.
var suForeignRelease = map[string]any{
	"tag_name": "pkg/ccme/v9.9.9", "draft": false, "prerelease": false, "assets": []any{},
}

// suPayload is the bytes a release of the given version hands out.
func suPayload(t *testing.T, version string) []byte {
	t.Helper()
	data, err := os.ReadFile(harness.BuildVersioned(t, version))
	require.NoError(t, err)
	return data
}

// suExe copies a version-stamped binary somewhere a self-update may
// replace it, which is the fixture of every scenario here: the suite's shared
// build must never be the file under test.
func suExe(t *testing.T, version string) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "dispat"+exeSuffix())
	copyFile(t, harness.BuildVersioned(t, version), exe)
	return exe
}

// versionOf asks a binary which version it is.
func versionOf(t *testing.T, r *harness.Repo, path string) string {
	t.Helper()
	res := r.CommandBin(path, "--version")
	require.Equal(t, 0, res.Code, "stderr:\n%s", res.Stderr)
	for _, line := range strings.Split(res.Stdout, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "dispat "); ok {
			v, _, _ := strings.Cut(rest, " (")
			return v
		}
	}
	t.Fatalf("no version line in:\n%s", res.Stdout)
	return ""
}

// hostileNotes is a release body written to act on a terminal rather than
// to be read by one: colour and title sequences, sequences that never finish,
// bare control bytes, a line far past what one line of notes may be, and more
// bullets than a summary prints. Whoever publishes a release writes the body,
// so this is less an attacker than a stray sequence, and the answer is the
// same either way — none of it reaches the terminal as itself.
var hostileNotes = "### \x1b[1mFeatures\x1b[0m\n\n" +
	"- a bullet that sets the window title \x1b]0;titled\x07 and carries on\n" +
	"- a bullet ending on a bare escape \x1b\n" +
	"- a bullet whose colour sequence never ends \x1b[38;2;255\n" +
	"- a bullet whose operating system command never ends \x1b]8;;http://never.printed\n" +
	"- a bullet with a two byte sequence \x1bN inside it\n" +
	"- \x07\x7fa bullet opening on a bell and a delete\n" +
	"- " + strings.Repeat("длинная строка про изменение ", 20) + "\n" +
	"\n### Fixes\n\n" + strings.Repeat("- one more fix nobody has room for\n", 60) +
	"\n---\n\n**Install this version:**\n\n```sh\ncurl -fsSL https://never.printed/install.sh | sh\n```\n"
