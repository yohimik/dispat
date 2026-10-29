package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const mailboxProofArtifactBytes = 2 << 20

func TestAuthenticatedMailboxAcceptsLargeChunkedWorkerPush(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	root := t.TempDir()
	origin, checkout := filepath.Join(root, "origin.git"), filepath.Join(root, "checkout")
	git := func(args ...string) []byte {
		t.Helper()
		content, err := exec.CommandContext(ctx, "git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("fixture Git command failed: %s: %v", content, err)
		}
		return content
	}
	git("init", "--bare", "--initial-branch=main", origin)
	git("-C", origin, "config", "http.receivepack", "true")
	git("init", "--initial-branch=main", checkout)
	git("-C", checkout, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "fixture")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	hook := "#!/bin/sh\nLLVM_RECEIVE_HELPER=1 exec " + shellQuote(self) + " -test.run=^TestReceiveHookProcess$\n"
	if err := os.WriteFile(filepath.Join(origin, "hooks/pre-receive"), []byte(hook), 0700); err != nil {
		t.Fatal(err)
	}
	execPath := strings.TrimSpace(string(git("--exec-path")))
	handler, err := newMailboxHandler(root, filepath.Join(execPath, "git-http-backend"), map[string]string{"coordinator": "test-password", "llvm-node-1": "worker-password"})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	chunkedRequests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/origin.git/git-receive-pack" && len(request.TransferEncoding) > 0 && request.TransferEncoding[0] == "chunked" {
			mu.Lock()
			chunkedRequests++
			mu.Unlock()
			t.Logf("actual Git receive-pack request: transferEncoding=%v contentLength=%d", request.TransferEncoding, request.ContentLength)
		}
		handler.ServeHTTP(writer, request)
	}))
	defer server.Close()
	certificate, err := x509.ParseCertificate(server.TLS.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	ca := filepath.Join(root, "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	push := func(role, ref string) ([]byte, error) {
		password := "test-password"
		if role != "coordinator" {
			password = "worker-password"
		}
		args := []string{"-C", checkout, "-c", "http.sslCAInfo=" + ca, "-c", "http.version=HTTP/1.1", "-c", "http.postBuffer=1048576", "-c", "http.extraHeader=Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(role+":"+password)), "push", server.URL + "/origin.git", "HEAD:" + ref}
		return exec.CommandContext(ctx, "git", args...).CombinedOutput()
	}
	if content, err := push("coordinator", "refs/heads/main"); err != nil {
		t.Fatalf("small initial push failed: %s: %v", content, err)
	}
	payload := make([]byte, mailboxProofArtifactBytes)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "artifact.bin"), payload, 0600); err != nil {
		t.Fatal(err)
	}
	git("-C", checkout, "add", "artifact.bin")
	git("-C", checkout, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-m", "incompressible artifact")
	nodeHash := sha256.Sum256([]byte("llvm-node-1"))
	ref := "refs/heads/dispat-output-" + strings.Repeat("a", 32) + "-" + strings.Repeat("b", 32) + "-" + hex.EncodeToString(nodeHash[:16]) + "-" + strings.Repeat("c", 32)
	content, pushErr := push("llvm-node-1", ref)
	mu.Lock()
	chunked := chunkedRequests
	mu.Unlock()
	if chunked == 0 {
		t.Fatal("real Git did not exercise chunked receive-pack")
	}
	if pushErr != nil {
		t.Fatalf("large authenticated worker push failed: %s: %v", content, pushErr)
	}
	want := sha256.Sum256(payload)
	actual := sha256.Sum256(git("-C", origin, "show", ref+":artifact.bin"))
	if actual != want {
		t.Fatalf("returned artifact SHA256 differs: got %x want %x", actual, want)
	}
	t.Logf("exact remote artifact verified: bytes=%d sha256=%x", len(payload), actual)
	git("-C", checkout, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "forbidden rewrite")
	if content, err := push("llvm-node-1", ref); err == nil {
		t.Fatalf("worker rewrote a create-only output ref: %s", content)
	}
	if content, err := push("llvm-node-1", "refs/heads/main"); err == nil {
		t.Fatalf("worker wrote a canonical ref: %s", content)
	}
	assertMailboxSpools(t, handler, 0)
	t.Log("worker overwrite/canonical-ref writes rejected; private request files removed")
}

