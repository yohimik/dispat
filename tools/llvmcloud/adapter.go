package main

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maximumFetchBytes int64 = 4 << 30

type remoteCommand struct {
	Dir  string   `json:"dir"`
	Name string   `json:"name"`
	Args []string `json:"args"`
	Env  []string `json:"env"`
}
type adapterRequest struct {
	Action      string         `json:"action"`
	Node        string         `json:"node"`
	TrialID     string         `json:"trialId"`
	LocalRoot   string         `json:"localRoot,omitempty"`
	Command     *remoteCommand `json:"command,omitempty"`
	Destination string         `json:"destination,omitempty"`
}
type adapterResponse struct {
	ExitCode int    `json:"exitCode"`
	Error    string `json:"error,omitempty"`
}
type adapterHandler struct {
	driver  *driver
	mutex   sync.Mutex
	trials  map[string]string
	started map[string]bool
}

func newAdapterHandler(d *driver) *adapterHandler {
	return &adapterHandler{driver: d, trials: map[string]string{}, started: map[string]bool{}}
}

func runAdapter(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("adapter", flag.ContinueOnError)
	socket := flags.String("socket", "/cloud/control.sock", "private control socket")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected adapter arguments")
	}
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", *socket)
	}}}
	defer client.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://llvmcloud/adapter", io.LimitReader(os.Stdin, maximumRequestBytes+1))
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, maximumRequestBytes))
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("cloud adapter refused request: %s", strings.TrimSpace(string(content)))
	}
	_, err = os.Stdout.Write(content)
	return err
}

func (handler *adapterHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.URL.Path != "/adapter" {
		http.NotFound(writer, request)
		return
	}
	var command adapterRequest
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, maximumRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&command); err != nil {
		http.Error(writer, "invalid adapter request", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		http.Error(writer, "adapter request contains trailing data", http.StatusBadRequest)
		return
	}
	result, err := handler.dispatch(request.Context(), command)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	json.NewEncoder(writer).Encode(result)
}

func (handler *adapterHandler) dispatch(ctx context.Context, request adapterRequest) (adapterResponse, error) {
	var node remoteNode
	var item instance
	found := false
	for index, candidate := range handler.driver.plan.Nodes {
		if candidate.Name == request.Node {
			node = candidate
			item = handler.driver.state.Instances[index]
			found = true
			break
		}
	}
	if !found || !safeName.MatchString(request.TrialID) {
		return adapterResponse{}, errors.New("adapter request has no valid planned node/trial")
	}
	handler.mutex.Lock()
	trial := handler.trials[node.Name]
	if trial == "" && request.Action == "prepare" {
		handler.trials[node.Name] = request.TrialID
		trial = request.TrialID
	}
	if trial != request.TrialID {
		handler.mutex.Unlock()
		return adapterResponse{}, errors.New("adapter request names another trial")
	}
	if request.Action == "start" {
		if handler.started[node.Name] {
			handler.mutex.Unlock()
			return adapterResponse{}, errors.New("worker already started for this trial")
		}
		handler.started[node.Name] = true
	}
	handler.mutex.Unlock()
	switch request.Action {
	case "prepare":
		return adapterResponse{}, handler.driver.prepareNode(ctx, item, node, request.LocalRoot)
	case "start":
		return adapterResponse{}, handler.driver.startNode(ctx, item, node, request)
	case "wait":
		content, err := handler.driver.ssh(ctx, item, invocation{Name: "sudo", Args: []string{"docker", "wait", containerName(node, request.TrialID)}})
		if err != nil {
			return adapterResponse{}, err
		}
		code, err := strconv.Atoi(strings.TrimSpace(string(content)))
		return adapterResponse{ExitCode: code}, err
	case "stop":
		_, err := handler.driver.ssh(ctx, item, invocation{Name: "sudo", Args: []string{"docker", "stop", "--time", "30", containerName(node, request.TrialID)}})
		return adapterResponse{}, err
	case "fetch":
		return adapterResponse{}, handler.driver.fetchNode(ctx, item, node, request.Destination)
	default:
		return adapterResponse{}, fmt.Errorf("unknown adapter action %q", request.Action)
	}
}

func containerName(node remoteNode, trial string) string { return node.Name + "-" + trial }

