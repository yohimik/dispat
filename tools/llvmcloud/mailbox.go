package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/cgi"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maximumMailboxBodyBytes        int64 = 2 << 30
	maximumMailboxBodyCount              = 2
	mailboxNoBodyBytes             int64 = 0
	mailboxBodyStartOffsetBytes    int64 = 0
	mailboxNoTransferEncodingCount       = 0
	mailboxBodyLengthRadix               = 10
	mailboxContentLengthHeader           = "Content-Length"
	mailboxTransferEncodingHeader        = "Transfer-Encoding"
	mailboxBodyDirectoryName             = ".http-requests"
	mailboxBodyFilePattern               = "request-*"
	mailboxBodyTooLargeError             = "Git mailbox request exceeds its body limit"
	mailboxBodyInvalidError              = "Git mailbox request body is incomplete or invalid"
	mailboxBodyCancelledError            = "Git mailbox request was cancelled"
	mailboxBodyStorageError              = "Git mailbox request body could not be retained"
	mailboxBodyCleanupErrorFormat        = "Git mailbox request temporary file cleanup failed: %v"
)

func (d *driver) startServers() (func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	origin := filepath.Join(d.settings.Root, "mailbox/origin.git")
	if _, err := d.runCommand(ctx, invocation{Name: "git", Args: []string{"init", "--bare", "--initial-branch=main", origin}}); err != nil {
		return nil, err
	}
	if _, err := d.runCommand(ctx, invocation{Name: "git", Args: []string{"-C", origin, "config", "http.receivepack", "true"}}); err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	hook := "#!/bin/sh\nexec " + shellQuote(self) + " authorize-receive\n"
	if err := os.WriteFile(filepath.Join(origin, "hooks/pre-receive"), []byte(hook), 0700); err != nil {
		return nil, err
	}
	credentials := map[string]string{}
	for _, name := range []string{"coordinator", "llvm-node-1", "llvm-node-2"} {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, err
		}
		credentials[name] = hex.EncodeToString(secret)
		header := base64.StdEncoding.EncodeToString([]byte(name + ":" + credentials[name]))
		config := "[http \"" + mailboxURL + "\"]\n\tsslCAInfo = /cloud/git-ca.pem\n\textraHeader = Authorization: Basic " + header + "\n[user]\n\tname = LLVM experiment\n\temail = experiments@dispat.dev\n"
		if err := os.WriteFile(filepath.Join(d.settings.Root, "private/"+name+".gitconfig"), []byte(config), privateFileMode); err != nil {
			return nil, err
		}
	}
	certificate, err := d.makeCertificate()
	if err != nil {
		return nil, err
	}
	execPath, err := d.runCommand(ctx, invocation{Name: "git", Args: []string{"--exec-path"}})
	if err != nil {
		return nil, err
	}
	gitHandler, err := newMailboxHandler(filepath.Join(d.settings.Root, "mailbox"), filepath.Join(strings.TrimSpace(string(execPath)), "git-http-backend"), credentials)
	if err != nil {
		return nil, err
	}
	mailbox := &http.Server{Handler: gitHandler, ReadHeaderTimeout: 15 * time.Second, MaxHeaderBytes: 16384}
	listener, err := tls.Listen("tcp", "127.0.0.1:"+mailboxPort, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
	if err != nil {
		return nil, err
	}
	control, err := net.Listen("unix", filepath.Join(d.settings.Root, "control.sock"))
	if err != nil {
		listener.Close()
		return nil, err
	}
	serverCtx, stop := context.WithCancel(context.Background())
	api := &http.Server{Handler: newAdapterHandler(d), ReadHeaderTimeout: 15 * time.Second, BaseContext: func(net.Listener) context.Context { return serverCtx }}
	go mailbox.Serve(listener)
	go api.Serve(control)
	return func() {
		stop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		api.Shutdown(shutdownCtx)
		api.Close()
		mailbox.Close()
	}, nil
}

type mailboxHandler struct {
	root, backend string
	credentials   map[string]string
	bodyDirectory string
	bodyLimit     int64
	bodySlots     chan struct{}
}

func newMailboxHandler(root, backend string, credentials map[string]string) (mailboxHandler, error) {
	handler := mailboxHandler{root: root, backend: backend, credentials: credentials,
		bodyDirectory: filepath.Join(root, mailboxBodyDirectoryName), bodyLimit: maximumMailboxBodyBytes,
		bodySlots: make(chan struct{}, maximumMailboxBodyCount)}
	if err := os.MkdirAll(handler.bodyDirectory, privateDirectoryMode); err != nil {
		return mailboxHandler{}, err
	}
	return handler, nil
}

func (handler mailboxHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	username, password, ok := request.BasicAuth()
	expected, known := handler.credentials[username]
	if !ok || !known || subtle.ConstantTimeCompare([]byte(password), []byte(expected)) != 1 {
		writer.Header().Set("WWW-Authenticate", `Basic realm="isolated LLVM trial"`)
		http.Error(writer, "authentication required", http.StatusUnauthorized)
		return
	}
	if !strings.HasPrefix(request.URL.Path, "/origin.git/") {
		http.NotFound(writer, request)
		return
	}
	backend := cgi.Handler{Path: handler.backend, Root: "/", Env: []string{"GIT_PROJECT_ROOT=" + handler.root, "GIT_HTTP_EXPORT_ALL=1", "REMOTE_USER=" + username, "AUTH_TYPE=Basic"}}
	if request.ContentLength == mailboxNoBodyBytes && len(request.TransferEncoding) == mailboxNoTransferEncodingCount {
		backend.ServeHTTP(writer, request)
		return
	}
	handler.serveBody(writer, request, &backend)
}