func testMailboxHandler(t *testing.T) mailboxHandler {
	t.Helper()
	handler, err := newMailboxHandler(t.TempDir(), "/must-not-execute", map[string]string{"coordinator": "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func assertMailboxSpools(t *testing.T, handler mailboxHandler, want int) {
	t.Helper()
	entries, err := os.ReadDir(handler.bodyDirectory)
	if err != nil || len(entries) != want {
		t.Fatalf("private request files: count=%d want=%d error=%v", len(entries), want, err)
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("request file is not private: %v, %v", info, err)
		}
	}
}

func TestMailboxBodyBoundsAndExactForwarding(t *testing.T) {
	for _, test := range []struct {
		name       string
		payload    string
		length     int64
		wantStatus int
	}{
		{name: "chunked", payload: "\x00exact\xff", length: -1, wantStatus: http.StatusNoContent},
		{name: "known", payload: "\x00exact\xff", length: 7, wantStatus: http.StatusNoContent},
		{name: "empty chunked", length: -1, wantStatus: http.StatusNoContent},
		{name: "known oversized", payload: "oversized", length: 9, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "chunked oversized", payload: "oversized", length: -1, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "short body", payload: "short", length: 7, wantStatus: http.StatusBadRequest},
		{name: "long body", payload: "longer", length: 4, wantStatus: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := testMailboxHandler(t)
			handler.bodyLimit = 8
			request := httptest.NewRequest(http.MethodPost, "/origin.git/git-receive-pack", strings.NewReader(test.payload))
			request.ContentLength = test.length
			request.Header.Set("Content-Type", "application/x-git-receive-pack-request")
			if test.length < 0 {
				request.TransferEncoding = []string{"chunked"}
				request.Header.Set("Transfer-Encoding", "chunked")
			}
			called := false
			backend := http.HandlerFunc(func(writer http.ResponseWriter, forwarded *http.Request) {
				called = true
				content, err := io.ReadAll(forwarded.Body)
				if err != nil || string(content) != test.payload || forwarded.ContentLength != int64(len(test.payload)) || len(forwarded.TransferEncoding) != 0 || forwarded.Header.Get("Transfer-Encoding") != "" {
					t.Fatalf("CGI received changed body/framing: request=%+v content=%q error=%v", forwarded, content, err)
				}
				if forwarded.Header.Get("Content-Type") != request.Header.Get("Content-Type") {
					t.Fatal("Git content type changed")
				}
				assertMailboxSpools(t, handler, 1)
				writer.WriteHeader(http.StatusNoContent)
			})
			response := httptest.NewRecorder()
			handler.serveBody(response, request, backend)
			if response.Code != test.wantStatus || called != (test.wantStatus == http.StatusNoContent) {
				t.Fatalf("status=%d backend called=%v, want status=%d", response.Code, called, test.wantStatus)
			}
			if test.length < 0 && request.Header.Get("Transfer-Encoding") != "chunked" {
				t.Fatal("normalization mutated the original request")
			}
			assertMailboxSpools(t, handler, 0)
		})
	}
}

type mailboxObservedBody struct {
	io.ReadCloser
	started chan struct{}
	once    sync.Once
}

func (body *mailboxObservedBody) Read(target []byte) (int, error) {
	body.once.Do(func() { close(body.started) })
	return body.ReadCloser.Read(target)
}