func withinRoot(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && filepath.IsAbs(path)
}

func checkedLocalPath(path, root string) (string, error) {
	if !withinRoot(root, path) {
		return "", errors.New("adapter path is outside its trial directory")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	if !withinRoot(resolvedRoot, resolved) {
		return "", errors.New("adapter path escapes through a symlink")
	}
	return resolved, nil
}

func (d *driver) prepareNode(ctx context.Context, item instance, node remoteNode, localRoot string) error {
	root, err := checkedLocalPath(localRoot, filepath.Join(d.settings.Root, "work"))
	if err != nil {
		return err
	}
	reader, writer := io.Pipe()
	go func() {
		_, err := d.runCommand(ctx, invocation{Name: "tar", Args: []string{"-C", root, "-cf", "-", "."}, Output: writer})
		writer.CloseWithError(err)
	}()
	_, err = d.ssh(ctx, item, invocation{Name: "tar", Args: []string{"-C", node.Root, "-xf", "-"}, Input: reader})
	if err := errors.Join(err, reader.Close()); err != nil {
		return err
	}
	// The prepared image runs as root. Give only this disposable checkout the
	// matching owner so Git's ownership protection remains enabled.
	_, err = d.ssh(ctx, item, invocation{Name: "sudo", Args: []string{"chown", "-R", "0:0", "--", node.Root}})
	return err
}

func (d *driver) startNode(ctx context.Context, item instance, node remoteNode, request adapterRequest) error {
	command := request.Command
	if command == nil || command.Dir != node.Root || command.Name != "dispat" && command.Name != "nx" && command.Name != "lerna" {
		return errors.New("remote command is not an admitted executor in its node root")
	}
	environment := append([]string{}, command.Env...)
	environment = append(environment, "GIT_CONFIG_GLOBAL="+node.GitConfig, "GIT_CONFIG_NOSYSTEM=1", "EXPERIMENT_LLVM_EVIDENCE_DIR="+node.EvidenceDir, "EXPERIMENT_LLVM_REQUIRE_GCE=true", "EXPERIMENT_LLVM_EXPECTED_HOST_ID="+node.HostID)
	for _, entry := range environment {
		if strings.ContainsAny(entry, "\r\n") || !strings.Contains(entry, "=") {
			return errors.New("remote environment contains an invalid entry")
		}
	}
	path := "/cloud/private/" + node.Name + ".env"
	if _, err := d.ssh(ctx, item, invocation{Name: "sh", Args: []string{"-c", "umask 077; cat > \"$1\"", "write-private", path}, Input: strings.NewReader(strings.Join(environment, "\n") + "\n")}); err != nil {
		return err
	}
	d.mutex.Lock()
	if d.state.Processes == nil {
		d.state.Processes = map[string]string{}
	}
	d.state.Processes[node.Name] = containerName(node, request.TrialID)
	saveErr := d.save()
	d.mutex.Unlock()
	if saveErr != nil {
		return saveErr
	}
	args := []string{"docker", "run", "-d", "--name", containerName(node, request.TrialID), "--label", "dispat-trial=" + request.TrialID, "--network", "host", "--cpus", "4", "--memory", strconv.FormatInt(workerMemoryBytes, 10), "--env-file", path, "--mount", "type=bind,src=" + filepath.Dir(node.Root) + ",dst=" + filepath.Dir(node.Root), "--mount", "type=bind,src=" + node.GitConfig + ",dst=" + node.GitConfig + ",readonly", "--mount", "type=bind,src=/cloud/git-ca.pem,dst=/cloud/git-ca.pem,readonly", "--workdir", node.Root, "--entrypoint", command.Name, d.settings.Image}
	args = append(args, command.Args...)
	_, err := d.ssh(ctx, item, invocation{Name: "sudo", Args: args})
	return err
}

func (d *driver) fetchNode(ctx context.Context, item instance, node remoteNode, destination string) error {
	if !withinRoot(filepath.Join(d.settings.Root, "results"), destination) {
		return errors.New("fetch destination is outside trial results")
	}
	if err := os.MkdirAll(destination, privateDirectoryMode); err != nil {
		return err
	}
	if _, err := checkedLocalPath(destination, filepath.Join(d.settings.Root, "results")); err != nil {
		return err
	}
	var failures []error
	for _, source := range []struct{ name, path string }{{"evidence", node.EvidenceDir}, {"artifacts", filepath.Join(node.Root, "artifacts")}} {
		// Missing representative artifacts are normal after a failed build;
		// evidence is always present because provisioning creates that directory.
		if source.name == "artifacts" {
			if _, err := d.ssh(ctx, item, invocation{Name: "test", Args: []string{"-d", source.path}}); err != nil {
				continue
			}
		}
		reader, writer := io.Pipe()
		go func() {
			_, err := d.ssh(ctx, item, invocation{Name: "sudo", Args: []string{"tar", "-C", source.path, "-cf", "-", "."}, Output: writer})
			writer.CloseWithError(err)
		}()
		err := extractEvidence(reader, filepath.Join(destination, source.name))
		failures = append(failures, errors.Join(err, reader.Close()))
	}
	return errors.Join(failures...)
}

func extractEvidence(input io.Reader, destination string) error {
	if err := os.MkdirAll(destination, privateDirectoryMode); err != nil {
		return err
	}
	root, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer root.Close()
	reader := tar.NewReader(io.LimitReader(input, maximumFetchBytes+1))
	var total int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		path := filepath.Join(destination, header.Name)
		if !withinRoot(destination, path) || filepath.IsAbs(header.Name) {
			return errors.New("evidence archive escapes destination")
		}
		if header.Typeflag == tar.TypeDir {
			if err := root.MkdirAll(header.Name, privateDirectoryMode); err != nil {
				return err
			}
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > maximumFetchBytes-total {
			return errors.New("evidence archive contains a link, unsupported entry, or oversized payload")
		}
		total += header.Size
		if err := root.MkdirAll(filepath.Dir(header.Name), privateDirectoryMode); err != nil {
			return err
		}
		file, err := root.OpenFile(header.Name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, os.FileMode(header.Mode)&0777|0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(file, reader)
		if err := errors.Join(copyErr, file.Close()); err != nil {
			return err
		}
	}
}

func (d *driver) collectNode(ctx context.Context, item instance) error {
	var failures []error
	for index, expected := range d.state.Instances {
		if item.Name != expected.Name {
			continue
		}
		name := "llvm-node-" + strconv.Itoa(index+1)
		node := remoteNode{Name: name, Root: "/cloud/work/" + name + "/repo", EvidenceDir: "/cloud/work/" + name + "/evidence"}
		collectCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		if process := d.state.Processes[name]; process != "" {
			if _, err := d.ssh(collectCtx, item, invocation{Name: "sudo", Args: []string{"docker", "stop", "--time", "30", process}}); err != nil {
				failures = append(failures, err)
			}
			logs, err := d.ssh(collectCtx, item, invocation{Name: "sudo", Args: []string{"docker", "logs", process}})
			if err != nil {
				failures = append(failures, err)
			} else {
				failures = append(failures, os.WriteFile(filepath.Join(d.settings.Root, "results/cloud/"+name+"-process.log"), logs, publicFileMode))
			}
		}
		finalRoot := filepath.Join(d.settings.Root, "results/cloud/final", name)
		if err := os.MkdirAll(finalRoot, privateDirectoryMode); err != nil {
			failures = append(failures, err)
			cancel()
			continue
		}
		finalPath, err := os.MkdirTemp(finalRoot, "capture-")
		if err != nil {
			failures = append(failures, err)
			cancel()
			continue
		}
		err = d.fetchNode(collectCtx, item, node, finalPath)
		if err != nil {
			failures = append(failures, err)
			os.WriteFile(filepath.Join(d.settings.Root, "results/cloud/"+name+"-collection-error.txt"), []byte(err.Error()), publicFileMode)
		}
		args, _ := d.sshArgs(item)
		if len(args) > 0 {
			stop := append(append([]string{}, args[:len(args)-1]...), "-S", filepath.Join(d.settings.Root, "private/"+name+".sock"), "-O", "exit", args[len(args)-1])
			d.runCommand(collectCtx, invocation{Name: "ssh", Args: stop, Output: io.Discard})
		}
		cancel()
	}
	return errors.Join(failures...)
}