// CGI requires a known CONTENT_LENGTH. Keep the quota until Git has consumed
// the file so concurrent completed uploads cannot escape the disk bound.
func (handler mailboxHandler) serveBody(writer http.ResponseWriter, request *http.Request, backend http.Handler) {
	if request.ContentLength > handler.bodyLimit {
		http.Error(writer, mailboxBodyTooLargeError, http.StatusRequestEntityTooLarge)
		return
	}
	select {
	case handler.bodySlots <- struct{}{}:
		defer func() { <-handler.bodySlots }()
	case <-request.Context().Done():
		http.Error(writer, mailboxBodyCancelledError, http.StatusRequestTimeout)
		return
	}
	file, err := os.CreateTemp(handler.bodyDirectory, mailboxBodyFilePattern)
	if err != nil {
		http.Error(writer, mailboxBodyStorageError, http.StatusInternalServerError)
		return
	}
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			if err := errors.Join(file.Close(), os.Remove(file.Name())); err != nil {
				log.Printf(mailboxBodyCleanupErrorFormat, err)
			}
		})
	}
	defer cleanup()
	stopCleanup := context.AfterFunc(request.Context(), func() {
		request.Body.Close()
		cleanup()
	})
	defer stopCleanup()
	length, err := io.Copy(file, http.MaxBytesReader(writer, request.Body, handler.bodyLimit))
	if request.Context().Err() != nil {
		http.Error(writer, mailboxBodyCancelledError, http.StatusRequestTimeout)
		return
	}
	if err != nil {
		var limit *http.MaxBytesError
		var storage *os.PathError
		switch {
		case errors.As(err, &limit):
			http.Error(writer, mailboxBodyTooLargeError, http.StatusRequestEntityTooLarge)
		case errors.As(err, &storage):
			http.Error(writer, mailboxBodyStorageError, http.StatusInternalServerError)
		default:
			http.Error(writer, mailboxBodyInvalidError, http.StatusBadRequest)
		}
		return
	}
	if request.ContentLength >= mailboxNoBodyBytes && length != request.ContentLength {
		http.Error(writer, mailboxBodyInvalidError, http.StatusBadRequest)
		return
	}
	if _, err := file.Seek(mailboxBodyStartOffsetBytes, io.SeekStart); err != nil {
		http.Error(writer, mailboxBodyStorageError, http.StatusInternalServerError)
		return
	}
	forward := request.Clone(request.Context())
	forward.Body, forward.ContentLength, forward.TransferEncoding = file, length, nil
	forward.Header.Del(mailboxTransferEncodingHeader)
	forward.Header.Set(mailboxContentLengthHeader, strconv.FormatInt(length, mailboxBodyLengthRadix))
	backend.ServeHTTP(writer, forward)
}

func (d *driver) makeCertificate() (tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "isolated LLVM mailbox"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(6 * time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	public := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	private := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(filepath.Join(d.settings.Root, "git-ca.pem"), public, publicFileMode); err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(public, private)
}

func authorizeReceive(role string, input io.Reader) error {
	if role != "coordinator" && role != "llvm-node-1" && role != "llvm-node-2" {
		return errors.New("mailbox write lacks a trial role")
	}
	scanner := bufio.NewScanner(input)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 || !isDigest(fields[0], "", 40) || !isDigest(fields[1], "", 40) {
			return errors.New("mailbox update is malformed")
		}
		if role == "coordinator" {
			continue
		}
		if fields[0] != strings.Repeat("0", 40) || fields[1] == strings.Repeat("0", 40) {
			return errors.New("worker pickup refs are create-only and cannot be rewritten or deleted")
		}
		if !isWorkerRef(role, fields[2]) {
			return fmt.Errorf("worker role cannot update canonical ref %s", fields[2])
		}
	}
	return scanner.Err()
}

func isWorkerRef(role, ref string) bool {
	hash := sha256.Sum256([]byte(role))
	nodeHash := hex.EncodeToString(hash[:16])
	for _, prefix := range []string{"refs/heads/dispat-pending-", "refs/heads/dispat-output-", "refs/heads/dispat-completed-", "refs/heads/dispat-failed-"} {
		if !strings.HasPrefix(ref, prefix) {
			continue
		}
		parts := strings.Split(strings.TrimPrefix(ref, prefix), "-")
		if len(parts) != 4 && len(parts) != 5 {
			return false
		}
		if parts[len(parts)-2] != nodeHash {
			return false
		}
		if !isDigest(parts[len(parts)-4], "", 32) || !isDigest(parts[len(parts)-3], "", 32) {
			return false
		}
		pickup := parts[len(parts)-1]
		return isDigest(pickup, "", 32) || isDigest(pickup, "", 48)
	}
	return false
}
