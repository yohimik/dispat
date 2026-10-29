package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCloudOptionsRequireExactCandidateAndExplicitExecutor(t *testing.T) {
	args := []string{"--project", "my-project", "--id", "123-1-hybrid", "--image", "sha256:" + strings.Repeat("a", 64), "--archive", "image.tar.gz", "--source", strings.Repeat("b", 40), "--binary-sha256", strings.Repeat("c", 64)}
	if _, err := parseOptions(args); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range [][]string{{"--tool", "nx"}, {"--mode", "distributed"}, {"--image", "tag:mutable"}, {"--root", "/tmp/other"}, {"--id", "../other"}} {
		if _, err := parseOptions(append(append([]string{}, args...), suffix...)); err == nil {
			t.Fatalf("accepted %v", suffix)
		}
	}
}

func TestMailboxWorkerCannotWriteCanonicalOrAnotherNodeRefs(t *testing.T) {
	role := "llvm-node-1"
	sum := sha256.Sum256([]byte(role))
	hash := hex.EncodeToString(sum[:16])
	oid := strings.Repeat("a", 40)
	for _, kind := range []string{"pending", "output", "completed", "failed"} {
		ref := "refs/heads/dispat-" + kind + "-" + strings.Repeat("b", 32) + "-" + strings.Repeat("c", 32) + "-" + hash + "-" + strings.Repeat("d", 32)
		if err := authorizeReceive(role, strings.NewReader(strings.Repeat("0", 40)+" "+oid+" "+ref+"\n")); err != nil {
			t.Fatal(err)
		}
		for _, update := range []string{oid + " " + oid + " " + ref + "\n", oid + " " + strings.Repeat("0", 40) + " " + ref + "\n"} {
			if err := authorizeReceive(role, strings.NewReader(update)); err == nil {
				t.Fatal("worker rewrote/deleted an immutable pickup ref")
			}
		}
		if err := authorizeReceive("llvm-node-2", strings.NewReader(oid+" "+oid+" "+ref+"\n")); err == nil {
			t.Fatal("another node's pickup accepted")
		}
	}
	for _, ref := range []string{"refs/heads/main", "refs/tags/v1", "refs/heads/dispat-task-x", "refs/heads/dispat-ledger-x", "refs/heads/dispat-slot-x", "refs/heads/dispat-worker-x"} {
		if err := authorizeReceive(role, strings.NewReader(strings.Repeat("0", 40)+" "+oid+" "+ref+"\n")); err == nil {
			t.Fatalf("canonical ref accepted: %s", ref)
		}
	}
	if err := authorizeReceive("coordinator", strings.NewReader(oid+" "+oid+" refs/heads/main\n")); err != nil {
		t.Fatal(err)
	}
	if err := authorizeReceive("unknown", strings.NewReader("")); err == nil {
		t.Fatal("unknown role accepted")
	}
}

func TestEvidenceExtractionRefusesLinksTraversalAndOversize(t *testing.T) {
	for _, header := range []tar.Header{{Name: "../escape", Mode: 0600, Size: 1}, {Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}, {Name: "large", Size: maximumFetchBytes + 1}} {
		var content bytes.Buffer
		writer := tar.NewWriter(&content)
		if err := writer.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if err := extractEvidence(&content, t.TempDir()); err == nil {
			t.Fatalf("accepted unsafe archive %+v", header)
		}
	}
	var content bytes.Buffer
	writer := tar.NewWriter(&content)
	writer.WriteHeader(&tar.Header{Name: "attempt/manifest.json", Mode: 0600, Size: 2})
	writer.Write([]byte("{}"))
	writer.Close()
	destination := t.TempDir()
	if err := extractEvidence(&content, destination); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(destination, "attempt/manifest.json")); err != nil || string(data) != "{}" {
		t.Fatalf("evidence = %s, %v", data, err)
	}
}

