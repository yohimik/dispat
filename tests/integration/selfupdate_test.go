package integration

// Area 20: dispat replacing its own binary, through the compiled binary and
// against a fake releases API. Everything here is real: two binaries built at
// two versions, one downloaded over HTTP, checked, and moved into the other's
// place, then run again to see which one answers. Nothing else in the suite
// can witness that, because nothing else in the suite replaces the file it is
// running from.

import (
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
	t.Helper()
	r := &suRepo{Repo: harness.New(t), assets: map[string][]byte{}, body: suBody}
	r.exe = filepath.Join(t.TempDir(), "dispat"+exeSuffix())
	copyFile(t, harness.BuildVersioned(t, suOld), r.exe)
	r.backup = backupPath(r.exe)
	r.serve(t, map[string]string{suNew: harness.BuildVersioned(t, suNew)})
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
// and the only binary the user has, so a release whose checksum does not
// describe what arrives is refused with the working binary still in place.
func TestSelfUpdateRefusesWhatItCannotTrust(t *testing.T) {
	r := newSURepo(t)

	// A release that advertises the right size and a checksum of something
	// else: what a corrupted or substituted download looks like.
	var base string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		data := r.assets[suNew]
		if strings.HasPrefix(req.URL.Path, "/dl/") {
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			_, _ = w.Write(data)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"tag_name": "services/dispat/v" + suNew, "draft": false, "prerelease": false,
			"assets": []map[string]any{{
				"name": assetName(), "size": len(data),
				"browser_download_url": base + "/dl/x",
				"digest":               "sha256:" + strings.Repeat("00", 32),
			}},
		}})
	}))
	base = "http://" + srv.Listener.Addr().String()
	srv.Start()
	defer srv.Close()

	res := r.CommandBin(r.exe, "self-update", "--api-url", base, "--owner", "o", "--repo", "r")
	assert.Equal(t, 1, res.Code)
	assert.Contains(t, res.Stdout, "hashes to")
	assert.Equal(t, suOld, r.version(r.exe), "the working binary is untouched")
	assert.NoFileExists(t, r.backup, "and nothing was moved, so there is no backup")

	entries, err := os.ReadDir(filepath.Dir(r.exe))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "the refused download is cleaned up: %v", entries)
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
		if runtime.GOOS == "darwin" {
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