func TestMailboxAuthenticatesBeforeReadingOrSpooling(t *testing.T) {
	handler := testMailboxHandler(t)
	for _, path := range []string{"/origin.git/git-receive-pack", "/other.git/git-receive-pack"} {
		body := &mailboxObservedBody{ReadCloser: io.NopCloser(strings.NewReader("payload")), started: make(chan struct{})}
		request := httptest.NewRequest(http.MethodPost, path, body)
		if strings.HasPrefix(path, "/other.git/") {
			request.SetBasicAuth("coordinator", "test-password")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized && response.Code != http.StatusNotFound {
			t.Fatalf("unadmitted request returned %d", response.Code)
		}
		select {
		case <-body.started:
			t.Fatal("unadmitted request body was read")
		default:
		}
		assertMailboxSpools(t, handler, 0)
	}
}

func TestMailboxCancellationUnblocksUploadAndRemovesSpool(t *testing.T) {
	handler := testMailboxHandler(t)
	reader, writer := io.Pipe()
	defer writer.Close()
	body := &mailboxObservedBody{ReadCloser: reader, started: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request := httptest.NewRequest(http.MethodPost, "/origin.git/git-receive-pack", body).WithContext(ctx)
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.serveBody(response, request, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("cancelled upload reached Git") }))
		close(done)
	}()
	select {
	case <-body.started:
	case <-time.After(time.Second):
		t.Fatal("upload did not begin")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not unblock upload read")
	}
	if response.Code != http.StatusRequestTimeout {
		t.Fatalf("cancelled upload status=%d", response.Code)
	}
	assertMailboxSpools(t, handler, 0)
}

func TestMailboxQuotaLastsThroughCGIAndCancellation(t *testing.T) {
	handler := testMailboxHandler(t)
	entered, completed := make(chan struct{}, maximumMailboxBodyCount), make(chan struct{}, maximumMailboxBodyCount)
	release := make(chan struct{})
	defer close(release)
	contexts := make([]context.CancelFunc, 0, maximumMailboxBodyCount)
	for range maximumMailboxBodyCount {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		contexts = append(contexts, cancel)
		request := httptest.NewRequest(http.MethodPost, "/origin.git/git-receive-pack", strings.NewReader("payload")).WithContext(ctx)
		go func() {
			handler.serveBody(httptest.NewRecorder(), request, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				entered <- struct{}{}
				<-release
			}))
			completed <- struct{}{}
		}()
	}
	for range maximumMailboxBodyCount {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("CGI did not receive both allowed requests")
		}
	}
	assertMailboxSpools(t, handler, maximumMailboxBodyCount)
	contexts[0]()
	deadline := time.Now().Add(time.Second)
	for {
		entries, err := os.ReadDir(handler.bodyDirectory)
		if err == nil && len(entries) == maximumMailboxBodyCount-1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancelled CGI request retained its temporary pathname")
		}
		time.Sleep(time.Millisecond)
	}
	if len(handler.bodySlots) != maximumMailboxBodyCount {
		t.Fatal("cancellation released quota while CGI could still hold the file")
	}
	ctx, cancel := context.WithCancel(t.Context())
	request := httptest.NewRequest(http.MethodPost, "/origin.git/git-receive-pack", strings.NewReader("payload")).WithContext(ctx)
	cancel()
	response := httptest.NewRecorder()
	handler.serveBody(response, request, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("third concurrent request reached CGI") }))
	if response.Code != http.StatusRequestTimeout {
		t.Fatalf("waiting request ignored cancellation: %d", response.Code)
	}
	// Let both handlers finish before TempDir cleanup.
	for range maximumMailboxBodyCount {
		release <- struct{}{}
	}
	for range maximumMailboxBodyCount {
		select {
		case <-completed:
		case <-time.After(time.Second):
			t.Fatal("CGI completion did not release request resources")
		}
	}
	assertMailboxSpools(t, handler, 0)
	if len(handler.bodySlots) != 0 {
		t.Fatal("completed CGI retained body quota")
	}
}

type mailboxFailedBody struct{}

func (mailboxFailedBody) Read([]byte) (int, error) { return 0, errors.New("broken upload") }
func (mailboxFailedBody) Close() error             { return nil }

func TestMailboxReadFailureRemovesSpool(t *testing.T) {
	handler := testMailboxHandler(t)
	request := httptest.NewRequest(http.MethodPost, "/origin.git/git-receive-pack", mailboxFailedBody{})
	response := httptest.NewRecorder()
	handler.serveBody(response, request, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("broken upload reached Git") }))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("broken upload returned %d", response.Code)
	}
	assertMailboxSpools(t, handler, 0)
}