func TestAdapterRejectsForeignTrialsAndPathEscapes(t *testing.T) {
	d := &driver{plan: remotePlan{Nodes: []remoteNode{{Name: "llvm-node-1"}}}, state: journal{Instances: []instance{{Name: "vm"}}}}
	handler := newAdapterHandler(d)
	for _, request := range []adapterRequest{{Action: "start", Node: "llvm-node-1", TrialID: "trial"}, {Action: "prepare", Node: "foreign", TrialID: "trial"}, {Action: "prepare", Node: "llvm-node-1", TrialID: "../bad"}} {
		if _, err := handler.dispatch(t.Context(), request); err == nil {
			t.Fatalf("accepted %+v", request)
		}
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := checkedLocalPath(filepath.Join(root, "escape"), root); err == nil {
		t.Fatal("symlink escape accepted")
	}
}

func TestEvidenceExtractionRefusesExistingSymlinkParents(t *testing.T) {
	destination, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(destination, "escape")); err != nil {
		t.Fatal(err)
	}
	var content bytes.Buffer
	writer := tar.NewWriter(&content)
	if err := writer.WriteHeader(&tar.Header{Name: "escape/record.json", Mode: 0600, Size: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extractEvidence(&content, destination); err == nil {
		t.Fatal("preexisting symlink escaped evidence root")
	}
	if _, err := os.Stat(filepath.Join(outside, "record.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside file changed: %v", err)
	}
}

func TestCleanupRequiresIdentityAndReportsDeleteFailures(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		actual     instance
		deleteErr  error
		wantDelete bool
		wantError  bool
	}{
		{"changed ID", instance{Name: "vm", ID: "other", Labels: map[string]string{"trial": "trial"}}, nil, false, true},
		{"changed label", instance{Name: "vm", ID: "123", Labels: map[string]string{"trial": "foreign"}}, nil, false, true},
		{"delete denied", instance{Name: "vm", ID: "123", Labels: map[string]string{"trial": "trial"}}, errors.New("permission denied"), true, true},
		{"deleted and verified", instance{Name: "vm", ID: "123", Labels: map[string]string{"trial": "trial"}}, nil, true, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			root := t.TempDir()
			os.MkdirAll(filepath.Join(root, "results/cloud/final/llvm-node-1"), 0700)
			d := &driver{settings: options{Root: root, Project: "project", Zone: defaultZone}, state: journal{Instances: []instance{{Name: "vm", ID: "123", Labels: map[string]string{"trial": "trial"}}}}}
			var network instance
			if err := json.Unmarshal([]byte(`{"networkInterfaces":[{"accessConfigs":[{"natIP":"127.0.0.1"}]}]}`), &network); err != nil {
				t.Fatal(err)
			}
			d.state.Instances[0].NetworkInterfaces = network.NetworkInterfaces
			deleted := false
			d.runCommand = func(_ context.Context, request invocation) ([]byte, error) {
				text := strings.Join(request.Args, " ")
				if request.Name != "gcloud" {
					return nil, nil
				}
				if strings.Contains(text, "instances delete") {
					deleted = true
					return nil, scenario.deleteErr
				}
				if strings.Contains(text, "id=123") {
					if scenario.deleteErr != nil {
						return json.Marshal([]instance{scenario.actual})
					}
					return []byte("[]"), nil
				}
				return json.Marshal([]instance{scenario.actual})
			}
			err := d.cleanup(t.Context())
			if (err != nil) != scenario.wantError || deleted != scenario.wantDelete {
				t.Fatalf("cleanup err=%v deleted=%v", err, deleted)
			}
		})
	}
}

func TestReleasePurposeCannotReachPublicationJobs(t *testing.T) {
	content, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		On struct {
			Dispatch struct {
				Inputs map[string]struct {
					Default string `yaml:"default"`
				} `yaml:"inputs"`
			} `yaml:"workflow_dispatch"`
		} `yaml:"on"`
		Jobs map[string]struct {
			If          string            `yaml:"if"`
			Permissions map[string]string `yaml:"permissions"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(content, &workflow); err != nil {
		t.Fatal(err)
	}
	if workflow.On.Dispatch.Inputs["purpose"].Default != "release" {
		t.Fatal("production release default changed")
	}
	for name, job := range workflow.Jobs {
		if strings.HasPrefix(name, "llvm-") {
			if job.Permissions["contents"] != "read" {
				t.Fatalf("cloud job %s widened repository permissions", name)
			}
			continue
		}
		if !strings.Contains(job.If, "inputs.purpose == 'release'") {
			t.Fatalf("release job %s is not explicitly gated: %s", name, job.If)
		}
	}
}

func TestReceiveHookProcess(t *testing.T) {
	if os.Getenv("LLVM_RECEIVE_HELPER") != "1" {
		return
	}
	if err := authorizeReceive(os.Getenv("REMOTE_USER"), os.Stdin); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestAuthenticatedMailboxRoutesGitWritesThroughRoleHook(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	checkout := filepath.Join(root, "checkout")
	git := func(args ...string) []byte {
		t.Helper()
		command := exec.CommandContext(t.Context(), "git", args...)
		content, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, content, err)
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
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	certificate, err := x509.ParseCertificate(server.TLS.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	ca := filepath.Join(root, "ca.pem")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0600)
	for _, role := range []string{"coordinator", "llvm-node-1"} {
		password := "test-password"
		if role != "coordinator" {
			password = "worker-password"
		}
		args := []string{"-C", checkout, "-c", "http.sslCAInfo=" + ca, "-c", "http.extraHeader=Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(role+":"+password)), "push", server.URL + "/origin.git", "HEAD:refs/heads/" + role}
		command := exec.CommandContext(t.Context(), "git", args...)
		content, err := command.CombinedOutput()
		if role == "coordinator" && err != nil {
			t.Fatalf("coordinator push: %s %v", content, err)
		}
		if role != "coordinator" && err == nil {
			t.Fatal("worker canonical push succeeded")
		}
	}
	response, err := server.Client().Get(server.URL + "/origin.git/info/refs?service=git-upload-pack")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("anonymous mailbox read accepted")
	}
}
